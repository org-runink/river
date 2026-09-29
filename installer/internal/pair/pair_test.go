// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package pair

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T, seed byte) SSHKey {
	t.Helper()
	pub := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, 32)).Public().(ed25519.PublicKey)
	var blob []byte
	for _, s := range [][]byte{[]byte(Ed25519KeyType), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(s)))
		blob = append(blob, s...)
	}
	k, err := ParseSSHKey(Ed25519KeyType + " " + base64.StdEncoding.EncodeToString(blob) + " a comment")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// --- code -------------------------------------------------------------------------------

func TestCodeGenerateAndNormalize(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		c, err := NewCode(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != CodeLen {
			t.Fatalf("code %q has %d chars", c, len(c))
		}
		for _, r := range c {
			if !strings.ContainsRune(CodeAlphabet, r) {
				t.Fatalf("code %q has %q outside the alphabet", c, r)
			}
		}
		n, err := Normalize(FormatCode(c))
		if err != nil || n != c {
			t.Fatalf("Normalize(FormatCode(%q)) = %q, %v", c, n, err)
		}
		// Case, spaces and dashes do not matter.
		if n, err := Normalize(" " + strings.ToLower(c[:3]) + "-" + c[3:] + " "); err != nil || n != c {
			t.Fatalf("lenient Normalize(%q) = %q, %v", c, n, err)
		}
		seen[c] = true
	}
	if len(seen) < 1990 {
		t.Fatalf("only %d distinct codes out of 2000", len(seen))
	}
}

func TestCodeCrockfordAliases(t *testing.T) {
	// A code made of 0 and 1 may be read back as O and I/L.
	var c string
	for i := 0; ; i++ {
		data := strings.NewReader(string([]byte{byte(i & 1), byte(i >> 1 & 1), 0, 1, 0, 1, byte(i >> 2 & 1)}))
		cc, _ := NewCode(data)
		if strings.Trim(cc, "01") == "" {
			c = cc
			break
		}
		if i > 127 {
			t.Skip("no all-0/1 code in the sample")
		}
	}
	typed := strings.NewReplacer("0", "O", "1", "l").Replace(c)
	if n, err := Normalize(typed); err != nil || n != c {
		t.Fatalf("Normalize(%q) = %q, %v; want %q", typed, n, err, c)
	}
}

func TestCodeRejectsTypos(t *testing.T) {
	c, _ := NewCode(nil)
	if _, err := Normalize(c[:7]); !errors.Is(err, ErrCodeLength) {
		t.Fatalf("short code: %v", err)
	}
	if _, err := Normalize(c + "0"); !errors.Is(err, ErrCodeLength) {
		t.Fatalf("long code: %v", err)
	}
	if _, err := Normalize(c[:3] + "U" + c[4:]); !errors.Is(err, ErrCodeChar) {
		t.Fatalf("U: %v", err)
	}
	if _, err := Normalize(c[:3] + "é" + c[4:]); !errors.Is(err, ErrCodeChar) {
		t.Fatalf("non-ASCII: %v", err)
	}
	// Every single-character substitution the check character catches is refused locally
	// (the check character catches 31 in 32 of them); count the rest.
	missed, total := 0, 0
	for i := 0; i < CodeDataLen; i++ {
		for _, r := range CodeAlphabet {
			if byte(r) == c[i] {
				continue
			}
			total++
			if _, err := Normalize(c[:i] + string(r) + c[i+1:]); err == nil {
				missed++
			} else if !errors.Is(err, ErrCodeChecksum) {
				t.Fatalf("substitution: %v", err)
			}
		}
	}
	if missed*8 > total { // expect ~1/32; allow generous slack
		t.Fatalf("check character missed %d of %d substitutions", missed, total)
	}
}

func TestShortID(t *testing.T) {
	id, err := NewShortID(nil)
	if err != nil || !hostRe.MatchString(HostName(id)) {
		t.Fatalf("short id %q (%v) makes host %q", id, err, HostName(id))
	}
}

