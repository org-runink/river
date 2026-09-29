// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package modelpack

// Incremental packing (pack --append, --consume; docs/MODEL-PAYLOAD.md "Packing a large set
// incrementally").
//
// A one-shot Pack needs the whole plaintext set on disk next to the whole payload. A set close
// to the build host's free space cannot be packed that way, so Append packs it a few files at
// a time into the SAME payload, and (Consume) deletes each source file once its ciphertext is
// written, fsynced and re-verified. The payload it ends in has the same format as a one-shot
// one (river-modelpack 1) and opens with the same Unpack:
//
//   - The tar archive is written across invocations: each one adds its entries (in the order
//     it packs them; extraction does not depend on the order) and the last one adds the
//     end-of-archive marker.
//   - Each invocation compresses its entries as its own zstd frame. zstd decodes concatenated
//     frames as one stream, and ignores skippable frames: an unfinished invocation ends with a
//     skippable frame that pads its plaintext to a whole number of 4 MiB chunks.
//   - So every chunk an unfinished invocation seals is full and not the last: the STREAM
//     counter simply continues in the next invocation (it is the ciphertext length divided by
//     the sealed chunk size), and no plaintext and no key material is ever kept between
//     invocations. The invocation that packs the last row seals the final chunk with the
//     last-chunk flag, as Pack does.
//
// Between invocations the payload directory holds a JOURNAL: a MANIFEST whose second line is
// "incomplete", which lists the rows packed so far ("packed <dest>") and the parts written for
// them. ParseManifest refuses it (ErrIncomplete), so `check`, `unpack` and a build's reuse test
// never accept a partial payload. The final invocation replaces it with an ordinary MANIFEST.
//
// Crash safety. The journal is only ever replaced atomically (write, fsync, rename, fsync the
// directory) AFTER the parts it names are written, fsynced and verified, and sources are only
// deleted after that. An interrupted invocation leaves the previous journal in place: the next
// one truncates the last recorded part back to its recorded size, removes unrecorded parts,
// and continues from there. Only the build host ever holds the discarded ciphertext of an
// interrupted invocation; the chunk counters it used are sealed again by the next one.

