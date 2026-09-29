// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package cloud

import (
	"encoding/json"
	"strings"
	"time"
)

// keyTypes are the OpenSSH public key algorithms accepted from metadata.
var keyTypes = map[string]bool{
	"ssh-ed25519": true, "ssh-rsa": true,
	"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true,
	"sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true,
}

// ParseSSHKeys turns GCE `ssh-keys` metadata into authorized_keys lines.
//
// Each metadata line is "USERNAME:KEYTYPE BASE64 [COMMENT]". Runink River has one admin
// account (runink), so every well-formed key is granted to it, whatever USERNAME says; the
// username is kept as the key's comment so `authorized_keys` shows whose key it is. A key
// whose comment is Google's `google-ssh {"userName":...,"expireOn":...}` is dropped once it
// has expired (that is how OS Login-less `gcloud compute ssh` issues short-lived keys).
// Malformed lines are skipped, never guessed at.
func ParseSSHKeys(text string, now time.Time) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		user, key, ok := strings.Cut(line, ":")
		if !ok || strings.ContainsAny(user, " \t") {
			continue
		}
		f := strings.Fields(key)
		if len(f) < 2 || !keyTypes[f[0]] || !isBase64(f[1]) {
			continue
		}
		comment := strings.Join(f[2:], " ")
		if strings.HasPrefix(comment, "google-ssh ") && expired(strings.TrimPrefix(comment, "google-ssh "), now) {
			continue
		}
		if seen[f[1]] {
			continue
		}
		seen[f[1]] = true
		out = append(out, f[0]+" "+f[1]+" "+sanitizeComment(user))
	}
	return out
}

func isBase64(s string) bool {
	if len(s) < 16 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/', c == '=':
		default:
			return false
		}
	}
	return true
}

func sanitizeComment(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c == '-' || c == '_' || c == '.' || c == '@' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "metadata"
	}
	return "metadata:" + b.String()
}

func expired(js string, now time.Time) bool {
	var v struct {
		ExpireOn string `json:"expireOn"`
	}
	if json.Unmarshal([]byte(js), &v) != nil || v.ExpireOn == "" {
		return false
	}
	for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339} {
		if t, err := time.Parse(layout, v.ExpireOn); err == nil {
			return !now.Before(t)
		}
	}
	// An expiry we cannot read is treated as expired: the key was meant to be temporary.
	return true
}
