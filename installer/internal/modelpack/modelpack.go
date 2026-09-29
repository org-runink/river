// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package modelpack builds and opens the encrypted model payload that a Runink River Server
// install medium can carry next to its live image (docs/MODEL-PAYLOAD.md).
//
// The payload is the model set pinned in models.lock, packed as
//
//	tar (lock order, fixed metadata) -> zstd -> AES-256-GCM in 4 MiB chunks -> parts
//
// The key is PBKDF2-HMAC-SHA256 of a passphrase chosen at build time; the passphrase is never
// written to the medium. The chunks use the STREAM construction (a 7-byte random nonce prefix,
// a 32-bit big-endian chunk counter and a last-chunk flag), so reordering, dropping, repeating
// or truncating chunks fails authentication. The parts stay under 2 GiB each (ISO 9660 level 2
// and GitHub release assets both cap a file below that), and the plaintext MANIFEST records
// their sizes and sha256 so a damaged medium is caught before or while it is read.
//
// Opening never trusts the payload: every tar entry must be a regular file whose name is a
// dest of models.lock, it is written to a fresh file in an empty directory, and its size and
// sha256 must match the lock row. A missing, extra, duplicated or altered file is an error.
//
// Standard library only. zstd is the one external program: it is on every image (pacman and
// mkinitcpio depend on it), and the Go standard library has no zstd codec.
package modelpack

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Format constants. Changing any of them is a new format version.
const (
	FormatName    = "river-modelpack"
	FormatVersion = 1
	// ManifestName is the plaintext description of a payload, next to its parts.
	ManifestName = "MANIFEST"
	// PartPrefix names the ciphertext parts: models.rmp.000, models.rmp.001, ...
	PartPrefix = "models.rmp."
	// ChunkSize is the plaintext size of every chunk but the last.
	ChunkSize = 4 << 20
	// DefaultPartSize keeps each part below 2 GiB (1900 MiB).
	DefaultPartSize = 1900 << 20
	// DefaultIterations is the PBKDF2 work factor for new payloads.
	DefaultIterations = 1 << 20
	// MinPassphrase is the shortest passphrase Pack accepts.
	MinPassphrase = 20

	saltLen   = 16
	prefixLen = 7
	tagLen    = 16
)

// Format names one on-medium payload format. The two formats share every constant and the
// whole construction; they differ in the name (the MANIFEST header, and bound into every
// chunk's associated data, so a payload of one kind can never be opened as the other) and in
// the part file prefix.
//
//   - ModelFormat (river-modelpack 1): the model set of models.lock (docs/MODEL-PAYLOAD.md).
//   - PayloadFormat (river-payloadpack 1): any downstream payload described by a generic
//     payload LOCK (river-payload-lock 1; docs/PAYLOADS.md).
type Format struct {
	Name       string
	PartPrefix string
}

var (
	// ModelFormat is the model payload format (unchanged since the first release).
	ModelFormat = Format{FormatName, PartPrefix}
	// PayloadFormat is the generic downstream payload format.
	PayloadFormat = Format{"river-payloadpack", "payload.rmp."}
)

func (f Format) header() string { return fmt.Sprintf("%s %d", f.Name, FormatVersion) }

func (f Format) partName(i int) string { return fmt.Sprintf("%s%03d", f.PartPrefix, i) }

// aad binds the format and the KDF salt into every chunk's authentication tag.
func (f Format) aad(salt []byte) []byte {
	return append([]byte(fmt.Sprintf("%s/%d\x00", f.Name, FormatVersion)), salt...)
}

func (m *Manifest) format() Format {
	if m.Format.Name == "" {
		return ModelFormat
	}
	return m.Format
}

// Row is one models.lock line: role repo revision file size sha256 license dest.
type Row struct {
	Role, Repo, Revision, File string
	Size                       int64
	SHA256, License, Dest      string
}

