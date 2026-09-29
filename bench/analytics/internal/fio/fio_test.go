package fio

import "testing"

const sample = `{"fio version":"fio-3.41","jobs":[
 {"jobname":"randread","error":0,"read":{"bw_bytes":104857600,"iops":25600.5,
  "clat_ns":{"percentile":{"50.000000":80000,"99.000000":250000}}}},
 {"jobname":"randread","error":0,"read":{"bw_bytes":104857600,"iops":25599.5,
  "clat_ns":{"percentile":{"50.000000":81000,"99.000000":300000}}}}]}`

func TestParseSumsJobsAndTakesMaxP99(t *testing.T) {
	r, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if r.BandwidthBytes != 209715200 || r.IOPS != 51200 || r.P99LatUsec != 300 {
		t.Fatalf("got %+v", r)
	}
}

func TestParseRejectsEmptyAndErrors(t *testing.T) {
	for _, in := range []string{`{"jobs":[]}`, `{"jobs":[{"error":5,"read":{"bw_bytes":1}}]}`,
		`{"jobs":[{"error":0,"read":{"bw_bytes":0}}]}`, `not json`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%s) should fail", in)
		}
	}
}
