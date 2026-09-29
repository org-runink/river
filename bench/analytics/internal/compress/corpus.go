package compress

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// File is one file of a data shape. A shape made of many small files (a Delta Lake log)
// keeps them separate, because ZFS gives a small file a block of its own size.
type File struct {
	Name string
	Data []byte
}

// Shape is one analytic data shape.
type Shape struct {
	Name  string // parquet-snappy, csv, delta-log, ...
	Desc  string
	Files []File
}

// Bytes is the shape's total size.
func (s Shape) Bytes() int64 {
	var n int64
	for _, f := range s.Files {
		n += int64(len(f.Data))
	}
	return n
}

// Concat is every file back to back (for the codec's benchmark mode).
func (s Shape) Concat() []byte {
	var b bytes.Buffer
	for _, f := range s.Files {
		b.Write(f.Data)
	}
	return b.Bytes()
}

// ---- Avro object container files (Apache Avro 1.11 specification, "Object Container
// Files"), uncompressed ("null" codec): the row-oriented interchange shape.

// AvroType is a column's Avro type: long, int, double, string, or date (int, logical date).
type AvroType string

// AvroCol is one column.
type AvroCol struct {
	Name string
	Type AvroType
}

func zigzag(b []byte, v int64) []byte { return binary.AppendUvarint(b, uint64((v<<1)^(v>>63))) }

func avroString(b []byte, s string) []byte {
	b = zigzag(b, int64(len(s)))
	return append(b, s...)
}

// EncodeAvro writes rows (text values, as a CSV reader returns them) as an Avro object
// container file. sync is the 16-byte sync marker (fixed so the output is reproducible).
func EncodeAvro(w io.Writer, record string, cols []AvroCol, rows [][]string, rowsPerBlock int, sync [16]byte) error {
	type field struct {
		Name string `json:"name"`
		Type any    `json:"type"`
	}
	schema := struct {
		Type   string  `json:"type"`
		Name   string  `json:"name"`
		Fields []field `json:"fields"`
	}{Type: "record", Name: record}
	for _, c := range cols {
		var t any = string(c.Type)
		if c.Type == "date" {
			t = map[string]string{"type": "int", "logicalType": "date"}
		}
		schema.Fields = append(schema.Fields, field{Name: c.Name, Type: t})
	}
	sj, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	hdr := []byte("Obj\x01")
	hdr = zigzag(hdr, 2) // metadata map: one block of two entries
	hdr = avroString(hdr, "avro.codec")
	hdr = avroString(hdr, "null")
	hdr = avroString(hdr, "avro.schema")
	hdr = avroString(hdr, string(sj))
	hdr = zigzag(hdr, 0)
	hdr = append(hdr, sync[:]...)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	for start := 0; start < len(rows); start += rowsPerBlock {
		end := min(start+rowsPerBlock, len(rows))
		var body []byte
		for _, r := range rows[start:end] {
			if len(r) != len(cols) {
				return fmt.Errorf("avro: row has %d values, schema %d", len(r), len(cols))
			}
			for i, c := range cols {
				v := r[i]
				switch c.Type {
				case "long", "int":
					n, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						return fmt.Errorf("avro: %s=%q: %w", c.Name, v, err)
					}
					body = zigzag(body, n)
				case "double":
					f, err := strconv.ParseFloat(v, 64)
					if err != nil {
						return fmt.Errorf("avro: %s=%q: %w", c.Name, v, err)
					}
					body = binary.LittleEndian.AppendUint64(body, math.Float64bits(f))
				case "date":
					d, err := time.Parse("2006-01-02", v)
					if err != nil {
						return fmt.Errorf("avro: %s=%q: %w", c.Name, v, err)
					}
					body = zigzag(body, int64(d.Sub(epoch).Hours()/24))
				default:
					body = avroString(body, v)
				}
			}
		}
		var blk []byte
		blk = zigzag(blk, int64(end-start))
		blk = zigzag(blk, int64(len(body)))
		blk = append(blk, body...)
		blk = append(blk, sync[:]...)
		if _, err := w.Write(blk); err != nil {
			return err
		}
	}
	return nil
}

// LineitemAvroCols is TPC-H lineitem as Avro types.
var LineitemAvroCols = []AvroCol{
	{"l_orderkey", "long"}, {"l_partkey", "long"}, {"l_suppkey", "long"}, {"l_linenumber", "int"},
	{"l_quantity", "double"}, {"l_extendedprice", "double"}, {"l_discount", "double"}, {"l_tax", "double"},
	{"l_returnflag", "string"}, {"l_linestatus", "string"}, {"l_shipdate", "date"}, {"l_commitdate", "date"},
	{"l_receiptdate", "date"}, {"l_shipinstruct", "string"}, {"l_shipmode", "string"}, {"l_comment", "string"},
}

