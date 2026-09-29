package compress

import "fmt"

// The recommendation rules are declared here, before any number is seen, and applied the
// same way to every run; a run's recommendations are therefore reproducible from its rows.
// Thresholds are parameters so an operator can re-rank for other hardware.

// Rules holds the thresholds.
type Rules struct {
	// ZFS: a setting must compress and decompress at least 250 MB/s per core. OpenZFS
	// compresses and decompresses records in parallel across cores, so 250 MB/s/core x 8
	// cores = 2 GB/s, the sustained sequential speed of a current NVMe SSD: below it the
	// codec, not the drive, sets the pace.
	ZFSMinCompressBps, ZFSMinDecompressBps float64
	// zram: a swapped-in page is a page fault the application waits on. ZramMinDecompressBps
	// = 400 MB/s keeps a 4 KiB page near 10 microseconds, an order of magnitude under the
	// ~100 microseconds of the NVMe read that would replace it without zram.
	ZramMinDecompressBps float64
	// Kernel, initramfs, modules: the lowest boot cost, decompress time + size /
	// BootReadBps. BootReadBps is how fast the boot loader and firmware read the ESP; it is
	// NOT measured here (it differs per firmware), so it is a parameter and the report says
	// over which range of it the choice holds.
	BootReadBps float64
	// Parquet: the smallest file among codecs whose cold full scan is within
	// (1 + ParquetReadSlack) x the fastest.
	ParquetReadSlack float64
}

// DefaultRules are the thresholds the documentation quotes.
var DefaultRules = Rules{ZFSMinCompressBps: 250e6, ZFSMinDecompressBps: 250e6, ZramMinDecompressBps: 400e6,
	BootReadBps: 100e6, ParquetReadSlack: 0.10}

// Mix is a dataset's declared content: shape weights by logical bytes.
type Mix struct {
	Dataset    string // where it lives on an installed machine
	Image      string // workstation, server, or both
	RecordSize int    // declared from the access pattern, not chosen by the rule
	Weights    map[string]float64
	Why        string
}

// Mixes describe the datasets of a Runink River workstation and of a downstream server
// that runs a Kubernetes node.
var Mixes = []Mix{
	{"<pool>/ROOT/<be> (/)", "workstation+server", 128 << 10, map[string]float64{"os-files": 1},
		"the boot environment: binaries, libraries, package files; small random reads"},
	{"<pool>/home (/home)", "workstation", 128 << 10, map[string]float64{"source-code": 0.4, "csv": 0.1, "ndjson": 0.1, "parquet-zstd": 0.2, "images-jpeg": 0.1, "model-weights": 0.1},
		"a developer's home: checkouts, notebooks' data, a few models and pictures; mixed access"},
	{"<pool>/home/<user>/data (data lake)", "workstation", 1 << 20, map[string]float64{"parquet-zstd": 0.5, "parquet-snappy": 0.2, "delta-log": 0.01, "delta-checkpoint": 0.01, "ndjson": 0.18},
		"open-table-format data: large Parquet files read sequentially, plus the JSON log"},
	{"<pool>/containers (/var/lib/k0s)", "server", 128 << 10, map[string]float64{"container-layers": 0.5, "os-files": 0.5},
		"compressed layer blobs in the content store plus their unpacked snapshots"},
	{"<pool>/state (/var/lib/core, node state)", "server", 1 << 20, map[string]float64{"container-layers": 0.5, "model-weights": 0.4, "ndjson": 0.1},
		"a node's local image registry blobs and model weights, plus logs; large sequential files"},
	{"<pool>/models (model files)", "workstation+server", 1 << 20, map[string]float64{"model-weights": 1},
		"large model files, written once, read sequentially"},
}

type zkey struct {
	shape, setting string
	rs             int
}

// better reports whether candidate (ratio r, compress c) beats the current best: a ratio
// more than 1% higher, or one within 1% at a higher compression speed.
func better(r, c, bestR, bestC float64) bool {
	return r > bestR*1.01 || (r >= bestR*0.99 && c > bestC)
}

