module github.com/org-runink/river/pkg

// Standard library only. Runink River's internals, and any downstream image built on them,
// import these packages instead of writing shell: pipe (the Go pipeline pattern over
// os/exec, io and channels) and fsx (the file operations shell scripts used cp/install/mv for).
go 1.24
