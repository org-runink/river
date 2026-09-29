// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package bundle

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

const doc = "# Guide\n\nIntro text.\n\n## Probe the hardware\n<!-- river-guide:step id=hwprobe kind=check run=\"river-hwprobe --json\" -->\n\nRun `river-hwprobe`.\n\n```sh\n# a comment, not a heading\nsudo runink-install\n```\n\n## Probe the hardware\n\nDuplicate title.\n"

func TestParseSectionsStepsAndAnchors(t *testing.T) {
	b, err := Parse("river", "1", []Doc{{Path: "a.md", Content: doc}})
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, s := range b.Sections {
		refs = append(refs, s.Ref())
	}
	want := "river#guide river#probe-the-hardware river#probe-the-hardware-1"
	if got := strings.Join(refs, " "); got != want {
		t.Fatalf("refs = %q, want %q", got, want)
	}
	if len(b.Steps) != 1 || b.Steps[0].ID != "hwprobe" || b.Steps[0].Kind != KindCheck || b.Steps[0].Run != "river-hwprobe --json" {
		t.Fatalf("steps = %+v", b.Steps[0])
	}
	sec := b.Section("probe-the-hardware")
	if strings.Contains(sec.Body, "river-guide:step") {
		t.Fatal("directive leaked into the body")
	}
	codes := strings.Join(sec.Code, "|")
	for _, c := range []string{"river-hwprobe", "sudo runink-install", "river-hwprobe --json"} {
		if !strings.Contains("|"+codes+"|", "|"+c+"|") {
			t.Fatalf("code %q missing from %q", c, codes)
		}
	}
}

func TestDirectiveValidation(t *testing.T) {
	for name, d := range map[string]string{
		"unknown kind":   `<!-- river-guide:step id=x kind=magic -->`,
		"info with run":  `<!-- river-guide:step id=x kind=info run="ls" -->`,
		"check no run":   `<!-- river-guide:step id=x kind=check -->`,
		"pipe":           `<!-- river-guide:step id=x kind=check run="ls | sh" -->`,
		"subshell":       `<!-- river-guide:step id=x kind=action run="echo $(id)" -->`,
		"bad id":         `<!-- river-guide:step id=X_1 kind=info -->`,
		"unknown key":    `<!-- river-guide:step id=x kind=info shell=yes -->`,
		"unterminated q": `<!-- river-guide:step id=x kind=check run="ls -->`,
	} {
		_, err := Parse("river", "1", []Doc{{Path: "a.md", Content: "## S\n" + d + "\n"}})
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Parse("river", "1", []Doc{{Path: "a.md", Content: "## A\n<!-- river-guide:step id=x kind=info -->\n## B\n<!-- river-guide:step id=x kind=info -->\n"}}); err == nil {
		t.Error("duplicate step id accepted")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Check the firmware mode":                       "check-the-firmware-mode",
		"GitHub is unreachable on an IPv6-only network": "github-is-unreachable-on-an-ipv6-only-network",
		"What's `zfs` (really)?":                        "whats-zfs-really",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	raw, err := Encode("platform", "7", []Doc{{Path: "a.md", Content: doc}})
	if err != nil {
		t.Fatal(err)
	}
	sig := Sign(priv, raw)
	b, err := VerifyAndDecode(pub, raw, sig, SHA256(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "platform" || b.Version != "7" || len(b.Steps) != 1 {
		t.Fatalf("decoded %+v", b)
	}
	k, err := ParsePublicKey(FormatPublicKey(pub))
	if err != nil || !k.Equal(pub) {
		t.Fatal("public key round trip")
	}

	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)/2] ^= 1
	if _, err := VerifyAndDecode(pub, tampered, sig, ""); err == nil {
		t.Fatal("tampered bundle verified")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := VerifyAndDecode(other, raw, sig, ""); err == nil {
		t.Fatal("wrong key verified")
	}
	if _, err := VerifyAndDecode(pub, raw, sig, strings.Repeat("0", 64)); err == nil {
		t.Fatal("sha256 pin ignored")
	}
	if _, err := Decode([]byte(`{"format":"other/v9","name":"x","version":"1","docs":[]}`)); err == nil {
		t.Fatal("wrong format accepted")
	}
}
