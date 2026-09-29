package compress

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Duck runs the pinned DuckDB CLI (bench/analytics/duckdb.lock).
type Duck struct {
	Bin          string
	ExtensionDir string
	DB           string // TPC-H database file (tpch.Generate), opened read-only
}

// Timing is one statement's `.timer on` line.
type Timing struct{ Real, User, Sys float64 }

var reTiming = regexp.MustCompile(`Run Time \(s\): real ([0-9.]+) user ([0-9.]+) sys ([0-9.]+)`)

// ParseTimings extracts every `.timer on` line.
func ParseTimings(out []byte) []Timing {
	var t []Timing
	for _, m := range reTiming.FindAllSubmatch(out, -1) {
		r, _ := strconv.ParseFloat(string(m[1]), 64)
		u, _ := strconv.ParseFloat(string(m[2]), 64)
		s, _ := strconv.ParseFloat(string(m[3]), 64)
		t = append(t, Timing{r, u, s})
	}
	return t
}

// Exec runs sql against the TPC-H database (read-only) and returns the timing of each
// statement that follows `.timer on` in sql.
func (d Duck) Exec(sql string, attr *syscall.SysProcAttr) ([]Timing, []byte, error) {
	pre := fmt.Sprintf("SET extension_directory=%s;\n", sqlString(d.ExtensionDir))
	args := []string{"-batch", "-bail"}
	if d.DB != "" {
		args = append(args, "-readonly", d.DB)
	}
	cmd := exec.Command(d.Bin, args...)
	cmd.Stdin = strings.NewReader(pre + sql)
	cmd.SysProcAttr = attr
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, out, fmt.Errorf("duckdb: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return ParseTimings(out), out, nil
}

// sqlString quotes a string for SQL.
func sqlString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Evict drops a file's pages from the page cache (posix_fadvise DONTNEED after fsync),
// which needs no privilege for a file the caller can open. It is how a rootless run gets a
// cold read of one file without dropping every cache on the machine.
func Evict(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		f, err = os.Open(path)
		if err != nil {
			return err
		}
	}
	defer f.Close()
	_ = f.Sync()
	return fadvise(int(f.Fd()), 0, 0, fadvDontneed)
}

const (
	fadvNormal     = 0
	fadvRandom     = 1
	fadvSequential = 2
	fadvDontneed   = 4
)

func fadvise(fd int, off, n int64, advice int) error {
	_, _, e := syscall.Syscall6(syscall.SYS_FADVISE64, uintptr(fd), uintptr(off), uintptr(n), uintptr(advice), 0, 0)
	if e != 0 {
		return e
	}
	return nil
}