import (
	"archive/tar"
	"bufio"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// sealedChunk is the ciphertext size of every chunk but the last.
const sealedChunk = ChunkSize + tagLen

// journalTmp is the name the next journal is written under before it is renamed into place.
const journalTmp = ManifestName + ".tmp"

// AppendOptions configures Append. The fields shared with PackOptions mean the same.
type AppendOptions struct {
	Lock        []byte // the FULL lock the payload is for
	ModelsDir   string // holds some (or all) of the lock's files
	OutDir      string // absent, empty, an unfinished payload (journal), or a finished one
	Passphrase  string
	PartSize    int64
	Iterations  int // for a new payload only
	ZstdLevel   int
	ZstdThreads int
	// Consume deletes each source file once it is in the payload (written, fsynced,
	// re-verified and recorded in the MANIFEST or journal).
	Consume bool
	Log     io.Writer
}

// AppendResult reports what one Append call did.
type AppendResult struct {
	Manifest  *Manifest // the complete MANIFEST, or the journal
	Packed    []string  // dests packed by this call
	Consumed  []string  // source files deleted by this call
	Skipped   []string  // present but not matching the lock (left in place, not packed)
	Remaining []string  // lock rows not yet in the payload, in lock order
	Complete  bool
}

// Append packs into OutDir the rows of the lock that are not in it yet and whose files are
// in ModelsDir with the pinned size and sha256 (see the top of this file). The first call
// creates the payload; the call that packs the last row finishes it.
func Append(o AppendOptions) (res *AppendResult, err error) {
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
	if o.PartSize >= 2<<30 {
		return nil, errors.New("modelpack: a part must stay below 2 GiB")
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
	digest := LockDigest(o.Lock)
	var total int64
	for _, r := range rows {
		total += r.Size
	}
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil { // #nosec G301 -- public model data
		return nil, err
	}
	unlock, err := lockDir(o.OutDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	_ = os.Remove(filepath.Join(o.OutDir, journalTmp)) // a journal an interrupted call never renamed

	res = &AppendResult{}
	m, err := loadOrCreateJournal(o, info.Format, digest, len(rows), total)
	if err != nil {
		return nil, err
	}
	if !m.Incomplete {
		// Already finished: every row is in it.
		res.Manifest, res.Complete = m, true
		if o.Consume {
			res.Consumed, err = consume(o.ModelsDir, rows, o.Log)
		}
		return res, err
	}
	aead, err := DeriveKey(o.Passphrase, m.Salt, m.Iterations)
	if err != nil {
		return nil, err
	}
	pw, err := resumeParts(o.OutDir, m, o.PartSize)
	if err != nil {
		return nil, err
	}
	defer pw.abort() // a no-op once the parts are finished
	var cipherLen int64
	for _, p := range m.Parts {
		cipherLen += p.Size
	}
	if cipherLen%sealedChunk != 0 {
		return nil, fmt.Errorf("%w: the journal's parts are not a whole number of chunks", ErrMedium)
	}
	if len(m.Parts) > 0 {
		// A wrong passphrase would seal the rest of the set under another key.
		if err := checkJournalKey(o.OutDir, m, aead); err != nil {
			return nil, err
		}
	}

	// Which rows to pack now; leftovers of packed rows can go (an earlier call recorded them
	// but stopped before deleting them).
	packed := map[string]bool{}
	for _, d := range m.Packed {
		packed[d] = true
	}
	var done []Row
	var todo []Row
	for _, r := range rows {
		if packed[r.Dest] {
			done = append(done, r)
			continue
		}
		p := filepath.Join(o.ModelsDir, filepath.FromSlash(r.Dest))
		st, serr := os.Stat(p)
		if serr != nil || !st.Mode().IsRegular() || st.Size() != r.Size {
			res.Remaining = append(res.Remaining, r.Dest)
			continue
		}
		n, sum, herr := HashFile(p)
		if herr != nil {
			return nil, herr
		}
		if n != r.Size || sum != r.SHA256 {
			fmt.Fprintf(o.Log, "append  %s  does not match the lock (sha256); skipped, re-fetch it\n", r.Dest)
			res.Skipped = append(res.Skipped, r.Dest)
			res.Remaining = append(res.Remaining, r.Dest)
			continue
		}
		todo = append(todo, r)
	}
	if o.Consume {
		if res.Consumed, err = consume(o.ModelsDir, done, o.Log); err != nil {
			return nil, err
		}
	}
	final := len(res.Remaining) == 0
	if len(todo) == 0 && !final {
		res.Manifest = m
		return res, nil
	}

	// From here on, an error rolls the parts back to what the journal records.
	recorded := slices.Clone(m.Parts)
	defer func() {
		if err != nil {
			pw.abort()
			rollbackParts(o.OutDir, m.Format, recorded)
		}
	}()
	startPart, startOff := len(recorded), int64(0)
	if n := len(recorded); n > 0 && recorded[n-1].Size < o.PartSize {
		startPart, startOff = n-1, recorded[n-1].Size // the last part is continued
	}
	ctr := uint32(cipherLen / sealedChunk)
	if int64(ctr)*sealedChunk != cipherLen {
		return nil, errors.New("modelpack: stream too long")
	}
	sw := NewSealWriter(pw, aead, m.Prefix, m.Format.aad(m.Salt))
	sw.ctr = ctr
	cw := &countWriter{w: sw}
	if err := compressEntries(cw, todo, o, final); err != nil {
		return nil, err
	}
	if final {
		err = sw.Close()
	} else {
		err = padAndSealOpen(cw, sw)
	}
	if err != nil {
		return nil, err
	}
	if err := pw.finish(); err != nil {
		return nil, err
	}
	if err := syncDir(o.OutDir); err != nil {
		return nil, err
	}

	// Re-read what this call wrote (the touched parts are checked against their new sizes
	// and sha256) and decrypt, decompress and hash every file in it before anything records
	// it or deletes a source.
	if err := verifyRange(o.OutDir, m, pw.parts[startPart:], startOff, ctr, final, todo, aead, o.Log); err != nil {
		return nil, fmt.Errorf("modelpack: re-verifying what was just written: %w", err)
	}

	next := *m
	next.Parts = slices.Clone(pw.parts)
	for _, r := range todo {
		next.Packed = append(next.Packed, r.Dest)
		res.Packed = append(res.Packed, r.Dest)
	}
	if final {
		next.Incomplete, next.Packed = false, nil
	}
	if err := writeManifestAtomic(o.OutDir, &next); err != nil {
		return nil, err
	}
	res.Manifest, res.Complete = &next, final
	if o.Consume {
		c, cerr := consume(o.ModelsDir, todo, o.Log)
		res.Consumed = append(res.Consumed, c...)
		if cerr != nil {
			return res, cerr
		}
	}
	return res, nil
}

// Pending lists, in lock order, the lock rows a payload directory does not hold yet: every
// row for an absent or empty directory, none for a finished payload of this lock.
func Pending(dir string, lock []byte) ([]string, error) {
	rows, _, err := ParseAnyLock(lock)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		var all []string
		for _, r := range rows {
			all = append(all, r.Dest)
		}
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := parseManifest(b)
	if err != nil {
		return nil, err
	}
	if m.LockSHA256 != LockDigest(lock) {
		return nil, fmt.Errorf("modelpack: %s was built from a different lock (sha256 %s)", dir, m.LockSHA256)
	}
	if !m.Incomplete {
		return nil, nil
	}
	var out []string
	for _, r := range rows {
		if !slices.Contains(m.Packed, r.Dest) {
			out = append(out, r.Dest)
		}
	}
	return out, nil
}

// VerifyPayload decrypts and decompresses the whole payload and checks every file against the
// lock, writing nothing (the check a one-shot `pack --consume` runs before deleting sources).
func VerifyPayload(dir string, lock []byte, passphrase string, log io.Writer) error {
	m, err := LoadManifest(dir)
	if err != nil {
		return err
	}
	if got := LockDigest(lock); got != m.LockSHA256 {
		return fmt.Errorf("modelpack: the payload was built from a different lock (sha256 %s, this one is %s)", m.LockSHA256, got)
	}
	rows, _, err := ParseAnyLock(lock)
	if err != nil {
		return err
	}
	aead, err := DeriveKey(passphrase, m.Salt, m.Iterations)
	if err != nil {
		return err
	}
	if log == nil {
		log = io.Discard
	}
	return verifyStream(dir, m, m.Parts, 0, 0, true, rows, aead, log)
}

// ConsumeSources deletes the source files of every lock row (after VerifyPayload).
func ConsumeSources(modelsDir string, lock []byte, log io.Writer) ([]string, error) {
	rows, _, err := ParseAnyLock(lock)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = io.Discard
	}
	return consume(modelsDir, rows, log)
}

// loadOrCreateJournal returns the payload's MANIFEST (complete) or journal, creating a new,
// empty journal in an empty directory.
func loadOrCreateJournal(o AppendOptions, f Format, digest string, files int, total int64) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(o.OutDir, ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		ents, rerr := os.ReadDir(o.OutDir)
		if rerr != nil {
			return nil, rerr
		}
		if len(ents) > 0 {
			return nil, fmt.Errorf("modelpack: %s has no MANIFEST and is not empty; refusing to pack into it", o.OutDir)
		}
		m := &Manifest{Format: f, Iterations: o.Iterations, ChunkSize: ChunkSize, LockSHA256: digest,
			Files: files, Bytes: total, Salt: make([]byte, saltLen), Prefix: make([]byte, prefixLen), Incomplete: true}
		if _, err := rand.Read(m.Salt); err != nil {
			return nil, err
		}
		if _, err := rand.Read(m.Prefix); err != nil {
			return nil, err
		}
		if err := writeManifestAtomic(o.OutDir, m); err != nil {
			return nil, err
		}
		fmt.Fprintf(o.Log, "append  new payload in %s (%d file(s), %d bytes in the lock)\n", o.OutDir, files, total)
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := parseManifest(b)
	if err != nil {
		return nil, err
	}
	switch {
	case m.LockSHA256 != digest:
		return nil, fmt.Errorf("modelpack: %s was built from a different lock (sha256 %s, this one is %s); remove it to start over", o.OutDir, m.LockSHA256, digest)
	case m.format() != f:
		return nil, fmt.Errorf("modelpack: %s is a %s payload, the lock is for %s", o.OutDir, m.format().Name, f.Name)
	case m.Files != files || m.Bytes != total:
		return nil, fmt.Errorf("modelpack: %s: MANIFEST content %d/%d does not match the lock (%d/%d)", o.OutDir, m.Files, m.Bytes, files, total)
	}
	m.Format = m.format()
	return m, nil
}

// lockDir takes an exclusive lock on the payload directory itself (no lock file lands in the
// payload), so two appends can never interleave.
func lockDir(dir string) (func(), error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // #nosec G115 -- a file descriptor fits an int
		_ = f.Close() // read-only directory handle
		return nil, fmt.Errorf("modelpack: %s is in use by another pack (%v)", dir, err)
	}
	return func() { _ = f.Close() }, nil // closing releases the lock
}

// resumeParts prepares a partWriter that continues after the journal's parts: parts the
// journal does not record are removed, the last recorded part is truncated back to its
// recorded size and re-hashed (it must still match), and every other part must have its
// recorded size.
func resumeParts(dir string, m *Manifest, size int64) (*partWriter, error) {
	f := m.format()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		name := e.Name()
		if name == ManifestName {
			continue
		}
		idx, ok := partIndex(f, name)
		switch {
		case ok && idx < len(m.Parts):
		case ok:
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("modelpack: %s: unexpected file %s in a payload directory", dir, name)
		}
	}
	pw := &partWriter{format: f, dir: dir, size: size, parts: slices.Clone(m.Parts)}
	for i, p := range m.Parts {
		path := filepath.Join(dir, p.Name)
		st, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMedium, err)
		}
		last := i == len(m.Parts)-1
		switch {
		case st.Size() == p.Size:
		case last && st.Size() > p.Size:
			// Written by an interrupted call after the journal: not part of the payload.
			if err := os.Truncate(path, p.Size); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: part %s is %d bytes, the journal records %d", ErrMedium, p.Name, st.Size(), p.Size)
		}
		if !last || p.Size >= size {
			continue
		}
		fh, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		if _, err := io.Copy(h, fh); err != nil {
			_ = fh.Close() // the error above is the one to report
			return nil, err
		}
		if hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
			_ = fh.Close() // damaged; nothing was written
			return nil, fmt.Errorf("%w: part %s does not match the journal (sha256)", ErrMedium, p.Name)
		}
		pw.cur, pw.h, pw.n = fh, h, p.Size // positioned at its end by the copy
	}
	return pw, nil
}

