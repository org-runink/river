// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package modelpack

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testPass = "correct horse battery staple, twice"

// fixture writes files into a models dir and returns a matching lock.
func fixture(t *testing.T, files map[string][]byte) (dir string, lock []byte) {
	t.Helper()
	dir = t.TempDir()
	var b strings.Builder
	b.WriteString("# test lock\n")
	for _, name := range sortedKeys(files) {
		data := files[name]
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		s := sha256.Sum256(data)
		fmt.Fprintf(&b, "role org/repo %s file.bin %d %s mit %s\n", strings.Repeat("a", 40), len(data), hex.EncodeToString(s[:]), name)
	}
	return dir, []byte(b.String())
}

func sortedKeys(m map[string][]byte) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	for i := range k {
		for j := i + 1; j < len(k); j++ {
			if k[j] < k[i] {
				k[i], k[j] = k[j], k[i]
			}
		}
	}
	return k
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func needZstd(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not installed")
	}
}

func TestParseLock(t *testing.T) {
	good := "coder org/r " + strings.Repeat("0", 40) + " a/b.gguf 3 " + strings.Repeat("f", 64) + " mit -\n"
	rows, err := ParseLock(strings.NewReader(good))
	if err != nil || len(rows) != 1 || rows[0].Dest != "b.gguf" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	for name, lock := range map[string]string{
		"7 columns":   "a b c d 1 " + strings.Repeat("f", 64) + " mit\n",
		"bad sha":     "a b c d 1 XYZ mit x\n",
		"traversal":   "a b c d 1 " + strings.Repeat("f", 64) + " mit ../etc/passwd\n",
		"absolute":    "a b c d 1 " + strings.Repeat("f", 64) + " mit /etc/passwd\n",
		"unclean":     "a b c d 1 " + strings.Repeat("f", 64) + " mit a//b\n",
		"duplicate":   good + good,
		"empty":       "# nothing\n",
		"negative sz": "a b c d -1 " + strings.Repeat("f", 64) + " mit x\n",
	} {
		if _, err := ParseLock(strings.NewReader(lock)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The STREAM framing must round-trip every boundary case and reject tampering.
func TestSealOpen(t *testing.T) {
	aead, err := DeriveKey(testPass, bytes.Repeat([]byte{1}, saltLen), 100000)
	if err != nil {
		t.Fatal(err)
	}
	prefix := bytes.Repeat([]byte{2}, prefixLen)
	ad := aad([]byte("salt"))
	seal := func(pt []byte) []byte {
		var ct bytes.Buffer
		w := NewSealWriter(&ct, aead, prefix, ad)
		// Uneven writes, to exercise the hold-back buffer.
		for p := pt; len(p) > 0; {
			n := min(len(p), 1_000_003)
			w.Write(p[:n])
			p = p[n:]
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return ct.Bytes()
	}
	open := func(ct []byte) ([]byte, error) {
		return io.ReadAll(NewOpenReader(bytes.NewReader(ct), aead, prefix, ad))
	}
	for _, n := range []int{0, 1, ChunkSize - 1, ChunkSize, ChunkSize + 1, 2 * ChunkSize, 3*ChunkSize + 17} {
		pt := randBytes(n)
		ct := seal(pt)
		got, err := open(ct)
		if err != nil || !bytes.Equal(got, pt) {
			t.Fatalf("n=%d: round trip failed: %v", n, err)
		}
		if n >= ChunkSize+1 {
			// Truncation at a chunk boundary: the new last chunk was sealed as not-last.
			if _, err := open(ct[:ChunkSize+tagLen]); err == nil {
				t.Errorf("n=%d: truncated stream accepted", n)
			}
			// Reordering the first two chunks.
			if n >= 2*ChunkSize+1 {
				swapped := append(append(append([]byte{}, ct[ChunkSize+tagLen:2*(ChunkSize+tagLen)]...), ct[:ChunkSize+tagLen]...), ct[2*(ChunkSize+tagLen):]...)
				if _, err := open(swapped); err == nil {
					t.Errorf("n=%d: reordered stream accepted", n)
				}
			}
		}
		// Appended garbage after the last chunk.
		if _, err := open(append(append([]byte{}, ct...), 0)); err == nil {
			t.Errorf("n=%d: trailing byte accepted", n)
		}
		// A flipped bit anywhere.
		if len(ct) > 0 {
			bad := append([]byte{}, ct...)
			bad[len(bad)/2] ^= 0x40
			if _, err := open(bad); !errors.Is(err, ErrAuth) {
				t.Errorf("n=%d: bit flip gave %v, want ErrAuth", n, err)
			}
		}
	}
	if _, err := open(nil); err == nil {
		t.Error("empty stream accepted")
	}
}

func TestManifestRoundTrip(t *testing.T) {
	m := &Manifest{Iterations: DefaultIterations, Salt: bytes.Repeat([]byte{7}, saltLen),
		Prefix: bytes.Repeat([]byte{9}, prefixLen), ChunkSize: ChunkSize,
		LockSHA256: strings.Repeat("ab", 32), Files: 3, Bytes: 42,
		Parts: []Part{{partName(0), 10, strings.Repeat("c", 64)}, {partName(1), 5, strings.Repeat("d", 64)}}}
	got, err := ParseManifest(m.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Marshal(), m.Marshal()) {
		t.Fatalf("round trip differs:\n%s\n%s", got.Marshal(), m.Marshal())
	}
	for _, mut := range []string{"kdf pbkdf2-sha256 10 ", "part models.rmp.005", "extra line\n", "compression gzip"} {
		s := string(m.Marshal())
		switch {
		case strings.HasPrefix(mut, "kdf"):
			s = strings.Replace(s, fmt.Sprintf("kdf pbkdf2-sha256 %d ", DefaultIterations), mut, 1)
		case strings.HasPrefix(mut, "part"):
			s = strings.Replace(s, "part models.rmp.001", mut, 1)
		case strings.HasPrefix(mut, "compression"):
			s = strings.Replace(s, "compression zstd", mut, 1)
		default:
			s += mut
		}
		if _, err := ParseManifest([]byte(s)); err == nil {
			t.Errorf("mutation %q accepted", mut)
		}
	}
}

func TestPackUnpack(t *testing.T) {
	needZstd(t)
	files := map[string][]byte{
		"big.gguf":          randBytes(3*ChunkSize + 5),
		"voice/a.onnx":      bytes.Repeat([]byte("compressible "), 200000),
		"voice/a.onnx.json": []byte(`{"x":1}`),
		"empty.txt":         {},
	}
	models, lock := fixture(t, files)
	out := filepath.Join(t.TempDir(), "payload")
	m, err := Pack(PackOptions{Lock: lock, ModelsDir: models, OutDir: out, Passphrase: testPass,
		PartSize: ChunkSize + 1000, Iterations: 100000, ZstdLevel: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Parts) < 2 {
		t.Fatalf("expected several parts, got %d", len(m.Parts))
	}
	if err := CheckParts(out, m); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := Unpack(UnpackOptions{PayloadDir: out, Lock: lock, Dest: dest, Passphrase: testPass}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest, lock, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		got, _ := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if !bytes.Equal(got, data) {
			t.Errorf("%s differs after unpack", name)
		}
	}

	if err := CheckKey(out, m, testPass); err != nil {
		t.Errorf("CheckKey with the right passphrase: %v", err)
	}
	if err := CheckKey(out, m, testPass+"x"); !errors.Is(err, ErrAuth) {
		t.Errorf("CheckKey with a wrong passphrase: %v", err)
	}
	// Wrong passphrase: refused before anything is written.
	d2 := t.TempDir()
	if err := Unpack(UnpackOptions{PayloadDir: out, Lock: lock, Dest: d2, Passphrase: testPass + "x"}); !errors.Is(err, ErrAuth) {
		t.Errorf("wrong passphrase gave %v, want ErrAuth", err)
	}
	if ents, _ := os.ReadDir(d2); len(ents) != 0 {
		t.Error("wrong passphrase wrote files")
	}
	// A different lock: refused.
	if err := Unpack(UnpackOptions{PayloadDir: out, Lock: append(lock, '\n'), Dest: t.TempDir(), Passphrase: testPass}); err == nil {
		t.Error("payload opened against a different lock")
	}
	// A non-empty destination: refused.
	if err := Unpack(UnpackOptions{PayloadDir: out, Lock: lock, Dest: dest, Passphrase: testPass}); err == nil {
		t.Error("unpacked into a non-empty directory")
	}
	// A damaged part: CheckParts and Unpack both fail.
	p1 := filepath.Join(out, m.Parts[1].Name)
	b, _ := os.ReadFile(p1)
	b[100] ^= 1
	os.WriteFile(p1, b, 0o644)
	if err := CheckParts(out, m); !errors.Is(err, ErrMedium) {
		t.Errorf("damaged part: CheckParts gave %v", err)
	}
	if err := Unpack(UnpackOptions{PayloadDir: out, Lock: lock, Dest: t.TempDir(), Passphrase: testPass}); err == nil {
		t.Error("damaged part unpacked")
	}
}

func TestPackRefusesMismatch(t *testing.T) {
	needZstd(t)
	models, lock := fixture(t, map[string][]byte{"a.bin": []byte("hello")})
	// Same size, different bytes: caught while streaming, and the output is removed.
	os.WriteFile(filepath.Join(models, "a.bin"), []byte("jello"), 0o644)
	out := filepath.Join(t.TempDir(), "p")
	if _, err := Pack(PackOptions{Lock: lock, ModelsDir: models, OutDir: out, Passphrase: testPass, Iterations: 100000}); err == nil {
		t.Fatal("pack accepted a file that does not match the lock")
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Errorf("failed pack left %d file(s)", len(ents))
	}
	// Short passphrase.
	if _, err := Pack(PackOptions{Lock: lock, ModelsDir: models, OutDir: t.TempDir(), Passphrase: "short", Iterations: 100000}); err == nil {
		t.Error("short passphrase accepted")
	}
}

func TestVerifyDetects(t *testing.T) {
	models, lock := fixture(t, map[string][]byte{"a.bin": []byte("hello"), "d/b.bin": []byte("world")})
	if err := Verify(models, lock, nil); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(models, "d", "b.bin"))
	if err := Verify(models, lock, nil); err == nil {
		t.Error("missing file not detected")
	}
}

func TestGeneratePassphrase(t *testing.T) {
	p, err := GeneratePassphrase()
	if err != nil || !isHex(p, 64) {
		t.Fatalf("%q %v", p, err)
	}
}

// The shipped models.lock must parse, and must carry no row for a tier models.tiers marks
// `pending=<tier>`: a pinned file no engine serves only makes every image and payload larger.
func TestShippedLock(t *testing.T) {
	tiers, err := os.ReadFile(filepath.Join("..", "..", "..", "models.tiers"))
	if err != nil {
		t.Fatal(err)
	}
	pending := map[string]bool{}
	for _, l := range strings.Split(string(tiers), "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(l), "pending="); ok {
			pending[p] = true
		}
	}
	alias := map[string]string{"voice": "stt", "embed": "embedding"}
	f, err := os.Open(filepath.Join("..", "..", "..", "models.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := ParseLock(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("shipped models.lock has no rows")
	}
	for _, r := range rows {
		role := r.Role
		if a, ok := alias[role]; ok {
			role = a
		}
		if pending[role] {
			t.Errorf("%s row %s %s while models.tiers marks the tier pending", role, r.Repo, r.File)
		}
	}
}
