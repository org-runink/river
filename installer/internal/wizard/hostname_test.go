// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import "testing"

// The installed machine's default name is the edition's, or a name the NETWORK assigned, but
// never the live medium's own hostname (river#136: every install that kept the default was
// named runink-live, after the stick).
func TestDefaultHostnameNeverTheLiveMedium(t *testing.T) {
	r := newRig(t, nil)
	r.w.liveHost = "runink-live"
	for _, c := range []struct{ net, want string }{
		{"runink-live", r.w.self.Hostname}, // the stick's own name: not offered
		{"runink", r.w.self.Hostname},      // the generic default: the edition's
		{"lab-7", "lab-7"},                 // a name the network assigned: kept
	} {
		r.w.st.Net.State = &NetState{Hostname: c.net}
		if got := r.w.defaultHostname(); got != c.want {
			t.Errorf("network hostname %q: default %q, want %q", c.net, got, c.want)
		}
	}
}
