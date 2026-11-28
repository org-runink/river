// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActivityKeepsAndAppends(t *testing.T) {
	f := filepath.Join(t.TempDir(), "installer.log")
	a := NewActivity(f)
	a.now = func() time.Time { return time.Date(2026, 11, 28, 9, 30, 0, 0, time.UTC) }
	for i := 0; i < activityMax+5; i++ {
		a.Add("line")
	}
	if n := len(a.Lines()); n != activityMax {
		t.Errorf("kept %d lines, want %d", n, activityMax)
	}
	if got := a.Lines()[0]; got != "09:30:00  line" {
		t.Errorf("line = %q", got)
	}
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "\n") != activityMax+5 {
		t.Errorf("the file must keep every line")
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
		t.Errorf("installer.log mode %v, want 0600", fi.Mode().Perm())
	}
	var nilA *Activity
	nilA.Add("ignored")
	if nilA.Lines() != nil {
		t.Error("a nil Activity has no lines")
	}
}

// /api/activity returns the backend's log for the details pane.
func TestActivityEndpoint(t *testing.T) {
	a := NewActivity("")
	a.Add("probe/plan: started")
	w := New(Config{StateFile: filepath.Join(t.TempDir(), "state.json"), LogFile: filepath.Join(t.TempDir(), "i.log"), Activity: a},
		&FakeSystem{})
	h := w.Handler(Guard{Port: 0})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	req.Host = "[::1]:0"
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Activity []string `json:"activity"`
		Install  []string `json:"install"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Activity) != 1 || !strings.HasSuffix(out.Activity[0], "probe/plan: started") {
		t.Errorf("activity = %q", out.Activity)
	}
}