func partIndex(f Format, name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, f.PartPrefix)
	if !ok || len(rest) < 3 {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 || f.partName(n) != name {
		return 0, false
	}
	return n, true
}

// abort closes the part being written without recording it (the rollback removes it).
func (w *partWriter) abort() {
	if w.cur != nil {
		_ = w.cur.Close() // rolled back next
		w.cur = nil
	}
}

// rollbackParts returns the directory to what the journal records (best effort; the next
// call does the same before it writes).
func rollbackParts(dir string, f Format, recorded []Part) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if idx, ok := partIndex(f, e.Name()); ok && idx >= len(recorded) {
			_ = os.Remove(filepath.Join(dir, e.Name())) // best effort, see above
		}
	}
	if n := len(recorded); n > 0 {
		_ = os.Truncate(filepath.Join(dir, recorded[n-1].Name), recorded[n-1].Size) // best effort, see above
	}
}

// checkJournalKey authenticates the first chunk of an unfinished payload.
func checkJournalKey(dir string, m *Manifest, aead cipher.AEAD) error {
	f, err := os.Open(filepath.Join(dir, m.Parts[0].Name))
	if err != nil {
		return err
	}
	defer f.Close()
	ct := make([]byte, sealedChunk)
	if _, err := io.ReadFull(f, ct); err != nil {
		return fmt.Errorf("%w: first part shorter than a chunk", ErrMedium)
	}
	if _, err := aead.Open(nil, nonce(m.Prefix, 0, false), ct, m.format().aad(m.Salt)); err != nil {
		return fmt.Errorf("%w (this passphrase does not open the payload being appended to)", ErrAuth)
	}
	return nil
}

