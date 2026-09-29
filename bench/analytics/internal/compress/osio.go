package compress

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// File access for Parquet (what the OS can do for a columnar reader beyond codecs):
// page-cache reads under each readahead hint, O_DIRECT, and io_uring with requests in
// flight, for a full sequential scan and for a projection that reads part of every row
// group. Cold means the file's pages were evicted first (posix_fadvise DONTNEED, no root).

// ioExtents are the reads of one test.
func ioExtents(size int64, test string, block int) [][2]int64 {
	var ex [][2]int64
	switch test {
	case "seq":
		for off := int64(0); off < size; off += int64(block) {
			ex = append(ex, [2]int64{off, min(int64(block), size-off)})
		}
	case "rowgroup":
		// A projection of a few columns: one block out of every four, in file order, the
		// way a reader walks column chunks across row groups.
		for off := int64(0); off+int64(block) <= size; off += 4 * int64(block) {
			ex = append(ex, [2]int64{off, int64(block)})
		}
	}
	return ex
}

// alignedBuf returns an anonymous mapping (page-aligned, as O_DIRECT needs).
func alignedBuf(n int) ([]byte, error) {
	return syscall.Mmap(-1, 0, n, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
}

func preadAll(fd int, ex [][2]int64, block, threads int) (int64, error) {
	var total int64
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	next := 0
	for t := 0; t < threads; t++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf, err := alignedBuf(block)
			if err != nil {
				mu.Lock()
				firstErr = err
				mu.Unlock()
				return
			}
			defer syscall.Munmap(buf)
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= len(ex) {
					return
				}
				n, err := syscall.Pread(fd, buf[:ex[i][1]], ex[i][0])
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				total += int64(n)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return total, firstErr
}

type ioCase struct {
	test, method, fadvise string
	block, qd             int
	cold                  bool
}

// RunIO measures file-access methods on path (reps times each, median).
func RunIO(o Options, path, fsType string, r *Report) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	size := st.Size()
	cases := []ioCase{
		{"seq", "pread", "normal", 1 << 20, 1, true},
		{"seq", "pread", "sequential", 1 << 20, 1, true},
		{"seq", "pread", "normal", 1 << 20, 1, false},
		{"seq", "odirect", "", 1 << 20, 1, true},
		{"seq", "odirect", "", 1 << 20, 8, true},
		{"seq", "io_uring", "", 1 << 20, 8, true},
		{"seq", "io_uring", "", 1 << 20, 32, true},
		{"rowgroup", "pread", "normal", 256 << 10, 1, true},
		{"rowgroup", "pread", "random", 256 << 10, 1, true},
		{"rowgroup", "odirect", "", 256 << 10, 1, true},
		{"rowgroup", "odirect", "", 256 << 10, 8, true},
		{"rowgroup", "io_uring", "", 256 << 10, 8, true},
		{"rowgroup", "io_uring", "", 256 << 10, 32, true},
	}
	for _, c := range cases {
		ex := ioExtents(size, c.test, c.block)
		var rates []float64
		var failed error
		for i := 0; i < max(o.Reps, 1) && failed == nil; i++ {
			if err := Evict(path); err != nil {
				failed = err
				break
			}
			flags := syscall.O_RDONLY | syscall.O_CLOEXEC
			if c.method != "pread" {
				flags |= syscall.O_DIRECT
			}
			fd, err := syscall.Open(path, flags, 0)
			if err != nil {
				failed = err
				break
			}
			if c.method == "pread" {
				adv := map[string]int{"normal": fadvNormal, "sequential": fadvSequential, "random": fadvRandom}[c.fadvise]
				_ = fadvise(fd, 0, 0, adv)
				if !c.cold { // warm: one untimed pass first
					_, _ = preadAll(fd, ex, c.block, 1)
				}
			}
			t0 := time.Now()
			var n int64
			switch c.method {
			case "pread", "odirect":
				n, err = preadAll(fd, ex, c.block, c.qd)
			case "io_uring":
				n, err = uringRead(fd, ex, c.block, c.qd)
			}
			el := time.Since(t0).Seconds()
			syscall.Close(fd)
			if err != nil {
				failed = err
				break
			}
			rates = append(rates, float64(n)/el)
		}
		cache := "cold"
		if !c.cold {
			cache = "warm"
		}
		name := fmt.Sprintf("io %s %s qd=%d %s", c.test, c.method, c.qd, c.fadvise)
		if failed != nil {
			r.Skip(name, failed.Error())
			continue
		}
		o.progress("%s: %.0f MB/s", name, median(rates)/1e6)
		note := ""
		if c.method == "odirect" && c.qd > 1 {
			note = fmt.Sprintf("%d threads, one pread each", c.qd)
		}
		r.IO = append(r.IO, IORow{Test: c.test, Method: c.method, BlockSize: c.block, QueueDepth: c.qd, Fadvise: c.fadvise,
			Cache: cache, Filesystem: fsType, Bps: median(rates), Notes: note})
	}
	return nil
}

