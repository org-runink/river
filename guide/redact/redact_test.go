// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package redact

import (
	"strings"
	"testing"
)

func TestRedacts(t *testing.T) {
	r := New("node-7.example.internal")
	in := strings.Join([]string{
		"host node-7.example.internal NODE-7.EXAMPLE.INTERNAL",
		"mac 3c:52:82:aa:bb:cc and 3C-52-82-AA-BB-CC",
		"v4 192.168.1.20/24 v6 2001:db8::1 fe80::1%eth0 fd00::/8",
		"serial: S4EWNX0R123456 SN=ABC123 /dev/disk/by-id/nvme-Samsung_SSD_990_S4EW",
		"token ghu_abcdefghijklmnopqrstuvwxyz0123 github_pat_11AAAAAAA0123456789abcdef",    // gitleaks:allow (fake token fixture)
		"key 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",             // gitleaks:allow (fake key fixture)
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl", // gitleaks:allow (fake SSH key fixture)
		"uuid 123e4567-e89b-12d3-a456-426614174000",
	}, "\n")
	out := r.String(in)
	for _, leak := range []string{"node-7", "NODE-7", "3c:52", "3C-52", "192.168", "2001:db8", "fe80", "fd00", "S4EWNX0R", "ABC123", "Samsung_SSD", "ghu_", "github_pat_", "0123456789abcdef0123", "AAAAC3Nz", "123e4567"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in:\n%s", leak, out)
		}
	}
	keep := "Runink River step `hwprobe` (Probe the hardware): done. 2 disk(s), x86-64-v3, 64 GiB."
	if got := r.String(keep); got != keep {
		t.Errorf("over-redacted:\n%s\n%s", keep, got)
	}
}
