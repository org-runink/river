// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package bundle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Format is the on-the-wire bundle format identifier.
//
// A bundle file is a single JSON document:
//
//	{"format":"river-guide-bundle/v1","name":"platform","version":"2026.09.24",
//	 "docs":[{"path":"01-intro.md","content":"# ..."}]}
//
// Its signature is a separate file holding one line, `ed25519:<base64 signature>`, over
// the EXACT bytes of the bundle file. The verifying key is configured on the install
// medium as `ed25519:<base64 public key>`. Only public keys ever live on the medium.
const Format = "river-guide-bundle/v1"

// MaxBundleBytes bounds a bundle file. A guide is text; anything bigger is refused
// before it is parsed.
const MaxBundleBytes = 4 << 20

type wire struct {
	Format  string `json:"format"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Docs    []Doc  `json:"docs"`
}

// Encode serialises documents into a bundle file (deterministic: docs in given order).
func Encode(name, version string, docs []Doc) ([]byte, error) {
	if _, err := Parse(name, version, docs); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(wire{Format: Format, Name: name, Version: version, Docs: docs}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode parses a bundle file WITHOUT verifying it. Callers that fetched the file from
// anywhere must use VerifyAndDecode.
func Decode(raw []byte) (*Bundle, error) {
	if len(raw) > MaxBundleBytes {
		return nil, fmt.Errorf("bundle is %d bytes, limit %d", len(raw), MaxBundleBytes)
	}
	var w wire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("decode bundle: %w", err)
	}
	if w.Format != Format {
		return nil, fmt.Errorf("bundle format %q, want %q", w.Format, Format)
	}
	return Parse(w.Name, w.Version, w.Docs)
}

// ParsePublicKey reads `ed25519:<base64>`.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b64, ok := strings.CutPrefix(strings.TrimSpace(s), "ed25519:")
	if !ok {
		return nil, errors.New("public key must be ed25519:<base64>")
	}
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(k) != ed25519.PublicKeySize {
		return nil, errors.New("public key is not a base64 ed25519 key")
	}
	return ed25519.PublicKey(k), nil
}

// FormatPublicKey is the inverse of ParsePublicKey.
func FormatPublicKey(k ed25519.PublicKey) string {
	return "ed25519:" + base64.StdEncoding.EncodeToString(k)
}

// Sign returns the signature file contents for raw.
func Sign(priv ed25519.PrivateKey, raw []byte) []byte {
	return []byte("ed25519:" + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw)) + "\n")
}

// Verify checks sig (a signature file) over raw with pub, and — when wantSHA256 is
// non-empty — that raw hashes to it. Both must hold; a pinned hash does not replace
// the signature.
func Verify(pub ed25519.PublicKey, raw, sig []byte, wantSHA256 string) error {
	b64, ok := strings.CutPrefix(strings.TrimSpace(string(sig)), "ed25519:")
	if !ok {
		return errors.New("signature file must be ed25519:<base64>")
	}
	s, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(s) != ed25519.SignatureSize {
		return errors.New("signature is not a base64 ed25519 signature")
	}
	if !ed25519.Verify(pub, raw, s) {
		return errors.New("bundle signature does not verify")
	}
	if wantSHA256 != "" {
		got := SHA256(raw)
		if !strings.EqualFold(got, strings.TrimPrefix(wantSHA256, "sha256:")) {
			return fmt.Errorf("bundle sha256 %s, pinned %s", got, wantSHA256)
		}
	}
	return nil
}

// VerifyAndDecode is the only way a fetched bundle is turned into a Bundle.
func VerifyAndDecode(pub ed25519.PublicKey, raw, sig []byte, wantSHA256 string) (*Bundle, error) {
	if err := Verify(pub, raw, sig, wantSHA256); err != nil {
		return nil, err
	}
	return Decode(raw)
}

// SHA256 is the hex digest of raw.
func SHA256(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