// Recommend applies the rules to r's rows.
func Recommend(r *Report, rules Rules) {
	zfs := map[zkey]Row{}
	settings := map[string]bool{}
	for _, row := range r.Rows {
		if row.Layer == "zfs" && row.Method == "model" {
			zfs[zkey{row.Shape, row.Setting, row.BlockSize}] = row
			settings[row.Setting] = true
		}
	}
	names := sortedKeys(settings)
	floor := func(c, d float64) bool {
		return c >= rules.ZFSMinCompressBps && (d == 0 || d >= rules.ZFSMinDecompressBps)
	}

	// Per dataset: the declared mix, weighted by logical bytes.
	for _, m := range Mixes {
		type cand struct{ ratio, cBps, dBps float64 }
		cands := map[string]cand{}
		missing := ""
		for _, s := range names {
			var w, wr, wc, wd float64
			for shape, wt := range m.Weights {
				row, found := zfs[zkey{shape, s, m.RecordSize}]
				if !found {
					missing = shape
					break
				}
				w += wt
				wr += wt / row.AllocRatio
				if row.CompressBps > 0 {
					wc += wt / row.CompressBps
				}
				if row.DecompressBps > 0 {
					wd += wt / row.DecompressBps
				}
			}
			if missing != "" {
				break
			}
			c := cand{ratio: w / wr}
			if wc > 0 {
				c.cBps = w / wc
			}
			if wd > 0 {
				c.dBps = w / wd
			}
			cands[s] = c
		}
		if missing != "" || len(cands) == 0 {
			r.Skip("recommendation "+m.Dataset, "shape "+missing+" not measured")
			continue
		}
		best := ""
		for _, s := range names {
			c := cands[s]
			if s == "off" || !floor(c.cBps, c.dBps) {
				continue
			}
			if best == "" || better(c.ratio, c.cBps, cands[best].ratio, cands[best].cBps) {
				best = s
			}
		}
		if best == "" {
			continue
		}
		b := cands[best]
		r.Recommendations = append(r.Recommendations, Recommendation{Layer: "zfs", Scope: fmt.Sprintf("%s [%s] recordsize=%s", m.Dataset, m.Image, bsStr(m.RecordSize)),
			Setting: best, Rule: "max-alloc-ratio-at-cpu-floor",
			Reason: fmt.Sprintf("allocated ratio %.3f (off %.3f), %.0f MB/s/core compress, %s decompress; %s",
				b.ratio, cands["off"].ratio, b.cBps/1e6, dStr(b.dBps), m.Why)})
	}

	// Per shape and recordsize, the same rule: what each kind of file wants on its own.
	type sk struct {
		shape string
		rs    int
	}
	var scopes []sk
	seen := map[sk]bool{}
	for _, row := range r.Rows {
		k := sk{row.Shape, row.BlockSize}
		if row.Layer == "zfs" && row.Method == "model" && !seen[k] {
			seen[k] = true
			scopes = append(scopes, k)
		}
	}
	for _, k := range scopes {
		var best *Row
		off := zfs[zkey{k.shape, "off", k.rs}]
		for _, s := range names {
			row, ok := zfs[zkey{k.shape, s, k.rs}]
			if !ok || s == "off" || !floor(row.CompressBps, row.DecompressBps) {
				continue
			}
			if best == nil || better(row.AllocRatio, row.CompressBps, best.AllocRatio, best.CompressBps) {
				b := row
				best = &b
			}
		}
		if best == nil {
			continue
		}
		r.Recommendations = append(r.Recommendations, Recommendation{Layer: "zfs-shape", Scope: fmt.Sprintf("%s recordsize=%s", k.shape, bsStr(k.rs)),
			Setting: best.Setting, Rule: "max-alloc-ratio-at-cpu-floor",
			Reason: fmt.Sprintf("allocated ratio %.3f (off %.3f), %.0f MB/s/core compress, %s decompress, %.0f%% of records stored raw",
				best.AllocRatio, off.AllocRatio, best.CompressBps/1e6, dStr(best.DecompressBps), best.Fractions["stored_raw"]*100)})
	}

	// zram
	byShape := map[string][]Row{}
	for _, row := range r.Rows {
		if row.Layer == "zram" {
			byShape[row.Shape] = append(byShape[row.Shape], row)
		}
	}
	for _, shape := range sortedKeys(byShape) {
		var best *Row
		for i := range byShape[shape] {
			row := &byShape[shape][i]
			if row.DecompressBps < rules.ZramMinDecompressBps {
				continue
			}
			if best == nil || row.AllocRatio > best.AllocRatio*1.01 || (row.AllocRatio >= best.AllocRatio*0.99 && row.DecompressBps > best.DecompressBps) {
				best = row
			}
		}
		if best != nil {
			r.Recommendations = append(r.Recommendations, Recommendation{Layer: "zram", Scope: shape, Setting: best.Setting,
				Rule: "max-alloc-ratio-at-fault-latency", Reason: fmt.Sprintf("allocated ratio %.3f, %.0f MB/s/core decompress (%.1f us per 4 KiB page)",
					best.AllocRatio, best.DecompressBps/1e6, 4096/best.DecompressBps*1e6)})
		}
	}

	// kernel, initramfs, modules
	type gk struct{ layer, shape string }
	groups := map[gk][]Row{}
	var order []gk
	for _, row := range r.Rows {
		if (row.Layer == "kernel" || row.Layer == "initramfs" || row.Layer == "modules") && row.DecompressS > 0 {
			k := gk{row.Layer, row.Shape}
			if groups[k] == nil {
				order = append(order, k)
			}
			groups[k] = append(groups[k], row)
		}
	}
	for _, k := range order {
		rows := groups[k]
		cost := func(x *Row) float64 { return x.DecompressS + float64(x.CompBytes)/rules.BootReadBps }
		var best *Row
		for i := range rows {
			if best == nil || cost(&rows[i]) < cost(best) {
				best = &rows[i]
			}
		}
		lo, hi := BootReadRange(best, rows)
		r.Recommendations = append(r.Recommendations, Recommendation{Layer: k.layer, Scope: k.shape, Setting: best.Setting,
			Rule: "min-boot-cost", Reason: fmt.Sprintf("%s, %.1f ms to decompress, boot cost %.0f ms at %.0f MB/s read; stays the pick for read rates %s",
				sizeStr(best.CompBytes), best.DecompressS*1000, cost(best)*1000, rules.BootReadBps/1e6, rangeStr(lo, hi))})
	}

	// parquet
	var pq []Row
	for _, row := range r.Rows {
		if row.Layer == "parquet" && row.DecompressS > 0 {
			pq = append(pq, row)
		}
	}
	if len(pq) > 0 {
		fastest := pq[0].DecompressS
		for _, x := range pq {
			fastest = min(fastest, x.DecompressS)
		}
		var best *Row
		for i := range pq {
			x := &pq[i]
			if x.DecompressS > fastest*(1+rules.ParquetReadSlack) {
				continue
			}
			if best == nil || x.CompBytes < best.CompBytes {
				best = x
			}
		}
		r.Recommendations = append(r.Recommendations, Recommendation{Layer: "parquet", Scope: "tpch-lineitem", Setting: best.Setting,
			Rule: "smallest-within-read-budget", Reason: fmt.Sprintf("%s (ratio %.2f vs uncompressed), cold scan %.0f ms (fastest %.0f ms), write %.0f ms",
				sizeStr(best.CompBytes), best.Ratio, best.DecompressS*1000, fastest*1000, best.CompressS*1000)})
	}
}

