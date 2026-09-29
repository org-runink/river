// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Validation errors are message keys; the UI translates them (web/i18n/*.json, "err.*").

var (
	hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	userName  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)
)

// Names an installer must not hand to useradd: system accounts that exist on the image, or
// that a node's services rely on.
var reservedUsers = map[string]bool{
	"root": true, "bin": true, "daemon": true, "mail": true, "ftp": true, "http": true,
	"nobody": true, "dbus": true, "polkitd": true, "sshd": true, "git": true, "uuidd": true,
	"river-kiosk": true, "river-pair": true, "sddm": true, "avahi": true, "rtkit": true,
	"systemd-network": true, "elogind": true, "tss": true, "nm-openconnect": true,
}

func validHostname(h string) string {
	if h == "" {
		return "err.hostname.empty"
	}
	if len(h) > 63 || !hostLabel.MatchString(h) {
		return "err.hostname.invalid"
	}
	return ""
}

func validUsername(u string) string {
	if u == "" {
		return "err.username.empty"
	}
	if !userName.MatchString(u) {
		return "err.username.invalid"
	}
	if reservedUsers[u] {
		return "err.username.reserved"
	}
	return ""
}

// validPassword: at least 8 characters, printable, and the two entries agree. Length is the
// only strength rule: a person who knows nothing about computers should not have to learn
// a policy, and the console password is a second factor to physical access.
func validPassword(p, confirm string) string {
	if p == "" {
		return "err.password.empty"
	}
	if !utf8.ValidString(p) {
		return "err.password.invalid"
	}
	if utf8.RuneCountInString(p) < 8 {
		return "err.password.short"
	}
	if len(p) > 512 {
		return "err.password.long"
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "err.password.invalid"
		}
	}
	if p != confirm {
		return "err.password.mismatch"
	}
	return ""
}

var sshKeyTypes = map[string]bool{
	"ssh-ed25519": true, "ssh-rsa": true, "ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true,
	"ecdsa-sha2-nistp521": true, "sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true,
}

// validSSHKeys checks pasted authorized_keys content: one or more "type base64 [comment]"
// lines whose base64 blob names the same key type. Blank lines and # comments are allowed.
// It returns the normalised text (one key per line) or an error key.
func validSSHKeys(s string) (string, string) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if s == "" {
		return "", ""
	}
	if len(s) > 64*1024 {
		return "", "err.sshkey.invalid"
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 || !sshKeyTypes[f[0]] {
			return "", "err.sshkey.invalid"
		}
		blob, err := base64.StdEncoding.DecodeString(f[1])
		if err != nil || len(blob) < 4+len(f[0]) {
			return "", "err.sshkey.invalid"
		}
		n := int(blob[0])<<24 | int(blob[1])<<16 | int(blob[2])<<8 | int(blob[3])
		if n != len(f[0]) || string(blob[4:4+n]) != f[0] {
			return "", "err.sshkey.invalid"
		}
		out = append(out, strings.Join(f, " "))
	}
	if len(out) == 0 {
		return "", "err.sshkey.invalid"
	}
	return strings.Join(out, "\n") + "\n", ""
}

// validWifi: an SSID of 1..32 bytes; a WPA passphrase of 8..63 printable characters (or 64
// hex digits), or empty for an open network.
func validWifi(ssid, psk string) string {
	if ssid == "" || len(ssid) > 32 || strings.ContainsAny(ssid, "\n\r\x00") {
		return "err.wifi.ssid"
	}
	if psk == "" {
		return ""
	}
	if strings.ContainsAny(psk, "\n\r\x00") {
		return "err.wifi.psk"
	}
	if len(psk) == 64 {
		for _, c := range psk {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return "err.wifi.psk"
			}
		}
		return ""
	}
	if len(psk) < 8 || len(psk) > 63 {
		return "err.wifi.psk"
	}
	return ""
}

// Languages and keyboard layouts the installer offers. Each layout maps to the console
// keymap (kbd) and the XKB layout (desktop, kiosk) of the same arrangement.
var languages = map[string]string{
	"en": "en_US.UTF-8", "es": "es_ES.UTF-8", "fr": "fr_FR.UTF-8", "pt": "pt_BR.UTF-8",
}

type layout struct {
	XKB, Variant, Keymap string
}

var layouts = map[string]layout{
	"us":    {"us", "", "us"},
	"gb":    {"gb", "", "uk"},
	"es":    {"es", "", "es"},
	"latam": {"latam", "", "la-latin1"},
	"fr":    {"fr", "", "fr-latin9"},
	"be":    {"be", "", "be-latin1"},
	"ch":    {"ch", "", "de_CH-latin1"},
	"de":    {"de", "", "de-latin1"},
	"it":    {"it", "", "it"},
	"pt":    {"pt", "", "pt-latin1"},
	"br":    {"br", "", "br-abnt2"},
	"ca":    {"ca", "", "cf"},
}

// defaultLayout is the keyboard a language preselects.
var defaultLayout = map[string]string{"en": "us", "es": "es", "fr": "fr", "pt": "br"}

// eraseWords are the words the erase confirmation accepts: ERASE in every language, and the
// word of the language on screen (the UI asks for that one).
var eraseWords = map[string]string{"en": "ERASE", "es": "BORRAR", "fr": "EFFACER", "pt": "APAGAR"}

func eraseWordOK(lang, typed string) bool {
	t := strings.ToUpper(strings.TrimSpace(typed))
	return t == "ERASE" || (eraseWords[lang] != "" && t == eraseWords[lang])
}
