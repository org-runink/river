// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Command river-pair is the OPERATOR side of opt-in LAN install pairing (docs/INSTALL.md,
// "LAN installs"). It installs Runink River onto other machines on the same link, one after
// another, but only machines whose own operator booted the live installer, chose "Let
// another machine install this one" and answers "y" on that machine's own screen.
//
//	river-pair list [--iface IF] [--listen DUR]
//	    print the installers announcing themselves on the local link (passive: it only
//	    listens, it never probes a host that did not announce)
//	river-pair [install] [--iface IF] [--listen DUR] [--lab] [--answers FILE]
//	           [--models-passphrase-file F] [--admin-keys FILE] [--payload DIR ...]
//	           [--pool P] [--be B]
//	    pick an announced target, type the code its screen shows, check its host key
//	    fingerprint against its screen, then drive its install over the restricted SSH
//	    channel: the plan, each disk's serial typed here, payloads, runink-autoinstall with
//	    the secrets streamed (never written to disk on either side), progress, the recovery
//	    key (shown once here and once on the target), reboot. Then the next target.
//
// --answers FILE (datacenter batches): serials listed there are confirmed without typing,
// with an optional hostname each; see docs/INSTALL.md. The pairing code and the target's
// "y" are still needed for every machine.
//
// Exit status: 0 success, 1 failure, 2 usage.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/org-runink/river/installer/internal/pair"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

type opts struct {
	iface     string
	listen    time.Duration
	lab       bool
	answers   string
	passFile  string
	adminKeys string
	payloads  multi
	pool, be  string
}

var stdin = bufio.NewReader(os.Stdin)

func main() {
	args := os.Args[1:]
	cmd := "install"
	if len(args) > 0 && (args[0] == "list" || args[0] == "install") {
		cmd, args = args[0], args[1:]
	}
	var o opts
	fs := flag.NewFlagSet("river-pair "+cmd, flag.ContinueOnError)
	fs.StringVar(&o.iface, "iface", "", "interface to listen on (default: the one the network step set up)")
	fs.DurationVar(&o.listen, "listen", 8*time.Second, "how long to listen for announcements")
	if cmd == "install" {
		fs.BoolVar(&o.lab, "lab", false, "plan with the documented minimums waived (VMs and test rigs ONLY)")
		fs.StringVar(&o.answers, "answers", "", "answers file with explicit disk serials (datacenter batches)")
		fs.StringVar(&o.passFile, "models-passphrase-file", "", "model payload passphrase (first line), streamed to each target")
		fs.StringVar(&o.adminKeys, "admin-keys", "", "SSH public keys for the installed nodes' runink admin")
		fs.Var(&o.payloads, "payload", "payload directory to rsync to each target and stage (repeatable)")
		fs.StringVar(&o.pool, "pool", "zriver", "ZFS pool name")
		fs.StringVar(&o.be, "be", "runink", "boot-environment name")
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		os.Exit(2)
	}
	link, err := pair.PickLink(o.iface)
	if err != nil {
		fail(err)
	}
	if cmd == "list" {
		heard, err := listen(link, o.listen, nil)
		if err != nil {
			fail(err)
		}
		printHeard(heard)
		return
	}
	if err := installLoop(link, o); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "river-pair:", err)
	os.Exit(1)
}

