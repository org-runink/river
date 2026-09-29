// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package cloud

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// A KeyProvider wraps the per-instance data key so the node can unlock its encrypted data
// datasets unattended at every boot. docs/CLOUD-IMAGES.md, "Key providers", is the contract.
//
//   - The data key is generated on the instance at first boot (32 random bytes). It never
//     leaves the instance and is never written anywhere in the clear.
//   - Seal returns a blob that is safe to keep on the UNENCRYPTED boot environment: it is
//     useless without whatever the provider binds it to (a secret in the project's secret
//     manager, the instance's TPM).
//   - Unseal gives the data key back, or an error. It must not block without bound.
//   - Blobs name their provider; several may exist side by side for the same key, and the
//     unlock tries them in turn.
type KeyProvider interface {
	Name() string
	Seal(ctx context.Context, key []byte) ([]byte, error)
	Unseal(ctx context.Context, blob []byte) ([]byte, error)
}

// ErrUnavailable means the provider cannot work on this instance (not configured, device
// or tool missing). The caller tries the next provider.
var ErrUnavailable = errors.New("key provider unavailable")

// blob is the on-disk envelope every provider writes.
type blob struct {
	V        int    `json:"v"`
	Provider string `json:"provider"`
	Ref      string `json:"ref,omitempty"` // what the provider binds to (a secret version, PCRs)
	Salt     []byte `json:"salt,omitempty"`
	Nonce    []byte `json:"nonce,omitempty"`
	CT       []byte `json:"ct"`
}

// BlobProvider returns the provider name recorded in a blob.
func BlobProvider(b []byte) (string, error) {
	var e blob
	if err := json.Unmarshal(b, &e); err != nil {
		return "", fmt.Errorf("key blob: %w", err)
	}
	if e.V != 1 || e.Provider == "" {
		return "", errors.New("key blob: unknown version or no provider")
	}
	return e.Provider, nil
}

const wrapInfo = "river-cloud/data-key/v1"

// wrap seals key under a key derived from secret (HKDF-SHA256, random salt) with AES-256-GCM;
// aad binds the blob to its use.
func wrap(secret, key []byte, aad string) (salt, nonce, ct []byte, err error) {
	salt = make([]byte, 32)
	if _, err = rand.Read(salt); err != nil {
		return
	}
	g, err := gcmFor(secret, salt)
	if err != nil {
		return
	}
	nonce = make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return
	}
	ct = g.Seal(nil, nonce, key, []byte(aad))
	return
}

func unwrap(secret, salt, nonce, ct []byte, aad string) ([]byte, error) {
	g, err := gcmFor(secret, salt)
	if err != nil {
		return nil, err
	}
	if len(nonce) != g.NonceSize() {
		return nil, errors.New("key blob: bad nonce")
	}
	k, err := g.Open(nil, nonce, ct, []byte(aad))
	if err != nil {
		return nil, errors.New("key blob does not open with this secret (wrong secret version, or the blob was altered)")
	}
	return k, nil
}

func gcmFor(secret, salt []byte) (cipher.AEAD, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("wrapping secret is %d bytes; at least 32 are required", len(secret))
	}
	kek, err := hkdf.Key(sha256.New, secret, salt, wrapInfo, 32)
	if err != nil {
		return nil, err
	}
	b, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// SecretManagerProvider wraps the data key under a key-encryption secret kept in the
// project's secret manager (Google Secret Manager on GCE). The instance reads it with its
// own service-account token, over HTTPS, from userspace, after the network is up. The
// operator creates the secret (32+ random bytes) and grants the instance's service account
// roles/secretmanager.secretAccessor on that one secret; the name comes from the
// `runink-key-secret` metadata attribute. Seal pins the exact version it used, so rotating
// `latest` later never strands a node; retire an old version only after re-sealing.
type SecretManagerProvider struct {
	SM     *SecretManager
	Secret string // projects/P/secrets/S[/versions/V]
	AAD    string // binds the blob to its dataset
}

func (p *SecretManagerProvider) Name() string { return "gcp-secret-manager" }

func (p *SecretManagerProvider) Seal(ctx context.Context, key []byte) ([]byte, error) {
	if p.Secret == "" {
		return nil, fmt.Errorf("%w: no runink-key-secret metadata attribute", ErrUnavailable)
	}
	sec, version, err := p.SM.Access(ctx, p.Secret)
	if err != nil {
		return nil, err
	}
	salt, nonce, ct, err := wrap(sec, key, p.AAD)
	if err != nil {
		return nil, err
	}
	return json.Marshal(blob{V: 1, Provider: p.Name(), Ref: version, Salt: salt, Nonce: nonce, CT: ct})
}

func (p *SecretManagerProvider) Unseal(ctx context.Context, b []byte) ([]byte, error) {
	var e blob
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	if e.Provider != p.Name() {
		return nil, fmt.Errorf("blob is for %q", e.Provider)
	}
	sec, _, err := p.SM.Access(ctx, e.Ref)
	if err != nil {
		return nil, err
	}
	return unwrap(sec, e.Salt, e.Nonce, e.CT, p.AAD)
}

// TPMProvider is the Shielded VM vTPM provider: the data key sealed to the instance's TPM
// under a PCR policy, so the blob opens only on this VM in its measured boot state.
//
// NOT IMPLEMENTED YET. The image closure carries libtss2 but no sealing tool (no
// tpm2-tools), and swtpm was not available to test one in QEMU. docs/CLOUD-IMAGES.md
// ("vTPM: next") and docs/ENCRYPTION.md ("Follow-up: TPM2 unattended unlock") describe the
// plan: a small pure-Go TPM2 client over /dev/tpmrm0. It is registered so the interface,
// the blob naming and the unlock order are already the ones it will use.
type TPMProvider struct{ Device string }

func (p *TPMProvider) Name() string { return "tpm2" }

func (p *TPMProvider) Seal(context.Context, []byte) ([]byte, error) { return nil, p.unavailable() }

func (p *TPMProvider) Unseal(context.Context, []byte) ([]byte, error) { return nil, p.unavailable() }

func (p *TPMProvider) unavailable() error {
	dev := p.Device
	if dev == "" {
		dev = "/dev/tpmrm0"
	}
	if _, err := os.Stat(dev); err != nil {
		return fmt.Errorf("%w: no TPM at %s", ErrUnavailable, dev)
	}
	return fmt.Errorf("%w: TPM present at %s, but TPM sealing is not implemented yet (docs/CLOUD-IMAGES.md)", ErrUnavailable, dev)
}
