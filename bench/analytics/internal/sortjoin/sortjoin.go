// Package sortjoin is the analytics-operator microbenchmark: a parallel sort (per-worker
// sort, then a parallel merge tree) and a radix-partitioned parallel hash join (build a
// table, probe it with a larger table), the two operators that dominate columnar query
// plans after the scan. They stress allocation, page faults, cache and memory bandwidth,
// and the scheduler's placement of many short-lived worker goroutines, which are the parts a
// kernel configuration can move.
//
// Inputs come from a fixed-seed generator, so every run on every kernel processes the same
// data, and each operation returns a checksum that must be identical across runs: a
// benchmark that silently computed something else would fail the harness rather than report
// a number.
package sortjoin

import (
	"fmt"
	"runtime"
	"slices"
	"sync"
	"time"
)

// Options configures one run.
type Options struct {
	SortRows  int // keys to sort
	BuildRows int // hash-join build side
	ProbeRows int // hash-join probe side
	Workers   int // 0 means GOMAXPROCS
}

// Result is one repetition: throughput in rows per second, and checksums.
type Result struct {
	SortRowsPerSec float64
	JoinRowsPerSec float64 // (build + probe) rows per second
	SortChecksum   uint64
	JoinMatches    int
	JoinChecksum   uint64
}

// splitmix64 is a small, fast, fixed-seed generator (Steele, Lea, Flood 2014).
type splitmix64 uint64

func (s *splitmix64) next() uint64 {
	*s += 0x9E3779B97F4A7C15
	z := uint64(*s)
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func workers(o Options) int {
	if o.Workers > 0 {
		return o.Workers
	}
	return runtime.GOMAXPROCS(0)
}

// Run executes one sort and one join. Input generation is not timed.
func Run(o Options) (Result, error) {
	if o.SortRows <= 0 || o.BuildRows <= 0 || o.ProbeRows <= 0 {
		return Result{}, fmt.Errorf("sortjoin: row counts must be > 0")
	}
	var r Result
	w := workers(o)

	keys := make([]uint64, o.SortRows)
	g := splitmix64(42)
	for i := range keys {
		keys[i] = g.next()
	}
	t := time.Now()
	sorted := parallelSort(keys, w)
	r.SortRowsPerSec = float64(o.SortRows) / time.Since(t).Seconds()
	if !slices.IsSorted(sorted) {
		return r, fmt.Errorf("sortjoin: output not sorted")
	}
	// Order-sensitive checksum: equal only if the whole sequence is the same.
	for i, k := range sorted {
		r.SortChecksum = r.SortChecksum*31 + k ^ uint64(i)
	}

	// Build: keys 0..BuildRows-1 in shuffled order, payload derived from the key. Probe: keys
	// uniform over [0, 2*BuildRows), so about half the probes match.
	build := make([]uint64, o.BuildRows)
	for i := range build {
		build[i] = uint64(i)
	}
	g = splitmix64(7)
	for i := len(build) - 1; i > 0; i-- {
		j := int(g.next() % uint64(i+1))
		build[i], build[j] = build[j], build[i]
	}
	probe := make([]uint64, o.ProbeRows)
	for i := range probe {
		probe[i] = g.next() % uint64(2*o.BuildRows)
	}
	t = time.Now()
	r.JoinMatches, r.JoinChecksum = hashJoin(build, probe, w)
	r.JoinRowsPerSec = float64(o.BuildRows+o.ProbeRows) / time.Since(t).Seconds()
	return r, nil
}

// parallelSort sorts w chunks concurrently, then merges pairs of runs level by level, each
// level's merges in parallel.
func parallelSort(keys []uint64, w int) []uint64 {
	n := len(keys)
	chunk := (n + w - 1) / w
	var runs [][]uint64
	for lo := 0; lo < n; lo += chunk {
		runs = append(runs, keys[lo:min(lo+chunk, n)])
	}
	var wg sync.WaitGroup
	for _, run := range runs {
		wg.Add(1)
		go func(s []uint64) { defer wg.Done(); slices.Sort(s) }(run)
	}
	wg.Wait()
	for len(runs) > 1 {
		next := make([][]uint64, (len(runs)+1)/2)
		for i := 0; i < len(runs); i += 2 {
			if i+1 == len(runs) {
				next[i/2] = runs[i]
				continue
			}
			wg.Add(1)
			go func(dst int, a, b []uint64) {
				defer wg.Done()
				next[dst] = merge(a, b)
			}(i/2, runs[i], runs[i+1])
		}
		wg.Wait()
		runs = next
	}
	if len(runs) == 0 {
		return nil
	}
	return runs[0]
}

func merge(a, b []uint64) []uint64 {
	out := make([]uint64, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] <= b[j] {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

func hash(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	return k
}

// hashJoin radix-partitions both sides by hash into P partitions (P = next power of two
// >= w), then joins each partition in its own goroutine with a Go map as the hash table.
// It returns the number of matches and an order-independent checksum of matched payloads.
func hashJoin(build, probe []uint64, w int) (int, uint64) {
	p := 1
	for p < w {
		p <<= 1
	}
	mask := uint64(p - 1)
	bparts := partition(build, p, mask, w)
	pparts := partition(probe, p, mask, w)
	matches := make([]int, p)
	sums := make([]uint64, p)
	var wg sync.WaitGroup
	for i := 0; i < p; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ht := make(map[uint64]uint64, len(bparts[i]))
			for _, k := range bparts[i] {
				ht[k] = k*2654435761 + 1 // payload
			}
			for _, k := range pparts[i] {
				if v, ok := ht[k]; ok {
					matches[i]++
					sums[i] += v
				}
			}
		}(i)
	}
	wg.Wait()
	total, sum := 0, uint64(0)
	for i := 0; i < p; i++ {
		total += matches[i]
		sum += sums[i]
	}
	return total, sum
}

// partition scatters keys into p partitions by hash, in parallel: each worker histograms and
// scatters its own slice into its own buckets, and the per-worker buckets are concatenated.
func partition(keys []uint64, p int, mask uint64, w int) [][]uint64 {
	n := len(keys)
	chunk := (n + w - 1) / w
	local := make([][][]uint64, 0, w)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for lo := 0; lo < n; lo += chunk {
		wg.Add(1)
		go func(s []uint64) {
			defer wg.Done()
			b := make([][]uint64, p)
			for _, k := range s {
				h := hash(k) & mask
				b[h] = append(b[h], k)
			}
			mu.Lock()
			local = append(local, b)
			mu.Unlock()
		}(keys[lo:min(lo+chunk, n)])
	}
	wg.Wait()
	out := make([][]uint64, p)
	for i := 0; i < p; i++ {
		size := 0
		for _, b := range local {
			size += len(b[i])
		}
		out[i] = make([]uint64, 0, size)
		for _, b := range local {
			out[i] = append(out[i], b[i]...)
		}
	}
	return out
}