func say(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

func ask(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	s, err := stdin.ReadString('\n')
	if err != nil && s == "" {
		fail(errors.New("no input"))
	}
	return strings.TrimSpace(s)
}

func yes(prompt string, def bool) bool {
	s := strings.ToLower(ask(prompt))
	if s == "" {
		return def
	}
	return s == "y" || s == "yes"
}

// askSecret reads a line with the terminal's echo off (stty; nothing else is needed).
func askSecret(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	echoOff := off.Run() == nil
	s, _ := stdin.ReadString('\n')
	if echoOff {
		on := exec.Command("stty", "echo")
		on.Stdin = os.Stdin
		_ = on.Run()
	}
	fmt.Fprintln(os.Stderr)
	return strings.TrimRight(s, "\r\n")
}

// listen collects announcements for d on the link's beacon group. It only receives.
func listen(link pair.Link, d time.Duration, skip map[string]bool) ([]pair.Heard, error) {
	ifi, err := net.InterfaceByName(link.Name)
	if err != nil {
		return nil, err
	}
	c, err := net.ListenMulticastUDP("udp6", ifi, &net.UDPAddr{IP: net.ParseIP(pair.BeaconGroup), Port: pair.BeaconPort, Zone: link.Name})
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %w", link.Name, err)
	}
	defer c.Close()
	say("river-pair: listening for Runink River installers on %s (link-local, passive) for %s...", link.Name, d)
	seen := map[string]pair.Heard{}
	deadline := time.Now().Add(d)
	buf := make([]byte, pair.MaxBeacon+1)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		n, src, err := c.ReadFromUDPAddrPort(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			return nil, err
		}
		if src.Addr().Zone() == "" && src.Addr().IsLinkLocalUnicast() {
			src = netip.AddrPortFrom(src.Addr().WithZone(link.Name), src.Port())
		}
		b, err := pair.ParseBeacon(buf[:n], src, link.Name)
		if err != nil || skip[b.Host] {
			continue // silently: a malformed, foreign or routed packet is not a target
		}
		seen[b.Host] = pair.Heard{Beacon: b, Addr: src.Addr()}
	}
	out := make([]pair.Heard, 0, len(seen))
	for _, h := range seen {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

func printHeard(h []pair.Heard) {
	if len(h) == 0 {
		fmt.Println("no Runink River installer is announcing itself on this link")
		return
	}
	for i, x := range h {
		fmt.Printf("%2d) %s\n", i+1, x)
	}
}

type answers struct {
	lab       bool
	pool, be  string
	serials   map[string]string // serial -> hostname ("" = ask)
	passFile  string
	adminKeys string
	payloads  []string
}

// readAnswers parses an answers file:
//
//	serial <SERIAL> [hostname <NAME>]   a disk the plan may erase without typing its serial
//	pool <NAME> | be <NAME> | lab yes|no
//	models-passphrase-file <FILE> | admin-keys <FILE> | payload <DIR>
func readAnswers(path string) (*answers, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	a := &answers{serials: map[string]string{}}
	for n, l := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		bad := fmt.Errorf("%s:%d: cannot read %q", path, n+1, strings.TrimSpace(l))
		switch {
		case f[0] == "serial" && (len(f) == 2 || len(f) == 4 && f[2] == "hostname"):
			if _, err := pair.ParseRPC("install --serial " + f[1]); err != nil {
				return nil, bad
			}
			a.serials[f[1]] = ""
			if len(f) == 4 {
				if _, err := pair.ParseRPC("install --serial x --hostname " + f[3]); err != nil {
					return nil, bad
				}
				a.serials[f[1]] = f[3]
			}
		case len(f) == 2 && f[0] == "pool":
			a.pool = f[1]
		case len(f) == 2 && f[0] == "be":
			a.be = f[1]
		case len(f) == 2 && f[0] == "lab" && (f[1] == "yes" || f[1] == "no"):
			a.lab = f[1] == "yes"
		case len(f) == 2 && f[0] == "models-passphrase-file":
			a.passFile = f[1]
		case len(f) == 2 && f[0] == "admin-keys":
			a.adminKeys = f[1]
		case len(f) == 2 && f[0] == "payload":
			a.payloads = append(a.payloads, f[1])
		default:
			return nil, bad
		}
	}
	return a, nil
}

