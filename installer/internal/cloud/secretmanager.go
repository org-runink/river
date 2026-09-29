// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package cloud

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultSecretManagerEndpoint is Google Secret Manager's REST endpoint.
const DefaultSecretManagerEndpoint = "https://secretmanager.googleapis.com/v1" // #nosec G101 -- a public API endpoint, not a credential

// secretName is a secret version resource: projects/P/secrets/S/versions/V. A bare
// projects/P/secrets/S means its latest version.
var secretName = regexp.MustCompile(`^projects/[A-Za-z0-9_.:-]+/secrets/[A-Za-z0-9_-]+(/versions/(latest|[0-9]+))?$`)

// SecretManager reads secret versions with the instance's default service-account token,
// which the metadata server issues. No cloud SDK and no credentials file on the node: the
// only secret it ever holds is a short-lived access token, in memory.
type SecretManager struct {
	Meta     *Metadata
	Endpoint string // default DefaultSecretManagerEndpoint
	HTTP     *http.Client

	insecureTestEndpoint bool // unit tests only: skip CheckEndpoint for an httptest server
}

// ValidSecretName reports whether s names a secret (version) this client accepts.
func ValidSecretName(s string) bool { return secretName.MatchString(s) }

// CheckEndpoint accepts the public HTTPS endpoint, or a plain-HTTP endpoint on the metadata
// server's own address. The second form exists for the QEMU stand-in of the cloud test
// (build/cloud-image-test.sh), whose fake metadata server also answers the secret API. On a
// real instance nothing else listens there, and the access token is only ever sent back to
// the host that issued it, so the override cannot leak it anywhere.
func CheckEndpoint(ep string) error {
	u, err := url.Parse(ep)
	if err != nil {
		return err
	}
	switch {
	case u.Scheme == "https" && u.Host != "":
		return nil
	case u.Scheme == "http" && u.Hostname() == MetadataIP:
		return nil
	}
	return fmt.Errorf("secret endpoint %q: must be https://..., or http://%s/... (test stand-in)", ep, MetadataIP)
}

func (s *SecretManager) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}}
}

// Token returns an access token for the instance's default service account.
func (s *SecretManager) Token(ctx context.Context) (string, error) {
	raw, err := s.Meta.Get(ctx, "instance/service-accounts/default/token")
	if err != nil {
		return "", fmt.Errorf("service-account token: %w", err)
	}
	var t struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal([]byte(raw), &t); err != nil || t.AccessToken == "" {
		return "", errors.New("service-account token: unreadable answer (does the instance have a service account?)")
	}
	return t.AccessToken, nil
}

// Access returns the payload of a secret version and the resolved version name
// (projects/<number>/secrets/S/versions/<n>), so a caller can pin exactly what it used.
func (s *SecretManager) Access(ctx context.Context, name string) ([]byte, string, error) {
	if !ValidSecretName(name) {
		return nil, "", fmt.Errorf("secret %q: want projects/P/secrets/S[/versions/V]", name)
	}
	if !strings.Contains(name, "/versions/") {
		name += "/versions/latest"
	}
	ep := s.Endpoint
	if ep == "" {
		ep = DefaultSecretManagerEndpoint
	}
	if err := CheckEndpoint(ep); err != nil && !s.insecureTestEndpoint {
		return nil, "", err
	}
	tok, err := s.Token(ctx)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(ep, "/")+"/"+name+":access", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("secret %s: %w", name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(bytes.TrimSpace(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, "", fmt.Errorf("secret %s: HTTP %d: %s", name, resp.StatusCode, msg)
	}
	var v struct {
		Name    string `json:"name"`
		Payload struct {
			Data   string `json:"data"`
			CRC32C string `json:"dataCrc32c"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, "", fmt.Errorf("secret %s: %w", name, err)
	}
	data, err := base64.StdEncoding.DecodeString(v.Payload.Data)
	if err != nil {
		return nil, "", fmt.Errorf("secret %s: payload is not base64", name)
	}
	if v.Payload.CRC32C != "" {
		want, err := strconv.ParseUint(v.Payload.CRC32C, 10, 32)
		if err != nil || uint32(want) != crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)) {
			return nil, "", fmt.Errorf("secret %s: payload fails its CRC32C", name)
		}
	}
	if v.Name == "" {
		v.Name = name
	}
	return data, v.Name, nil
}
