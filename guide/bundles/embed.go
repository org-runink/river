// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package bundles embeds the built-in guide bundle: the public Runink River install guide.
//
// The markdown under river/ is the single source. It is compiled into river-guide AND
// installed read-only on the medium (/usr/share/river-guide/bundles/river/) so an
// operator can page it with `less` when the guide itself is not running.
package bundles

import (
	"embed"

	"github.com/org-runink/river/guide/bundle"
)

//go:embed river/*.md
var riverFS embed.FS

// Version of the built-in guide; bump when its steps change.
const Version = "2026.09.25"

// River returns the parsed built-in bundle, the public Runink River install guide.
func River() (*bundle.Bundle, error) {
	return bundle.LoadFS(riverFS, "river", "river", Version)
}
