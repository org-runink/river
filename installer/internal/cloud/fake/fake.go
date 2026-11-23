// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package fake is a stand-in for a GCE metadata server and the part of Secret Manager that
// river-cloud-init uses. The unit tests serve it with httptest; build/cloud-image-test.sh
// serves it to a QEMU guest (installer/fakemeta). It is a TEST tool: it never ships on an
// image.
package fake

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"maps"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is the fake server's state.
type Config struct {
	InstanceID        string            `json:"instance_id"`
	Hostname          string            `json:"hostname"`
	Attributes        map[string]string `json:"attributes"`
	ProjectAttributes map[string]string `json:"project_attributes"`
	Token             string            `json:"token"`
	ProjectID         string            `json:"project_id"`
	ProjectNumber     string            `json:"project_number"`
	// Secrets: "projects/P/secrets/S" -> version number -> base64 payload.
	Secrets map[string]map[string]string `json:"secrets"`
	// NoServiceAccount makes the token endpoint answer 404, as on an instance with none.
	NoServiceAccount bool `json:"no_service_account"`

	mu     sync.Mutex
	reload func() (*Config, error) // FileHandler: re-read the file on every request
}

// Load reads a Config from a JSON file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// SecretPrefix is where the fake answers the secret API, on the metadata address.
const SecretPrefix = "/secretmanager/v1/"

// Handler serves metadata under /computeMetadata/v1/ and the secret access call under
// SecretPrefix.
func (c *Config) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/computeMetadata/v1/", c.metadata)
	mux.HandleFunc(SecretPrefix, c.secret)
	return mux
}

func (c *Config) metadata(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Metadata-Flavor") != "Google" {
		http.Error(w, "missing Metadata-Flavor", http.StatusForbidden)
		return
	}
	w.Header().Set("Metadata-Flavor", "Google")
	p := strings.TrimPrefix(r.URL.Path, "/computeMetadata/v1/")
	if p == "" {
		c.tree(w, r)
		return
	}
	c = c.view()
	var v string
	ok := true
	switch {
	case p == "instance/id":
		v = c.InstanceID
	case p == "instance/hostname":
		v = c.Hostname
	case p == "project/project-id" && c.ProjectID != "":
		v = c.ProjectID
	case strings.HasPrefix(p, "instance/attributes/"):
		v, ok = c.Attributes[strings.TrimPrefix(p, "instance/attributes/")]
	case strings.HasPrefix(p, "project/attributes/"):
		v, ok = c.ProjectAttributes[strings.TrimPrefix(p, "project/attributes/")]
	case p == "instance/service-accounts/default/token":
		if c.NoServiceAccount {
			ok = false
			break
		}
		b, _ := json.Marshal(map[string]any{"access_token": c.Token, "expires_in": 3599, "token_type": "Bearer"})
		v = string(b)
	default:
		ok = false
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte(v))
}

func (c *Config) secret(w http.ResponseWriter, r *http.Request) {
	c = c.view()
	if r.Header.Get("Authorization") != "Bearer "+c.Token || c.Token == "" {
		http.Error(w, `{"error":{"code":401,"message":"bad token"}}`, http.StatusUnauthorized)
		return
	}
	name, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, SecretPrefix), ":access")
	if !ok {
		http.NotFound(w, r)
		return
	}
	sec, ver, ok := strings.Cut(name, "/versions/")
	vers := c.Secrets[sec]
	if vers == nil && c.ProjectNumber != "" {
		// Like the real API, accept the project NUMBER in place of its id.
		if _, short, found := strings.Cut(sec, "/secrets/"); found && strings.HasPrefix(sec, "projects/"+c.ProjectNumber+"/") {
			for k, v := range c.Secrets {
				if strings.HasSuffix(k, "/secrets/"+short) {
					vers = v
				}
			}
		}
	}
	if !ok || vers == nil {
		http.Error(w, `{"error":{"code":404,"message":"secret not found"}}`, http.StatusNotFound)
		return
	}
	if ver == "latest" {
		var ns []int
		for k := range vers {
			if n, err := strconv.Atoi(k); err == nil {
				ns = append(ns, n)
			}
		}
		if len(ns) == 0 {
			http.NotFound(w, r)
			return
		}
		sort.Ints(ns)
		ver = strconv.Itoa(ns[len(ns)-1])
	}
	data, ok := vers[ver]
	if !ok {
		http.Error(w, `{"error":{"code":404,"message":"version not found"}}`, http.StatusNotFound)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		http.Error(w, "bad fixture", http.StatusInternalServerError)
		return
	}
	resolved := sec + "/versions/" + ver
	if c.ProjectNumber != "" {
		if _, rest, ok := strings.Cut(resolved, "/secrets/"); ok {
			resolved = "projects/" + c.ProjectNumber + "/secrets/" + rest
		}
	}
	crc := crc32.Checksum(raw, crc32.MakeTable(crc32.Castagnoli))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":    resolved,
		"payload": map[string]string{"data": data, "dataCrc32c": strconv.FormatUint(uint64(crc), 10)},
	})
}

