// Package compress measures compression codecs for the layers of a Runink River machine
// (ZFS records, zram pages, the kernel, its modules and initramfs, Parquet files) on
// analytics-shaped data, and models what the kernel does with the codec's output (ZFS
// sector rounding and early abort, zram's same-filled and huge pages).
//
// Codecs run through their reference command-line tools (zstd, lz4, xz, gzip) from the
// machine's pinned packages, never a vendored reimplementation: the numbers belong to the
// same libraries the kernel and userland use, and riverbench stays standard-library Go.
// Every tool's version is recorded with the results.
package compress

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Setting is one codec configuration.
type Setting struct {
	Codec string // off, lz4, zstd, zstd-fast, xz, gzip
	Level int    // zstd/xz/gzip level; for zstd-fast the N of --fast=N
}

// Name is the setting as ZFS (and this report) spells it: off, lz4, zstd-3, zstd-fast-1,
// xz-6, gzip-9.
func (s Setting) Name() string {
	switch s.Codec {
	case "off":
		return "off"
	case "lz4":
		if s.Level > 1 {
			return fmt.Sprintf("lz4-%d", s.Level)
		}
		return "lz4"
	}
	return fmt.Sprintf("%s-%d", s.Codec, s.Level)
}

// ParseSetting is the inverse of Name.
func ParseSetting(s string) (Setting, error) {
	switch s {
	case "off":
		return Setting{Codec: s}, nil
	case "lz4":
		return Setting{Codec: s, Level: 1}, nil
	}
	i := strings.LastIndexByte(s, '-')
	if i < 0 {
		return Setting{}, fmt.Errorf("setting %q: want codec-level", s)
	}
	lv, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return Setting{}, fmt.Errorf("setting %q: bad level", s)
	}
	c := s[:i]
	switch c {
	case "zstd", "zstd-fast", "xz", "gzip", "lz4":
		return Setting{Codec: c, Level: lv}, nil
	}
	return Setting{}, fmt.Errorf("setting %q: unknown codec %q", s, c)
}

// Tools are the codec command-line tools and their versions.
type Tools struct {
	Zstd, LZ4, XZ, Gzip string
	Versions            map[string]string
}

// FindTools locates the codec tools on PATH and records their versions. A missing tool is
// left empty; the settings that need it are then skipped with a reason.
func FindTools() Tools {
	t := Tools{Versions: map[string]string{}}
	look := func(name string, args ...string) string {
		p, err := exec.LookPath(name)
		if err != nil {
			return ""
		}
		out, _ := exec.Command(p, args...).CombinedOutput()
		line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		t.Versions[name] = strings.Trim(line, "* ")
		return p
	}
	t.Zstd = look("zstd", "--version")
	t.LZ4 = look("lz4", "--version")
	t.XZ = look("xz", "--version")
	t.Gzip = look("gzip", "--version")
	return t
}

// bin is the tool for a setting, or "" when it is not installed.
func (t Tools) bin(s Setting) string {
	switch s.Codec {
	case "zstd", "zstd-fast":
		return t.Zstd
	case "lz4":
		return t.LZ4
	case "xz":
		return t.XZ
	case "gzip":
		return t.Gzip
	}
	return ""
}

// compressArgs are the flags that compress with setting s (single-threaded, no checksum
// where the format allows omitting it: ZFS and zram checksum elsewhere or not at all).
func compressArgs(s Setting) []string {
	switch s.Codec {
	case "zstd":
		a := []string{"-q", "-T1", "--no-check", "-" + strconv.Itoa(s.Level)}
		if s.Level > 19 {
			a = append(a, "--ultra")
		}
		return a
	case "zstd-fast":
		return []string{"-q", "-T1", "--no-check", "--fast=" + strconv.Itoa(s.Level)}
	case "lz4":
		return []string{"-q", "--no-frame-crc", "-" + strconv.Itoa(max(s.Level, 1))}
	case "xz":
		return []string{"-q", "-T1", "--check=crc32", "-" + strconv.Itoa(s.Level)}
	case "gzip":
		return []string{"-q", "-n", "-" + strconv.Itoa(s.Level)}
	}
	return nil
}

func suffix(s Setting) string {
	switch s.Codec {
	case "zstd", "zstd-fast":
		return ".zst"
	case "lz4":
		return ".lz4"
	case "xz":
		return ".xz"
	case "gzip":
		return ".gz"
	}
	return ""
}

