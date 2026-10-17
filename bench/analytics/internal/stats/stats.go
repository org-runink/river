// Package stats reduces repeated measurements to the numbers the report prints: the
// median (the headline figure, robust to one noisy rep), the min and max, and the spread.
//
// THE MEDIAN AND THE DEVIATION COME FROM runi. This package used to carry its own
// Median and inline the median-absolute-deviation beside it, which is how riverbench
// ended up with THREE medians that disagreed: compress/parquet.go and
// compress/layers.go returned the upper middle value for an even count while this one
// averaged the two (river#47). One definition cannot drift from itself.
//
// WHY THE PUBLISHED NUMBERS DO NOT MOVE. runi's Median is Quantile(x, 0.5) with
// R type-7 / NumPy-default linear interpolation, so for an even count it is
// (s[m-1]+s[m])/2 and for an odd count s[m] — exactly the definition written here
// before, element for element, and NaN for an empty input in both. A comparison
// across this change is still a comparison; that is the whole reason it was safe to
// make inside a benchmark whose figures are published.
//
// The one difference is on NaN input, which this package never had a defined answer
// for: runi PROPAGATES it (a column with an unknown value has an unknown median)
// where the old sort-based code returned whatever sort.Float64s left in the middle.
// Benchmark samples are timings and byte counts; a NaN there is a bug, and it now
// surfaces as NaN instead of as a plausible number.
package stats

import (
	"math"

	runistats "github.com/org-runink/runi/stats"
)

// Summary is the reduction of one metric's samples.
type Summary struct {
	N      int     `json:"n"`
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	// SpreadPct is (max-min)/median*100: how far apart the reps landed. A comparison between
	// two kernels whose medians differ by less than both spreads is not a result.
	SpreadPct float64 `json:"spread_pct"`
	// MADPct is the median absolute deviation as a percentage of the median.
	MADPct float64 `json:"mad_pct"`
}

// Median returns the median of xs (the mean of the two middle values for even n).
// It does not modify xs. It returns NaN for an empty slice.
//
// Kept as a named function because this package's callers and its sibling compress
// package both read it, and because the name is what makes "one median" checkable.
func Median(xs []float64) float64 { return runistats.Median(xs) }

// Summarize reduces samples to a Summary. An empty input gives the zero Summary (N=0),
// which callers must treat as "no result"; it is never NaN, so it always encodes as JSON.
func Summarize(xs []float64) Summary {
	if len(xs) == 0 {
		return Summary{}
	}
	med := Median(xs)
	lo, hi := xs[0], xs[0]
	for _, x := range xs {
		lo = math.Min(lo, x)
		hi = math.Max(hi, x)
	}
	s := Summary{N: len(xs), Median: med, Min: lo, Max: hi}
	if med != 0 {
		s.SpreadPct = (hi - lo) / math.Abs(med) * 100
		// runistats.MAD is the median of |xᵢ − median(x)| — the deviation slice this
		// function used to build by hand, one line above where it was consumed.
		s.MADPct = runistats.MAD(xs) / math.Abs(med) * 100
	}
	return s
}