// --- HMAC pairing -----------------------------------------------------------------------

type fixture struct {
	sess    *Session
	code    string
	hostKey SSHKey
	opKey   SSHKey
	now     time.Time
}

func newFixture(t *testing.T) fixture {
	code, _ := NewCode(nil)
	hk, ok := testKey(t, 1), testKey(t, 2)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	return fixture{sess: NewSession("abc123", code, hk, now), code: code, hostKey: hk, opKey: ok, now: now}
}

func TestPairingHappyPath(t *testing.T) {
	f := newFixture(t)
	req, err := NewRequest(f.code, "abc123", f.hostKey.Fingerprint(), f.opKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.sess.Verify(f.now.Add(time.Minute), req)
	if err != nil || got.Fingerprint() != f.opKey.Fingerprint() {
		t.Fatalf("Verify: %v", err)
	}
	if !f.sess.Pending() {
		t.Fatal("not pending after a verified request")
	}
	// A second request while the local operator decides is turned away, not counted.
	if _, err := f.sess.Verify(f.now.Add(time.Minute), req); !errors.Is(err, ErrBusy) {
		t.Fatalf("second request while pending: %v", err)
	}
	hk, err := VerifyConfirm(f.code, f.hostKey.Fingerprint(), req, f.sess.Confirm(req))
	if err != nil || hk.String() != f.hostKey.String() {
		t.Fatalf("VerifyConfirm: %v", err)
	}
	ok, err := VerifyVerdict(f.code, req, f.sess.Decide(req, true))
	if err != nil || !ok {
		t.Fatalf("VerifyVerdict: %v %v", ok, err)
	}
	// One use: the same code (or request) never pairs again.
	if _, err := f.sess.Verify(f.now.Add(2*time.Minute), req); !errors.Is(err, ErrUsed) {
		t.Fatalf("reuse: %v", err)
	}
}

func TestPairingRefusedSpendsTheCode(t *testing.T) {
	f := newFixture(t)
	req, _ := NewRequest(f.code, "abc123", f.hostKey.Fingerprint(), f.opKey)
	if _, err := f.sess.Verify(f.now, req); err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyVerdict(f.code, req, f.sess.Decide(req, false))
	if err != nil || ok {
		t.Fatalf("refused verdict: %v %v", ok, err)
	}
	req2, _ := NewRequest(f.code, "abc123", f.hostKey.Fingerprint(), f.opKey)
	if _, err := f.sess.Verify(f.now, req2); !errors.Is(err, ErrUsed) {
		t.Fatalf("after refusal: %v", err)
	}
}

func TestPairingWrongCodeAndLockout(t *testing.T) {
	f := newFixture(t)
	wrong, _ := NewCode(nil)
	for wrong == f.code {
		wrong, _ = NewCode(nil)
	}
	for i := 1; i < MaxAttempts; i++ {
		req, _ := NewRequest(wrong, "abc123", f.hostKey.Fingerprint(), f.opKey)
		if _, err := f.sess.Verify(f.now, req); !errors.Is(err, ErrBadMAC) {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if f.sess.Attempts() != i {
			t.Fatalf("attempts = %d, want %d", f.sess.Attempts(), i)
		}
	}
	req, _ := NewRequest(wrong, "abc123", f.hostKey.Fingerprint(), f.opKey)
	if _, err := f.sess.Verify(f.now, req); !errors.Is(err, ErrLocked) || !f.sess.Locked() {
		t.Fatalf("attempt %d: %v (locked=%v)", MaxAttempts, err, f.sess.Locked())
	}
	// Locked for good: even the right code is refused now.
	good, _ := NewRequest(f.code, "abc123", f.hostKey.Fingerprint(), f.opKey)
	if _, err := f.sess.Verify(f.now, good); !errors.Is(err, ErrLocked) {
		t.Fatalf("right code after lockout: %v", err)
	}
}

func TestPairingExpiry(t *testing.T) {
	f := newFixture(t)
	req, _ := NewRequest(f.code, "abc123", f.hostKey.Fingerprint(), f.opKey)
	if _, err := f.sess.Verify(f.now.Add(CodeTTL), req); !errors.Is(err, ErrExpired) {
		t.Fatalf("at expiry: %v", err)
	}
	if f.sess.Attempts() != 0 {
		t.Fatal("an expired code counted an attempt")
	}
	if _, err := f.sess.Verify(f.now.Add(CodeTTL-time.Second), req); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	if CodeTTL != 15*time.Minute || MaxAttempts != 5 {
		t.Fatal("the documented limits are 15 minutes and 5 attempts")
	}
}

func TestPairingBindings(t *testing.T) {
	f := newFixture(t)
	other := testKey(t, 3)
	// A request made for another target: another host key counts as a wrong code, another
	// session id is not even a request for this session.
	r1, _ := NewRequest(f.code, "abc123", other.Fingerprint(), f.opKey)
	if _, err := f.sess.Verify(f.now, r1); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("request bound to another host key: %v", err)
	}
	r2, _ := NewRequest(f.code, "zzz999", f.hostKey.Fingerprint(), f.opKey)
	if _, err := f.sess.Verify(f.now, r2); !errors.Is(err, ErrProtocol) {
		t.Fatalf("request for another session: %v", err)
	}
	// Swapping the operator key under a valid MAC fails.
	r3, _ := NewRequest(f.code, "abc123", f.hostKey.Fingerprint(), f.opKey)
	r3.OpKey = other.String()
	if _, err := f.sess.Verify(f.now, r3); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("swapped operator key: %v", err)
	}
	// A key with options, a comment or another type is a protocol error, not an attempt.
	before := f.sess.Attempts()
	for _, k := range []string{f.opKey.String() + " comment", "ssh-rsa AAAAB3NzaC1yc2E=", `command="sh" ` + f.opKey.String()} {
		r, _ := NewRequest(f.code, "abc123", f.hostKey.Fingerprint(), f.opKey)
		r.OpKey = k
		if _, err := f.sess.Verify(f.now, r); !errors.Is(err, ErrProtocol) {
			t.Fatalf("op key %q: %v", k, err)
		}
	}
	if f.sess.Attempts() != before {
		t.Fatal("malformed requests counted attempts")
	}
	// The operator side rejects a confirm that is not under the code, or whose host key
	// is not the announced one.
	good, _ := NewRequest(f.code, "abc123", f.hostKey.Fingerprint(), f.opKey)
	conf := f.sess.Confirm(good)
	if _, err := VerifyConfirm(f.code, other.Fingerprint(), good, conf); err == nil {
		t.Fatal("confirm accepted for a host key the announcement did not carry")
	}
	wrongCode, _ := NewCode(nil)
	if _, err := VerifyConfirm(wrongCode, f.hostKey.Fingerprint(), good, conf); err == nil {
		t.Fatal("confirm accepted under another code")
	}
	forged := conf
	forged.HostKey = other.String()
	if _, err := VerifyConfirm(f.code, other.Fingerprint(), good, forged); err == nil {
		t.Fatal("confirm with a substituted host key accepted")
	}
	v := f.sess.Decide(good, false)
	v.Status = "accepted"
	if _, err := VerifyVerdict(f.code, good, v); err == nil {
		t.Fatal("a refusal flipped to accepted verified")
	}
}