func installLoop(link pair.Link, o opts) error {
	ans := &answers{serials: map[string]string{}}
	if o.answers != "" {
		var err error
		if ans, err = readAnswers(o.answers); err != nil {
			return err
		}
	}
	lab := o.lab || ans.lab
	pool, be := o.pool, o.be
	if ans.pool != "" {
		pool = ans.pool
	}
	if ans.be != "" {
		be = ans.be
	}
	passFile, adminFile := o.passFile, o.adminKeys
	if passFile == "" {
		passFile = ans.passFile
	}
	if adminFile == "" {
		adminFile = ans.adminKeys
	}
	payloads := append(append([]string{}, o.payloads...), ans.payloads...)
	for _, p := range payloads {
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return fmt.Errorf("payload %s is not a directory", p)
		}
	}

	// Secrets are read once, held in memory, and streamed to each target.
	var sec pair.Secrets
	if passFile != "" {
		b, err := os.ReadFile(passFile)
		if err != nil {
			return err
		}
		sec.ModelsPassphrase = strings.TrimRight(strings.SplitN(string(b), "\n", 2)[0], "\r")
	}
	if adminFile != "" {
		keys, err := readAdminKeys(adminFile)
		if err != nil {
			return err
		}
		sec.AdminKeys = keys
	}

	work, err := os.MkdirTemp(runtimeDir(), "river-pair-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; _ = os.RemoveAll(work); fmt.Fprintln(os.Stderr); os.Exit(130) }()

	done := map[string]bool{}
	for {
		heard, err := listen(link, o.listen, done)
		if err != nil {
			return err
		}
		if len(heard) == 0 {
			say("No Runink River installer is announcing itself on %s.", link.Name)
			say("On the machine to install: boot the Runink River medium, run sudo runink-install, choose")
			say("\"Let another machine install this one\".")
			if yes("Listen again? [Y/n] ", true) {
				continue
			}
			return nil
		}
		fmt.Fprintln(os.Stderr)
		for i, h := range heard {
			fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, h)
		}
		sel := ask(fmt.Sprintf("Select a target [1-%d], r to listen again, q to quit: ", len(heard)))
		if sel == "q" {
			return nil
		}
		n, err := strconv.Atoi(sel)
		if err != nil || n < 1 || n > len(heard) {
			continue
		}
		t := heard[n-1]
		ok, err := installOne(work, t, lab, pool, be, sec, payloads, ans)
		if err != nil {
			say("river-pair: %s: %v", t.Host, err)
		}
		if ok {
			done[t.Host] = true
		}
		if !yes("Install another machine on this network? [y/N] ", false) {
			if ok {
				return nil
			}
			return errors.New(t.Host + ": not installed")
		}
	}
}

func readAdminKeys(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, err := pair.ParseAuthorizedKey(l)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, errors.New(path + ": no public key")
	}
	return keys, nil
}

