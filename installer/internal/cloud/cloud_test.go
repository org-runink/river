// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/org-runink/river/installer/internal/cloud/fake"
)

func fixture(t *testing.T) (*fake.Config, *Metadata, *SecretManager) {
	t.Helper()
	kek := bytes.Repeat([]byte{7}, 32)
	cfg := &fake.Config{
		InstanceID: "1234567890", Hostname: "node-a.europe-west4-a.c.example-project.internal",
		Token: "tok", ProjectNumber: "4242",
		Attributes: map[string]string{
			"ssh-keys":          "alice:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOuXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX alice@example.org",
			"runink-key-secret": "projects/example-project/secrets/river-kek",
		},
		ProjectAttributes: map[string]string{"ssh-keys": "bob:ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7YYYYYYYYYYYY bob"},
		Secrets: map[string]map[string]string{
			"projects/example-project/secrets/river-kek": {"1": base64.StdEncoding.EncodeToString(kek)},
		},
	}
	srv := httptest.NewServer(cfg.Handler())
	t.Cleanup(srv.Close)
	md := &Metadata{Base: srv.URL + "/computeMetadata/v1"}
	// The unit test talks plain HTTP to 127.0.0.1, which CheckEndpoint (tested below) refuses.
	sm := &SecretManager{Meta: md, Endpoint: srv.URL + fake.SecretPrefix, insecureTestEndpoint: true}
	return cfg, md, sm
}

func TestMetadata(t *testing.T) {
	_, md, _ := fixture(t)
	ctx := context.Background()
	id, err := md.Wait(ctx, time.Millisecond)
	if err != nil || id != "1234567890" {
		t.Fatalf("Wait = %q, %v", id, err)
	}
	if v, err := md.Attr(ctx, "ssh-keys"); err != nil || !strings.HasPrefix(v, "alice:") {
		t.Fatalf("instance attribute first: %q %v", v, err)
	}
	if _, err := md.Attr(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing attribute: %v", err)
	}
}

func TestMetadataRejectsImpostor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello")) // no Metadata-Flavor header
	}))
	defer srv.Close()
	md := &Metadata{Base: srv.URL}
	if _, err := md.Get(context.Background(), "instance/id"); err == nil {
		t.Fatal("an answer without Metadata-Flavor: Google was believed")
	}
}