// FileHandler is Handler for a Config read from path, re-read on every request, so a test
// can change the instance's metadata (add an SSH key, say) while a guest is running.
func FileHandler(path string) (http.Handler, error) {
	c, err := Load(path)
	if err != nil {
		return nil, err
	}
	c.reload = func() (*Config, error) { return Load(path) }
	return c.Handler(), nil
}

// Update changes the in-memory state under the server's lock (unit tests).
func (c *Config) Update(f func(*Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c)
}

// view is a copy of the current state: the file's, for a FileHandler (the last good one
// when the file does not parse mid-write), else the in-memory one.
func (c *Config) view() *Config {
	if c.reload != nil {
		if n, err := c.reload(); err == nil {
			c.Update(func(cur *Config) { cur.copyFrom(n) })
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v := &Config{}
	v.copyFrom(c)
	return v
}

func (c *Config) copyFrom(o *Config) {
	c.InstanceID, c.Hostname, c.Token = o.InstanceID, o.Hostname, o.Token
	c.ProjectID, c.ProjectNumber, c.NoServiceAccount = o.ProjectID, o.ProjectNumber, o.NoServiceAccount
	c.Attributes, c.ProjectAttributes = maps.Clone(o.Attributes), maps.Clone(o.ProjectAttributes)
	c.Secrets = make(map[string]map[string]string, len(o.Secrets))
	for k, v := range o.Secrets {
		c.Secrets[k] = maps.Clone(v)
	}
}

// treeJSON is the recursive metadata document, in the shape the real server returns for
// /computeMetadata/v1/?recursive=true (the parts this fake serves), and its ETag.
func (c *Config) treeJSON() ([]byte, string) {
	b, _ := json.Marshal(map[string]any{
		"instance": map[string]any{"id": c.InstanceID, "hostname": c.Hostname, "attributes": nonNil(c.Attributes)},
		"project":  map[string]any{"projectId": c.ProjectID, "attributes": nonNil(c.ProjectAttributes)},
	})
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:8])
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// tree serves the recursive document. With wait_for_change=true and a last_etag equal to
// the current one it holds the request until the content changes or timeout_sec passes
// (then answers with the unchanged document), as the real server does.
func (c *Config) tree(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("recursive") != "true" {
		http.NotFound(w, r)
		return
	}
	body, tag := c.view().treeJSON()
	if q.Get("wait_for_change") == "true" && q.Get("last_etag") == tag {
		secs, err := strconv.Atoi(q.Get("timeout_sec"))
		if err != nil || secs < 1 || secs > 3600 {
			secs = 60
		}
		deadline := time.After(time.Duration(secs) * time.Second)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
	wait:
		for {
			select {
			case <-r.Context().Done():
				return
			case <-deadline:
				break wait
			case <-tick.C:
				if b, t := c.view().treeJSON(); t != tag {
					body, tag = b, t
					break wait
				}
			}
		}
	}
	w.Header().Set("ETag", tag)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
