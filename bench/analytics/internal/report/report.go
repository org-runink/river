// Package report is the results file format (JSON, schema "riverbench/v1") and its two
// renderings: one run as a markdown table, and several runs, one per kernel, side by side.
//
// The comparison only divides numbers that were measured. It never fills a missing cell,
// and it flags a difference as within noise when the two medians are closer than the larger
// of the two runs' spreads.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/org-runink/river/bench/analytics/internal/stats"
	"github.com/org-runink/river/bench/analytics/internal/sysinfo"
)

// Schema identifies the file format.
const Schema = "riverbench/v1"

// Metric is one measured quantity with all its samples.
type Metric struct {
	Suite          string        `json:"suite"`
	Name           string        `json:"name"`
	Unit           string        `json:"unit"`
	HigherIsBetter bool          `json:"higher_is_better"`
	Samples        []float64     `json:"samples"`
	Summary        stats.Summary `json:"summary"`
	Notes          string        `json:"notes,omitempty"`
}

// Skipped records a suite that did not run, and why. A skipped suite is never silently
// absent from a report.
type Skipped struct {
	Suite  string `json:"suite"`
	Reason string `json:"reason"`
}

// Results is one harness run on one kernel.
type Results struct {
	Schema   string            `json:"schema"`
	Label    string            `json:"label"`
	Started  time.Time         `json:"started"`
	Finished time.Time         `json:"finished"`
	Smoke    bool              `json:"smoke"`
	Reps     int               `json:"reps"`
	Params   map[string]string `json:"params"`
	Host     sysinfo.Info      `json:"host"`
	Metrics  []Metric          `json:"metrics"`
	Skipped  []Skipped         `json:"skipped"`
}

// Add appends a metric and computes its summary.
func (r *Results) Add(m Metric) {
	m.Summary = stats.Summarize(m.Samples)
	r.Metrics = append(r.Metrics, m)
}

// Skip records a suite that was not run.
func (r *Results) Skip(suite, reason string) {
	r.Skipped = append(r.Skipped, Skipped{Suite: suite, Reason: reason})
}

// Load reads a results file and checks its schema.
func Load(path string) (*Results, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Results
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != Schema {
		return nil, fmt.Errorf("%s: schema %q, want %q", path, r.Schema, Schema)
	}
	return &r, nil
}

// WriteJSON writes r, indented.
func (r *Results) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func fmtNum(v float64) string {
	switch a := math.Abs(v); {
	case a == 0:
		return "0"
	case a >= 1000:
		return fmt.Sprintf("%.0f", v)
	case a >= 100:
		return fmt.Sprintf("%.1f", v)
	case a >= 1:
		return fmt.Sprintf("%.2f", v)
	default:
		return fmt.Sprintf("%.4f", v)
	}
}

func gib(kib int64) string { return fmt.Sprintf("%.1f GiB", float64(kib)/(1<<20)) }

// WriteMarkdown renders one run.
func (r *Results) WriteMarkdown(w io.Writer) {
	h := r.Host
	fmt.Fprintf(w, "# riverbench: %s\n\n", r.Label)
	if r.Smoke {
		fmt.Fprintf(w, "> **Smoke run.** Small sizes to validate the harness. These numbers are not a\n> kernel comparison and must not be quoted as one.\n\n")
	}
	fmt.Fprintf(w, "| | |\n|---|---|\n")
	fmt.Fprintf(w, "| Kernel | `%s` |\n", h.KernelRelease)
	fmt.Fprintf(w, "| Command line | `%s` |\n", h.Cmdline)
	fmt.Fprintf(w, "| Config sha256 | `%s` (%s) |\n", short(h.ConfigSHA256), h.ConfigSource)
	fmt.Fprintf(w, "| CPU | %s (%d cores / %d threads, %d NUMA node(s)) |\n", h.CPUModel, h.PhysicalCores, h.LogicalCPUs, h.NUMANodes)
	fmt.Fprintf(w, "| RAM | %s |\n", gib(h.MemTotalKiB))
	fmt.Fprintf(w, "| THP enabled / defrag | %s / %s |\n", h.Runtime["thp_enabled"], h.Runtime["thp_defrag"])
	fmt.Fprintf(w, "| HZ, preemption | %s, lazy=%s full=%s dynamic=%s |\n", h.Config["CONFIG_HZ"], h.Config["CONFIG_PREEMPT_LAZY"], h.Config["CONFIG_PREEMPT"], h.Config["CONFIG_PREEMPT_DYNAMIC"])
	fmt.Fprintf(w, "| TCP cc / qdisc | %s / %s |\n", h.Runtime["tcp_congestion"], h.Runtime["default_qdisc"])
	fmt.Fprintf(w, "| Reps | %d (median, min-max spread) |\n", r.Reps)
	keys := make([]string, 0, len(r.Params))
	for k := range r.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "| %s | %s |\n", k, r.Params[k])
	}
	fmt.Fprintf(w, "\n| Suite | Metric | Unit | Median | Min | Max | Spread %% |\n|---|---|---|---:|---:|---:|---:|\n")
	for _, m := range r.Metrics {
		s := m.Summary
		fmt.Fprintf(w, "| %s | %s | %s%s | %s | %s | %s | %.1f |\n", m.Suite, m.Name, m.Unit, arrow(m.HigherIsBetter),
			fmtNum(s.Median), fmtNum(s.Min), fmtNum(s.Max), s.SpreadPct)
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "\n**Not run:**\n\n")
		for _, s := range r.Skipped {
			fmt.Fprintf(w, "- `%s`: %s\n", s.Suite, s.Reason)
		}
	}
}