// countWriter counts the plaintext handed to the SealWriter.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// compressEntries runs one zstd frame over the tar entries of rows (and the end-of-archive
// marker when final) into w.
func compressEntries(w io.Writer, rows []Row, o AppendOptions, final bool) error {
	args := []string{"-q", "-c", fmt.Sprintf("-%d", o.ZstdLevel), fmt.Sprintf("-T%d", o.ZstdThreads)}
	if o.ZstdLevel > 19 {
		args = append(args, "--ultra")
	}
	z := exec.Command("zstd", args...) // #nosec G204 -- fixed program; the arguments are integers formatted here
	z.Stdout = w
	z.Stderr = os.Stderr
	zin, err := z.StdinPipe()
	if err != nil {
		return err
	}
	if err := z.Start(); err != nil {
		return fmt.Errorf("modelpack: zstd: %v", err)
	}
	werr := writeEntries(zin, rows, o.ModelsDir, o.Log, final)
	_ = zin.Close() // EOF for zstd; its exit status is checked by Wait
	zerr := z.Wait()
	if werr != nil {
		return werr
	}
	if zerr != nil {
		return fmt.Errorf("modelpack: zstd: %v", zerr)
	}
	return nil
}

// writeEntries is writeTar for part of an archive: the entries of rows, then the
// end-of-archive marker only when final (otherwise the last entry is padded and the archive
// continues in the next call).
func writeEntries(w io.Writer, rows []Row, dir string, log io.Writer, final bool) error {
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
			return fmt.Errorf("modelpack: %s changed while it was packed (size or sha256)", r.Dest)
		}
		fmt.Fprintf(log, "append %2d/%d  %s  %d bytes  sha256 ok\n", i+1, len(rows), r.Dest, r.Size)
	}
	if final {
		return tw.Close()
	}
	return tw.Flush()
}

