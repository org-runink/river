// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package modelpack

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// payloadFixture writes files and returns a generic LOCK for them.
func payloadFixture(t *testing.T, kind, group string, files map[string][]byte) (dir string, lock []byte) {
	t.Helper()
	dir = t.TempDir()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nkind %s\ngroup %s\n", LockHeader, kind, group)
	for _, name := range sortedKeys(files) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, files[name], 0o644); err != nil {
			t.Fatal(err)
		}
		s := sha256.Sum256(files[name])
		fmt.Fprintf(&b, "file %d %s %s\n", len(files[name]), hex.EncodeToString(s[:]), name)
	}
	return dir, []byte(b.String())
}

func TestParseAnyLock(t *testing.T) {
	_, models := fixture(t, map[string][]byte{"a.gguf": []byte("x")})
	if _, info, err := ParseAnyLock(models); err != nil || info.Format != ModelFormat {
		t.Fatalf("models.lock: %v %+v", err, info)
	}
	_, generic := payloadFixture(t, "images", "shared", map[string][]byte{"x/a.tar": []byte("y")})
	rows, info, err := ParseAnyLock(generic)
	if err != nil || info.Format != PayloadFormat || info.Kind != "images" || info.Group != "shared" || len(rows) != 1 || rows[0].Dest != "x/a.tar" {
		t.Fatalf("generic: %v %+v %+v", err, info, rows)
	}
	sum := strings.Repeat("a", 64)
	for name, bad := range map[string]string{
		"no kind":     LockHeader + "\ngroup g\nfile 1 " + sum + " a\n",
		"no group":    LockHeader + "\nkind k\nfile 1 " + sum + " a\n",
		"no files":    LockHeader + "\nkind k\ngroup g\n",
		"bad word":    LockHeader + "\nkind K\ngroup g\nfile 1 " + sum + " a\n",
		"dotdot":      LockHeader + "\nkind k\ngroup g\nfile 1 " + sum + " ../a\n",
		"absolute":    LockHeader + "\nkind k\ngroup g\nfile 1 " + sum + " /a\n",
		"dup":         LockHeader + "\nkind k\ngroup g\nfile 1 " + sum + " a\nfile 1 " + sum + " a\n",
		"short sha":   LockHeader + "\nkind k\ngroup g\nfile 1 abc a\n",
		"unknown":     LockHeader + "\nkind k\ngroup g\nfile 1 " + sum + " a\nextra x\n",
		"twice kind":  LockHeader + "\nkind k\nkind j\ngroup g\nfile 1 " + sum + " a\n",
		"negative sz": LockHeader + "\nkind k\ngroup g\nfile -1 " + sum + " a\n",
	} {
		if _, _, err := ParseAnyLock([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPayloadPackUnpackStage(t *testing.T) {
	needZstd(t)
	files := map[string][]byte{"images-alpha.tar": randBytes(5 << 20), "extra/notes.txt": []byte("hello")}
	dir, lock := payloadFixture(t, "images", "alpha", files)
	medium := t.TempDir()
	out := filepath.Join(medium, "river-images", "alpha")
	m, err := Pack(PackOptions{Lock: lock, ModelsDir: dir, OutDir: out, Passphrase: testPass, PartSize: 2 << 20, Iterations: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != PayloadFormat || !strings.HasPrefix(m.Parts[0].Name, "payload.rmp.") || len(m.Parts) < 3 {
		t.Fatalf("manifest: %+v", m)
	}
	if err := os.WriteFile(filepath.Join(out, LockName), lock, 0o644); err != nil {
		t.Fatal(err)
	}
	// The model payload next to it is not a downstream payload: stage skips it.
	if err := os.MkdirAll(filepath.Join(medium, "river-models"), 0o755); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "payloads")
	sp, err := Stage(medium, dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp) != 1 || sp[0].Kind != "images" || sp[0].Group != "alpha" || sp[0].LockSHA256 != LockDigest(lock) {
		t.Fatalf("staged: %+v", sp)
	}
	idx, _ := os.ReadFile(filepath.Join(dest, IndexName))
	if !strings.HasPrefix(string(idx), IndexHeader+"\npayload images alpha "+LockDigest(lock)+" 2 ") {
		t.Fatalf("INDEX: %q", idx)
	}
	for _, p := range []string{dest, filepath.Join(dest, "images"), filepath.Join(dest, "images", "alpha")} {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o700 {
			t.Errorf("%s: mode %v", p, st.Mode().Perm())
		}
	}
	if st, _ := os.Stat(filepath.Join(dest, "images", "alpha", m.Parts[0].Name)); st.Mode().Perm() != 0o600 {
		t.Errorf("part mode %v", st.Mode().Perm())
	}

	// Open the STAGED copy (what a downstream tool does on the node).
	un := t.TempDir()
	staged := filepath.Join(dest, "images", "alpha")
	slock, _ := os.ReadFile(filepath.Join(staged, LockName))
	if err := Unpack(UnpackOptions{PayloadDir: staged, Lock: slock, Dest: un, Passphrase: testPass}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(un, lock, nil); err != nil {
		t.Fatal(err)
	}
	// Wrong passphrase.
	if err := Unpack(UnpackOptions{PayloadDir: staged, Lock: slock, Dest: t.TempDir(), Passphrase: testPass + "x"}); !errors.Is(err, ErrAuth) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	// The format is bound in: a models.lock with the same digest cannot open it (and the
	// lock digest check comes first anyway), and a MANIFEST relabelled as a model payload
	// fails authentication.
	relabel := t.TempDir()
	for _, e := range []string{ManifestName, LockName, m.Parts[0].Name, m.Parts[1].Name, m.Parts[2].Name} {
		b, _ := os.ReadFile(filepath.Join(staged, e))
		if e == ManifestName {
			b = bytes.Replace(b, []byte(PayloadFormat.header()), []byte(ModelFormat.header()), 1)
			b = bytes.ReplaceAll(b, []byte(PayloadFormat.PartPrefix), []byte(ModelFormat.PartPrefix))
		}
		if e != ManifestName && e != LockName {
			e = strings.Replace(e, PayloadFormat.PartPrefix, ModelFormat.PartPrefix, 1)
		}
		_ = os.WriteFile(filepath.Join(relabel, e), b, 0o600)
	}
	if rm, err := LoadManifest(relabel); err == nil {
		if err := CheckKey(relabel, rm, testPass); !errors.Is(err, ErrAuth) {
			t.Fatalf("relabelled payload opened: %v", err)
		}
	}
}

func TestStageRefuses(t *testing.T) {
	needZstd(t)
	dir, lock := payloadFixture(t, "apps", "shared", map[string][]byte{"a.bundle": randBytes(1 << 20)})
	build := func(t *testing.T) (medium, payload string) {
		medium = t.TempDir()
		payload = filepath.Join(medium, "river-apps", "shared")
		if _, err := Pack(PackOptions{Lock: lock, ModelsDir: dir, OutDir: payload, Passphrase: testPass, Iterations: 100000}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(payload, LockName), lock, 0o644); err != nil {
			t.Fatal(err)
		}
		return medium, payload
	}
	cases := map[string]func(medium, payload string){
		"flipped part byte": func(_, p string) {
			f := filepath.Join(p, "payload.rmp.000")
			b, _ := os.ReadFile(f)
			b[100] ^= 1
			_ = os.WriteFile(f, b, 0o644)
		},
		"edited LOCK": func(_, p string) {
			_ = os.WriteFile(filepath.Join(p, LockName), append(lock, '\n'), 0o644)
		},
		"extra file": func(_, p string) { _ = os.WriteFile(filepath.Join(p, "evil"), []byte("x"), 0o644) },
		"wrong group dir": func(m, p string) {
			_ = os.Rename(p, filepath.Join(m, "river-apps", "beta"))
		},
		"missing LOCK": func(_, p string) { _ = os.Remove(filepath.Join(p, LockName)) },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			medium, payload := build(t)
			spoil(medium, payload)
			if _, err := Stage(medium, filepath.Join(t.TempDir(), "d"), nil); err == nil {
				t.Fatal("staged a spoiled payload")
			}
		})
	}
	t.Run("nothing to stage", func(t *testing.T) {
		if _, err := Stage(t.TempDir(), filepath.Join(t.TempDir(), "d"), nil); err == nil {
			t.Fatal("an empty medium staged")
		}
	})
	t.Run("dest not empty", func(t *testing.T) {
		medium, _ := build(t)
		d := t.TempDir()
		_ = os.WriteFile(filepath.Join(d, "x"), nil, 0o600)
		if _, err := Stage(medium, d, nil); err == nil {
			t.Fatal("staged into a non-empty dest")
		}
	})
}

func TestNormalizeRef(t *testing.T) {
	for in, want := range map[string]string{
		"registry:2":                                    "docker.io/library/registry:2",
		"library/registry:2":                            "docker.io/library/registry:2",
		"docker.io/library/registry:2":                  "docker.io/library/registry:2",
		"bitnami/kubectl":                               "docker.io/bitnami/kubectl:latest",
		"localhost:5000/x/y:latest":                     "localhost:5000/x/y:latest",
		"localhost/y:1":                                 "localhost/y:1",
		"quay.io/k0sproject/coredns:1.11.3":             "quay.io/k0sproject/coredns:1.11.3",
		"ghcr.io/o/a@sha256:" + strings.Repeat("a", 64): "ghcr.io/o/a@sha256:" + strings.Repeat("a", 64),
	} {
		if got := NormalizeRef(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

// dockerArchive builds a minimal docker-archive with one image per entry.
func dockerArchive(t *testing.T, images map[string][][]byte, corruptLayer bool) (path string, digests map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, b []byte) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(b)
	}
	digests = map[string]string{}
	var man []dockerManifestEntry
	for _, ref := range func() []string {
		var k []string
		for r := range images {
			k = append(k, r)
		}
		return k
	}() {
		var cfg imageConfig
		var layers []string
		for _, l := range images[ref] {
			s := sha256.Sum256(l)
			h := hex.EncodeToString(s[:])
			cfg.RootFS.DiffIDs = append(cfg.RootFS.DiffIDs, "sha256:"+h)
			if corruptLayer {
				l = append([]byte{1}, l...)
			}
			add(h+".tar", l)
			layers = append(layers, h+".tar")
		}
		cb, _ := json.Marshal(cfg)
		cs := sha256.Sum256(cb)
		ch := hex.EncodeToString(cs[:])
		add(ch+".json", cb)
		digests[ref] = "sha256:" + ch
		man = append(man, dockerManifestEntry{Config: ch + ".json", RepoTags: []string{ref}, Layers: layers})
	}
	mb, _ := json.Marshal(man)
	add("manifest.json", mb)
	_ = tw.Close()
	path = filepath.Join(t.TempDir(), "a.tar")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, digests
}

func TestCheckImageArchive(t *testing.T) {
	imgs := map[string][][]byte{
		"localhost:5000/x/a:latest": {[]byte("layer-1"), []byte("layer-2")},
		"registry:2":                {[]byte("layer-3")},
	}
	p, d := dockerArchive(t, imgs, false)
	want := []ExpectedImage{{"localhost:5000/x/a:latest", d["localhost:5000/x/a:latest"]}, {"docker.io/library/registry:2", d["registry:2"]}}
	if _, err := CheckImageArchive(p, want); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckImageArchive(p, want[:1]); err == nil {
		t.Error("an unexpected extra image passed")
	}
	bad := []ExpectedImage{want[0], {want[1].Ref, "sha256:" + strings.Repeat("0", 64)}}
	if _, err := CheckImageArchive(p, bad); err == nil {
		t.Error("a wrong config digest passed")
	}
	missing := append(append([]ExpectedImage{}, want...), ExpectedImage{"localhost:5000/x/b:1", d["registry:2"]})
	if _, err := CheckImageArchive(p, missing); err == nil {
		t.Error("a missing image passed")
	}
	pc, _ := dockerArchive(t, imgs, true)
	if _, err := CheckImageArchive(pc, want); err == nil {
		t.Error("a corrupted layer passed")
	}
	if _, err := CheckImageArchive(p, nil); err == nil {
		t.Error("an empty expectation passed")
	}
	if _, err := ParseExpected(strings.NewReader("x sha256:abc\n")); err == nil {
		t.Error("a short digest parsed")
	}
}