func dStr(v float64) string {
	if v == 0 {
		return "no decompression (records stored raw)"
	}
	if v > 1e11 {
		return ">100000 MB/s/core (almost every record stored raw)"
	}
	return fmt.Sprintf("%.0f MB/s/core", v/1e6)
}

// BootReadRange returns the read rates (bytes/s) between which best keeps the lowest boot
// cost, decompress time + size / rate, against every other row: a smaller file wins below
// lo, a faster-decompressing one above hi (-1: none).
func BootReadRange(best *Row, rows []Row) (lo, hi float64) {
	hi = -1
	for _, x := range rows {
		ds := x.DecompressS - best.DecompressS
		dz := float64(best.CompBytes - x.CompBytes)
		switch {
		case dz > 0 && ds > 0: // x is smaller and slower: it wins when reading is slower
			lo = max(lo, dz/ds)
		case dz < 0 && ds < 0: // x is larger and faster: it wins when reading is faster
			if r := dz / ds; hi < 0 || r < hi {
				hi = r
			}
		}
	}
	return lo, hi
}

func rangeStr(lo, hi float64) string {
	l := "any"
	if lo > 0 {
		l = fmt.Sprintf("%.0f", lo/1e6)
	}
	if hi < 0 {
		return fmt.Sprintf("above %s MB/s", l)
	}
	return fmt.Sprintf("%s to %.0f MB/s", l, hi/1e6)
}