func TestErrorReply(t *testing.T) {
	for _, e := range []error{ErrExpired, ErrLocked, ErrBadMAC, ErrUsed, ErrBusy, ErrProtocol, errors.New("x")} {
		r := ErrorReply(e)
		if r.Status != "error" || r.MAC != "" || r.HostKey != "" {
			t.Fatalf("ErrorReply(%v) = %+v", e, r)
		}
	}
}

// --- SSH keys ---------------------------------------------------------------------------

func TestSSHKey(t *testing.T) {
	k := testKey(t, 7)
	if !strings.HasPrefix(k.String(), "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI") || strings.Contains(k.String(), "comment") {
		t.Fatalf("String() = %q", k.String())
	}
	if !ValidFingerprint(k.Fingerprint()) {
		t.Fatalf("fingerprint %q", k.Fingerprint())
	}
	for _, bad := range []string{"", "ssh-ed25519", "ssh-rsa " + strings.Fields(k.String())[1], "ssh-ed25519 !!!",
		"ssh-ed25519 " + base64.StdEncoding.EncodeToString(append(k.Blob, 0))} {
		if _, err := ParseSSHKey(bad); err == nil {
			t.Fatalf("ParseSSHKey(%q) accepted", bad)
		}
	}
}

// --- beacon -----------------------------------------------------------------------------