func TestSecretManagerProviderRoundTrip(t *testing.T) {
	cfg, _, sm := fixture(t)
	ctx := context.Background()
	p := &SecretManagerProvider{SM: sm, Secret: "projects/example-project/secrets/river-kek", AAD: "river-cloud/zriver/data"}
	key := bytes.Repeat([]byte{0xAB}, 32)
	b, err := p.Seal(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, key) {
		t.Fatal("the blob contains the key")
	}
	if name, _ := BlobProvider(b); name != "gcp-secret-manager" {
		t.Fatalf("provider %q", name)
	}
	if !strings.Contains(string(b), `"ref":"projects/4242/secrets/river-kek/versions/1"`) {
		t.Fatalf("seal did not pin the resolved version: %s", b)
	}
	// Rotating `latest` must not strand the blob: it opens with the version it pinned.
	cfg.Secrets["projects/example-project/secrets/river-kek"]["2"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	got, err := p.Unseal(ctx, b)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("unseal after rotation: %v", err)
	}
	// Another dataset's AAD does not open it.
	q := *p
	q.AAD = "river-cloud/other/data"
	if _, err := q.Unseal(ctx, b); err == nil {
		t.Fatal("blob opened with a different AAD")
	}
	// A different secret does not open it.
	cfg.Secrets["projects/example-project/secrets/river-kek"]["1"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	if _, err := p.Unseal(ctx, b); err == nil {
		t.Fatal("blob opened with the wrong secret")
	}
}

func TestSecretManagerErrors(t *testing.T) {
	cfg, _, sm := fixture(t)
	ctx := context.Background()
	if _, _, err := sm.Access(ctx, "not-a-name"); err == nil {
		t.Fatal("bad name accepted")
	}
	if _, _, err := sm.Access(ctx, "projects/example-project/secrets/absent"); err == nil {
		t.Fatal("absent secret accepted")
	}
	cfg.Secrets["projects/example-project/secrets/short"] = map[string]string{"1": base64.StdEncoding.EncodeToString([]byte("too short"))}
	p := &SecretManagerProvider{SM: sm, Secret: "projects/example-project/secrets/short"}
	if _, err := p.Seal(ctx, make([]byte, 32)); err == nil {
		t.Fatal("a secret under 32 bytes was used as a wrapping key")
	}
	p = &SecretManagerProvider{SM: sm}
	if _, err := p.Seal(ctx, make([]byte, 32)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unconfigured provider: %v", err)
	}
	cfg.NoServiceAccount = true
	if _, _, err := sm.Access(ctx, "projects/example-project/secrets/river-kek"); err == nil {
		t.Fatal("access without a service account succeeded")
	}
}

func TestCheckEndpoint(t *testing.T) {
	for ep, ok := range map[string]bool{
		"https://secretmanager.googleapis.com/v1":  true,
		"http://169.254.169.254/secretmanager/v1/": true,
		"http://secretmanager.googleapis.com/v1":   false,
		"http://192.0.2.10/secretmanager/v1":       false,
		"http://169.254.169.254.example.org/v1":    false,
		"ftp://169.254.169.254/":                   false,
	} {
		if (CheckEndpoint(ep) == nil) != ok {
			t.Errorf("CheckEndpoint(%q) ok=%v, want %v", ep, !ok, ok)
		}
	}
}

func TestTPMProviderUnavailable(t *testing.T) {
	p := &TPMProvider{Device: filepath.Join(t.TempDir(), "tpmrm0")}
	if _, err := p.Seal(context.Background(), make([]byte, 32)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("TPM seal: %v", err)
	}
}

func TestParseSSHKeys(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	in := strings.Join([]string{
		"alice:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOuAAAAAAAAAAAAAAAAAAAAA alice@example.org",
		"bob:ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB google-ssh {\"userName\":\"bob@example.org\",\"expireOn\":\"2026-09-25T11:00:00+0000\"}",
		"carol:ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAC google-ssh {\"userName\":\"carol@example.org\",\"expireOn\":\"2026-09-25T13:00:00+0000\"}",
		"no-colon ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOuBBBBBBBBBBBBB",
		"dave:ssh-dss AAAAB3NzaC1kc3MAAACBAAAA",
		"eve:ssh-ed25519 not*base64!",
		"alice2:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOuAAAAAAAAAAAAAAAAAAAAA duplicate",
		"",
		"# comment",
		"mallory:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOuCCCCCCCCCCCCC\x00;rm -rf",
	}, "\n")
	got := ParseSSHKeys(in, now)
	want := []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOuAAAAAAAAAAAAAAAAAAAAA metadata:alice",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAC metadata:carol",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestGrow(t *testing.T) {
	sys := t.TempDir()
	mk := func(p, v string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// /sys/class/block/nvme0n1p2 -> ../../devices/.../nvme0n1/nvme0n1p2
	dev := filepath.Join(sys, "devices/pci0000:00/nvme/nvme0/nvme0n1")
	mk(filepath.Join(dev, "size"), "209715200") // 100 GiB
	mk(filepath.Join(dev, "nvme0n1p2/partition"), "2")
	mk(filepath.Join(dev, "nvme0n1p2/start"), "2099200")
	mk(filepath.Join(dev, "nvme0n1p2/size"), "132116447") // ends at 64 GiB
	if err := os.MkdirAll(filepath.Join(sys, "class/block"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"nvme0n1", "nvme0n1/nvme0n1p2"} {
		if err := os.Symlink(filepath.Join(dev, strings.TrimPrefix(n, "nvme0n1")), filepath.Join(sys, "class/block", filepath.Base(n))); err != nil {
			t.Fatal(err)
		}
	}
	p, err := ReadPartition(sys, "/dev/nvme0n1p2")
	if err != nil {
		t.Fatal(err)
	}
	if p.Disk != "/dev/nvme0n1" || p.Number != 2 || p.Start != 2099200 || p.DiskTotal != 209715200 {
		t.Fatalf("%+v", p)
	}
	if !p.NeedsGrow() {
		t.Fatal("a 64 GiB partition on a 100 GiB disk does not need to grow?")
	}
	p.Sectors = p.DiskTotal - 34 - p.Start - 2048
	if p.NeedsGrow() {
		t.Fatal("a partition within 1 MiB of the end wants to grow")
	}

	info, err := ParseSgdiskInfo(`Partition GUID code: 0FC63DAF-8483-4772-8E79-3D69D8477DE4 (Linux filesystem)
Partition unique GUID: 3B0A5D0E-0000-4000-8000-000000000002
First sector: 2099200 (at 1.0 GiB)
Last sector: 134215646 (at 64.0 GiB)
Partition size: 132116447 sectors (63.0 GiB)
Attribute flags: 0000000000000000
Partition name: 'runink'
`)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(SgdiskGrowArgs("/dev/nvme0n1", 2, info), " ")
	want := "-d 2 -n 2:2099200:0 -t 2:0FC63DAF-8483-4772-8E79-3D69D8477DE4 -u 2:3B0A5D0E-0000-4000-8000-000000000002 -c 2:runink /dev/nvme0n1"
	if got != want {
		t.Fatalf("sgdisk args\n got %s\nwant %s", got, want)
	}
	if _, err := ParseSgdiskInfo("Partition name: ''\n"); err == nil {
		t.Fatal("incomplete sgdisk output accepted")
	}
}

func TestFindDataDiskSCSI(t *testing.T) {
	sys, dev := t.TempDir(), t.TempDir()
	mk := func(p string, v []byte) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, v, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pg80 := func(s string) []byte { return append([]byte{0, 0x80, 0, byte(len(s))}, s...) }
	mk(filepath.Join(sys, "block/sda/device/vendor"), []byte("Google  \n"))
	mk(filepath.Join(sys, "block/sda/device/model"), []byte("PersistentDisk  \n"))
	mk(filepath.Join(sys, "block/sda/device/vpd_pg80"), pg80("persistent-disk-0"))
	mk(filepath.Join(sys, "block/sdb/device/vendor"), []byte("Google\n"))
	mk(filepath.Join(sys, "block/sdb/device/model"), []byte("PersistentDisk\n"))
	mk(filepath.Join(sys, "block/sdb/device/vpd_pg80"), pg80("runink-data"))
	mk(filepath.Join(sys, "block/sdc/device/vendor"), []byte("QEMU\n")) // not a PD
	mk(filepath.Join(sys, "block/sdc/device/model"), []byte("PersistentDisk\n"))
	mk(filepath.Join(sys, "block/sdc/device/vpd_pg80"), pg80("runink-data"))
	got, err := FindDataDisk(sys, dev, "runink-data")
	if err != nil || got != filepath.Join(dev, "sdb") {
		t.Fatalf("FindDataDisk = %q, %v", got, err)
	}
	if _, err := FindDataDisk(sys, dev, "absent"); err == nil {
		t.Fatal("found a disk that is not attached")
	}
	if _, err := FindDataDisk(sys, dev, "../sda"); err == nil {
		t.Fatal("accepted a path as a device name")
	}
	// A by-id link wins.
	mk(filepath.Join(dev, "sdz"), nil)
	if err := os.MkdirAll(filepath.Join(dev, "disk/by-id"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dev, "sdz"), filepath.Join(dev, "disk/by-id/scsi-0Google_PersistentDisk_runink-data")); err != nil {
		t.Fatal(err)
	}
	if got, _ := FindDataDisk(sys, dev, "runink-data"); got != filepath.Join(dev, "sdz") {
		t.Fatalf("by-id link not preferred: %q", got)
	}
}

func TestParseNVMeVendorName(t *testing.T) {
	id := make([]byte, 4096)
	copy(id[384:], `{"device_name":"runink-data","disk_type":"PERSISTENT"}`)
	if n, err := ParseNVMeVendorName(id); err != nil || n != "runink-data" {
		t.Fatalf("got %q %v", n, err)
	}
	if _, err := ParseNVMeVendorName(make([]byte, 4096)); err == nil {
		t.Fatal("empty vendor area accepted")
	}
	if _, err := ParseNVMeVendorName(id[:1000]); err == nil {
		t.Fatal("short buffer accepted")
	}
}

func TestSecretResource(t *testing.T) {
	for in, want := range map[string]string{
		"core-oidc":                       "projects/example-project/secrets/core-oidc/versions/latest",
		"projects/p/secrets/s":            "projects/p/secrets/s/versions/latest",
		"projects/p/secrets/s/versions/4": "projects/p/secrets/s/versions/4",
		" spaced ":                        "projects/example-project/secrets/spaced/versions/latest",
	} {
		if got, err := SecretResource("example-project", in); err != nil || got != want {
			t.Errorf("SecretResource(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"a/b", "projects/p/other/s", ""} {
		if _, err := SecretResource("example-project", bad); err == nil {
			t.Errorf("SecretResource(%q) accepted", bad)
		}
	}
	if _, err := SecretResource("", "id"); err == nil {
		t.Error("a bare id without a project was accepted")
	}
}
