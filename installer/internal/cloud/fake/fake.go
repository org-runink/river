// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package fake is a stand-in for a GCE metadata server and the part of Secret Manager that
// river-cloud-init uses. The unit tests serve it with httptest; build/cloud-image-test.sh
// serves it to a QEMU guest (installer/fakemeta). It is a TEST tool: it never ships on an
// image.
package fake

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
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
