// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package modelpack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// appendFiles is a set with multi-chunk, part-crossing, tiny and empty files.
func appendFiles() map[string][]byte {
	return map[string][]byte{
		"a/big.gguf":        randBytes(3*ChunkSize + 12345),
		"b/voice.onnx":      bytes.Repeat([]byte("compressible "), 400000),
		"b/voice.onnx.json": []byte(`{"x":1}`),
		"c/empty.txt":       {},
		"d/mid.bin":         randBytes(ChunkSize - 3),
		"e/last.safetensor": randBytes(2*ChunkSize + 7),
	}
}

func appendOpts(lock []byte, models, out string) AppendOptions {
	return AppendOptions{Lock: lock, ModelsDir: models, OutDir: out, Passphrase: testPass,
		PartSize: 3*ChunkSize + 999, Iterations: 100000, ZstdLevel: 3, Consume: true}
}

// stageInto writes the named files of set into dir (as a fetcher would, one batch at a time).
func stageInto(t *testing.T, dir string, set map[string][]byte, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, set[n], 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// emptyModels returns the fixture's lock with an empty models dir (nothing fetched yet).
func emptyModels(t *testing.T, set map[string][]byte) (models string, lock []byte) {
	t.Helper()
	src, lock := fixture(t, set)
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	models = t.TempDir()
	return models, lock
}

func unpackAndCompare(t *testing.T, out string, lock []byte, set map[string][]byte) {
	t.Helper()
	m, err := LoadManifest(out)
	if err != nil {
		t.Fatalf("finished payload: %v", err)
	}
	if err := CheckParts(out, m); err != nil {
		t.Fatal(err)
	}
	if err := CheckKey(out, m, testPass); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Parts {
		if p.Size >= 2<<30 {
			t.Errorf("part %s is %d bytes (>= 2 GiB)", p.Name, p.Size)
		}
	}
	dest := t.TempDir()
	if err := Unpack(UnpackOptions{PayloadDir: out, Lock: lock, Dest: dest, Passphrase: testPass}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest, lock, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range set {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s differs after unpack (%v)", name, err)
		}
	}
	if err := VerifyPayload(out, lock, testPass, nil); err != nil {
		t.Errorf("VerifyPayload: %v", err)
	}
	ents, _ := os.ReadDir(out)
	for _, e := range ents {
		if e.Name() != ManifestName && !strings.HasPrefix(e.Name(), PartPrefix) {
			t.Errorf("stray file %s in the finished payload", e.Name())
		}
	}
}

func TestAppendIncrementalConsume(t *testing.T) {
	needZstd(t)
	set := appendFiles()
	models, lock := emptyModels(t, set)
	out := filepath.Join(t.TempDir(), "river-models")

	// Nothing fetched: the first call creates the journal and packs nothing.
	r, err := Append(appendOpts(lock, models, out))
	if err != nil {
		t.Fatal(err)
	}
	if r.Complete || len(r.Packed) != 0 || len(r.Remaining) != len(set) {
		t.Fatalf("empty models dir: %+v", r)
	}
	batches := [][]string{{"b/voice.onnx", "c/empty.txt"}, {"a/big.gguf"}, {"d/mid.bin", "b/voice.onnx.json"}, {"e/last.safetensor"}}
	packed := 0
	for i, batch := range batches {
		stageInto(t, models, set, batch...)
		r, err := Append(appendOpts(lock, models, out))
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		packed += len(batch)
		if len(r.Packed) != len(batch) || len(r.Consumed) != len(batch) {
			t.Fatalf("batch %d: packed %v consumed %v", i, r.Packed, r.Consumed)
		}
		for _, n := range batch {
			if _, err := os.Stat(filepath.Join(models, n)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("batch %d: %s not consumed", i, n)
			}
		}
		last := i == len(batches)-1
		if r.Complete != last {
			t.Fatalf("batch %d: complete=%v", i, r.Complete)
		}
		pend, err := Pending(out, lock)
		if err != nil {
			t.Fatal(err)
		}
		if len(pend) != len(set)-packed {
			t.Errorf("batch %d: Pending = %v", i, pend)
		}
		if !last {
			// A partial payload is refused by everything that opens or reuses one.
			if _, err := LoadManifest(out); !errors.Is(err, ErrIncomplete) {
				t.Errorf("batch %d: LoadManifest of a journal gave %v, want ErrIncomplete", i, err)
			}
			if err := Unpack(UnpackOptions{PayloadDir: out, Lock: lock, Dest: t.TempDir(), Passphrase: testPass}); !errors.Is(err, ErrIncomplete) {
				t.Errorf("batch %d: Unpack of a journal gave %v", i, err)
			}
			b, _ := os.ReadFile(filepath.Join(out, ManifestName))
			if !bytes.HasPrefix(b, []byte("river-modelpack 1\nincomplete\n")) {
				t.Errorf("batch %d: journal not marked incomplete:\n%s", i, b)
			}
			if !bytes.Contains(b, []byte("lock-sha256 "+LockDigest(lock)+"\n")) {
				t.Errorf("batch %d: journal does not carry the full lock's digest", i)
			}
		}
	}
	m, err := LoadManifest(out)
	if err != nil {
		t.Fatal(err)
	}
	if m.LockSHA256 != LockDigest(lock) || m.Files != len(set) {
		t.Errorf("finished MANIFEST: lock %s files %d", m.LockSHA256, m.Files)
	}
	if len(m.Parts) < 3 {
		t.Errorf("expected several parts, got %d", len(m.Parts))
	}
	unpackAndCompare(t, out, lock, set)

	// Appending to a finished payload is a no-op (and consumes a re-fetched copy).
	stageInto(t, models, set, "c/empty.txt")
	before, _ := os.ReadFile(filepath.Join(out, ManifestName))
	r, err = Append(appendOpts(lock, models, out))
	if err != nil || !r.Complete || len(r.Packed) != 0 || len(r.Consumed) != 1 {
		t.Fatalf("append to a finished payload: %+v %v", r, err)
	}
	after, _ := os.ReadFile(filepath.Join(out, ManifestName))
	if !bytes.Equal(before, after) {
		t.Error("append to a finished payload changed its MANIFEST")
	}
}

// Every lock row fetched at once: one call finishes, and the result matches a one-shot pack in
// meaning (same MANIFEST header lines, all files unpack).
func TestAppendOneCallEqualsPack(t *testing.T) {
	needZstd(t)
	set := appendFiles()
	models, lock := fixture(t, set)
	out := filepath.Join(t.TempDir(), "p")
	o := appendOpts(lock, models, out)
	o.Consume = false
	r, err := Append(o)
	if err != nil || !r.Complete {
		t.Fatalf("%+v %v", r, err)
	}
	unpackAndCompare(t, out, lock, set)
	if _, err := os.Stat(filepath.Join(models, "a/big.gguf")); err != nil {
		t.Error("a source was deleted without --consume")
	}
}

func TestAppendResumesAfterInterruption(t *testing.T) {
	needZstd(t)
	set := appendFiles()
	models, lock := emptyModels(t, set)
	out := filepath.Join(t.TempDir(), "p")
	stageInto(t, models, set, "a/big.gguf")
	if _, err := Append(appendOpts(lock, models, out)); err != nil {
		t.Fatal(err)
	}
	journal, _ := os.ReadFile(filepath.Join(out, ManifestName))
	m, err := parseManifest(journal)
	if err != nil || !m.Incomplete || len(m.Parts) == 0 {
		t.Fatalf("journal: %+v %v", m, err)
	}
	// An interrupted call: bytes after the recorded end of the last part, a part the journal
	// does not name, and a journal that was never renamed into place.
	lastPart := filepath.Join(out, m.Parts[len(m.Parts)-1].Name)
	f, _ := os.OpenFile(lastPart, os.O_WRONLY|os.O_APPEND, 0)
	f.Write(randBytes(5000))
	f.Close()
	os.WriteFile(filepath.Join(out, partName(len(m.Parts))), randBytes(100), 0o644)
	os.WriteFile(filepath.Join(out, journalTmp), []byte("garbage"), 0o644)

	stageInto(t, models, set, "b/voice.onnx", "b/voice.onnx.json", "c/empty.txt", "d/mid.bin", "e/last.safetensor")
	r, err := Append(appendOpts(lock, models, out))
	if err != nil || !r.Complete {
		t.Fatalf("resume: %+v %v", r, err)
	}
	unpackAndCompare(t, out, lock, set)
}

func TestAppendKeepsSourcesUntilRecorded(t *testing.T) {
	needZstd(t)
	set := appendFiles()
	models, lock := emptyModels(t, set)
	out := filepath.Join(t.TempDir(), "p")
	o := appendOpts(lock, models, out)
	// Packed without --consume (as if a call stopped between recording and deleting).
	o.Consume = false
	stageInto(t, models, set, "a/big.gguf")
	if _, err := Append(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(models, "a/big.gguf")); err != nil {
		t.Fatal("source deleted without --consume")
	}
	// The next --consume call deletes the leftover of an already-packed row.
	o.Consume = true
	r, err := Append(o)
	if err != nil || !slices.Equal(r.Consumed, []string{"a/big.gguf"}) || len(r.Packed) != 0 {
		t.Fatalf("leftover consume: %+v %v", r, err)
	}
}

func TestAppendRefuses(t *testing.T) {
	needZstd(t)
	set := appendFiles()
	models, lock := emptyModels(t, set)
	out := filepath.Join(t.TempDir(), "p")
	stageInto(t, models, set, "a/big.gguf")
	if _, err := Append(appendOpts(lock, models, out)); err != nil {
		t.Fatal(err)
	}
	journal, _ := os.ReadFile(filepath.Join(out, ManifestName))

	// A wrong passphrase would seal the rest under another key.
	stageInto(t, models, set, "d/mid.bin")
	o := appendOpts(lock, models, out)
	o.Passphrase = testPass + "x"
	if _, err := Append(o); !errors.Is(err, ErrAuth) {
		t.Errorf("wrong passphrase: %v", err)
	}
	// Another lock.
	if _, err := Append(appendOpts(append(slices.Clone(lock), '\n'), models, out)); err == nil || !strings.Contains(err.Error(), "different lock") {
		t.Errorf("different lock: %v", err)
	}
	if now, _ := os.ReadFile(filepath.Join(out, ManifestName)); !bytes.Equal(now, journal) {
		t.Error("a refused append changed the journal")
	}
	if _, err := os.Stat(filepath.Join(models, "d/mid.bin")); err != nil {
		t.Error("a refused append consumed a source")
	}

	// A file of the right size with the wrong bytes: skipped, never consumed.
	bad := slices.Clone(set["e/last.safetensor"])
	bad[0] ^= 1
	stageInto(t, models, map[string][]byte{"e/last.safetensor": bad}, "e/last.safetensor")
	r, err := Append(appendOpts(lock, models, out))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.Skipped, "e/last.safetensor") || slices.Contains(r.Packed, "e/last.safetensor") {
		t.Errorf("mismatching file: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(models, "e/last.safetensor")); err != nil {
		t.Error("a mismatching file was consumed")
	}

	// A directory that holds something else.
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "x"), []byte("x"), 0o644)
	if _, err := Append(appendOpts(lock, models, other)); err == nil {
		t.Error("appended into a non-empty directory without a MANIFEST")
	}
	// A one-shot pack's directory is not a journal: a finished payload of ANOTHER lock.
	full := filepath.Join(t.TempDir(), "full")
	fm, flock := fixture(t, map[string][]byte{"z.bin": []byte("zzz")})
	if _, err := Pack(PackOptions{Lock: flock, ModelsDir: fm, OutDir: full, Passphrase: testPass, Iterations: 100000, ZstdLevel: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(appendOpts(lock, models, full)); err == nil {
		t.Error("appended into a payload of another lock")
	}
}

func TestJournalParsing(t *testing.T) {
	m := &Manifest{Iterations: 100000, Salt: make([]byte, saltLen), Prefix: make([]byte, prefixLen), ChunkSize: ChunkSize,
		LockSHA256: strings.Repeat("a", 64), Files: 3, Bytes: 10, Incomplete: true, Packed: []string{"x/a", "b"}}
	b := m.Marshal()
	if _, err := ParseManifest(b); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("ParseManifest(journal) = %v", err)
	}
	got, err := parseManifest(b)
	if err != nil || !got.Incomplete || !slices.Equal(got.Packed, m.Packed) || len(got.Parts) != 0 {
		t.Fatalf("journal round trip: %+v %v", got, err)
	}
	for _, bad := range []string{
		strings.Replace(string(b), "incomplete\n", "", 1),                       // packed without incomplete
		strings.Replace(string(b), "packed b\n", "packed x/a\n", 1),             // repeated
		strings.Replace(string(b), "packed b\n", "packed ../b\n", 1),            // not a clean path
		strings.Replace(string(b), "incomplete\n", "", 1) + "incomplete\n",      // not under the header
		strings.Replace(string(b), "incomplete\nkdf", "incomplete now\nkdf", 1), // extra field
	} {
		if _, err := parseManifest([]byte(bad)); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
	// A complete MANIFEST still needs its parts.
	m.Incomplete, m.Packed = false, nil
	if _, err := ParseManifest(m.Marshal()); err == nil {
		t.Error("a complete MANIFEST without parts was accepted")
	}
}

func TestPadAndSealOpen(t *testing.T) {
	for _, n := range []int{1, ChunkSize - 8, ChunkSize - 7, ChunkSize - 1, ChunkSize, ChunkSize + 1, 2*ChunkSize - 3} {
		var ct bytes.Buffer
		aead, _ := DeriveKey(testPass, make([]byte, saltLen), 1000)
		sw := NewSealWriter(&ct, aead, make([]byte, prefixLen), nil)
		cw := &countWriter{w: sw}
		cw.Write(make([]byte, n))
		if err := padAndSealOpen(cw, sw); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if cw.n%ChunkSize != 0 || int64(ct.Len()) != cw.n/ChunkSize*sealedChunk {
			t.Errorf("n=%d: plaintext %d, ciphertext %d", n, cw.n, ct.Len())
		}
		// Every chunk opens as not-last, and the padding is one skippable frame from the end
		// of the data to the chunk boundary.
		var pt []byte
		for i := 0; i*sealedChunk < ct.Len(); i++ {
			p, err := aead.Open(nil, nonce(make([]byte, prefixLen), uint32(i), false), ct.Bytes()[i*sealedChunk:(i+1)*sealedChunk], nil)
			if err != nil {
				t.Fatalf("n=%d chunk %d: %v", n, i, err)
			}
			pt = append(pt, p...)
		}
		if n%ChunkSize == 0 {
			if len(pt) != n {
				t.Errorf("n=%d: padded an aligned stream to %d", n, len(pt))
			}
			continue
		}
		if binary.LittleEndian.Uint32(pt[n:]) != skippableMagic {
			t.Errorf("n=%d: no skippable frame after the data", n)
		}
		if got := int(binary.LittleEndian.Uint32(pt[n+4:])) + 8 + n; got != len(pt) {
			t.Errorf("n=%d: skippable frame ends at %d, the stream at %d", n, got, len(pt))
		}
	}
}