func runtimeDir() string {
	for _, d := range []string{os.Getenv("XDG_RUNTIME_DIR"), "/run"} {
		if d == "" {
			continue
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() && syscall.Access(d, 2) == nil { // #nosec G703 -- probing candidate runtime dirs (/run/user/1000, /run) for writability
			return d
		}
	}
	return os.TempDir()
}

// target is one paired machine.
type target struct {
	pair.Heard
	work string // this target's key, known_hosts
}

func installOne(work string, h pair.Heard, lab bool, pool, be string, sec pair.Secrets, payloads []string, ans *answers) (bool, error) {
	t := &target{Heard: h, work: filepath.Join(work, h.ID())}
	if err := os.Mkdir(t.work, 0o700); err != nil {
		return false, err
	}
	defer os.RemoveAll(t.work)

	// 1. The code, typed from the target's screen. Checked here first, so a typo never
	// costs one of the target's five attempts.
	var code string
	for {
		c, err := pair.Normalize(ask(fmt.Sprintf("Type the pairing code shown on %s's screen: ", h.Host)))
		if err == nil {
			code = c
			break
		}
		say("  %v", err)
	}
	// 2. The fingerprint, compared with the target's screen by a human.
	say("")
	say("%s announces this host key:", h.Host)
	say("    %s", h.FP)
	if !yes("Does the target's screen show exactly this host key? [y/N] ", false) {
		return false, errors.New("host key not confirmed: not paired (a machine announcing a key its screen does not show is not your target)")
	}
	// 3. Pair.
	key := filepath.Join(t.work, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "river-pair-operator", "-f", key).CombinedOutput(); err != nil { // #nosec G204 -- fixed program; the key path is in this program's own 0700 temp dir
		return false, fmt.Errorf("ssh-keygen: %v: %s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return false, err
	}
	opKey, err := pair.ParseSSHKey(string(pub))
	if err != nil {
		return false, err
	}
	hostKey, err := pairWith(t, code, opKey)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(t.work, "known_hosts"), []byte(h.Host+" "+hostKey.String()+"\n"), 0o600); err != nil { // #nosec G703 -- t.work is this program's own 0700 temp dir joined with a validated session id
		return false, err
	}
	say("river-pair: paired with %s.", h.Host)
	if _, err := t.rpc("hello", nil, os.Stderr); err != nil {
		return false, err
	}

	// 4. The plan, made on the target.
	cmd := "plan"
	if lab {
		cmd = "plan --lab"
	}
	var planOut bytes.Buffer
	rc, err := t.rpc(cmd, nil, &planOut)
	if err != nil {
		return false, err
	}
	text, disks, _ := strings.Cut(planOut.String(), "RIVER-PAIR-DISKS\n")
	say("")
	say("=== install plan of %s ===", h.Host)
	fmt.Fprint(os.Stderr, text)
	if rc == 3 {
		_, _ = t.rpc("finish", nil, io.Discard)
		return false, errors.New("the plan is REFUSED (below the documented minimums; --lab waives them on test rigs only)")
	}
	if rc != 0 {
		_, _ = t.rpc("finish", nil, io.Discard)
		return false, fmt.Errorf("planning failed (exit %d)", rc)
	}
	// 5. Every disk the plan erases, confirmed by serial.
	var serials []string
	hostname := ""
	for _, l := range strings.Split(disks, "\n") { // not TrimSpace: an empty model is a trailing tab
		f := strings.Split(l, "\t")
		if len(f) < 6 {
			continue
		}
		name, id, gib, kind, role, model := f[0], f[1], f[2], f[3], f[4], f[5]
		if model == "" {
			model = "no model string"
		}
		if hn, ok := ans.serials[id]; ok {
			say("  %s (%s, %s GiB): serial %s confirmed by the answers file", name, role, gib, id)
			serials = append(serials, id)
			if hn != "" {
				hostname = hn
			}
			continue
		}
		confirmed := false
		for try := 0; try < 3 && !confirmed; try++ {
			s := ask(fmt.Sprintf("ERASE %s on %s (%s, %s, %s GiB, %s)? Type its serial [%s] to confirm: ", name, h.Host, role, kind, gib, model, id))
			if s == id {
				confirmed = true
			} else {
				say("  serial does not match %s", name)
			}
		}
		if !confirmed {
			_, _ = t.rpc("finish", nil, io.Discard)
			return false, fmt.Errorf("%s not confirmed: aborted, nothing was written", name)
		}
		serials = append(serials, id)
	}
	if len(serials) == 0 {
		_, _ = t.rpc("finish", nil, io.Discard)
		return false, errors.New("the plan selects no disk")
	}
	if hostname == "" {
		hostname = ask("Hostname for this machine [runink]: ")
		if hostname == "" {
			hostname = "runink"
		}
	}
	inst := pair.RPC{Verb: "install", Serials: serials, Pool: pool, BE: be, Hostname: hostname}
	if _, err := pair.ParseRPC(inst.String()); err != nil {
		_, _ = t.rpc("finish", nil, io.Discard)
		return false, err
	}
	s := sec
	if s.ModelsPassphrase == "" {
		s.ModelsPassphrase = askSecret("Model payload passphrase (blank: models are deferred to enrollment): ")
	}
	if len(s.AdminKeys) == 0 {
		if p := ask("File with the SSH public key(s) for the installed node's runink admin (blank: none): "); p != "" {
			keys, err := readAdminKeys(p)
			if err != nil {
				_, _ = t.rpc("finish", nil, io.Discard)
				return false, err
			}
			s.AdminKeys = keys
		}
	}
	say("")
	say("About to install %s: pool %s, boot environment %s, hostname %s, erasing %s.", h.Host, pool, be, hostname, strings.Join(serials, ", "))
	say("The new pool is ENCRYPTED; its recovery key is shown ONCE below and once on the target's screen.")
	if ask("Type YES to proceed: ") != "YES" {
		_, _ = t.rpc("finish", nil, io.Discard)
		return false, errors.New("aborted")
	}

	// 6. Payloads, over the same restricted SSH channel, then staged on the target.
	for _, p := range payloads {
		name := filepath.Base(filepath.Clean(p))
		say("=== payload %s -> %s ===", name, h.Host)
		rs := exec.Command("rsync", "-rt", "--delete", "-e", strings.Join(append([]string{"ssh"}, t.sshArgs()...), " "), // #nosec G204 -- fixed program; the payload dir is the operator's own argument, the ssh arguments are built here
			strings.TrimSuffix(p, "/")+"/", pair.PairUser+"@"+h.Host+":payload/"+name+"/")
		rs.Stdout, rs.Stderr = os.Stderr, os.Stderr
		if err := rs.Run(); err != nil {
			_, _ = t.rpc("finish", nil, io.Discard)
			return false, fmt.Errorf("rsync %s: %w", p, err)
		}
		if rc, err := t.rpc("stage-payload "+name, nil, os.Stderr); err != nil || rc != 0 {
			_, _ = t.rpc("finish", nil, io.Discard)
			return false, fmt.Errorf("staging %s failed (exit %d)", name, rc)
		}
	}

	// 7. The install. Secrets go on stdin; the output streams here unchanged.
	say("=== installing %s ===", h.Host)
	var in bytes.Buffer
	_ = pair.EncodeSecrets(&in, s)
	s = pair.Secrets{}
	rw := &recoveryWatch{w: os.Stderr}
	rc, err = t.rpc(inst.String(), &in, rw)
	in.Reset()
	if err != nil {
		return false, err
	}
	if rc != 0 || !rw.done {
		_, _ = t.rpc("finish", nil, io.Discard)
		return false, fmt.Errorf("the install FAILED (exit %d); the target's screen and /var/log/river-pair on it say more", rc)
	}
	say("")
	say("river-pair: %s installed (pool %s, hostname %s).", h.Host, pool, hostname)
	if rw.key {
		say("The recovery key was shown above, once. It is stored nowhere, here or there.")
	}
	if yes(fmt.Sprintf("Reboot %s into Runink River now? [Y/n] ", h.Host), true) {
		_, _ = t.rpc("reboot", nil, os.Stderr)
		say("river-pair: %s is rebooting. Remove its install medium; it asks for the pool key at its console.", h.Host)
	} else {
		_, _ = t.rpc("finish", nil, os.Stderr)
	}
	return true, nil
}

// recoveryWatch passes the install output through and notes the recovery key and the
// installer's completion line as they go by.
type recoveryWatch struct {
	w         io.Writer
	line      []byte
	key, done bool
}

func (r *recoveryWatch) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' || b == '\r' {
			l := string(r.line)
			if pair.RecoveryKeyLine.MatchString(l) {
				r.key = true
			}
			if strings.HasPrefix(l, "AUTOINSTALL-DONE") {
				r.done = true
			}
			r.line = r.line[:0]
		} else if len(r.line) < 4096 {
			r.line = append(r.line, b)
		}
	}
	return r.w.Write(p)
}