// ReadCSV parses a CSV file with a header line, tolerating a truncated last line.
func ReadCSV(data []byte) (header []string, rows [][]string, err error) {
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		data = data[:i+1]
	}
	r := csv.NewReader(bytes.NewReader(data))
	all, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(all) == 0 {
		return nil, nil, fmt.Errorf("csv: empty")
	}
	return all[0], all[1:], nil
}

// ---- Model weights: a GGUF-shaped file (GGUF v3 magic and header, then tensor data) whose
// tensors are drawn from N(0, 0.02), the usual initialisation scale, in the three
// encodings a local model file mixes: bf16, Q8_0 (fp16 scale + 32 x int8) and Q4_0 (fp16
// scale + 32 x 4-bit). Synthetic: a trained model's weights are not Gaussian, so this
// bounds, rather than predicts, what a codec can do with real weights.

func f32ToF16(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	exp := int((b>>23)&0xff) - 127 + 15
	mant := b & 0x7fffff
	switch {
	case exp <= 0:
		return sign
	case exp >= 31:
		return sign | 0x7c00
	}
	return sign | uint16(exp<<10) | uint16(mant>>13)
}

// Weights returns n bytes of GGUF-shaped synthetic weights.
func Weights(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	norm := func() float32 { return float32(rng.NormFloat64() * 0.02) }
	b := make([]byte, 0, n+64)
	b = append(b, "GGUF"...)
	b = binary.LittleEndian.AppendUint32(b, 3)
	b = binary.LittleEndian.AppendUint64(b, 3) // tensor count
	b = binary.LittleEndian.AppendUint64(b, 0) // metadata kv count
	third := n / 3
	for len(b) < third { // bf16
		b = binary.LittleEndian.AppendUint16(b, uint16(math.Float32bits(norm())>>16))
	}
	for len(b) < 2*third { // Q8_0
		var blk [32]float32
		amax := float32(0)
		for i := range blk {
			blk[i] = norm()
			amax = max(amax, float32(math.Abs(float64(blk[i]))))
		}
		d := amax / 127
		b = binary.LittleEndian.AppendUint16(b, f32ToF16(d))
		for _, v := range blk {
			q := int8(0)
			if d > 0 {
				q = int8(math.Round(float64(v / d)))
			}
			b = append(b, byte(q))
		}
	}
	for len(b) < n { // Q4_0
		var blk [32]float32
		amax := float32(0)
		for i := range blk {
			blk[i] = norm()
			amax = max(amax, float32(math.Abs(float64(blk[i]))))
		}
		d := amax / 8
		b = binary.LittleEndian.AppendUint16(b, f32ToF16(d))
		q := func(v float32) byte {
			if d == 0 {
				return 8
			}
			return byte(min(15, max(0, int(math.Round(float64(v/d)))+8)))
		}
		for i := 0; i < 16; i++ {
			b = append(b, q(blk[i])|q(blk[i+16])<<4)
		}
	}
	return b[:n]
}

// ---- Images: photo-like JPEGs (multi-octave value noise plus sensor grain, quality 90).
// An image store is the canonical already-compressed payload.

// Images returns JPEG files totalling at least n bytes.
func Images(n int, seed uint64) ([]File, error) {
	var out []File
	total := 0
	for i := 0; total < n; i++ {
		rng := rand.New(rand.NewPCG(seed+uint64(i), 0x51ed))
		const w, h = 1920, 1080
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		type grid struct {
			cell, cols int
			v          [][3]float64
		}
		var grids []grid
		for _, cell := range []int{240, 60, 12} {
			cols, rows := w/cell+2, h/cell+2
			g := grid{cell: cell, cols: cols, v: make([][3]float64, cols*rows)}
			for j := range g.v {
				g.v[j] = [3]float64{rng.Float64(), rng.Float64(), rng.Float64()}
			}
			grids = append(grids, g)
		}
		weights := []float64{0.65, 0.25, 0.10}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var c [3]float64
				for gi, g := range grids {
					gx, gy := float64(x)/float64(g.cell), float64(y)/float64(g.cell)
					x0, y0 := int(gx), int(gy)
					fx, fy := gx-float64(x0), gy-float64(y0)
					at := func(xx, yy int) [3]float64 { return g.v[yy*g.cols+xx] }
					a, b, cc, d := at(x0, y0), at(x0+1, y0), at(x0, y0+1), at(x0+1, y0+1)
					for k := 0; k < 3; k++ {
						top := a[k]*(1-fx) + b[k]*fx
						bot := cc[k]*(1-fx) + d[k]*fx
						c[k] += weights[gi] * (top*(1-fy) + bot*fy)
					}
				}
				px := func(v float64) uint8 {
					v = v*255 + rng.NormFloat64()*4
					return uint8(min(255, max(0, v)))
				}
				img.SetRGBA(x, y, color.RGBA{px(c[0]), px(c[1]), px(c[2]), 255})
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return nil, err
		}
		out = append(out, File{Name: fmt.Sprintf("img-%04d.jpg", i), Data: buf.Bytes()})
		total += buf.Len()
	}
	return out, nil
}

