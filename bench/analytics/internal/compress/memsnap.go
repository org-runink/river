package compress

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// The zram corpus is real memory: resident anonymous pages of a process doing analytics
// work, read through /proc/<pid>/mem. Reading another process's memory needs ptrace
// access; with Yama ptrace_scope=1 (the common default) that is allowed for the caller's
// own descendants, which is why the snapshot target is always a child riverbench started.

// Region is one mapping from /proc/<pid>/maps.
type Region struct {
	Start, End uint64
	Perms      string
	Path       string
}

// ParseMaps parses /proc/<pid>/maps content.
func ParseMaps(r io.Reader) ([]Region, error) {
	var out []Region
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		lo, hi, ok := strings.Cut(f[0], "-")
		if !ok {
			return nil, fmt.Errorf("maps: bad range %q", f[0])
		}
		s, err1 := strconv.ParseUint(lo, 16, 64)
		e, err2 := strconv.ParseUint(hi, 16, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("maps: bad range %q", f[0])
		}
		reg := Region{Start: s, End: e, Perms: f[1]}
		if len(f) >= 6 {
			reg.Path = strings.Join(f[5:], " ")
		}
		out = append(out, reg)
	}
	return out, sc.Err()
}

// Anonymous reports whether a region is private writable anonymous memory, the memory
// that goes to swap: the heap and unnamed mappings.
func (r Region) Anonymous() bool {
	if !strings.HasPrefix(r.Perms, "rw") || !strings.HasSuffix(r.Perms, "p") {
		return false
	}
	return r.Path == "" || r.Path == "[heap]" || strings.HasPrefix(r.Path, "[anon")
}

// pagemap bits (Documentation/admin-guide/mm/pagemap.rst). Unprivileged readers get the
// flags with the PFN zeroed, which is all this needs.
const (
	pmPresent = uint64(1) << 63
	pmSwapped = uint64(1) << 62
)

// Resident reports whether a pagemap entry is a page with content (in RAM or in swap).
// A never-touched anonymous page reads back as zeros without existing; counting it would
// flatter every codec.
func Resident(entry uint64) bool { return entry&(pmPresent|pmSwapped) != 0 }

// Snapshot reads up to maxPages resident anonymous pages of pid, evenly spread over all of
// them, in address order.
func Snapshot(pid int, maxPages int) ([][]byte, error) {
	const page = 4096
	mf, err := os.Open(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return nil, err
	}
	regs, err := ParseMaps(mf)
	mf.Close()
	if err != nil {
		return nil, err
	}
	pm, err := os.Open(fmt.Sprintf("/proc/%d/pagemap", pid))
	if err != nil {
		return nil, err
	}
	defer pm.Close()
	var addrs []uint64
	buf := make([]byte, 8*512)
	for _, r := range regs {
		if !r.Anonymous() {
			continue
		}
		for a := r.Start; a < r.End; {
			n := min(uint64(512), (r.End-a)/page)
			b := buf[:n*8]
			if _, err := pm.ReadAt(b, int64(a/page*8)); err != nil {
				break
			}
			for i := uint64(0); i < n; i++ {
				if Resident(binary.LittleEndian.Uint64(b[i*8:])) {
					addrs = append(addrs, a+i*page)
				}
			}
			a += n * page
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("pid %d: no resident anonymous pages readable (ptrace access?)", pid)
	}
	mem, err := os.Open(fmt.Sprintf("/proc/%d/mem", pid))
	if err != nil {
		return nil, err
	}
	defer mem.Close()
	stride := 1.0
	if len(addrs) > maxPages {
		stride = float64(len(addrs)) / float64(maxPages)
	}
	var pages [][]byte
	for f := 0.0; int(f) < len(addrs) && len(pages) < maxPages; f += stride {
		p := make([]byte, page)
		if _, err := mem.ReadAt(p, int64(addrs[int(f)])); err != nil {
			continue // unmapped since the pagemap read
		}
		pages = append(pages, p)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("pid %d: /proc/%d/mem unreadable", pid, pid)
	}
	return pages, nil
}

// PageStats counts same-filled pages in a sample.
func PageStats(pages [][]byte) (same int) {
	for _, p := range pages {
		if SameFilled(p) {
			same++
		}
	}
	return same
}

// joinPages concatenates pages (for the codec's benchmark mode with -B4096).
func joinPages(pages [][]byte) []byte { return bytes.Join(pages, nil) }
