// Package membw is a STREAM-like memory bandwidth test (Copy, Scale, Add, Triad over three
// float64 arrays, one contiguous slice per worker), written in Go so the harness has no C
// toolchain dependency. It follows STREAM's byte accounting (McCalpin, "STREAM: Sustainable
// Memory Bandwidth in High Performance Computers", https://www.cs.virginia.edu/stream/):
// Copy and Scale move 2 arrays, Add and Triad move 3. It is not the reference STREAM binary,
// so its numbers compare kernels on one machine; they are not STREAM results.
//
// Huge pages: with Madvise set, the arrays are mmap'd and madvise(MADV_HUGEPAGE)'d, which is
// what an engine that opts into THP does. Without it they come from the Go heap, which does
// not madvise. Under THP "always" both get huge pages; under "madvise" only the first does.
// Running both shows the THP policy's effect instead of hiding it.
package membw

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Options configures one run.
type Options struct {
	Elements int  // per array; STREAM's rule is at least 4x the last-level cache in total
	Workers  int  // goroutines; 0 means GOMAXPROCS
	Madvise  bool // mmap + MADV_HUGEPAGE instead of Go-heap slices
}

// Result is one pass's bandwidth per kernel, in bytes per second.
type Result struct {
	Copy, Scale, Add, Triad float64
}

const madvHugepage = 14 // MADV_HUGEPAGE, include/uapi/asm-generic/mman-common.h

// Arrays holds the three arrays so repetitions reuse (and do not re-fault) them.
type Arrays struct {
	a, b, c []float64
	unmap   []func()
}

// Alloc allocates and initialises the arrays (first touch happens here, in parallel, so pages
// are placed on the NUMA node of the worker that uses them).
func Alloc(o Options) (*Arrays, error) {
	if o.Elements <= 0 {
		return nil, fmt.Errorf("membw: Elements must be > 0")
	}
	ar := &Arrays{}
	for i := 0; i < 3; i++ {
		var s []float64
		if o.Madvise {
			b, err := syscall.Mmap(-1, 0, o.Elements*8, syscall.PROT_READ|syscall.PROT_WRITE,
				syscall.MAP_PRIVATE|syscall.MAP_ANON)
			if err != nil {
				ar.Free()
				return nil, fmt.Errorf("membw: mmap: %w", err)
			}
			if err := syscall.Madvise(b, madvHugepage); err != nil {
				_ = syscall.Munmap(b)
				ar.Free()
				return nil, fmt.Errorf("membw: madvise(MADV_HUGEPAGE): %w", err)
			}
			ar.unmap = append(ar.unmap, func() { _ = syscall.Munmap(b) })
			s = unsafe.Slice((*float64)(unsafe.Pointer(&b[0])), o.Elements)
		} else {
			s = make([]float64, o.Elements)
		}
		switch i {
		case 0:
			ar.a = s
		case 1:
			ar.b = s
		case 2:
			ar.c = s
		}
	}
	parallel(o, len(ar.a), func(lo, hi int) {
		for j := lo; j < hi; j++ {
			ar.a[j], ar.b[j], ar.c[j] = 1, 2, 0
		}
	})
	return ar, nil
}

// Free releases mmap'd arrays.
func (ar *Arrays) Free() {
	for _, f := range ar.unmap {
		f()
	}
	ar.unmap = nil
}

// Run performs one pass of the four kernels and returns their bandwidth.
func Run(o Options, ar *Arrays) Result {
	n := len(ar.a)
	const scalar = 3.0
	bytes2 := float64(2 * 8 * n)
	bytes3 := float64(3 * 8 * n)
	a, b, c := ar.a, ar.b, ar.c
	var r Result
	r.Copy = bytes2 / timed(func() {
		parallel(o, n, func(lo, hi int) { copy(c[lo:hi], a[lo:hi]) })
	})
	r.Scale = bytes2 / timed(func() {
		parallel(o, n, func(lo, hi int) {
			for j := lo; j < hi; j++ {
				b[j] = scalar * c[j]
			}
		})
	})
	r.Add = bytes3 / timed(func() {
		parallel(o, n, func(lo, hi int) {
			for j := lo; j < hi; j++ {
				c[j] = a[j] + b[j]
			}
		})
	})
	r.Triad = bytes3 / timed(func() {
		parallel(o, n, func(lo, hi int) {
			for j := lo; j < hi; j++ {
				a[j] = b[j] + scalar*c[j]
			}
		})
	})
	return r
}

func timed(f func()) float64 {
	t := time.Now()
	f()
	return time.Since(t).Seconds()
}

func parallel(o Options, n int, f func(lo, hi int)) {
	w := o.Workers
	if w <= 0 {
		w = runtime.GOMAXPROCS(0)
	}
	if w > n {
		w = n
	}
	var wg sync.WaitGroup
	chunk := (n + w - 1) / w
	for lo := 0; lo < n; lo += chunk {
		hi := min(lo+chunk, n)
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			f(lo, hi)
		}(lo, hi)
	}
	wg.Wait()
}
