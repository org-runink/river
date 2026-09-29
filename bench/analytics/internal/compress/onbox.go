package compress

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The on-box layers measure the kernel itself rather than a model of it. zfs-pool needs
// root and an existing ZFS dataset to create children under; zram-pressure needs root to
// create a zram device per codec, and without root runs once on the host's own zram
// through a systemd user scope where one exists. Nothing here touches a dataset or a swap
// device it did not create.

func isRoot() bool { return os.Geteuid() == 0 }

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// cpuTimes is the machine's busy CPU seconds (user+nice, system+irq+softirq) from
// /proc/stat, so kernel threads (ZFS's zio taskqs) are counted.
func cpuTimes() (user, sys float64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
	v := func(i int) float64 {
		if i >= len(f) {
			return 0
		}
		x, _ := strconv.ParseFloat(f[i], 64)
		return x / 100 // USER_HZ
	}
	return v(1) + v(2), v(3) + v(6) + v(7)
}

// VMStat reads the named counters from /proc/vmstat.
func VMStat(keys ...string) map[string]float64 {
	out := map[string]float64{}
	f, err := os.Open("/proc/vmstat")
	if err != nil {
		return out
	}
	defer f.Close()
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if ok && want[k] {
			out[k], _ = strconv.ParseFloat(v, 64)
		}
	}
	return out
}

// RunZFSPool writes each shape to a fresh child dataset of parent per setting and
// recordsize, and reads it back with the ARC holding metadata only.
func RunZFSPool(o Options, parent string, shapes []Shape, r *Report) error {
	if !isRoot() {
		r.Skip("zfs-pool", "needs root (it creates and destroys child datasets under -zfs-parent)")
		return nil
	}
	if _, err := exec.LookPath("zfs"); err != nil {
		r.Skip("zfs-pool", "zfs not installed")
		return nil
	}
	if _, err := run("zfs", "list", "-H", "-o", "name", parent); err != nil {
		return fmt.Errorf("zfs-pool: parent dataset: %w", err)
	}
	if props, err := run("zfs", "get", "-H", "-o", "property,value", "encryption,compression,recordsize,primarycache", parent); err == nil {
		r.Provenance.DatasetProps = map[string]string{}
		for _, l := range strings.Split(props, "\n") {
			if k, v, ok := strings.Cut(l, "\t"); ok {
				r.Provenance.DatasetProps[k] = v
			}
		}
	}
	pool, _, _ := strings.Cut(parent, "/")
	for _, rs := range o.RecordSizes {
		for _, st := range o.ZFSSettings {
			ds := fmt.Sprintf("%s/riverbench-%s-%s", parent, st.Name(), bsStr(rs))
			if _, err := run("zfs", "create", "-o", "compression="+st.Name(), "-o", "recordsize="+strconv.Itoa(rs),
				"-o", "primarycache=metadata", "-o", "atime=off", ds); err != nil {
				return err
			}
			mnt, err := run("zfs", "get", "-H", "-o", "value", "mountpoint", ds)
			if err == nil {
				for _, sh := range shapes {
					o.progress("zfs-pool %s %s rs=%s", sh.Name, st.Name(), bsStr(rs))
					row, err := poolShape(filepath.Join(mnt, sh.Name), pool, ds, sh)
					if err != nil {
						r.Skip("zfs-pool "+sh.Name+" "+st.Name(), err.Error())
						continue
					}
					row.Setting, row.BlockSize = st.Name(), rs
					r.Rows = append(r.Rows, row)
				}
			}
			if _, derr := run("zfs", "destroy", "-r", ds); derr != nil && err == nil {
				err = derr
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func poolShape(dir, pool, ds string, sh Shape) (Row, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Row{}, err
	}
	before, _ := run("zfs", "get", "-Hp", "-o", "value", "used", ds)
	u0, s0 := cpuTimes()
	t0 := time.Now()
	for i, f := range sh.Files {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%06d", i)), f.Data, 0o600); err != nil {
			return Row{}, err
		}
	}
	if _, err := run("zpool", "sync", pool); err != nil {
		return Row{}, err
	}
	wall := time.Since(t0).Seconds()
	u1, s1 := cpuTimes()
	vals, err := run("zfs", "get", "-Hp", "-o", "value", "logicalused,used", ds)
	if err != nil {
		return Row{}, err
	}
	v := strings.Fields(vals)
	if len(v) < 2 {
		return Row{}, fmt.Errorf("zfs get: %q", vals)
	}
	var lused, used, used0 float64
	fmt.Sscan(v[0], &lused)
	fmt.Sscan(v[1], &used)
	fmt.Sscan(before, &used0)
	// Read back: primarycache=metadata keeps file data out of the ARC, so every read goes
	// to the disk and through decryption and decompression.
	buf := make([]byte, 1<<20)
	u2, s2 := cpuTimes()
	t1 := time.Now()
	for i := range sh.Files {
		f, err := os.Open(filepath.Join(dir, fmt.Sprintf("%06d", i)))
		if err != nil {
			return Row{}, err
		}
		for {
			n, err := f.Read(buf)
			if n == 0 || err != nil {
				break
			}
		}
		f.Close()
	}
	rwall := time.Since(t1).Seconds()
	u3, s3 := cpuTimes()
	if err := os.RemoveAll(dir); err != nil {
		return Row{}, err
	}
	orig := sh.Bytes()
	alloc := used - used0
	row := Row{Layer: "zfs-pool", Shape: sh.Name, Method: "on-pool", Files: len(sh.Files), OrigBytes: orig,
		AllocBytes: int64(alloc), CompressS: wall, DecompressS: rwall, ColdDecompress: true,
		CompressCPU: [2]float64{u1 - u0, s1 - s0}, DecompressCPU: [2]float64{u3 - u2, s3 - s2},
		Extra: map[string]float64{"write_bps": float64(orig) / wall, "read_bps": float64(orig) / rwall, "logicalused": lused},
		Notes: "machine-wide CPU (includes zio threads); write includes zpool sync"}
	if alloc > 0 {
		row.AllocRatio = float64(orig) / alloc
	}
	return row, nil
}

// ---- zram under memory pressure

// PressureSQL is the workload: DuckDB building lineitem, a sorted copy and a self-join in
// memory. DuckDB's own memory limit is lifted so it swaps instead of spilling to disk.
func PressureSQL(d Duck) string {
	return fmt.Sprintf(`SET extension_directory=%s;
SET memory_limit='1TB';
SET preserve_insertion_order=false;
ATTACH %s AS t (READ_ONLY);
CREATE TABLE m AS SELECT * FROM t.lineitem;
CREATE TABLE s AS SELECT * FROM m ORDER BY l_comment;
SELECT count(*), sum(s.l_extendedprice) FROM s JOIN m USING (l_orderkey, l_linenumber);
`, sqlString(d.ExtensionDir), sqlString(d.DB))
}

type zramPeak struct{ orig, compr, used float64 }

func readMMStat(dev string) zramPeak {
	b, err := os.ReadFile(filepath.Join("/sys/block", dev, "mm_stat"))
	if err != nil {
		return zramPeak{}
	}
	f := strings.Fields(string(b))
	var p zramPeak
	if len(f) >= 3 {
		fmt.Sscan(f[0], &p.orig)
		fmt.Sscan(f[1], &p.compr)
		fmt.Sscan(f[2], &p.used)
	}
	return p
}

// pollPeak samples a zram device until stop is closed and returns the sample with the most
// original data stored.
func pollPeak(dev string, stop chan struct{}) chan zramPeak {
	out := make(chan zramPeak, 1)
	go func() {
		var best zramPeak
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				out <- best
				return
			case <-t.C:
				if p := readMMStat(dev); p.orig > best.orig {
					best = p
				}
			}
		}
	}()
	return out
}