// FrameOverhead is the container overhead the CLI adds to one independently compressed
// block beyond what the kernel stores, per consumer. The block estimates subtract it so the
// sizes are the kernel's, not the CLI's:
//
//   - lz4 frame (--no-frame-crc): 4 magic + 3 descriptor + 4 block size + 4 end mark = 15;
//     ZFS stores a 4-byte length + the raw block, zram the raw block.
//   - zstd frame (--no-check): 4 magic + 1 descriptor + 1, 2 or 4 content size; ZFS writes
//     the magicless format behind its own 8-byte header, zram a standard frame.
func FrameOverhead(consumer string, s Setting, blockLen int) int {
	switch s.Codec {
	case "lz4":
		if consumer == "zfs" {
			return 15 - 4
		}
		return 15
	case "zstd", "zstd-fast":
		fcs := 4
		if blockLen < 65536+256 {
			fcs = 2
		}
		if blockLen < 256 {
			fcs = 1
		}
		if consumer == "zfs" {
			return 4 + fcs - 8
		}
		return 0
	}
	return 0
}

// BlockSizes compresses each block independently with setting s and returns each block's
// compressed size as the CLI wrote it. Each block is its own file and the files go through
// few tool invocations, which is what makes per-record estimates affordable.
func BlockSizes(t Tools, s Setting, blocks [][]byte, workdir string) ([]int, error) {
	bin := t.bin(s)
	if bin == "" {
		return nil, fmt.Errorf("%s: tool not installed", s.Codec)
	}
	dir, err := os.MkdirTemp(workdir, "blocks-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	names := make([]string, len(blocks))
	for i, b := range blocks {
		names[i] = filepath.Join(dir, fmt.Sprintf("b%07d", i))
		if err := os.WriteFile(names[i], b, 0o600); err != nil {
			return nil, err
		}
	}
	args := compressArgs(s)
	if s.Codec == "lz4" {
		args = append(args, "-m")
	}
	// Sizes do not depend on timing, so the batches run in parallel (one single-threaded
	// tool per worker); the speed measurements (BenchSpeed) never do.
	workers := max(1, runtime.NumCPU()/2)
	batch := min(2048, max(16, (len(names)+workers-1)/workers))
	var wg sync.WaitGroup
	errs := make(chan error, len(names)/batch+1)
	sem := make(chan struct{}, workers)
	for i := 0; i < len(names); i += batch {
		j := min(i+batch, len(names))
		wg.Add(1)
		sem <- struct{}{}
		go func(part []string) {
			defer func() { <-sem; wg.Done() }()
			cmd := exec.Command(bin, append(append(append([]string{}, args...), "-f", "--"), part...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("%s: %w: %s", s.Name(), err, bytes.TrimSpace(out))
			}
		}(names[i:j])
	}
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		return nil, err
	}
	sizes := make([]int, len(blocks))
	for i, n := range names {
		st, err := os.Stat(n + suffix(s))
		if err != nil {
			return nil, err
		}
		sizes[i] = int(st.Size())
	}
	return sizes, nil
}

// Speed is a codec's single-core throughput on a file, as its own benchmark mode measured
// it (in memory, no I/O): the fastest of the iterations within the time budget.
type Speed struct {
	Ratio         float64 // the tool's own ratio over the chunked file
	CompressBps   float64 // bytes of input per second, one core
	DecompressBps float64 // bytes of output per second, one core
}

var reBench = regexp.MustCompile(`(\d+) -> +(\d+) \(x?([0-9.]+)\), *([0-9.]+) MB/s[ ,]+([0-9.]+) MB/s`)

// ParseBench reads the last result line of `zstd -b` or `lz4 -b` output. Both tools print
// decimal megabytes (10^6).
func ParseBench(out []byte) (Speed, error) {
	m := reBench.FindAllSubmatch(out, -1)
	if len(m) == 0 {
		return Speed{}, fmt.Errorf("no benchmark result in %q", tail(out))
	}
	last := m[len(m)-1]
	in, _ := strconv.ParseFloat(string(last[1]), 64)
	outb, _ := strconv.ParseFloat(string(last[2]), 64)
	c, _ := strconv.ParseFloat(string(last[4]), 64)
	d, _ := strconv.ParseFloat(string(last[5]), 64)
	sp := Speed{CompressBps: c * 1e6, DecompressBps: d * 1e6}
	if outb > 0 {
		sp.Ratio = in / outb
	}
	return sp, nil
}

func tail(b []byte) string {
	if len(b) > 200 {
		b = b[len(b)-200:]
	}
	return string(b)
}

// BenchSpeed runs the tool's benchmark mode on file, cut into independent blocks of
// blockSize (0: the whole file), for at least seconds per direction.
func BenchSpeed(t Tools, s Setting, file string, blockSize, seconds int) (Speed, error) {
	var args []string
	switch s.Codec {
	case "zstd":
		args = []string{"-T1", fmt.Sprintf("-b%d", s.Level), fmt.Sprintf("-e%d", s.Level)}
		if s.Level > 19 {
			args = append(args, "--ultra")
		}
	case "zstd-fast":
		args = []string{"-T1", "-b", fmt.Sprintf("--fast=%d", s.Level)}
	case "lz4":
		args = []string{fmt.Sprintf("-b%d", max(s.Level, 1)), fmt.Sprintf("-e%d", max(s.Level, 1))}
	default:
		return Speed{}, fmt.Errorf("%s has no benchmark mode", s.Codec)
	}
	bin := t.bin(s)
	if bin == "" {
		return Speed{}, fmt.Errorf("%s: tool not installed", s.Codec)
	}
	args = append(args, fmt.Sprintf("-i%d", seconds))
	if blockSize > 0 {
		args = append(args, fmt.Sprintf("-B%d", blockSize))
	}
	out, err := exec.Command(bin, append(args, file)...).CombinedOutput()
	if err != nil {
		return Speed{}, fmt.Errorf("%s %v: %w: %s", bin, args, err, tail(out))
	}
	return ParseBench(out)
}

// Stream is one whole-file compress and decompress through the CLI, with wall and CPU time.
type Stream struct {
	OrigBytes, CompBytes int64
	CompressWall         time.Duration
	DecompressWall       time.Duration
	CompressCPU          [2]time.Duration // user, system
	DecompressCPU        [2]time.Duration
}

func rusage(ps *os.ProcessState) [2]time.Duration {
	if ps == nil {
		return [2]time.Duration{}
	}
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
		return [2]time.Duration{time.Duration(ru.Utime.Nano()), time.Duration(ru.Stime.Nano())}
	}
	return [2]time.Duration{}
}

