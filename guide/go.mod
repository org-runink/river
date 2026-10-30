module github.com/org-runink/river/guide

// Standard library, plus FIRST-PARTY modules that are themselves dependency-free.
// river-guide ships on the public install medium, so the rule this header has always
// enforced is about what travels WITH it: no third-party code, no build scripts, and no
// external-AI SDK may ever enter this module.
//
// github.com/org-runink/runi satisfies that rule rather than bending it: it is ours, it is
// BSD-3, and its own go.mod declares NO requires, so adding it adds exactly one module to
// the medium and nothing transitively. `go mod graph` for this module stays two lines.
// A third-party retry library would have failed the same test on its dependency tree alone.
go 1.24

require github.com/org-runink/runi v1.1.1