// skippable frame magic (zstd frame format, "Skippable frames"): 0x184D2A50..5F.
const skippableMagic = 0x184D2A50

// padAndSealOpen pads the plaintext written so far to a whole number of chunks with a zstd
// skippable frame, then seals the held-back chunk as NOT the last one.
func padAndSealOpen(cw *countWriter, sw *SealWriter) error {
	pad := (ChunkSize - cw.n%ChunkSize) % ChunkSize
	if pad > 0 && pad < 8 {
		pad += ChunkSize // a skippable frame is at least its 8-byte header
	}
	if pad > 0 {
		frame := make([]byte, pad)
		binary.LittleEndian.PutUint32(frame[0:4], skippableMagic)
		binary.LittleEndian.PutUint32(frame[4:8], uint32(pad-8)) // #nosec G115 -- pad < 2 chunks
		if _, err := cw.Write(frame); err != nil {
			return err
		}
	}
	if len(sw.buf) != ChunkSize || cw.n%ChunkSize != 0 {
		return fmt.Errorf("modelpack: internal error: %d bytes held back after padding", len(sw.buf))
	}
	if err := sw.seal(sw.buf, false); err != nil {
		return err
	}
	sw.buf = sw.buf[:0]
	sw.closed = true
	return nil
}

// verifyRange re-reads the parts a call touched, skipping the startOff bytes of the first
// one that an earlier call wrote, and checks that they decrypt (from chunk ctr), decompress
// and hold exactly rows, each matching the lock. The touched parts are checked against their
// new sizes and sha256 on the way.
func verifyRange(dir string, m *Manifest, parts []Part, startOff int64, ctr uint32, final bool, rows []Row, aead cipher.AEAD, log io.Writer) error {
	return verifyStream(dir, m, parts, startOff, ctr, final, rows, aead, log)
}

