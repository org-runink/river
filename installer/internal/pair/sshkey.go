// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package pair

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

// Ed25519KeyType is the only SSH key type pairing accepts, for the target's host key and
// for the operator's key alike.
const Ed25519KeyType = "ssh-ed25519"

// ErrKey is returned for anything that is not exactly one OpenSSH ed25519 public key.
var ErrKey = errors.New("not an ssh-ed25519 public key")

// SSHKey is an ed25519 public key in SSH wire format.
type SSHKey struct {
	Blob []byte // string "ssh-ed25519" + string <32 bytes>
}

// ParseSSHKey reads "ssh-ed25519 <base64> [comment]" (an OpenSSH .pub line). The comment is
// dropped, so nothing an untrusted party wrote can reach an authorized_keys file except the
// re-encoded key itself.
func ParseSSHKey(line string) (SSHKey, error) {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < 2 || f[0] != Ed25519KeyType || len(f[1]) > 128 {
		return SSHKey{}, ErrKey
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return SSHKey{}, ErrKey
	}
	// string "ssh-ed25519", string key(32), nothing else.
	rest, typ, ok := sshString(blob)
	if !ok || string(typ) != Ed25519KeyType {
		return SSHKey{}, ErrKey
	}
	rest, key, ok := sshString(rest)
	if !ok || len(key) != 32 || len(rest) != 0 {
		return SSHKey{}, ErrKey
	}
	return SSHKey{Blob: blob}, nil
}

func sshString(b []byte) (rest, s []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) { // #nosec G115 -- len(b) >= 4 was checked above; a slice length is never negative
		return nil, nil, false
	}
	return b[4+n:], b[4 : 4+n], true
}

// String is the canonical one-line form, "ssh-ed25519 <base64>", with no comment.
func (k SSHKey) String() string {
	return Ed25519KeyType + " " + base64.StdEncoding.EncodeToString(k.Blob)
}

// Fingerprint is OpenSSH's SHA256 fingerprint, as `ssh-keygen -l` prints it.
func (k SSHKey) Fingerprint() string {
	h := sha256.Sum256(k.Blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(h[:])
}

// ValidFingerprint reports whether s has the shape of an OpenSSH SHA256 fingerprint.
func ValidFingerprint(s string) bool {
	b, ok := strings.CutPrefix(s, "SHA256:")
	if !ok || len(b) != 43 {
		return false
	}
	d, err := base64.RawStdEncoding.DecodeString(b)
	return err == nil && len(d) == sha256.Size
}