// ---- A columnar filter-and-aggregate scan (TPC-H Q6 shape) over anonymous memory, with
// and without transparent huge pages for the mapping (madvise), to see what THP buys a
// column scan: fewer page faults when the columns are built, fewer TLB misses when scanned.

const (
	madvHugepage   = 14
	madvNohugepage = 15
)

func madvise(b []byte, advice int) error {
	_, _, e := syscall.Syscall(syscall.SYS_MADVISE, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(advice))
	if e != 0 {
		return e
	}
	return nil
}

// RunScan builds rows of four columns in one mapping and scans them.
func RunScan(o Options, rows int, r *Report) error {
	for _, adv := range []struct {
		name string
		a    int
	}{{"madvise-hugepage", madvHugepage}, {"madvise-nohugepage", madvNohugepage}} {
		// 28 bytes a row: shipdate int32, quantity, price, discount float64.
		n := rows * 28
		mem, err := syscall.Mmap(-1, 0, n+(2<<20), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
		if err != nil {
			return err
		}
		if err := madvise(mem, adv.a); err != nil {
			syscall.Munmap(mem)
			r.Skip("scan "+adv.name, err.Error())
			continue
		}
		base := unsafe.Pointer(&mem[0])
		date := unsafe.Slice((*int32)(base), rows)
		q := roundUp(rows*4, 8) // the float64 columns stay 8-byte aligned
		qty := unsafe.Slice((*float64)(unsafe.Add(base, q)), rows)
		price := unsafe.Slice((*float64)(unsafe.Add(base, q+rows*8)), rows)
		disc := unsafe.Slice((*float64)(unsafe.Add(base, q+rows*16)), rows)
		threads := runtime.GOMAXPROCS(0)
		parallel := func(nt int, f func(lo, hi int)) {
			var wg sync.WaitGroup
			chunk := (rows + nt - 1) / nt
			for t := 0; t < nt; t++ {
				lo, hi := t*chunk, min((t+1)*chunk, rows)
				if lo >= hi {
					continue
				}
				wg.Add(1)
				go func() { defer wg.Done(); f(lo, hi) }()
			}
			wg.Wait()
		}
		t0 := time.Now()
		parallel(threads, func(lo, hi int) {
			x := uint64(lo)*0x9e3779b97f4a7c15 + 1
			for i := lo; i < hi; i++ {
				x ^= x << 13
				x ^= x >> 7
				x ^= x << 17
				date[i] = int32(8036 + x%2557) // 1992-01-01 .. 1998-12-31 as days
				qty[i] = float64(1 + (x>>12)%50)
				price[i] = float64(90000+(x>>20)%10000000) / 100
				disc[i] = float64((x>>40)%11) / 100
			}
		})
		pop := time.Since(t0).Seconds()
		thpKB := anonHugeKB()
		r.Scan = append(r.Scan, ScanRow{Memory: "go-mmap", Phase: "populate", THP: adv.name, Threads: threads,
			RowsPS: float64(rows) / pop, Seconds: pop, Notes: fmt.Sprintf("%d rows, %.1f GiB; process AnonHugePages %d MiB", rows, float64(n)/(1<<30), thpKB/1024)})
		for _, nt := range []int{1, threads} {
			var best, sink float64
			for rep := 0; rep < max(o.Reps, 1); rep++ {
				t0 := time.Now()
				var mu sync.Mutex
				parallel(nt, func(lo, hi int) {
					s := 0.0
					for i := lo; i < hi; i++ {
						if date[i] >= 8766 && date[i] < 9131 && disc[i] >= 0.05 && disc[i] <= 0.07 && qty[i] < 24 {
							s += price[i] * disc[i]
						}
					}
					mu.Lock()
					sink += s
					mu.Unlock()
				})
				el := time.Since(t0).Seconds()
				if best == 0 || el < best {
					best = el
				}
			}
			_ = sink
			r.Scan = append(r.Scan, ScanRow{Memory: "go-mmap", Phase: "scan", THP: adv.name, Threads: nt,
				RowsPS: float64(rows) / best, Seconds: best, Notes: "TPC-H Q6 predicate, best of reps"})
		}
		syscall.Munmap(mem)
	}
	return nil
}

func anonHugeKB() int64 {
	b, err := os.ReadFile("/proc/self/smaps_rollup")
	if err != nil {
		return 0
	}
	var kb int64
	for _, line := range strings.Split(string(b), "\n") {
		if n, _ := fmt.Sscanf(line, "AnonHugePages: %d kB", &kb); n == 1 {
			return kb
		}
	}
	return 0
}
