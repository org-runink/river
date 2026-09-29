package otel

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ---- OTLP JSON (the OTLP/HTTP JSON encoding: lowerCamelCase fields, 64-bit integers as
// decimal strings, enums as integers).

type jKV struct {
	Key   string `json:"key"`
	Value jAny   `json:"value"`
}

type jAny struct {
	StringValue *string  `json:"stringValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

type jPoint struct {
	Attributes        []jKV   `json:"attributes,omitempty"`
	StartTimeUnixNano string  `json:"startTimeUnixNano,omitempty"`
	TimeUnixNano      string  `json:"timeUnixNano"`
	AsDouble          float64 `json:"asDouble"`
}

type jGauge struct {
	DataPoints []jPoint `json:"dataPoints"`
}

type jSum struct {
	DataPoints             []jPoint `json:"dataPoints"`
	AggregationTemporality int      `json:"aggregationTemporality"`
	IsMonotonic            bool     `json:"isMonotonic"`
}

type jMetric struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Unit        string  `json:"unit,omitempty"`
	Gauge       *jGauge `json:"gauge,omitempty"`
	Sum         *jSum   `json:"sum,omitempty"`
}

type jScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type jScopeMetrics struct {
	Scope   jScope    `json:"scope"`
	Metrics []jMetric `json:"metrics"`
}

type jResource struct {
	Attributes []jKV `json:"attributes"`
}

type jResourceMetrics struct {
	Resource     jResource       `json:"resource"`
	ScopeMetrics []jScopeMetrics `json:"scopeMetrics"`
}

type jRequest struct {
	ResourceMetrics []jResourceMetrics `json:"resourceMetrics"`
}

func jAttrs(a []Attr) []jKV {
	var out []jKV
	for _, x := range sortedAttrs(a) {
		kv := jKV{Key: x.Key}
		switch x.Type {
		case TString:
			s := x.Str
			kv.Value.StringValue = &s
		case TInt:
			s := strconv.FormatInt(x.Int, 10)
			kv.Value.IntValue = &s
		case TDouble:
			d := x.Dbl
			kv.Value.DoubleValue = &d
		case TBool:
			v := x.Bool
			kv.Value.BoolValue = &v
		}
		out = append(out, kv)
	}
	return out
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// EncodeJSON writes b as an OTLP ExportMetricsServiceRequest in the OTLP JSON encoding.
func EncodeJSON(w io.Writer, b *Batch) error {
	ts := strconv.FormatInt(b.Time.UnixNano(), 10)
	start := strconv.FormatInt(b.Start.UnixNano(), 10)
	sm := jScopeMetrics{Scope: jScope{Name: b.ScopeName, Version: b.ScopeVersion}}
	for _, m := range b.Metrics {
		jm := jMetric{Name: m.Name, Description: m.Description, Unit: m.Unit}
		var pts []jPoint
		for _, p := range m.Points {
			if !finite(p.Value) {
				continue
			}
			jp := jPoint{Attributes: jAttrs(p.Attrs), TimeUnixNano: ts, AsDouble: p.Value}
			if m.Kind == Counter {
				jp.StartTimeUnixNano = start
			}
			pts = append(pts, jp)
		}
		if m.Kind == Counter {
			jm.Sum = &jSum{DataPoints: pts, AggregationTemporality: 2, IsMonotonic: true}
		} else {
			jm.Gauge = &jGauge{DataPoints: pts}
		}
		sm.Metrics = append(sm.Metrics, jm)
	}
	req := jRequest{ResourceMetrics: []jResourceMetrics{{
		Resource:     jResource{Attributes: jAttrs(b.Resource)},
		ScopeMetrics: []jScopeMetrics{sm},
	}}}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(req)
}

// ---- OTLP protobuf (opentelemetry-proto v1, collector/metrics/v1 ExportMetricsServiceRequest).
// Field numbers are from opentelemetry/proto/{metrics,common,resource}/v1/*.proto.

const (
	wtVarint  = 0
	wtFixed64 = 1
	wtLen     = 2
)

type pb struct{ b []byte }

func (p *pb) tag(field, wt int) { p.varint(uint64(field<<3 | wt)) }
func (p *pb) varint(v uint64)   { p.b = binary.AppendUvarint(p.b, v) }
func (p *pb) bytesField(field int, v []byte) {
	p.tag(field, wtLen)
	p.varint(uint64(len(v)))
	p.b = append(p.b, v...)
}
func (p *pb) str(field int, s string) {
	if s == "" {
		return
	}
	p.bytesField(field, []byte(s))
}
func (p *pb) fixed64(field int, v uint64) {
	p.tag(field, wtFixed64)
	p.b = binary.LittleEndian.AppendUint64(p.b, v)
}
func (p *pb) msg(field int, f func(*pb)) {
	var inner pb
	f(&inner)
	p.bytesField(field, inner.b)
}

func pbKV(p *pb, field int, a Attr) {
	p.msg(field, func(kv *pb) {
		kv.bytesField(1, []byte(a.Key)) // KeyValue.key
		kv.msg(2, func(av *pb) {        // KeyValue.value (AnyValue)
			switch a.Type {
			case TString:
				av.bytesField(1, []byte(a.Str)) // string_value
			case TBool:
				av.tag(2, wtVarint) // bool_value
				if a.Bool {
					av.varint(1)
				} else {
					av.varint(0)
				}
			case TInt:
				av.tag(3, wtVarint) // int_value (int64, two's complement varint)
				av.varint(uint64(a.Int))
			case TDouble:
				av.fixed64(4, math.Float64bits(a.Dbl)) // double_value
			}
		})
	})
}

// EncodeProto returns b as a serialized OTLP ExportMetricsServiceRequest.
func EncodeProto(b *Batch) []byte {
	ts := uint64(b.Time.UnixNano())
	start := uint64(b.Start.UnixNano())
	var req pb
	req.msg(1, func(rm *pb) { // ExportMetricsServiceRequest.resource_metrics
		rm.msg(1, func(res *pb) { // ResourceMetrics.resource
			for _, a := range sortedAttrs(b.Resource) {
				pbKV(res, 1, a) // Resource.attributes
			}
		})
		rm.msg(2, func(sm *pb) { // ResourceMetrics.scope_metrics
			sm.msg(1, func(sc *pb) { // ScopeMetrics.scope
				sc.str(1, b.ScopeName)
				sc.str(2, b.ScopeVersion)
			})
			for _, m := range b.Metrics {
				sm.msg(2, func(mm *pb) { // ScopeMetrics.metrics
					mm.str(1, m.Name)
					mm.str(2, m.Description)
					mm.str(3, m.Unit)
					field := 5 // Metric.gauge
					if m.Kind == Counter {
						field = 7 // Metric.sum
					}
					mm.msg(field, func(data *pb) {
						for _, pt := range m.Points {
							if !finite(pt.Value) {
								continue
							}
							data.msg(1, func(dp *pb) { // data_points (NumberDataPoint)
								if m.Kind == Counter {
									dp.fixed64(2, start) // start_time_unix_nano
								}
								dp.fixed64(3, ts)                         // time_unix_nano
								dp.fixed64(4, math.Float64bits(pt.Value)) // as_double
								for _, a := range sortedAttrs(pt.Attrs) {
									pbKV(dp, 7, a) // attributes
								}
							})
						}
						if m.Kind == Counter {
							data.tag(2, wtVarint) // Sum.aggregation_temporality
							data.varint(2)        // CUMULATIVE
							data.tag(3, wtVarint) // Sum.is_monotonic
							data.varint(1)
						}
					})
				})
			}
		})
	})
	return req.b
}

// ---- Prometheus text exposition 0.0.4, with the names Prometheus 3 gives the same
// metrics when it ingests them over OTLP (otlp.translation_strategy
// UnderscoreEscapingWithSuffixes, the default): dots become underscores, the unit becomes a
// suffix, a monotonic Sum gets _total, and the resource becomes a target_info series.

var unitSuffix = map[string]string{
	"By":   "bytes",
	"By/s": "bytes_per_second",
	"s":    "seconds",
	"ms":   "milliseconds",
	"%":    "percent",
}

var reInvalid = regexp.MustCompile(`[^a-zA-Z0-9_:]`)
var reUnder = regexp.MustCompile(`__+`)
var reAnnotation = regexp.MustCompile(`\{[^}]*\}`)

func perUnit(u string) string {
	switch u {
	case "s":
		return "second"
	case "m", "min":
		return "minute"
	case "h":
		return "hour"
	case "d":
		return "day"
	}
	return sanitize(u)
}

func sanitize(s string) string {
	s = reUnder.ReplaceAllString(reInvalid.ReplaceAllString(s, "_"), "_")
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	return s
}

// PromName is the Prometheus metric name for an OpenTelemetry metric.
func PromName(m Metric) string {
	name := sanitize(m.Name)
	// Curly-brace annotations ({operation}) carry no unit and are dropped, so
	// "{row}/s" is "per second".
	unit := reAnnotation.ReplaceAllString(m.Unit, "")
	main, per, _ := strings.Cut(unit, "/")
	suffix := ""
	switch {
	case main == "1" && per == "" && m.Kind == Gauge:
		suffix = "ratio"
	case main == "1" && per == "":
	case unitSuffix[unit] != "":
		suffix = unitSuffix[unit]
	case main == "" && per != "":
		suffix = "per_" + perUnit(per)
	case unit != "":
		suffix = sanitize(strings.ReplaceAll(unit, "/", "_per_"))
	}
	if suffix != "" && !strings.HasSuffix(name, "_"+suffix) {
		name += "_" + suffix
	}
	if m.Kind == Counter && !strings.HasSuffix(name, "_total") {
		name += "_total"
	}
	return name
}

func promValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func escLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

func escHelp(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

func promLabels(a []Attr) string {
	if len(a) == 0 {
		return ""
	}
	var parts []string
	for _, x := range sortedAttrs(a) {
		var v string
		switch x.Type {
		case TString:
			v = x.Str
		case TInt:
			v = strconv.FormatInt(x.Int, 10)
		case TDouble:
			v = strconv.FormatFloat(x.Dbl, 'g', -1, 64)
		case TBool:
			v = strconv.FormatBool(x.Bool)
		}
		parts = append(parts, fmt.Sprintf(`%s="%s"`, sanitize(x.Key), escLabel(v)))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// EncodePrometheus writes b in the Prometheus text exposition format. It carries no
// timestamps, so the file suits node_exporter's textfile collector as well as a scrape.
func EncodePrometheus(w io.Writer, b *Batch) error {
	var sb strings.Builder
	if len(b.Resource) > 0 {
		sb.WriteString("# HELP target_info Target metadata (the OpenTelemetry resource)\n# TYPE target_info gauge\n")
		sb.WriteString("target_info" + promLabels(b.Resource) + " 1\n")
	}
	for _, m := range b.Metrics {
		name := PromName(m)
		typ := "gauge"
		if m.Kind == Counter {
			typ = "counter"
		}
		if m.Description != "" {
			fmt.Fprintf(&sb, "# HELP %s %s\n", name, escHelp(m.Description))
		}
		fmt.Fprintf(&sb, "# TYPE %s %s\n", name, typ)
		for _, p := range m.Points {
			fmt.Fprintf(&sb, "%s%s %s\n", name, promLabels(p.Attrs), promValue(p.Value))
		}
	}
	_, err := io.WriteString(w, sb.String())
	return err
}
