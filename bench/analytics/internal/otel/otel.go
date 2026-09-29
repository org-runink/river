// Package otel encodes benchmark results as OpenTelemetry metrics, with the standard
// library only: OTLP protobuf and OTLP JSON (the two OTLP/HTTP encodings,
// opentelemetry-proto v1 metrics), and the Prometheus text exposition format 0.0.4.
//
// It covers the subset riverbench needs: a Resource, one InstrumentationScope, Gauge and
// monotonic cumulative Sum metrics whose points are doubles with string, int, double or
// bool attributes. Names follow the OpenTelemetry semantic conventions where one exists
// (process.cpu.time, system.paging.operations) and river.bench.* otherwise; units are UCUM
// as OpenTelemetry requires ("By", "By/s", "s", "1").
//
// Exporting is never implicit. Push sends to exactly the endpoint the operator passed and
// refuses an empty one; nothing in this package has a default destination.
package otel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Kind is the OTLP data type of a metric.
type Kind int

const (
	// Gauge is a sampled value (OTLP Gauge).
	Gauge Kind = iota
	// Counter is a monotonic cumulative Sum (OTLP Sum, is_monotonic, CUMULATIVE).
	Counter
)

// Attr is one attribute. Exactly one of the value fields is meaningful, chosen by Type.
type Attr struct {
	Key  string
	Type AttrType
	Str  string
	Int  int64
	Dbl  float64
	Bool bool
}

// AttrType selects the AnyValue variant.
type AttrType int

// AnyValue variants used here.
const (
	TString AttrType = iota
	TInt
	TDouble
	TBool
)

// S builds a string attribute.
func S(k, v string) Attr { return Attr{Key: k, Type: TString, Str: v} }

// I builds an int attribute.
func I(k string, v int64) Attr { return Attr{Key: k, Type: TInt, Int: v} }

// F builds a double attribute.
func F(k string, v float64) Attr { return Attr{Key: k, Type: TDouble, Dbl: v} }

// B builds a bool attribute.
func B(k string, v bool) Attr { return Attr{Key: k, Type: TBool, Bool: v} }

// Point is one data point.
type Point struct {
	Attrs []Attr
	Value float64
}

// Metric is one named metric with its points.
type Metric struct {
	Name        string
	Description string
	Unit        string
	Kind        Kind
	Points      []Point
}

// Batch is everything one export carries.
type Batch struct {
	Resource     []Attr
	ScopeName    string
	ScopeVersion string
	// Start is the start of the measured interval (Sum start_time_unix_nano); Time is when
	// the values were observed. Both are fixed per batch so a golden file is stable.
	Start, Time time.Time
	Metrics     []Metric
}

// Add appends a point to the metric called name, creating it (with unit, kind and
// description) the first time. A non-finite value (a measurement that did not happen) is
// not recorded at all.
func (b *Batch) Add(name, unit, desc string, kind Kind, value float64, attrs ...Attr) {
	if !finite(value) {
		return
	}
	for i := range b.Metrics {
		if b.Metrics[i].Name == name {
			b.Metrics[i].Points = append(b.Metrics[i].Points, Point{Attrs: attrs, Value: value})
			return
		}
	}
	b.Metrics = append(b.Metrics, Metric{Name: name, Unit: unit, Description: desc, Kind: kind,
		Points: []Point{{Attrs: attrs, Value: value}}})
}

// Push POSTs the batch to endpoint, a full OTLP/HTTP metrics URL such as
// http://collector.example.org:4318/v1/metrics or Prometheus 3's
// http://prometheus.example.org:9090/api/v1/otlp/v1/metrics (started with
// --web.enable-otlp-receiver). format is "protobuf" or "json". headers are extra request
// headers (for an authenticating collector). An empty endpoint is an error: there is no
// default destination.
func Push(ctx context.Context, endpoint, format string, headers map[string]string, b *Batch) error {
	if endpoint == "" {
		return fmt.Errorf("otel: no endpoint; exporting is off unless one is passed explicitly")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return fmt.Errorf("otel: endpoint %q is not an http(s) URL", endpoint)
	}
	var body []byte
	var ctype string
	switch format {
	case "protobuf", "":
		body, ctype = EncodeProto(b), "application/x-protobuf"
	case "json":
		var buf bytes.Buffer
		if err := EncodeJSON(&buf, b); err != nil {
			return err
		}
		body, ctype = buf.Bytes(), "application/json"
	default:
		return fmt.Errorf("otel: unknown OTLP format %q (protobuf or json)", format)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("otel: %s answered %s: %s", endpoint, resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// sortedAttrs returns attrs ordered by key, so every encoding is deterministic.
func sortedAttrs(a []Attr) []Attr {
	c := append([]Attr(nil), a...)
	sort.SliceStable(c, func(i, j int) bool { return c[i].Key < c[j].Key })
	return c
}