// ParseLock reads models.lock. `dest` of "-" means the file's basename. Every dest must be
// a clean relative path and unique, and every sha256 64 lowercase hex characters.
func ParseLock(r io.Reader) ([]Row, error) {
	var rows []Row
	seen := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	ln := 0
	for sc.Scan() {
		ln++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 8 {
			return nil, fmt.Errorf("models.lock:%d: expected 8 columns, got %d", ln, len(f))
		}
		size, err := strconv.ParseInt(f[4], 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("models.lock:%d: bad size %q", ln, f[4])
		}
		if !isHex(f[5], 64) {
			return nil, fmt.Errorf("models.lock:%d: sha256 must be 64 lowercase hex characters", ln)
		}
		dest := f[7]
		if dest == "-" {
			dest = path.Base(f[3])
		}
		if err := checkRel(dest); err != nil {
			return nil, fmt.Errorf("models.lock:%d: dest %q: %v", ln, dest, err)
		}
		if prev, dup := seen[dest]; dup {
			return nil, fmt.Errorf("models.lock:%d: dest %q already used on line %d", ln, dest, prev)
		}
		seen[dest] = ln
		rows = append(rows, Row{f[0], f[1], f[2], f[3], size, f[5], f[6], dest})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("models.lock has no rows")
	}
	return rows, nil
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// checkRel accepts only clean, relative, slash-separated paths without "." or ".." parts.
func checkRel(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) {
		return errors.New("not a relative path")
	}
	if path.Clean(p) != p {
		return errors.New("not a clean path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." || part == "" {
			return errors.New("contains an empty, '.' or '..' element")
		}
	}
	return nil
}

// LockDigest is the sha256 of the lock file's bytes; a payload records the lock it was
// built from, and opening it against a different lock fails before any decryption.
func LockDigest(lock []byte) string {
	s := sha256.Sum256(lock)
	return hex.EncodeToString(s[:])
}

// Manifest describes a payload. It is plaintext: it holds no secret, and every field that
// matters to decryption is bound into the key or the associated data.
type Manifest struct {
	Format     Format // zero value = ModelFormat
	Iterations int
	Salt       []byte
	Prefix     []byte
	ChunkSize  int
	LockSHA256 string
	Files      int
	Bytes      int64
	Parts      []Part
}

// Part is one ciphertext file.
type Part struct {
	Name   string
	Size   int64
	SHA256 string
}

// Marshal renders the manifest in its line format.
func (m *Manifest) Marshal() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\n", m.format().header())
	fmt.Fprintf(&b, "kdf pbkdf2-sha256 %d %s\n", m.Iterations, hex.EncodeToString(m.Salt))
	fmt.Fprintf(&b, "aead aes-256-gcm %d %s\n", m.ChunkSize, hex.EncodeToString(m.Prefix))
	fmt.Fprintf(&b, "compression zstd\n")
	fmt.Fprintf(&b, "lock-sha256 %s\n", m.LockSHA256)
	fmt.Fprintf(&b, "content %d %d\n", m.Files, m.Bytes)
	for _, p := range m.Parts {
		fmt.Fprintf(&b, "part %s %d %s\n", p.Name, p.Size, p.SHA256)
	}
	return b.Bytes()
}

// ParseManifest is the inverse of Marshal, and strict: unknown lines are an error.
func ParseManifest(data []byte) (*Manifest, error) {
	m := &Manifest{}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	switch lines[0] {
	case ModelFormat.header():
		m.Format = ModelFormat
	case PayloadFormat.header():
		m.Format = PayloadFormat
	default:
		return nil, fmt.Errorf("MANIFEST: not a %s or %s payload", ModelFormat.header(), PayloadFormat.header())
	}
	have := map[string]bool{}
	for i, l := range lines[1:] {
		f := strings.Fields(l)
		bad := func() error { return fmt.Errorf("MANIFEST:%d: malformed %q", i+2, l) }
		if len(f) == 0 {
			return nil, bad()
		}
		switch f[0] {
		case "kdf":
			if len(f) != 4 || f[1] != "pbkdf2-sha256" {
				return nil, bad()
			}
			n, err := strconv.Atoi(f[2])
			s, err2 := hex.DecodeString(f[3])
			if err != nil || err2 != nil || n < 100000 || len(s) != saltLen {
				return nil, bad()
			}
			m.Iterations, m.Salt = n, s
		case "aead":
			if len(f) != 4 || f[1] != "aes-256-gcm" {
				return nil, bad()
			}
			n, err := strconv.Atoi(f[2])
			p, err2 := hex.DecodeString(f[3])
			if err != nil || err2 != nil || n != ChunkSize || len(p) != prefixLen {
				return nil, bad()
			}
			m.ChunkSize, m.Prefix = n, p
		case "compression":
			if len(f) != 2 || f[1] != "zstd" {
				return nil, bad()
			}
		case "lock-sha256":
			if len(f) != 2 || !isHex(f[1], 64) {
				return nil, bad()
			}
			m.LockSHA256 = f[1]
		case "content":
			if len(f) != 3 {
				return nil, bad()
			}
			n, err := strconv.Atoi(f[1])
			b, err2 := strconv.ParseInt(f[2], 10, 64)
			if err != nil || err2 != nil {
				return nil, bad()
			}
			m.Files, m.Bytes = n, b
		case "part":
			if len(f) != 4 || !isHex(f[3], 64) || f[1] != m.Format.partName(len(m.Parts)) {
				return nil, bad()
			}
			n, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil || n <= 0 {
				return nil, bad()
			}
			m.Parts = append(m.Parts, Part{f[1], n, f[3]})
		default:
			return nil, bad()
		}
		have[f[0]] = true
	}
	for _, k := range []string{"kdf", "aead", "compression", "lock-sha256", "content", "part"} {
		if !have[k] {
			return nil, fmt.Errorf("MANIFEST: no %q line", k)
		}
	}
	return m, nil
}