// pairWith runs the pairing exchange: request (proof of the code), confirm (the target's
// proof and host key), then the target owner's verdict.
func pairWith(t *target, code string, opKey pair.SSHKey) (pair.SSHKey, error) {
	addr := net.JoinHostPort(t.Addr.String(), strconv.Itoa(pair.PairPort))
	c, err := net.DialTimeout("tcp6", addr, 10*time.Second)
	if err != nil {
		return pair.SSHKey{}, err
	}
	defer c.Close()
	req, err := pair.NewRequest(code, t.ID(), t.FP, opKey)
	if err != nil {
		return pair.SSHKey{}, err
	}
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return pair.SSHKey{}, err
	}
	r := bufio.NewReader(io.LimitReader(c, 4*pair.MaxLine))
	read := func() (pair.Reply, error) {
		var rep pair.Reply
		l, err := r.ReadBytes('\n')
		if err != nil {
			return rep, errors.New("the target closed the pairing connection")
		}
		if err := json.Unmarshal(l, &rep); err != nil {
			return rep, err
		}
		if rep.Status == "error" {
			return rep, pairError(rep.Error)
		}
		return rep, nil
	}
	rep, err := read()
	if err != nil {
		return pair.SSHKey{}, err
	}
	hostKey, err := pair.VerifyConfirm(code, t.FP, req, rep)
	if err != nil {
		return pair.SSHKey{}, err
	}
	say("")
	say("The target knows the code. This operator's key is:")
	say("    %s", opKey.Fingerprint())
	say("On %s's screen, check it says \"Paired with %s\" and answer y THERE.", t.Host, opKey.Fingerprint())
	say("Waiting for the target's owner (up to 2 minutes)...")
	_ = c.SetDeadline(time.Now().Add(3 * time.Minute))
	rep, err = read()
	if err != nil {
		return pair.SSHKey{}, err
	}
	ok, err := pair.VerifyVerdict(code, req, rep)
	if err != nil {
		return pair.SSHKey{}, err
	}
	if !ok {
		return pair.SSHKey{}, errors.New("the target's owner REFUSED the pairing")
	}
	return hostKey, nil
}

