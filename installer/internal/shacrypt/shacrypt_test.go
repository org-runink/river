// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package shacrypt

import (
	"strings"
	"testing"
)

// Vectors from Drepper's specification (and glibc's crypt tests).
func TestVectors(t *testing.T) {
	for _, v := range []struct{ setting, pw, want string }{
		{"$6$saltstring", "Hello world!",
			"$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"$6$rounds=10000$saltstringsaltstring", "Hello world!",
			"$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
		{"$6$rounds=5000$toolongsaltstring", "This is just a test",
			"$6$rounds=5000$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0"},
		{"$6$rounds=10$roundstoolow", "the minimum number is still observed",
			"$6$rounds=1000$roundstoolow$kUMsbe306n21p9R.FRkW3IGn.S9NPN0x50YhH1xhLsPuWGsUSklZt58jaTfF4ZEQpyUNGc0dqbpBYYBaHHrsX."},
	} {
		got, err := Crypt([]byte(v.pw), v.setting)
		if err != nil || got != v.want {
			t.Errorf("Crypt(%q, %q) = %q, %v; want %q", v.pw, v.setting, got, err, v.want)
		}
	}
}

func TestHashVerifies(t *testing.T) {
	h, err := Hash([]byte("correct horse"), 0)
	if err != nil || !strings.HasPrefix(h, "$6$") {
		t.Fatalf("Hash: %q %v", h, err)
	}
	again, _ := Crypt([]byte("correct horse"), h)
	if again != h {
		t.Fatalf("round trip %q != %q", again, h)
	}
	if other, _ := Crypt([]byte("wrong"), h); other == h {
		t.Fatal("a wrong password verified")
	}
	if strings.Contains(h, "correct") {
		t.Fatal("hash contains the password")
	}
}