func partName(i int) string { return ModelFormat.partName(i) }

func aad(salt []byte) []byte { return ModelFormat.aad(salt) }

// DeriveKey runs PBKDF2-HMAC-SHA256 and returns an AES-256-GCM AEAD.
func DeriveKey(passphrase string, salt []byte, iterations int) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, passphrase, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(prefix []byte, ctr uint32, last bool) []byte {
	n := make([]byte, 12)
	copy(n, prefix)
	binary.BigEndian.PutUint32(n[prefixLen:], ctr)
	if last {
		n[11] = 1
	}
	return n
}

// SealWriter encrypts a stream in ChunkSize chunks. It always holds back one chunk, so that
// Close can seal the final one with the last-chunk flag.
type SealWriter struct {
	aead   cipher.AEAD
	prefix []byte
	ad     []byte
	out    io.Writer
	buf    []byte
	ctr    uint32
	closed bool
}

// NewSealWriter returns a writer that seals into out.
func NewSealWriter(out io.Writer, aead cipher.AEAD, prefix, ad []byte) *SealWriter {
	return &SealWriter{aead: aead, prefix: prefix, ad: ad, out: out, buf: make([]byte, 0, 2*ChunkSize)}
}

func (w *SealWriter) seal(pt []byte, last bool) error {
	if w.ctr == ^uint32(0) {
		return errors.New("modelpack: stream too long")
	}
	ct := w.aead.Seal(nil, nonce(w.prefix, w.ctr, last), pt, w.ad)
	w.ctr++
	_, err := w.out.Write(ct)
	return err
}

// Write buffers p and seals every chunk that is known not to be the last.
func (w *SealWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("modelpack: write after close")
	}
	n := len(p)
	for len(p) > 0 {
		take := min(len(p), 2*ChunkSize-len(w.buf))
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		for len(w.buf) > ChunkSize {
			if err := w.seal(w.buf[:ChunkSize], false); err != nil {
				return 0, err
			}
			w.buf = append(w.buf[:0], w.buf[ChunkSize:]...)
		}
	}
	return n, nil
}

// Close seals what is left (possibly nothing) as the last chunk.
func (w *SealWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.seal(w.buf, true)
}

// OpenReader decrypts a SealWriter stream. It returns plaintext only from chunks that
// authenticated, and fails on a stream that ends without its last chunk or continues after it.
type OpenReader struct {
	aead   cipher.AEAD
	prefix []byte
	ad     []byte
	in     *bufio.Reader
	ct     []byte
	pt     []byte
	ptbuf  []byte
	ctr    uint32
	done   bool
}

// NewOpenReader returns a reader over the plaintext of in.
func NewOpenReader(in io.Reader, aead cipher.AEAD, prefix, ad []byte) *OpenReader {
	return &OpenReader{aead: aead, prefix: prefix, ad: ad, in: bufio.NewReaderSize(in, 1<<20),
		ct: make([]byte, ChunkSize+tagLen)}
}

// ErrAuth means a chunk did not authenticate: a wrong passphrase, or altered bytes.
var ErrAuth = errors.New("modelpack: authentication failed (wrong passphrase, or the payload was altered)")