// ---- A Delta Lake transaction log (_delta_log/NNNNNNNNNNNNNNNNNNNN.json): newline-
// delimited JSON actions, one small file per commit. Each commit is a blind append of a few
// Parquet files with per-column statistics, the shape a streaming or micro-batch writer
// produces. Structure per the Delta transaction protocol (actions commitInfo, add, and
// protocol/metaData in the first commit); values are seeded and synthetic.

// DeltaLog returns commits files.
func DeltaLog(commits int, seed uint64) []File {
	rng := rand.New(rand.NewPCG(seed, 0xde17a))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var out []File
	orderkey := int64(1)
	for v := 0; v < commits; v++ {
		var b strings.Builder
		ts := base.Add(time.Duration(v) * time.Minute).UnixMilli()
		adds := 1 + rng.IntN(4)
		fmt.Fprintf(&b, `{"commitInfo":{"timestamp":%d,"operation":"WRITE","operationParameters":{"mode":"Append","partitionBy":"[\"l_shipdate\"]"},"readVersion":%d,"isolationLevel":"Serializable","isBlindAppend":true,"operationMetrics":{"numFiles":"%d","numOutputRows":"%d","numOutputBytes":"%d"},"engineInfo":"riverbench-synthetic","txnId":"%08x-%04x-%04x-%04x-%012x"}}`+"\n",
			ts, v-1, adds, adds*120000, adds*9_000_000, rng.Uint32(), rng.Uint32()&0xffff, rng.Uint32()&0xffff, rng.Uint32()&0xffff, rng.Uint64()&0xffffffffffff)
		if v == 0 {
			b.WriteString(`{"protocol":{"minReaderVersion":1,"minWriterVersion":2}}` + "\n")
			b.WriteString(`{"metaData":{"id":"00000000-0000-4000-8000-000000000000","format":{"provider":"parquet","options":{}},"schemaString":"{\"type\":\"struct\",\"fields\":[{\"name\":\"l_orderkey\",\"type\":\"long\",\"nullable\":true,\"metadata\":{}},{\"name\":\"l_extendedprice\",\"type\":\"double\",\"nullable\":true,\"metadata\":{}},{\"name\":\"l_shipdate\",\"type\":\"date\",\"nullable\":true,\"metadata\":{}},{\"name\":\"l_comment\",\"type\":\"string\",\"nullable\":true,\"metadata\":{}}]}","partitionColumns":["l_shipdate"],"configuration":{},"createdTime":` + strconv.FormatInt(ts, 10) + `}}` + "\n")
		}
		for a := 0; a < adds; a++ {
			day := base.AddDate(0, 0, rng.IntN(2400)).Format("2006-01-02")
			rows := 100000 + rng.IntN(40000)
			stats := fmt.Sprintf(`{"numRecords":%d,"minValues":{"l_orderkey":%d,"l_extendedprice":%.2f,"l_comment":"%s"},"maxValues":{"l_orderkey":%d,"l_extendedprice":%.2f,"l_comment":"%s"},"nullCount":{"l_orderkey":0,"l_extendedprice":0,"l_comment":0}}`,
				rows, orderkey, 900+rng.Float64()*100, "a"+strconv.Itoa(rng.IntN(1000)), orderkey+int64(rows)*4, 100000+rng.Float64()*5000, "z"+strconv.Itoa(rng.IntN(1000)))
			orderkey += int64(rows) * 4
			js, _ := json.Marshal(stats)
			fmt.Fprintf(&b, `{"add":{"path":"l_shipdate=%s/part-%05d-%08x-%04x-%04x-%04x-%012x.c000.zstd.parquet","partitionValues":{"l_shipdate":"%s"},"size":%d,"modificationTime":%d,"dataChange":true,"stats":%s}}`+"\n",
				day, a, rng.Uint32(), rng.Uint32()&0xffff, rng.Uint32()&0xffff, rng.Uint32()&0xffff, rng.Uint64()&0xffffffffffff, day, 7_000_000+rng.IntN(3_000_000), ts, js)
		}
		out = append(out, File{Name: fmt.Sprintf("%020d.json", v), Data: []byte(b.String())})
	}
	return out
}