func TestBeaconRoundTrip(t *testing.T) {
	b := Beacon{Host: "river-install-ab12cd", FP: testKey(t, 4).Fingerprint()}
	src := netip.MustParseAddrPort("[fe80::5054:ff:fe12:3456%eth0]:40000")
	got, err := ParseBeacon(b.Marshal(), src, "eth0")
	if err != nil || got != b || got.ID() != "ab12cd" {
		t.Fatalf("ParseBeacon = %+v, %v", got, err)
	}
}

func TestBeaconIgnoresNonLinkLocal(t *testing.T) {
	b := Beacon{Host: "river-install-ab12cd", FP: testKey(t, 4).Fingerprint()}.Marshal()
	for _, s := range []string{
		"[2001:db8::1]:40000",        // global unicast: could have been routed
		"[fd00::1]:40000",            // ULA
		"[::1]:40000",                // loopback
		"[ff02::1%eth0]:40000",       // multicast source
		"192.0.2.10:40000",           // IPv4
		"169.254.1.1:40000",          // IPv4 link-local: not used
		"[::ffff:169.254.1.1]:40000", // v4-mapped
	} {
		if _, err := ParseBeacon(b, netip.MustParseAddrPort(s), "eth0"); !errors.Is(err, ErrNotLinkLocal) {
			t.Errorf("source %s: %v, want ErrNotLinkLocal", s, err)
		}
	}
	if _, err := ParseBeacon(b, netip.MustParseAddrPort("[fe80::1%eth1]:40000"), "eth0"); !errors.Is(err, ErrWrongLink) {
		t.Errorf("other interface: %v", err)
	}
}