func runWorkload(cmd *exec.Cmd, sql string) (float64, int64, error) {
	cmd.Stdin = strings.NewReader(sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	t0 := time.Now()
	err := cmd.Run()
	wall := time.Since(t0).Seconds()
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var rss int64
	if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
		rss = ru.Maxrss * 1024
	}
	return wall, rss, nil
}

// RunZramPressure runs the workload unconstrained, then under a memory limit of `frac` of
// its peak with zram as the fast swap, once per codec.
func RunZramPressure(o Options, codecs []string, frac float64, r *Report) error {
	if o.Duck.Bin == "" {
		r.Skip("zram-pressure", "no DuckDB binary")
		return nil
	}
	sql := PressureSQL(o.Duck)
	o.progress("zram-pressure: unconstrained baseline")
	base, peak, err := runWorkload(exec.Command(o.Duck.Bin, "-batch", "-bail", ":memory:"), sql)
	if err != nil {
		return fmt.Errorf("zram-pressure baseline: %w", err)
	}
	limit := int64(float64(peak) * frac)
	if !isRoot() {
		sr, err := exec.LookPath("systemd-run")
		if err != nil {
			r.Skip("zram-pressure", "needs root on a Runink River machine (or a systemd user scope to run once on this host's own zram)")
			return nil
		}
		dev, algo := activeZram()
		if dev == "" {
			r.Skip("zram-pressure", "no active zram swap on this host")
			return nil
		}
		o.progress("zram-pressure: host zram %s (%s), limit %d MiB of %d MiB peak", dev, algo, limit>>20, peak>>20)
		v0 := VMStat("pswpin", "pswpout")
		stop := make(chan struct{})
		peakc := pollPeak(dev, stop)
		cmd := exec.Command(sr, "--user", "--scope", "--quiet", "-p", fmt.Sprintf("MemoryMax=%d", limit),
			"-p", "MemorySwapMax=infinity", "--", o.Duck.Bin, "-batch", "-bail", ":memory:")
		wall, _, err := runWorkload(cmd, sql)
		close(stop)
		<-peakc
		if err != nil {
			r.Skip("zram-pressure (host zram)", err.Error())
			return nil
		}
		v1 := VMStat("pswpin", "pswpout")
		r.Rows = append(r.Rows, pressureRow(algo, base, wall, peak, limit, v0, v1, zramPeak{},
			"host zram device shared with the rest of the machine; codec as configured, not chosen"))
		return nil
	}
	cg := fmt.Sprintf("/sys/fs/cgroup/riverbench-%d", os.Getpid())
	if err := os.Mkdir(cg, 0o755); err != nil {
		return fmt.Errorf("zram-pressure: cgroup: %w", err)
	}
	defer os.Remove(cg)
	for _, f := range []struct{ k, v string }{{"memory.max", strconv.FormatInt(limit, 10)}, {"memory.swap.max", "max"}} {
		if err := os.WriteFile(filepath.Join(cg, f.k), []byte(f.v), 0o644); err != nil {
			return fmt.Errorf("zram-pressure: %s: %w", f.k, err)
		}
	}
	for _, codec := range codecs {
		row, err := pressureOne(o, codec, cg, sql, base, peak, limit)
		if err != nil {
			r.Skip("zram-pressure "+codec, err.Error())
			continue
		}
		r.Rows = append(r.Rows, row)
	}
	return nil
}