// verifyStream decrypts parts (from byte startOff, chunk ctr; an unterminated range unless
// final), decompresses, and checks that the archive holds exactly rows, each with its pinned
// size and sha256. It writes nothing.
func verifyStream(dir string, m *Manifest, parts []Part, startOff int64, ctr uint32, final bool, rows []Row, aead cipher.AEAD, log io.Writer) error {
	pr := &partReader{dir: dir, parts: parts}
	defer pr.Close()
	if startOff > 0 {
		if _, err := io.CopyN(io.Discard, pr, startOff); err != nil {
			return err
		}
	}
	or := NewOpenReader(pr, aead, m.Prefix, m.format().aad(m.Salt))
	or.ctr, or.open = ctr, !final
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
	xerr := checkEntries(zout, rows, log)
	if xerr != nil {
		_ = z.Process.Kill() // already failing; Wait reaps it
	}
	_, _ = io.Copy(io.Discard, zout)
	ferr := <-feedErr
	zerr := z.Wait()
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

// checkEntries reads a tar stream (ending with or without its end-of-archive marker) and
// checks it holds exactly rows, each once, with the pinned size and sha256.
func checkEntries(r io.Reader, rows []Row, log io.Writer) error {
	want := map[string]Row{}
	for _, row := range rows {
		want[row.Dest] = row
	}
	got := map[string]bool{}
	tr := tar.NewReader(bufio.NewReaderSize(r, 1<<20))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("modelpack: archive: %v", err)
		}
		row, ok := want[hdr.Name]
		if !ok || hdr.Typeflag != tar.TypeReg || got[hdr.Name] || hdr.Size != row.Size {
			return fmt.Errorf("modelpack: archive entry %q is not an expected lock file", hdr.Name)
		}
		got[hdr.Name] = true
		h := sha256.New()
		n, err := io.CopyN(h, tr, row.Size)
		if err != nil && err != io.EOF {
			return fmt.Errorf("modelpack: %s: %v", row.Dest, err)
		}
		if n != row.Size || hex.EncodeToString(h.Sum(nil)) != row.SHA256 {
			return fmt.Errorf("modelpack: %s does not match the lock after packing (size or sha256)", row.Dest)
		}
		fmt.Fprintf(log, "verify %2d/%d  %s  sha256 ok\n", len(got), len(rows), row.Dest)
	}
	for _, row := range rows {
		if !got[row.Dest] {
			return fmt.Errorf("modelpack: %s is missing from what was just packed", row.Dest)
		}
	}
	return nil
}

// consume deletes the source file of each row that is still there with its pinned size (a
// file of another size is not the one that was packed, and is left alone).
func consume(dir string, rows []Row, log io.Writer) ([]string, error) {
	var out []string
	for _, r := range rows {
		p := filepath.Join(dir, filepath.FromSlash(r.Dest))
		st, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		if !st.Mode().IsRegular() || st.Size() != r.Size {
			fmt.Fprintf(log, "consume %s  not the packed file (size %d); left in place\n", r.Dest, st.Size())
			continue
		}
		if err := os.Remove(p); err != nil {
			return out, err
		}
		fmt.Fprintf(log, "consume %s  deleted (in the payload)\n", r.Dest)
		out = append(out, r.Dest)
	}
	return out, nil
}

// writeManifestAtomic replaces DIR/MANIFEST: write a temporary file, fsync it, rename it into
// place, fsync the directory. A crash leaves either the old or the new MANIFEST.
func writeManifestAtomic(dir string, m *Manifest) error {
	tmp := filepath.Join(dir, journalTmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) // #nosec G302 G304 -- the MANIFEST holds no secret; the path is the payload dir's
	if err != nil {
		return err
	}
	_, err = f.Write(m.Marshal())
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, ManifestName))
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the next call removes it too
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
