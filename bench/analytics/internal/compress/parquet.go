package compress

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// ParquetCodec is one DuckDB Parquet writer setting.
type ParquetCodec struct {
	Name, Compression string
	Level             int // 0: the writer's default
}

// ParquetCodecs are the page codecs compared.
var ParquetCodecs = []ParquetCodec{
	{"uncompressed", "uncompressed", 0}, {"snappy", "snappy", 0}, {"lz4_raw", "lz4_raw", 0},
	{"zstd-1", "zstd", 1}, {"zstd-3", "zstd", 3}, {"zstd-9", "zstd", 9}, {"gzip", "gzip", 0},
}

func (c ParquetCodec) copyOpts() string {
	s := "FORMAT parquet, COMPRESSION " + c.Compression
	if c.Level > 0 {
		s += fmt.Sprintf(", COMPRESSION_LEVEL %d", c.Level)
	}
	return s
}

// scanSQL decodes every column of every row: hashing the whole row forces each page to be
// decompressed and decoded.
func scanSQL(path string) string {
	return fmt.Sprintf("SELECT sum(hash(t)) FROM read_parquet(%s) t;\n", sqlString(path))
}

func median(xs []float64) float64 {
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	return c[len(c)/2]
}

// RunParquet writes TPC-H lineitem with every codec and reads it back cold and warm.
func RunParquet(o Options, r *Report) error {
	var baseline int64
	type res struct {
		c                 ParquetCodec
		size              int64
		w, wu, ws         float64
		cold, cu, cs, hot float64
	}
	var all []res
	for _, c := range ParquetCodecs {
		path := filepath.Join(o.Work, "pq-"+c.Name+".parquet")
		var ws, wus, wss, cold, cus, css, hot []float64
		for i := 0; i < max(o.Reps, 1); i++ {
			_ = os.Remove(path)
			o.progress("parquet %s rep %d", c.Name, i+1)
			t, _, err := o.Duck.Exec(fmt.Sprintf(".timer on\nCOPY lineitem TO %s (%s);\n", sqlString(path), c.copyOpts()), nil)
			if err != nil || len(t) == 0 {
				r.Skip("parquet "+c.Name, fmt.Sprint("write: ", err))
				break
			}
			ws, wus, wss = append(ws, t[len(t)-1].Real), append(wus, t[len(t)-1].User), append(wss, t[len(t)-1].Sys)
			if err := Evict(path); err != nil {
				r.Skip("parquet "+c.Name+" cold read", err.Error())
				break
			}
			t, _, err = o.Duck.Exec(".timer on\n"+scanSQL(path)+scanSQL(path), nil)
			if err != nil || len(t) < 2 {
				r.Skip("parquet "+c.Name, fmt.Sprint("read: ", err))
				break
			}
			cold, cus, css = append(cold, t[len(t)-2].Real), append(cus, t[len(t)-2].User), append(css, t[len(t)-2].Sys)
			hot = append(hot, t[len(t)-1].Real)
		}
		if len(hot) == 0 {
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if c.Name == "uncompressed" {
			baseline = st.Size()
		}
		all = append(all, res{c, st.Size(), median(ws), median(wus), median(wss), median(cold), median(cus), median(css), median(hot)})
		if c.Name != "zstd-3" && c.Name != "snappy" { // kept for the file-access and THP tests
			_ = os.Remove(path)
		}
	}
	for _, x := range all {
		row := Row{Layer: "parquet", Shape: "tpch-lineitem", Setting: x.c.Name, Method: "duckdb",
			OrigBytes: baseline, CompBytes: x.size, CompressS: x.w, DecompressS: x.cold, ColdDecompress: true,
			CompressCPU: [2]float64{x.wu, x.ws}, DecompressCPU: [2]float64{x.cu, x.cs},
			Extra: map[string]float64{"warm_read_s": x.hot}}
		if baseline > 0 {
			row.Ratio = float64(baseline) / float64(x.size)
		}
		row.Notes = "ratio vs the uncompressed Parquet file"
		r.Rows = append(r.Rows, row)
	}
	return nil
}

// ---- THP for a DuckDB Parquet scan: the same query with transparent huge pages disabled
// for the process (prctl PR_SET_THP_DISABLE, inherited across fork and exec) and without.

const prSetTHPDisable = 41

func setTHPDisable(on bool) error {
	v := uintptr(0)
	if on {
		v = 1
	}
	_, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetTHPDisable, v, 0, 0, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

// RunDuckTHP runs a TPC-H Q1-shaped aggregation over a Parquet file, warm, with and
// without THP for the DuckDB process.
func RunDuckTHP(o Options, parquet string, rows int64, r *Report) {
	q := fmt.Sprintf(`SELECT l_returnflag, l_linestatus, sum(l_quantity), sum(l_extendedprice*(1-l_discount)), avg(l_discount), count(*) FROM read_parquet(%s) GROUP BY ALL;
`, sqlString(parquet))
	sql := ".timer on\n" + strings.Repeat(q, 5)
	thp, _ := os.ReadFile("/sys/kernel/mm/transparent_hugepage/enabled")
	for _, disable := range []bool{false, true} {
		label := "system-default"
		if disable {
			label = "disabled-for-process"
			if err := setTHPDisable(true); err != nil {
				r.Skip("thp duckdb", "prctl(PR_SET_THP_DISABLE): "+err.Error())
				return
			}
		}
		o.progress("thp: duckdb Q1 over parquet, THP %s", label)
		t, _, err := o.Duck.Exec(sql, nil)
		if disable {
			_ = setTHPDisable(false)
		}
		if err != nil || len(t) < 5 {
			r.Skip("thp duckdb "+label, fmt.Sprint(err))
			return
		}
		var reals []float64
		for _, x := range t[len(t)-4:] { // the first run warms the cache
			reals = append(reals, x.Real)
		}
		m := median(reals)
		r.Scan = append(r.Scan, ScanRow{Memory: "duckdb-parquet", Phase: "query", THP: label, Threads: 0,
			RowsPS: float64(rows) / m, Seconds: m, Notes: "TPC-H Q1 shape, warm, median of 4; system THP: " + strings.TrimSpace(string(thp))})
	}
}