func activeZram() (dev, algo string) {
	b, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return "", ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 0 && strings.HasPrefix(f[0], "/dev/zram") {
			dev = strings.TrimPrefix(f[0], "/dev/")
			a, _ := os.ReadFile(filepath.Join("/sys/block", dev, "comp_algorithm"))
			for _, w := range strings.Fields(string(a)) {
				if strings.HasPrefix(w, "[") {
					algo = strings.Trim(w, "[]")
				}
			}
			return dev, algo
		}
	}
	return "", ""
}

func pressureOne(o Options, codec, cg, sql string, base float64, peak, limit int64) (Row, error) {
	idb, err := os.ReadFile("/sys/class/zram-control/hot_add")
	if err != nil {
		return Row{}, fmt.Errorf("zram hot_add (modprobe zram?): %w", err)
	}
	id := strings.TrimSpace(string(idb))
	dev := "zram" + id
	sys := filepath.Join("/sys/block", dev)
	defer os.WriteFile("/sys/class/zram-control/hot_remove", []byte(id), 0o200)
	if err := os.WriteFile(filepath.Join(sys, "comp_algorithm"), []byte(codec), 0o644); err != nil {
		return Row{}, fmt.Errorf("comp_algorithm %s: %w", codec, err)
	}
	if err := os.WriteFile(filepath.Join(sys, "disksize"), []byte(strconv.FormatInt(peak, 10)), 0o644); err != nil {
		return Row{}, err
	}
	defer os.WriteFile(filepath.Join(sys, "reset"), []byte("1"), 0o200)
	if _, err := run("mkswap", "/dev/"+dev); err != nil {
		return Row{}, err
	}
	if _, err := run("swapon", "-p", "32767", "/dev/"+dev); err != nil {
		return Row{}, err
	}
	defer run("swapoff", "/dev/"+dev)
	o.progress("zram-pressure: %s (%s), limit %d MiB of %d MiB peak", dev, codec, limit>>20, peak>>20)
	v0 := VMStat("pswpin", "pswpout")
	stop := make(chan struct{})
	peakc := pollPeak(dev, stop)
	cmd := exec.Command("/bin/sh", "-c", `echo $$ > "$1/cgroup.procs" && shift && exec "$@"`, "sh", cg,
		o.Duck.Bin, "-batch", "-bail", ":memory:")
	wall, _, err := runWorkload(cmd, sql)
	close(stop)
	pk := <-peakc
	if err != nil {
		return Row{}, err
	}
	v1 := VMStat("pswpin", "pswpout")
	row := pressureRow(codec, base, wall, peak, limit, v0, v1, pk, "dedicated zram device, highest swap priority")
	if psi, err := os.ReadFile(filepath.Join(cg, "memory.pressure")); err == nil {
		var avg10, avg60, avg300, total float64
		if n, _ := fmt.Sscanf(strings.SplitN(string(psi), "\n", 2)[0], "some avg10=%f avg60=%f avg300=%f total=%f", &avg10, &avg60, &avg300, &total); n == 4 {
			row.Extra["psi_some_total_s"] = total / 1e6
		}
	}
	return row, nil
}

func pressureRow(codec string, base, wall float64, peak, limit int64, v0, v1 map[string]float64, pk zramPeak, note string) Row {
	setting := codec
	if codec == "zstd" {
		setting = "zstd-3"
	}
	row := Row{Layer: "zram-pressure", Shape: "duckdb-sort-join", Setting: setting, BlockSize: 4096, Method: "in-kernel",
		CompressS: wall, Extra: map[string]float64{
			"slowdown": wall / base, "baseline_s": base, "peak_rss_bytes": float64(peak), "limit_bytes": float64(limit),
			"pswpin": v1["pswpin"] - v0["pswpin"], "pswpout": v1["pswpout"] - v0["pswpout"]},
		Notes: note}
	if pk.compr > 0 {
		row.OrigBytes, row.CompBytes, row.AllocBytes = int64(pk.orig), int64(pk.compr), int64(pk.used)
		row.Ratio, row.AllocRatio = pk.orig/pk.compr, pk.orig/pk.used
	}
	return row
}
