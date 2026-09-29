package stats

import (
	"math"
	"testing"
)

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{[]float64{5}, 5},
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
		{[]float64{10, 10, 10, 10, 1000}, 10},
	}
	for _, c := range cases {
		if got := Median(c.in); got != c.want {
			t.Errorf("Median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if !math.IsNaN(Median(nil)) {
		t.Error("Median(nil) should be NaN")
	}
}

func TestMedianDoesNotMutate(t *testing.T) {
	in := []float64{3, 1, 2}
	Median(in)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Errorf("input mutated: %v", in)
	}
}

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{90, 100, 110, 100, 100})
	if s.N != 5 || s.Median != 100 || s.Min != 90 || s.Max != 110 {
		t.Fatalf("bad summary %+v", s)
	}
	if s.SpreadPct != 20 {
		t.Errorf("SpreadPct = %v, want 20", s.SpreadPct)
	}
	if s.MADPct != 0 {
		t.Errorf("MADPct = %v, want 0", s.MADPct)
	}
	if e := Summarize(nil); e.N != 0 || e.Median != 0 {
		t.Errorf("empty summary = %+v", e)
	}
}