func arrow(higher bool) string {
	if higher {
		return " (higher is better)"
	}
	return " (lower is better)"
}

func short(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

// Compare renders several runs side by side. The first run is the baseline; each other
// column shows its median and its change versus the baseline.
func Compare(w io.Writer, runs []*Results) error {
	if len(runs) < 2 {
		return fmt.Errorf("compare needs at least two results files")
	}
	cpu := runs[0].Host.CPUModel
	for _, r := range runs[1:] {
		if r.Host.CPUModel != cpu || r.Host.MemTotalKiB != runs[0].Host.MemTotalKiB {
			fmt.Fprintf(w, "> **Warning:** these results come from different hardware (%q vs %q); they are not a kernel comparison.\n\n", cpu, r.Host.CPUModel)
			break
		}
	}
	for _, r := range runs {
		if r.Smoke {
			fmt.Fprintf(w, "> **Warning:** at least one input is a smoke run; do not quote this table.\n\n")
			break
		}
	}
	fmt.Fprintf(w, "Baseline: `%s`. Cells: median (change vs baseline). `~` marks a change smaller than the larger spread of the two runs, i.e. within noise.\n\n", runs[0].Host.KernelRelease)
	fmt.Fprintf(w, "| Suite | Metric | Unit |")
	for _, r := range runs {
		fmt.Fprintf(w, " `%s` |", r.Host.KernelRelease)
	}
	fmt.Fprintf(w, "\n|---|---|---|%s\n", strings.Repeat("---:|", len(runs)))

	type key struct{ suite, name string }
	index := make([]map[key]Metric, len(runs))
	var order []key
	seen := map[key]bool{}
	for i, r := range runs {
		index[i] = map[key]Metric{}
		for _, m := range r.Metrics {
			k := key{m.Suite, m.Name}
			index[i][k] = m
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	for _, k := range order {
		base, hasBase := index[0][k]
		unit := ""
		for i := range runs {
			if m, ok := index[i][k]; ok {
				unit = m.Unit + arrow(m.HigherIsBetter)
				break
			}
		}
		fmt.Fprintf(w, "| %s | %s | %s |", k.suite, k.name, unit)
		for i := range runs {
			m, ok := index[i][k]
			switch {
			case !ok || m.Summary.N == 0:
				fmt.Fprintf(w, " not run |")
			case i == 0 || !hasBase || base.Summary.Median == 0:
				fmt.Fprintf(w, " %s |", fmtNum(m.Summary.Median))
			default:
				d := (m.Summary.Median - base.Summary.Median) / base.Summary.Median * 100
				noise := ""
				if math.Abs(d) < math.Max(m.Summary.SpreadPct, base.Summary.SpreadPct) {
					noise = "~"
				}
				fmt.Fprintf(w, " %s (%s%+.1f%%) |", fmtNum(m.Summary.Median), noise, d)
			}
		}
		fmt.Fprintln(w)
	}
	return nil
}
