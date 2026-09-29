// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package github_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/org-runink/river/guide/github"
	"github.com/org-runink/river/guide/internal/fakegh"
)

func TestDeviceLogin(t *testing.T) {
	gh := fakegh.New("o/r")
	defer gh.Close()
	c := github.New("")
	c.API, c.Web = gh.URL, gh.URL
	c.SetPollInterval(10 * time.Millisecond)
	var shown string
	tok, err := c.DeviceLogin(context.Background(), "Iv1.clientid", func(code, uri string) { shown = code + " " + uri })
	if err != nil {
		t.Fatal(err)
	}
	if tok != gh.Token || !strings.HasPrefix(shown, "ABCD-1234") {
		t.Fatalf("tok=%q shown=%q", tok, shown)
	}
	if _, err := c.DeviceLogin(context.Background(), "", nil); err == nil {
		t.Fatal("empty client id accepted")
	}
}

func TestParseIssueRef(t *testing.T) {
	for in, want := range map[string]string{"o/r#12": "o r 12", "o/r/12": "o r 12", "o-x/r.y": "o-x r.y 0"} {
		o, r, n, err := github.ParseIssueRef(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := strings.Join([]string{o, r, itoa(n)}, " "); got != want {
			t.Errorf("%s = %s want %s", in, got, want)
		}
	}
	for _, bad := range []string{"o", "o/r#x", "o/r/0", "../r", "o/r/1/2", "o r/x"} {
		if _, _, _, err := github.ParseIssueRef(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
