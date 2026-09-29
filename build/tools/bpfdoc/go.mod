module github.com/org-runink/river/build/tools/bpfdoc

// Standard library only. bpfdoc replaces the kernel's scripts/bpf_doc.py inside the
// linux-runink build (build/pkgbuilds/runink-kernel), so the kernel builds with no Python
// interpreter. It takes no third-party dependency.
go 1.22