// ErrMedium means a ciphertext part does not match the MANIFEST: the medium is damaged.
var ErrMedium = errors.New("modelpack: the medium is damaged")

func (r *OpenReader) next() error {
	n, err := io.ReadFull(r.in, r.ct)
	last := false
	switch {
	case err == io.EOF:
		return errors.New("modelpack: payload truncated (no final chunk)")
	case err == io.ErrUnexpectedEOF:
		last = true
	case err != nil:
		return err
	default:
		if _, perr := r.in.Peek(1); perr == io.EOF {
			last = true
		} else if perr != nil {
			return perr
		}
	}
	if n < tagLen {
		return errors.New("modelpack: payload truncated (short chunk)")
	}
	pt, oerr := r.aead.Open(r.ptbuf[:0], nonce(r.prefix, r.ctr, last), r.ct[:n], r.ad)
	if oerr != nil {
		if r.ctr == 0 {
			return ErrAuth
		}
		return fmt.Errorf("%w at chunk %d", ErrAuth, r.ctr)
	}
	r.ctr++
	r.ptbuf = pt
	r.pt = pt
	r.done = last
	return nil
}

// Read implements io.Reader.
func (r *OpenReader) Read(p []byte) (int, error) {
	for len(r.pt) == 0 {
		if r.done {
			return 0, io.EOF
		}
		if err := r.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.pt)
	r.pt = r.pt[n:]
	return n, nil
}

// partWriter splits a stream into numbered parts of at most size bytes.
type partWriter struct {
	format Format
	dir    string
	size   int64
	cur    *os.File
	h      hash.Hash
	n      int64
	parts  []Part
}

