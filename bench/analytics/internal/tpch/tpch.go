// Package tpch runs the 22 TPC-H queries in DuckDB (the pinned CLI release binary) and
// parses the per-query wall-clock times from the CLI's `.timer on` output.
//
// The data is generated once per scale factor with DuckDB's tpch extension (CALL dbgen) into
// a database file in the cache directory and reused by every rep and every kernel, so all
// kernels query identical bytes. These are TPC-H-derived measurements for comparing kernels
// on one machine; they are not audited TPC-H results and must never be presented as such.
package tpch

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Queries is the number of TPC-H queries.
const Queries = 22

// Options for the suite.
type Options struct {
	DuckDB       string  // pinned duckdb CLI binary
	DataDir      string  // where tpch-sf<N>.duckdb lives (on the filesystem under test)
	ExtensionDir string  // DuckDB extension directory (kept in the cache, not ~/.duckdb)
	ScaleFactor  float64 // 10 for the real comparison; small for a smoke run
	Threads      int     // 0 = DuckDB default (all cores)
}

// DBPath is the database file for o.ScaleFactor.
func (o Options) DBPath() string {
	sf := strconv.FormatFloat(o.ScaleFactor, 'f', -1, 64)
	return filepath.Join(o.DataDir, "tpch-sf"+sf+".duckdb")
}

func (o Options) preamble() string {
	s := fmt.Sprintf("SET extension_directory='%s';\n", o.ExtensionDir)
	if o.Threads > 0 {
		s += fmt.Sprintf("SET threads=%d;\n", o.Threads)
	}
	return s
}

// Generate creates the database if it does not exist yet. INSTALL fetches the tpch
// extension from DuckDB's extension repository the first time; DuckDB verifies extension
// signatures by default and the harness does not disable that.
func Generate(o Options) (created bool, err error) {
	if _, err := os.Stat(o.DBPath()); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(o.DataDir, 0o755); err != nil {
		return false, err
	}
	tmp := o.DBPath() + ".partial"
	_ = os.Remove(tmp)
	sql := o.preamble() + "INSTALL tpch;\nLOAD tpch;\n" +
		fmt.Sprintf("CALL dbgen(sf=%s);\n", strconv.FormatFloat(o.ScaleFactor, 'f', -1, 64)) +
		"CHECKPOINT;\n"
	if _, err := runCLI(o.DuckDB, tmp, sql); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("tpch: dbgen sf=%v: %w", o.ScaleFactor, err)
	}
	_ = os.Remove(tmp + ".wal")
	return true, os.Rename(tmp, o.DBPath())
}

// Script is the CLI input for one rep: results are discarded (`.mode trash`), only the timer
// lines remain on stdout, one per query, in order.
func Script(o Options) string {
	var sb strings.Builder
	sb.WriteString(o.preamble())
	sb.WriteString("LOAD tpch;\n.mode trash\n.timer on\n")
	for q := 1; q <= Queries; q++ {
		fmt.Fprintf(&sb, "PRAGMA tpch(%d);\n", q)
	}
	return sb.String()
}

// Run executes all 22 queries once, in one DuckDB process, and returns each query's wall
// time in seconds.
func Run(o Options) ([]float64, error) {
	out, err := runCLI(o.DuckDB, o.DBPath(), Script(o))
	if err != nil {
		return nil, err
	}
	return ParseTimes(out)
}

var reTimer = regexp.MustCompile(`Run Time \(s\): real ([0-9.]+)`)

// ParseTimes extracts the `Run Time (s): real X` values. The preamble statements are timed
// too, so the LAST 22 timer lines are the queries; fewer than 22 is an error.
func ParseTimes(out []byte) ([]float64, error) {
	m := reTimer.FindAllSubmatch(out, -1)
	if len(m) < Queries {
		return nil, fmt.Errorf("tpch: found %d timer lines, need at least %d", len(m), Queries)
	}
	m = m[len(m)-Queries:]
	t := make([]float64, Queries)
	for i, s := range m {
		v, err := strconv.ParseFloat(string(s[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("tpch: bad timer value %q", s[1])
		}
		t[i] = v
	}
	return t, nil
}

func runCLI(bin, db, sql string) ([]byte, error) {
	cmd := exec.Command(bin, "-batch", "-bail", db)
	cmd.Stdin = strings.NewReader(sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("duckdb: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
