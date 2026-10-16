package fio

import (
	"slices"
	"strings"
	"testing"
)

// grouped is fio's JSON for a 4-process randread job run with --group_reporting: one entry,
// whose bandwidth and IOPS are the processes' sum and whose percentiles come from their
// merged latency histogram.
const grouped = `{"fio version":"fio-3.41","jobs":[
 {"jobname":"randread","groupid":0,"error":0,"read":{"bw_bytes":209715200,"iops":51200,
  "clat_ns":{"percentile":{"50.000000":80000,"99.000000":270336,"99.900000":403456}}}}]}`

// perProcess is the same run without --group_reporting: one entry per process, each with its
// own p99. The largest of them (300 us) only bounds the merged p99 from above.
const perProcess = `{"fio version":"fio-3.41","jobs":[
 {"jobname":"randread","error":0,"read":{"bw_bytes":104857600,"iops":25600.5,
  "clat_ns":{"percentile":{"50.000000":80000,"99.000000":250000}}}},
 {"jobname":"randread","error":0,"read":{"bw_bytes":104857600,"iops":25599.5,
  "clat_ns":{"percentile":{"50.000000":81000,"99.000000":300000}}}}]}`

func TestParseGroupReported(t *testing.T) {
	r, err := Parse([]byte(grouped))
	if err != nil {
		t.Fatal(err)
	}
	if r.BandwidthBytes != 209715200 || r.IOPS != 51200 || r.P99LatUsec != 270.336 {
		t.Fatalf("got %+v", r)
	}
}

// Per-process entries cannot be merged into the job's p99 after the fact, so Parse refuses
// them instead of reporting the largest per-process p99 as if it were the job's.
func TestParseRefusesPerProcessEntries(t *testing.T) {
	_, err := Parse([]byte(perProcess))
	if err == nil || !strings.Contains(err.Error(), "group_reporting") {
		t.Fatalf("Parse(per-process output) = %v, want an error naming --group_reporting", err)
	}
}

func TestParseRejectsEmptyAndErrors(t *testing.T) {
	for _, in := range []string{`{"jobs":[]}`, `{"jobs":[{"error":5,"read":{"bw_bytes":1}}]}`,
		`{"jobs":[{"error":0,"read":{"bw_bytes":0}}]}`, `not json`,
		// no p99: not measured, never a 0 us sample
		`{"jobs":[{"error":0,"read":{"bw_bytes":1,"iops":1,"clat_ns":{"percentile":{"50.000000":1}}}}]}`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%s) should fail", in)
		}
	}
}

func TestArgsGroupReporting(t *testing.T) {
	if len(Jobs) == 0 {
		t.Fatal("no jobs to check")
	}
	for _, j := range Jobs {
		a := Args(Options{Binary: "fio", Dir: "/x", Size: "1G", Runtime: 1, IOEngine: "io_uring"}, j)
		if !slices.Contains(a, "--group_reporting") {
			t.Errorf("%s: fio args %v lack --group_reporting", j.Name, a)
		}
		for _, s := range a {
			if strings.HasPrefix(s, "--group_reporting=") {
				t.Errorf("%s: %s overrides group reporting", j.Name, s)
			}
		}
	}
}
