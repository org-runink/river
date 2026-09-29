package sortjoin

import (
	"slices"
	"testing"
)

func TestParallelSortMatchesSequential(t *testing.T) {
	for _, w := range []int{1, 3, 8} {
		in := make([]uint64, 10007)
		g := splitmix64(1)
		for i := range in {
			in[i] = g.next() % 1000
		}
		want := slices.Clone(in)
		slices.Sort(want)
		if got := parallelSort(slices.Clone(in), w); !slices.Equal(got, want) {
			t.Fatalf("w=%d: parallel sort differs from slices.Sort", w)
		}
	}
}

func TestHashJoinMatchesNaive(t *testing.T) {
	build := []uint64{5, 1, 9, 3}
	probe := []uint64{1, 2, 3, 3, 9, 10, 5}
	want, wantSum := 0, uint64(0)
	set := map[uint64]bool{}
	for _, k := range build {
		set[k] = true
	}
	for _, k := range probe {
		if set[k] {
			want++
			wantSum += k*2654435761 + 1
		}
	}
	for _, w := range []int{1, 2, 5} {
		got, sum := hashJoin(build, probe, w)
		if got != want || sum != wantSum {
			t.Fatalf("w=%d: got %d/%d, want %d/%d", w, got, sum, want, wantSum)
		}
	}
}

func TestRunIsDeterministicAcrossWorkerCounts(t *testing.T) {
	a, err := Run(Options{SortRows: 50000, BuildRows: 20000, ProbeRows: 80000, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Run(Options{SortRows: 50000, BuildRows: 20000, ProbeRows: 80000, Workers: 7})
	if err != nil {
		t.Fatal(err)
	}
	if a.SortChecksum != b.SortChecksum || a.JoinMatches != b.JoinMatches || a.JoinChecksum != b.JoinChecksum {
		t.Fatalf("results differ: %+v vs %+v", a, b)
	}
	if a.JoinMatches == 0 || a.JoinMatches == 80000 {
		t.Fatalf("implausible match count %d (expected about half the probes)", a.JoinMatches)
	}
}
