// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package cloud is the Go standard library side of Runink River's cloud images: a client
// for the instance metadata server, a client for a secret manager reached with the
// instance's own service-account token, the data-key provider interface, SSH key parsing
// and the helpers that grow the root pool to the attached disk. river-cloud-init uses it;
// docs/CLOUD-IMAGES.md is the design.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultMetadataBase is the GCE metadata server. Reached by IP, not by the
// metadata.google.internal name, so resolving it never depends on DNS.
const DefaultMetadataBase = "http://169.254.169.254/computeMetadata/v1"

// MetadataIP is the link-local address every supported cloud serves its metadata on.
const MetadataIP = "169.254.169.254"

// ErrNotFound is returned for a metadata key the server does not have (HTTP 404).
var ErrNotFound = errors.New("metadata: not found")

// Metadata reads the GCE-style metadata server.
type Metadata struct {
	Base string       // default DefaultMetadataBase
	HTTP *http.Client // default: 5 s timeout, no proxy
}

func (m *Metadata) base() string {
	if m.Base != "" {
		return strings.TrimRight(m.Base, "/")
	}
	return DefaultMetadataBase
}

func (m *Metadata) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	// Never through a proxy: the token this server hands out must not leave the host.
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
}

// Get returns the value at path (relative to the base, e.g. "instance/id").
func (m *Metadata) Get(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.base()+"/"+strings.TrimLeft(path, "/"), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := m.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("%w: %s", ErrNotFound, path)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("metadata %s: HTTP %d", path, resp.StatusCode)
	}
	// The real server always answers with this header; anything else on this address is
	// not the metadata server (a misrouted packet, a captive portal), and its answers are
	// not to be believed.
	if resp.Header.Get("Metadata-Flavor") != "Google" {
		return "", fmt.Errorf("metadata %s: response lacks Metadata-Flavor: Google", path)
	}
	return string(body), nil
}

// WaitForChange long-polls the metadata tree (recursive, as JSON) and returns its ETag. With
// an empty lastETag the server answers at once; otherwise it holds the request until the
// tree's content differs from lastETag or timeoutSec passes, and then answers with the
// current ETag either way, so an unchanged ETag means "timed out, nothing changed". This is
// the server's documented wait_for_change protocol; the guest environment watches metadata
// the same way. The client timeout is widened past timeoutSec for the held request.
func (m *Metadata) WaitForChange(ctx context.Context, lastETag string, timeoutSec int) (string, error) {
	if timeoutSec < 1 {
		timeoutSec = 60
	}
	q := url.Values{"recursive": {"true"}, "alt": {"json"}, "wait_for_change": {"true"}, "timeout_sec": {strconv.Itoa(timeoutSec)}}
	if lastETag != "" {
		q.Set("last_etag", lastETag)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.base()+"/?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	c := m.client()
	held := *c
	held.Timeout = time.Duration(timeoutSec+10) * time.Second
	resp, err := held.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata wait_for_change: HTTP %d", resp.StatusCode)
	}
	if resp.Header.Get("Metadata-Flavor") != "Google" {
		return "", errors.New("metadata wait_for_change: response lacks Metadata-Flavor: Google")
	}
	tag := resp.Header.Get("ETag")
	if tag == "" {
		return "", errors.New("metadata wait_for_change: response has no ETag")
	}
	return tag, nil
}

// Attr returns an instance attribute, falling back to the project attribute of the same
// name. ErrNotFound when neither is set.
func (m *Metadata) Attr(ctx context.Context, name string) (string, error) {
	v, err := m.Get(ctx, "instance/attributes/"+name)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return v, err
	}
	return m.Get(ctx, "project/attributes/"+name)
}

// Wait polls instance/id until the server answers or ctx ends. The network comes up in
// parallel with the boot, so the first requests commonly fail.
func (m *Metadata) Wait(ctx context.Context, every time.Duration) (string, error) {
	for {
		id, err := m.Get(ctx, "instance/id")
		if err == nil && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id), nil
		}
		select {
		case <-ctx.Done():
			if err == nil {
				err = errors.New("empty instance id")
			}
			return "", fmt.Errorf("metadata server not reachable: %w (last error: %v)", ctx.Err(), err)
		case <-time.After(every):
		}
	}
}
