// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
)

// The same binary installs every edition a medium carries, so the UI names the product from
// the running edition's descriptor, never a hardcoded title: the window title and header
// ("app.title") take {edition} in every catalog, and the page shell names no product.
func TestUINamesTheRunningEdition(t *testing.T) {
	cats, err := fs.Glob(webFS, "web/i18n/*.json")
	if err != nil || len(cats) == 0 {
		t.Fatalf("no catalogs embedded (%v)", err)
	}
	for _, c := range cats {
		b, err := webFS.ReadFile(c)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if !strings.Contains(m["app.title"], "{edition}") {
			t.Errorf("%s: app.title %q does not take {edition}", c, m["app.title"])
		}
	}
	shell, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shell), "Runink River") {
		t.Error("web/index.html hardcodes a product name; the edition title fills it in")
	}
}