func (w *partWriter) rotate() error {
	if err := w.finish(); err != nil {
		return err
	}
	name := w.format.partName(len(w.parts))
	f, err := os.OpenFile(filepath.Join(w.dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // #nosec G302 -- public model data, read by the inference pods
	if err != nil {
		return err
	}
	w.cur, w.h, w.n = f, sha256.New(), 0
	w.parts = append(w.parts, Part{Name: name})
	return nil
}

func (w *partWriter) finish() error {
	if w.cur == nil {
		return nil
	}
	p := &w.parts[len(w.parts)-1]
	p.Size, p.SHA256 = w.n, hex.EncodeToString(w.h.Sum(nil))
	err := w.cur.Sync()
	if cerr := w.cur.Close(); err == nil {
		err = cerr
	}
	w.cur = nil
	return err
}

func (w *partWriter) Write(b []byte) (int, error) {
	total := 0
	for len(b) > 0 {
		if w.cur == nil || w.n == w.size {
			if err := w.rotate(); err != nil {
				return total, err
			}
		}
		take := int(min(int64(len(b)), w.size-w.n))
		n, err := w.cur.Write(b[:take])
		w.h.Write(b[:n])
		w.n += int64(n)
		total += n
		if err != nil {
			return total, err
		}
		b = b[n:]
	}
	return total, nil
}

// partReader concatenates the parts, checking each one's size and sha256 as it ends.
type partReader struct {
	dir   string
	parts []Part
	i     int
	cur   *os.File
	h     hash.Hash
	n     int64
}

func (r *partReader) Read(b []byte) (int, error) {
	for {
		if r.cur == nil {
			if r.i == len(r.parts) {
				return 0, io.EOF
			}
			f, err := os.Open(filepath.Join(r.dir, r.parts[r.i].Name))
			if err != nil {
				return 0, err
			}
			r.cur, r.h, r.n = f, sha256.New(), 0
		}
		n, err := r.cur.Read(b)
		r.h.Write(b[:n])
		r.n += int64(n)
		if err == io.EOF {
			p := r.parts[r.i]
			_ = r.cur.Close() // read-only; the data was already checked
			r.cur = nil
			r.i++
			if r.n != p.Size || hex.EncodeToString(r.h.Sum(nil)) != p.SHA256 {
				return n, fmt.Errorf("%w: part %s does not match MANIFEST (size or sha256)", ErrMedium, p.Name)
			}
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (r *partReader) Close() {
	if r.cur != nil {
		_ = r.cur.Close() // read-only; the data was already checked
	}
}

// PackOptions configures Pack.
type PackOptions struct {
	Lock        []byte // models.lock bytes
	ModelsDir   string // the fetched model cache (build/models-fetch.sh)
	OutDir      string // empty or absent; receives MANIFEST and the parts
	Passphrase  string
	PartSize    int64 // default DefaultPartSize
	Iterations  int   // default DefaultIterations
	ZstdLevel   int   // default 12
	ZstdThreads int   // default 0 (zstd picks)
	Log         io.Writer
}

// Pack verifies every lock row in ModelsDir (size and sha256, while streaming) and writes an
// encrypted payload to OutDir. On any error OutDir's partial contents are removed.
func Pack(o PackOptions) (m *Manifest, err error) {
	rows, info, err := ParseAnyLock(o.Lock)
	if err != nil {
		return nil, err
	}
	if len(o.Passphrase) < MinPassphrase {
		return nil, fmt.Errorf("modelpack: the passphrase must be at least %d characters", MinPassphrase)
	}
	if o.PartSize <= 0 {
		o.PartSize = DefaultPartSize
	}
	if o.Iterations <= 0 {
		o.Iterations = DefaultIterations
	}
	if o.ZstdLevel == 0 {
		o.ZstdLevel = 12
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	// Check every file's size first: a missing file should fail in a second, not after
	// twenty gigabytes of compression.
	var total int64
	for _, r := range rows {
		st, serr := os.Stat(filepath.Join(o.ModelsDir, filepath.FromSlash(r.Dest)))
		if serr != nil {
			return nil, fmt.Errorf("modelpack: %s: %v (fill the cache with build/models-fetch.sh)", r.Dest, serr)
		}
		if !st.Mode().IsRegular() || st.Size() != r.Size {
			return nil, fmt.Errorf("modelpack: %s: size %d, models.lock pins %d", r.Dest, st.Size(), r.Size)
		}
		total += r.Size
	}
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil { // #nosec G301 -- public model data
		return nil, err
	}
	if ents, _ := os.ReadDir(o.OutDir); len(ents) > 0 {
		return nil, fmt.Errorf("modelpack: %s is not empty", o.OutDir)
	}
	defer func() {
		if err != nil {
			ents, _ := os.ReadDir(o.OutDir)
			for _, e := range ents {
				_ = os.Remove(filepath.Join(o.OutDir, e.Name())) // best-effort cleanup after an error
			}
		}
	}()

	m = &Manifest{Format: info.Format, Iterations: o.Iterations, ChunkSize: ChunkSize, LockSHA256: LockDigest(o.Lock),
		Files: len(rows), Bytes: total, Salt: make([]byte, saltLen), Prefix: make([]byte, prefixLen)}
	if _, err := rand.Read(m.Salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(m.Prefix); err != nil {
		return nil, err
	}
	aead, err := DeriveKey(o.Passphrase, m.Salt, m.Iterations)
	if err != nil {
		return nil, err
	}
	pw := &partWriter{format: m.Format, dir: o.OutDir, size: o.PartSize}
	sw := NewSealWriter(pw, aead, m.Prefix, m.Format.aad(m.Salt))

	args := []string{"-q", "-c", fmt.Sprintf("-%d", o.ZstdLevel), fmt.Sprintf("-T%d", o.ZstdThreads)}
	if o.ZstdLevel > 19 {
		args = append(args, "--ultra")
	}
	z := exec.Command("zstd", args...) // #nosec G204 -- fixed program; the arguments are integers formatted here
	z.Stdout = sw
	z.Stderr = os.Stderr
	zin, err := z.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := z.Start(); err != nil {
		return nil, fmt.Errorf("modelpack: zstd: %v", err)
	}
	werr := writeTar(zin, rows, o.ModelsDir, o.Log)
	_ = zin.Close() // EOF for zstd; its exit status is checked by Wait
	zerr := z.Wait()
	if werr != nil {
		return nil, werr
	}
	if zerr != nil {
		return nil, fmt.Errorf("modelpack: zstd: %v", zerr)
	}
	if err := sw.Close(); err != nil {
		return nil, err
	}
	if err := pw.finish(); err != nil {
		return nil, err
	}
	m.Parts = pw.parts
	if err := os.WriteFile(filepath.Join(o.OutDir, ManifestName), m.Marshal(), 0o644); err != nil { // #nosec G306 -- the MANIFEST holds no secret
		return nil, err
	}
	return m, nil
}

// writeTar streams every lock row into a tar archive with fixed metadata, hashing as it
// reads, and fails if any file does not match its pin.
func writeTar(w io.Writer, rows []Row, dir string, log io.Writer) error {
	tw := tar.NewWriter(w)
	epoch := time.Unix(0, 0)
	for i, r := range rows {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(r.Dest)))
		if err != nil {
			return err
		}
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: r.Dest, Size: r.Size, Mode: 0o644,
			ModTime: epoch, Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = f.Close() // read-only file
			return err
		}
		h := sha256.New()
		n, err := io.Copy(tw, io.TeeReader(f, h))
		_ = f.Close() // read-only file
		if err != nil {
			return fmt.Errorf("modelpack: %s: %v", r.Dest, err)
		}
		if n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
			return fmt.Errorf("modelpack: %s does not match models.lock (size or sha256)", r.Dest)
		}
		fmt.Fprintf(log, "pack %2d/%d  %s  %d bytes  sha256 ok\n", i+1, len(rows), r.Dest, r.Size)
	}
	return tw.Close()
}

// LoadManifest reads DIR/MANIFEST.
func LoadManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	return ParseManifest(b)
}

// CheckParts verifies every part's size and sha256 (no passphrase needed).
func CheckParts(dir string, m *Manifest) error {
	r := &partReader{dir: dir, parts: m.Parts}
	defer r.Close()
	_, err := io.Copy(io.Discard, r)
	return err
}

// UnpackOptions configures Unpack.
type UnpackOptions struct {
	PayloadDir string // MANIFEST + parts
	Lock       []byte // the models.lock of the image doing the install
	Dest       string // an existing, EMPTY directory
	Passphrase string
	Log        io.Writer
}

// Unpack decrypts and extracts a payload into Dest, checking every file against Lock.
// Dest is left partially filled on error; the caller discards it (the installer destroys
// the dataset).
func Unpack(o UnpackOptions) error {
	if o.Log == nil {
		o.Log = io.Discard
	}
	m, err := LoadManifest(o.PayloadDir)
	if err != nil {
		return err
	}
	if got := LockDigest(o.Lock); got != m.LockSHA256 {
		return fmt.Errorf("modelpack: the payload was built from a different lock (sha256 %s, this one is %s)", m.LockSHA256, got)
	}
	rows, info, err := ParseAnyLock(o.Lock)
	if err != nil {
		return err
	}
	if info.Format != m.Format {
		return fmt.Errorf("modelpack: the MANIFEST says %s, the lock is for %s", m.Format.Name, info.Format.Name)
	}
	ents, err := os.ReadDir(o.Dest)
	if err != nil {
		return err
	}
	if len(ents) > 0 {
		return fmt.Errorf("modelpack: destination %s is not empty", o.Dest)
	}
	aead, err := DeriveKey(o.Passphrase, m.Salt, m.Iterations)
	if err != nil {
		return err
	}
	pr := &partReader{dir: o.PayloadDir, parts: m.Parts}
	defer pr.Close()
	or := NewOpenReader(pr, aead, m.Prefix, m.Format.aad(m.Salt))
	// Decrypt the first chunk before starting anything, so a wrong passphrase is reported
	// as exactly that.
	if err := or.next(); err != nil {
		return err
	}

	z := exec.Command("zstd", "-q", "-d", "-c")
	z.Stderr = os.Stderr
	zout, err := z.StdoutPipe()
	if err != nil {
		return err
	}
	zin, err := z.StdinPipe()
	if err != nil {
		return err
	}
	if err := z.Start(); err != nil {
		return fmt.Errorf("modelpack: zstd: %v", err)
	}
	feedErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(zin, or)
		_ = zin.Close() // EOF for zstd; its exit status is checked by Wait
		feedErr <- err
	}()
	xerr := extract(zout, rows, o.Dest, o.Log)
	if xerr != nil {
		// Stop zstd so the feeder does not decrypt the rest of the payload for nothing.
		_ = z.Process.Kill() // already failing; Wait reaps it
		_, _ = io.Copy(io.Discard, zout)
	}
	ferr := <-feedErr
	zerr := z.Wait()
	// An authentication or medium error explains a broken archive after it, so it wins;
	// a broken pipe from killing zstd does not.
	if ferr != nil && (errors.Is(ferr, ErrAuth) || errors.Is(ferr, ErrMedium) || xerr == nil) {
		return ferr
	}
	if xerr != nil {
		return xerr
	}
	if zerr != nil {
		return fmt.Errorf("modelpack: zstd: %v", zerr)
	}
	return nil
}

func extract(r io.Reader, rows []Row, dest string, log io.Writer) error {
	want := map[string]Row{}
	for _, row := range rows {
		want[row.Dest] = row
	}
	got := map[string]bool{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("modelpack: archive: %v", err)
		}
		row, ok := want[hdr.Name]
		if !ok || hdr.Typeflag != tar.TypeReg || checkRel(hdr.Name) != nil {
			return fmt.Errorf("modelpack: archive entry %q is not a models.lock file", hdr.Name)
		}
		if got[hdr.Name] {
			return fmt.Errorf("modelpack: archive entry %q repeated", hdr.Name)
		}
		if hdr.Size != row.Size {
			return fmt.Errorf("modelpack: %s: size %d, models.lock pins %d", row.Dest, hdr.Size, row.Size)
		}
		got[hdr.Name] = true
		p := filepath.Join(dest, filepath.FromSlash(row.Dest))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { // #nosec G301 -- public model data
			return err
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // #nosec G302 -- public model data, read by the inference pods
		if err != nil {
			return err
		}
		h := sha256.New()
		// At most the pinned size (hdr.Size was checked against the lock above).
		n, err := io.CopyN(io.MultiWriter(f, h), tr, row.Size)
		if err == io.EOF {
			err = nil // short entry: caught by the size check below
		}
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("modelpack: %s: %v", row.Dest, err)
		}
		if n != row.Size || hex.EncodeToString(h.Sum(nil)) != row.SHA256 {
			return fmt.Errorf("modelpack: %s does not match models.lock (size or sha256)", row.Dest)
		}
		fmt.Fprintf(log, "unpack %2d/%d  %s  %d bytes  sha256 ok\n", len(got), len(rows), row.Dest, row.Size)
	}
	for _, row := range rows {
		if !got[row.Dest] {
			return fmt.Errorf("modelpack: %s is missing from the payload", row.Dest)
		}
	}
	return nil
}