func pairError(e string) error {
	switch e {
	case pair.ErrBadMAC.Error():
		return errors.New("the target rejected the code (wrong code; it locks after 5)")
	case pair.ErrLocked.Error():
		return errors.New("the target's pairing is LOCKED after 5 wrong codes; its owner must start a new session")
	case pair.ErrExpired.Error():
		return errors.New("the target's pairing code EXPIRED; its owner must start a new session")
	case pair.ErrUsed.Error():
		return errors.New("the target's code was already used; its owner must start a new session")
	case pair.ErrBusy.Error():
		return errors.New("the target is waiting for its owner's answer to another pairing")
	}
	return errors.New("the target refused the request: " + e)
}

// sshArgs pins everything: this session's key only, the target's host key only (by alias:
// the host is its announced name, the address is the announcement's source), no agent, no
// forwarding, no user configuration.
func (t *target) sshArgs() []string {
	return []string{
		"-F", "/dev/null",
		"-i", filepath.Join(t.work, "id_ed25519"),
		"-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none",
		"-o", "UserKnownHostsFile=" + filepath.Join(t.work, "known_hosts"),
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=yes", "-o", "HostKeyAlgorithms=ssh-ed25519",
		"-o", "HostKeyAlias=" + t.Host,
		"-o", "HostName=" + strings.ReplaceAll(t.Addr.String(), "%", "%%"), // ssh expands % tokens in HostName
		"-o", "Port=" + strconv.Itoa(pair.SSHPort),
		"-o", "BatchMode=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ClearAllForwardings=yes",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=8",
		"-o", "LogLevel=ERROR",
	}
}

// rpc runs one command on the target through its forced RPC wrapper.
func (t *target) rpc(cmd string, in io.Reader, out io.Writer) (int, error) {
	args := append(t.sshArgs(), "-T", pair.PairUser+"@"+t.Host, cmd)
	c := exec.Command("ssh", args...) // #nosec G204 -- fixed program; the command is built from pair.RPC and checked by pair.ParseRPC
	c.Stdin, c.Stdout, c.Stderr = in, out, os.Stderr
	err := c.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ee.ExitCode() == 255 {
			return 255, fmt.Errorf("ssh to %s failed", t.Host)
		}
		return ee.ExitCode(), nil
	}
	return 1, err
}
