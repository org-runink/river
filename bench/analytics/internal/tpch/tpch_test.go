package tpch

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseTimesTakesLast22(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("Run Time (s): real 0.001 user 0.0 sys 0.0\n") // SET extension_directory
	sb.WriteString("Run Time (s): real 0.002 user 0.0 sys 0.0\n") // LOAD tpch
	for q := 1; q <= Queries; q++ {
		fmt.Fprintf(&sb, "Run Time (s): real %d.500 user 1.0 sys 0.1\n", q)
	}
	got, err := ParseTimes([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != Queries || got[0] != 1.5 || got[21] != 22.5 {
		t.Fatalf("got %v", got)
	}
}

func TestParseTimesTooFew(t *testing.T) {
	if _, err := ParseTimes([]byte("Run Time (s): real 1.0\n")); err == nil {
		t.Fatal("expected an error for fewer than 22 timer lines")
	}
}

func TestScriptRunsAll22Queries(t *testing.T) {
	s := Script(Options{ExtensionDir: "/x"})
	if n := strings.Count(s, "PRAGMA tpch("); n != Queries {
		t.Fatalf("%d PRAGMA lines", n)
	}
	if !strings.Contains(s, ".mode trash") || !strings.Contains(s, ".timer on") {
		t.Fatal("script must discard results and time queries")
	}
}