// Verify checks that dir holds every lock row with the pinned size and sha256.
func Verify(dir string, lock []byte, log io.Writer) error {
	rows, _, err := ParseAnyLock(lock)
	if err != nil {
		return err
	}
	if log == nil {
		log = io.Discard
	}
	bad := 0
	for i, r := range rows {
		p := filepath.Join(dir, filepath.FromSlash(r.Dest))
		f, err := os.Open(p)
		if err != nil {
			fmt.Fprintf(log, "verify %2d/%d  %s  MISSING\n", i+1, len(rows), r.Dest)
			bad++
			continue
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		_ = f.Close() // read-only file
		if err != nil || n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
			fmt.Fprintf(log, "verify %2d/%d  %s  BAD\n", i+1, len(rows), r.Dest)
			bad++
			continue
		}
		fmt.Fprintf(log, "verify %2d/%d  %s  ok\n", i+1, len(rows), r.Dest)
	}
	if bad > 0 {
		return fmt.Errorf("modelpack: %d of %d file(s) missing or not matching models.lock", bad, len(rows))
	}
	return nil
}

// GeneratePassphrase returns 32 random bytes as 64 lowercase hex characters (the same shape
// as the ZFS recovery key, so it can be typed at a console).
func GeneratePassphrase() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ReadPassphrase reads a passphrase from a file (or "-" for stdin): the first line, without
// its line ending.
func ReadPassphrase(name string) (string, error) {
	var r io.Reader
	if name == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(name)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("modelpack: empty passphrase")
	}
	return line, nil
}

// CheckKey reports whether passphrase opens the payload, by authenticating its first chunk
// only (seconds, not a full read).
func CheckKey(dir string, m *Manifest, passphrase string) error {
	aead, err := DeriveKey(passphrase, m.Salt, m.Iterations)
	if err != nil {
		return err
	}
	pr := &partReader{dir: dir, parts: m.Parts}
	defer pr.Close()
	return NewOpenReader(pr, aead, m.Prefix, m.Format.aad(m.Salt)).next()
}