func TestBeaconIgnoresMalformed(t *testing.T) {
	fp := testKey(t, 4).Fingerprint()
	src := netip.MustParseAddrPort("[fe80::1%eth0]:40000")
	for name, p := range map[string]string{
		"empty":         "",
		"no newline":    "RIVER-PAIR 1\nhost river-install-ab12cd\nfp " + fp,
		"wrong magic":   "RIVER-PAIR 2\nhost river-install-ab12cd\nfp " + fp + "\n",
		"extra line":    "RIVER-PAIR 1\nhost river-install-ab12cd\nfp " + fp + "\nport 22\n",
		"missing fp":    "RIVER-PAIR 1\nhost river-install-ab12cd\nhost river-install-ab12cd\n",
		"dup host":      "RIVER-PAIR 1\nhost river-install-ab12cd\nhost river-install-ab12ce\n",
		"bad host":      "RIVER-PAIR 1\nhost evil.example.org\nfp " + fp + "\n",
		"host too long": "RIVER-PAIR 1\nhost river-install-ab12cdx\nfp " + fp + "\n",
		"upper host":    "RIVER-PAIR 1\nhost river-install-AB12CD\nfp " + fp + "\n",
		"bad fp":        "RIVER-PAIR 1\nhost river-install-ab12cd\nfp SHA256:short\n",
		"md5 fp":        "RIVER-PAIR 1\nhost river-install-ab12cd\nfp MD5:aa:bb\n",
		"crlf":          "RIVER-PAIR 1\r\nhost river-install-ab12cd\r\nfp " + fp + "\r\n",
		"nul":           "RIVER-PAIR 1\nhost river-install-ab12cd\x00\nfp " + fp + "\n",
		"no space":      "RIVER-PAIR 1\nhostriver-install-ab12cd\nfp " + fp + "\n",
		"oversized":     "RIVER-PAIR 1\nhost river-install-ab12cd\nfp " + fp + "\n" + strings.Repeat("x", MaxBeacon),
	} {
		if _, err := ParseBeacon([]byte(p), src, "eth0"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// --- interface choice -------------------------------------------------------------------

func TestChooseLink(t *testing.T) {
	ll := func(z string) netip.Addr { return netip.MustParseAddr("fe80::1").WithZone(z) }
	links := []Link{
		{Name: "lo", Up: true, Loopback: true},
		{Name: "eth0", Up: true, LinkLocal: ll("eth0")},
		{Name: "eth1", Up: true, LinkLocal: ll("eth1")},
		{Name: "eth2", Up: false, LinkLocal: ll("eth2")},
		{Name: "wg0", Up: true}, // no link-local address
	}
	pick := func(st, want string) string {
		l, err := ChooseLink(links, []byte(st), want)
		if err != nil {
			return "ERR"
		}
		return l.Name
	}
	if got := pick("", ""); got != "eth0" {
		t.Errorf("no net-state: %s", got)
	}
	st := `{"schema":"river.net-state/v1","links":[{"name":"eth2","up":true,"carrier":true},{"name":"eth1","up":true,"carrier":true}]}`
	if got := pick(st, ""); got != "eth1" {
		t.Errorf("net-state prefers eth1: %s", got)
	}
	if got := pick(`{"schema":"other"}`, ""); got != "eth0" {
		t.Errorf("foreign schema: %s", got)
	}
	if got := pick("", "eth1"); got != "eth1" {
		t.Errorf("--iface eth1: %s", got)
	}
	for _, w := range []string{"eth2", "wg0", "lo", "nope"} {
		if got := pick("", w); got != "ERR" {
			t.Errorf("--iface %s accepted", w)
		}
	}
	if _, err := ChooseLink(links[:1], nil, ""); !errors.Is(err, ErrNoLink) {
		t.Errorf("loopback only: %v", err)
	}
}

// --- RPC grammar ------------------------------------------------------------------------

func TestParseRPCAllowed(t *testing.T) {
	for _, c := range []string{
		"hello", "plan", "plan --lab", "reboot", "finish", "stage-payload models",
		"install --serial RIVERQEMU0002",
		"install --serial NOSERIAL-vda-80G --serial S3Z9NX0M123456 --pool zriver --be runink --hostname node-01",
	} {
		r, err := ParseRPC(c)
		if err != nil {
			t.Errorf("%q: %v", c, err)
			continue
		}
		if r.String() != c {
			t.Errorf("%q round-trips as %q", c, r.String())
		}
	}
}

func TestParseRPCRefused(t *testing.T) {
	for _, c := range []string{
		"", "sh", "bash -c id", "hello; id", "hello && id", "plan $(id)", "plan `id`", "plan --lab --lab",
		"hello world", "install", "install --serial", "install --serial a --pool 'x'", "install --serial ../x",
		"install --serial a --hostname Node_1", "install --serial a --pool 1pool", "install --serial a --shell /bin/sh",
		"stage-payload ../etc", "stage-payload a/b", "stage-payload", "reboot now",
		"scp -t /etc", "internal-sftp", "hello\nid", strings.Repeat("a", MaxCommand+1),
	} {
		if _, err := ParseRPC(c); err == nil {
			t.Errorf("%q accepted", c)
		}
	}
}

func TestParseRsync(t *testing.T) {
	r, err := ParseRPC("rsync --server -tre.iLsfxCIvu --delete . payload/models/")
	if err != nil || r.Verb != "rsync" || r.Name != "models" {
		t.Fatalf("rsync -rt --delete: %+v %v", r, err)
	}
	want := "rsync --server -tre.iLsfxCIvu --munge-links --delete . payload/models/"
	if strings.Join(r.Argv, " ") != want {
		t.Fatalf("argv %q, want %q", strings.Join(r.Argv, " "), want)
	}
	if _, err := ParseRPC("rsync --server -tre.iLsfxCIvu . payload/models"); err != nil {
		t.Fatalf("without --delete: %v", err)
	}
	for _, c := range []string{
		"rsync --server --sender -tre.iLsfxCIvu . payload/models/", // reading back
		"rsync --server -logDtpre.iLsfxCIvu . payload/models/",     // -a: links, owners, devices
		"rsync --server -tre.iLsfxCIvu --log-file=/etc/x . payload/models/",
		"rsync --server -tre.iLsfxCIvu . /etc/",
		"rsync --server -tre.iLsfxCIvu . payload/../../etc/",
		"rsync --server -tre.iLsfxCIvu . payload/",
		"rsync --server -tre.iLsfxCIvu . payload/a/b/",
		"rsync --daemon",
		"rsync -tre.iLsfxCIvu . payload/models/",
	} {
		if _, err := ParseRPC(c); err == nil {
			t.Errorf("%q accepted", c)
		}
	}
}

func TestSecretsRoundTrip(t *testing.T) {
	adm := "ssh-ed25519 " + strings.Fields(testKey(t, 9).String())[1] + " admin@example.org"
	in := Secrets{ModelsPassphrase: "correct horse battery staple", AdminKeys: []string{adm}}
	var b bytes.Buffer
	if err := EncodeSecrets(&b, in); err != nil {
		t.Fatal(err)
	}
	b.WriteString("trailing data is not read\n")
	out, err := ReadSecrets(&b)
	if err != nil || out.ModelsPassphrase != in.ModelsPassphrase || len(out.AdminKeys) != 1 || out.AdminKeys[0] != adm {
		t.Fatalf("ReadSecrets = %+v, %v", out, err)
	}
	for _, bad := range []string{
		"", "models-passphrase !!!\nend\n", "unknown eA==\nend\n", "models-passphrase eA==\n",
		"admin-key " + base64.StdEncoding.EncodeToString([]byte(`command="sh" `+adm)) + "\nend\n",
		"models-passphrase " + base64.StdEncoding.EncodeToString([]byte("a\nb")) + "\nend\n",
	} {
		if _, err := ReadSecrets(strings.NewReader(bad)); err == nil {
			t.Errorf("ReadSecrets(%q) accepted", bad)
		}
	}
}

func TestFrames(t *testing.T) {
	var b bytes.Buffer
	w := FrameWriter{W: &b}
	w.Write([]byte("hello "))
	w.Write([]byte("world"))
	WriteFrame(&b, FrameExit, []byte("3"))
	var got []string
	for {
		typ, d, err := ReadFrame(&b)
		if err != nil {
			break
		}
		got = append(got, string(typ)+":"+string(d))
	}
	if strings.Join(got, "|") != "o:hello |o:world|x:3" {
		t.Fatalf("frames %q", got)
	}
}

func TestRecoveryKeyLine(t *testing.T) {
	if !RecoveryKeyLine.MatchString("     0123abcd 0123abcd 0123abcd 0123abcd 0123abcd 0123abcd 0123abcd 0123abcd") {
		t.Fatal("grouped key not matched")
	}
	if RecoveryKeyLine.MatchString("===> 10-disk-zfs") {
		t.Fatal("step line matched")
	}
}
