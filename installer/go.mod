module github.com/org-runink/river/installer

// Standard library only. The binaries built from here (river-hwprobe, river-plan,
// river-modelpack) ship in the live ISO and on installed nodes; they take no third-party
// dependency.
go 1.24
