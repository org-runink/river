module github.com/org-runink/river/bench/analytics

// Standard library, plus FIRST-PARTY modules that are themselves dependency-free.
// riverbench measures a kernel, so the rule this header has always enforced is that
// nothing it drags along can perturb the machine under measurement: no third-party
// code and no build scripts on the box.
//
// github.com/org-runink/runi satisfies that rule rather than bending it. It is ours,
// BSD-3, pure Go with no cgo and no generate step, and its own go.mod declares NO
// requires — `go mod graph` for this module stays two lines, so the closure installed
// beside the kernel grows by exactly one module and nothing transitively.
//
// What it buys is the opposite of a risk to the numbers: internal/stats carried its
// own median, and riverbench ended up with THREE that disagreed on an even count
// (river#47). runi's is R type-7, identical to what this module already computed, so
// the published figures are unchanged and there is now one definition to disagree with.
go 1.24

require github.com/org-runink/runi v1.1.1
