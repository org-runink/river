// Package fio runs fio (https://github.com/axboe/fio) jobs and parses its JSON output. fio
// is the one external tool the storage suite needs; it is a packaged binary (`pacman -S
// fio`), not a build dependency of the harness.
//
// Two jobs per target directory, each shaped like an analytics read path:
//   - seqread: 1 MiB sequential reads at queue depth 32, like a columnar scan;
//   - randread: 4 KiB random reads, 4 jobs at queue depth 64, like index / point lookups.
//
// Both use O_DIRECT by default so the page cache (and, on ZFS, as far as its direct-I/O
// support allows, the ARC) does not turn a storage test into a memory test. The filesystem
// of each target is detected from statfs and recorded, never taken from its label.
package fio

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// Job is one fio job definition.
type Job struct {
	Name    string
	RW      string // read | randread
	BS      string
	IODepth int
	NumJobs int
}

// Jobs are the two read shapes the suite runs.
var Jobs = []Job{
	{Name: "seqread", RW: "read", BS: "1M", IODepth: 32, NumJobs: 1},
	{Name: "randread", RW: "randread", BS: "4k", IODepth: 64, NumJobs: 4},
}

// Options for one invocation.
type Options struct {
	Binary   string // fio executable
	Dir      string // target directory (on the filesystem under test)
	Size     string // per-job file size, e.g. "4G"
	Runtime  int    // seconds per job
	Direct   bool
	IOEngine string // io_uring (default) or libaio / psync
}

// Result is what the report keeps from one job.
type Result struct {
	BandwidthBytes float64 // bytes/s, summed over jobs
	IOPS           float64
	P99LatUsec     float64 // completion latency p99, max over jobs
}

type fioOut struct {
	Jobs []struct {
		Read struct {
			BWBytes float64 `json:"bw_bytes"`
			IOPS    float64 `json:"iops"`
			ClatNS  struct {
				Percentile map[string]float64 `json:"percentile"`
			} `json:"clat_ns"`
		} `json:"read"`
		Error int `json:"error"`
	} `json:"jobs"`
}

// Parse extracts a Result from fio's --output-format=json output.
func Parse(b []byte) (Result, error) {
	var out fioOut
	if err := json.Unmarshal(b, &out); err != nil {
		return Result{}, fmt.Errorf("fio: parse json: %w", err)
	}
	if len(out.Jobs) == 0 {
		return Result{}, fmt.Errorf("fio: no jobs in output")
	}
	var r Result
	for _, j := range out.Jobs {
		if j.Error != 0 {
			return Result{}, fmt.Errorf("fio: job error %d", j.Error)
		}
		r.BandwidthBytes += j.Read.BWBytes
		r.IOPS += j.Read.IOPS
		if p, ok := j.Read.ClatNS.Percentile["99.000000"]; ok && p/1000 > r.P99LatUsec {
			r.P99LatUsec = p / 1000
		}
	}
	if r.BandwidthBytes <= 0 {
		return Result{}, fmt.Errorf("fio: zero read bandwidth (did the job read anything?)")
	}
	return r, nil
}

// Run executes one job and parses it. The job's data file is kept in Dir between reps so
// layout (the write) is paid once; Cleanup removes it.
func Run(o Options, j Job) (Result, error) {
	args := []string{
		"--name=" + j.Name,
		"--directory=" + o.Dir,
		"--filename_format=riverbench.$jobname.$jobnum",
		"--rw=" + j.RW,
		"--bs=" + j.BS,
		"--size=" + o.Size,
		"--iodepth=" + strconv.Itoa(j.IODepth),
		"--numjobs=" + strconv.Itoa(j.NumJobs),
		"--ioengine=" + o.IOEngine,
		"--time_based", "--runtime=" + strconv.Itoa(o.Runtime),
		"--group_reporting=0",
		"--invalidate=1",
		"--output-format=json",
	}
	if o.Direct {
		args = append(args, "--direct=1")
	}
	cmd := exec.Command(o.Binary, args...)
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("fio %s in %s: %w", j.Name, o.Dir, err)
	}
	return Parse(b)
}

// Cleanup removes the data files Run created in dir.
func Cleanup(dir string) {
	m, _ := filepath.Glob(filepath.Join(dir, "riverbench.*"))
	for _, f := range m {
		_ = os.Remove(f)
	}
}

// FSType names the filesystem holding dir, from statfs's f_type.
func FSType(dir string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return "unknown"
	}
	switch uint64(st.Type) {
	case 0x2fc12fc1:
		return "zfs"
	case 0xef53:
		return "ext4"
	case 0x9123683e:
		return "btrfs"
	case 0x58465342:
		return "xfs"
	case 0x01021994:
		return "tmpfs"
	case 0x794c7630:
		return "overlayfs"
	default:
		return fmt.Sprintf("0x%x", uint64(st.Type))
	}
}