// StreamRun compresses src into workdir and decompresses it to /dev/null, reps times, and
// keeps the median wall times (and the CPU of the median run). The kernel and initramfs are
// single streams, so this is how their codecs are measured.
func StreamRun(t Tools, s Setting, src, workdir string, reps int) (Stream, error) {
	bin := t.bin(s)
	if bin == "" {
		return Stream{}, fmt.Errorf("%s: tool not installed", s.Codec)
	}
	st, err := os.Stat(src)
	if err != nil {
		return Stream{}, err
	}
	dst := filepath.Join(workdir, filepath.Base(src)+"."+s.Name()+suffix(s))
	defer os.Remove(dst)
	type one struct {
		wall time.Duration
		cpu  [2]time.Duration
	}
	var comp, dec []one
	for i := 0; i < max(reps, 1); i++ {
		in, err := os.Open(src)
		if err != nil {
			return Stream{}, err
		}
		out, err := os.Create(dst)
		if err != nil {
			in.Close()
			return Stream{}, err
		}
		cmd := exec.Command(bin, append(compressArgs(s), "-c")...)
		cmd.Stdin, cmd.Stdout = in, out
		t0 := time.Now()
		err = cmd.Run()
		w := time.Since(t0)
		in.Close()
		out.Close()
		if err != nil {
			return Stream{}, fmt.Errorf("%s compress: %w", s.Name(), err)
		}
		comp = append(comp, one{w, rusage(cmd.ProcessState)})

		in, err = os.Open(dst)
		if err != nil {
			return Stream{}, err
		}
		devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		cmd = exec.Command(bin, "-d", "-c", "-q")
		cmd.Stdin, cmd.Stdout = in, devnull
		t0 = time.Now()
		err = cmd.Run()
		w = time.Since(t0)
		in.Close()
		devnull.Close()
		if err != nil {
			return Stream{}, fmt.Errorf("%s decompress: %w", s.Name(), err)
		}
		dec = append(dec, one{w, rusage(cmd.ProcessState)})
	}
	median := func(xs []one) one {
		c := append([]one(nil), xs...)
		for i := 1; i < len(c); i++ {
			for j := i; j > 0 && c[j].wall < c[j-1].wall; j-- {
				c[j], c[j-1] = c[j-1], c[j]
			}
		}
		return c[len(c)/2]
	}
	cs, err := os.Stat(dst)
	if err != nil {
		return Stream{}, err
	}
	mc, md := median(comp), median(dec)
	return Stream{OrigBytes: st.Size(), CompBytes: cs.Size(), CompressWall: mc.wall, DecompressWall: md.wall,
		CompressCPU: mc.cpu, DecompressCPU: md.cpu}, nil
}

// Split cuts data into blocks of size n (the last one shorter).
func Split(data []byte, n int) [][]byte {
	var out [][]byte
	for len(data) > 0 {
		k := min(n, len(data))
		out = append(out, data[:k])
		data = data[k:]
	}
	return out
}
