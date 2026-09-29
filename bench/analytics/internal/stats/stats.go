// Package stats reduces repeated measurements to the numbers the report prints: the
// median (the headline figure, robust to one noisy rep), the min and max, and the spread.
package stats

import (
	"math"
	"sort"
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
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// Summarize reduces samples to a Summary. An empty input gives the zero Summary (N=0),
// which callers must treat as "no result"; it is never NaN, so it always encodes as JSON.
func Summarize(xs []float64) Summary {
	if len(xs) == 0 {
		return Summary{}
	}
	med := Median(xs)
	lo, hi := xs[0], xs[0]
	dev := make([]float64, len(xs))
	for i, x := range xs {
		lo = math.Min(lo, x)
		hi = math.Max(hi, x)
		dev[i] = math.Abs(x - med)
	}
	s := Summary{N: len(xs), Median: med, Min: lo, Max: hi}
	if med != 0 {
		s.SpreadPct = (hi - lo) / math.Abs(med) * 100
		s.MADPct = Median(dev) / math.Abs(med) * 100
	}
	return s
}
