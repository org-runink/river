// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package modelpack

// CheckImageArchive verifies a docker-archive (the format of `podman save
// --multi-image-archive` and `docker save`, which containerd, and so k0s's
// /var/lib/k0s/images/ bundle import, loads as is) against an expected list of
// "reference config-digest" pairs, reading the archive once:
//
//   - every expected reference is a RepoTag of an image whose config hashes to the
//     expected digest (the image ID, which survives a docker-archive round trip);
//   - every image in the archive is expected (no extra image rides along);
//   - every layer file an image names is in the archive, and the layers' sha256 are the
//     config's rootfs.diff_ids, in order.
//
// References are compared in their normalised form: "registry:2",
// "library/registry:2" and "docker.io/library/registry:2" are the same image, which is
// how containerd names them on import.

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// ExpectedImage is one row of the list CheckImageArchive checks against.
type ExpectedImage struct {
	Ref          string
	ConfigDigest string // sha256:<64 hex>
}

// ArchiveImage is one image found in an archive.
type ArchiveImage struct {
	ConfigDigest string
	RepoTags     []string
	Layers       int
}

// NormalizeRef returns the containerd form of an image reference: a registry host is
// added when there is none (docker.io), docker.io's single-element names get library/,
// and a reference without tag or digest gets :latest.
func NormalizeRef(ref string) string {
	name, rest := ref, ""
	if i := strings.Index(name, "@"); i >= 0 {
		name, rest = name[:i], name[i:]
	} else if j := strings.LastIndex(name, ":"); j > strings.LastIndex(name, "/") {
		name, rest = name[:j], name[j:]
	}
	if rest == "" {
		rest = ":latest"
	}
	first, _, hasSlash := strings.Cut(name, "/")
	if !hasSlash || !(strings.ContainsAny(first, ".:") || first == "localhost") {
		name = "docker.io/" + name
	}
	if strings.HasPrefix(name, "docker.io/") && !strings.Contains(strings.TrimPrefix(name, "docker.io/"), "/") {
		name = "docker.io/library/" + strings.TrimPrefix(name, "docker.io/")
	}
	return name + rest
}

type dockerManifestEntry struct {
	Config   string
	RepoTags []string
	Layers   []string
}

type imageConfig struct {
	RootFS struct {
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

// CheckImageArchive reads the archive at p once and checks it against want (see above).
// It returns the images found, for reporting.
func CheckImageArchive(p string, want []ExpectedImage) ([]ArchiveImage, error) {
	if len(want) == 0 {
		return nil, fmt.Errorf("imagecheck: no expected images given")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sums := map[string]string{}  // entry name -> sha256 hex
	small := map[string][]byte{} // json entries, kept for parsing
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("imagecheck: %s: %v", p, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(hdr.Name)
		h := sha256.New()
		var w io.Writer = h
		var buf strings.Builder
		keep := strings.HasSuffix(name, ".json") && hdr.Size <= 8<<20
		if keep {
			w = io.MultiWriter(h, &buf)
		}
		if _, err := io.CopyN(w, tr, hdr.Size); err != nil { // bounded by the entry's declared size
			return nil, fmt.Errorf("imagecheck: %s: %s: %v", p, name, err)
		}
		sums[name] = hex.EncodeToString(h.Sum(nil))
		if keep {
			small[name] = []byte(buf.String())
		}
	}
	raw, ok := small["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("imagecheck: %s: no manifest.json (not a docker-archive)", p)
	}
	var entries []dockerManifestEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("imagecheck: %s: manifest.json: %v", p, err)
	}
	byRef := map[string]string{} // normalised ref -> config digest
	var found []ArchiveImage
	for _, e := range entries {
		cfgName := path.Clean(e.Config)
		sum, ok := sums[cfgName]
		if !ok {
			return nil, fmt.Errorf("imagecheck: %s: config %s missing", p, e.Config)
		}
		digest := "sha256:" + sum
		var cfg imageConfig
		if err := json.Unmarshal(small[cfgName], &cfg); err != nil {
			return nil, fmt.Errorf("imagecheck: %s: config %s: %v", p, e.Config, err)
		}
		if len(cfg.RootFS.DiffIDs) != len(e.Layers) {
			return nil, fmt.Errorf("imagecheck: %s: image %s names %d layers, its config %d", p, digest, len(e.Layers), len(cfg.RootFS.DiffIDs))
		}
		for i, l := range e.Layers {
			ls, ok := sums[path.Clean(l)]
			if !ok {
				return nil, fmt.Errorf("imagecheck: %s: image %s: layer %s missing", p, digest, l)
			}
			if "sha256:"+ls != cfg.RootFS.DiffIDs[i] {
				return nil, fmt.Errorf("imagecheck: %s: image %s: layer %s does not match diff_id %s", p, digest, l, cfg.RootFS.DiffIDs[i])
			}
		}
		if len(e.RepoTags) == 0 {
			return nil, fmt.Errorf("imagecheck: %s: image %s has no RepoTags (it would import unnamed)", p, digest)
		}
		for _, t := range e.RepoTags {
			byRef[NormalizeRef(t)] = digest
		}
		found = append(found, ArchiveImage{ConfigDigest: digest, RepoTags: e.RepoTags, Layers: len(e.Layers)})
	}
	expected := map[string]bool{}
	for _, w := range want {
		n := NormalizeRef(w.Ref)
		expected[n] = true
		got, ok := byRef[n]
		if !ok {
			return nil, fmt.Errorf("imagecheck: %s: %s is not in the archive", p, w.Ref)
		}
		if got != w.ConfigDigest {
			return nil, fmt.Errorf("imagecheck: %s: %s has config %s, expected %s", p, w.Ref, got, w.ConfigDigest)
		}
	}
	for ref := range byRef {
		if !expected[ref] {
			return nil, fmt.Errorf("imagecheck: %s: %s is in the archive but not expected", p, ref)
		}
	}
	return found, nil
}

// ParseExpected reads "ref config-digest" lines (# comments and blank lines ignored).
func ParseExpected(r io.Reader) ([]ExpectedImage, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []ExpectedImage
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) != 2 || !strings.HasPrefix(f[1], "sha256:") || !isHex(strings.TrimPrefix(f[1], "sha256:"), 64) {
			return nil, fmt.Errorf("refs:%d: expected \"<ref> sha256:<64 hex>\"", i+1)
		}
		out = append(out, ExpectedImage{Ref: f[0], ConfigDigest: f[1]})
	}
	return out, nil
}
