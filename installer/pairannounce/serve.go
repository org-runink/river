// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/org-runink/river/installer/internal/pair"
)

const (
	consoleWait   = 60 * time.Second // the local console must attach within this
	decisionWait  = 2 * time.Minute  // the local operator answers "Allow?" within this
	pairedIdle    = 60 * time.Minute // a paired session with no RPC for this long ends
	afterInstall  = 15 * time.Minute // after a finished install, reboot/finish within this
	beaconEvery   = 2 * time.Second
	manifestTiers = "/usr/local/share/runink/models.tiers"
	manifestLock  = "/usr/local/share/runink/models.lock"
)

type daemon struct {
	link    pair.Link
	sess    *pair.Session
	hostKey pair.SSHKey
	log     *logger

	conMu   sync.Mutex
	con     net.Conn
	answers chan bool

	quitOnce sync.Once
	quit     chan string

	stopBeacon context.CancelFunc
	pairLn     net.Listener
	rpcLn      net.Listener
	sshd       *exec.Cmd
	uid, gid   int

	rpcMu sync.Mutex // one RPC at a time
	stMu  sync.Mutex
	opFP  string
	// plan/install state
	planned    bool
	verdict    int
	installing bool
	installed  bool
	lastRPC    time.Time
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	iface := fs.String("iface", "", "interface to pair on (link-local only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// s6 starts services with a PATH that has no /usr/local/bin, where the installer lives.
	_ = os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin")
	if err := needRootLive(); err != nil {
		return err
	}
	link, err := pair.PickLink(*iface)
	if err != nil {
		return err
	}
	id, err := pair.NewShortID(nil)
	if err != nil {
		return err
	}
	code, err := pair.NewCode(nil)
	if err != nil {
		return err
	}
	d := &daemon{link: link, log: newLogger(id), answers: make(chan bool, 1), quit: make(chan string, 1), uid: -1, gid: -1}
	d.log.Printf("session %s starting on %s (%s)", id, link.Name, link.LinkLocal)

	if err := d.prepare(id); err != nil {
		d.teardown("setup failed")
		return err
	}
	d.sess = pair.NewSession(id, code, d.hostKey, time.Now())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() { s := <-sig; d.stop("stopped (" + s.String() + ")") }()

	if err := d.waitConsole(); err != nil {
		d.teardown("no local console")
		return err
	}
	d.event(event{Ev: "session", Host: pair.HostName(id), Code: pair.FormatCode(code), FP: d.hostKey.Fingerprint(),
		Iface: link.Name, Addr: link.LinkLocal.String(), Expires: d.sess.Expires().Format("15:04:05")})
	d.log.Printf("announcing %s host key %s; pairing code expires %s", pair.HostName(id), d.hostKey.Fingerprint(), d.sess.Expires().UTC().Format(time.RFC3339))

	ctx, cancel := context.WithCancel(context.Background())
	d.stopBeacon = cancel
	go func() {
		if err := d.beacon(ctx); err != nil {
			d.log.Printf("announcement failed: %v", err)
			d.stop("announcement failed: " + err.Error())
		}
	}()
	ln, err := net.Listen("tcp6", net.JoinHostPort(link.LinkLocal.String(), strconv.Itoa(pair.PairPort)))
	if err != nil {
		d.teardown("pairing endpoint failed")
		return err
	}
	d.pairLn = ln
	go d.acceptPairing()
	go d.timers()

	why := <-d.quit
	d.teardown(why)
	if why == "reboot" {
		time.Sleep(time.Second)
		return exec.Command("reboot").Run()
	}
	return nil
}

func (d *daemon) stop(why string) {
	d.quitOnce.Do(func() { d.quit <- why })
}

// prepare creates the session directories and the ephemeral SSH host key.
func (d *daemon) prepare(id string) error {
	if _, err := os.Stat(privDir); err == nil {
		return errors.New("a pairing session is already running (" + stateDir + " exists)")
	}
	if err := os.MkdirAll(stateDir, 0o711); err != nil { // #nosec G301 -- the session dir must be traversable (0711) for the pairing user, not listable
		return err
	}
	if err := os.Chmod(stateDir, 0o711); err != nil { // #nosec G302 -- the session dir must be traversable (0711) for the pairing user, not listable
		return err
	}
	for _, dm := range []struct {
		p string
		m os.FileMode
	}{{privDir, 0o700}, {sshDir, 0o755}} {
		if err := os.Mkdir(dm.p, dm.m); err != nil {
			return err
		}
		if err := os.Chmod(dm.p, dm.m); err != nil {
			return err
		}
	}
	key := filepath.Join(privDir, "ssh_host_ed25519_key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", pair.HostName(id), "-f", key).CombinedOutput(); err != nil { // #nosec G204 -- fixed program; the comment is river-install-<generated id>
		return fmt.Errorf("ssh-keygen: %v: %s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return err
	}
	d.hostKey, err = pair.ParseSSHKey(string(pub))
	return err
}

// waitConsole waits for the local console (the process the local operator sees).
func (d *daemon) waitConsole() error {
	_ = os.Remove(consoleSock)
	ln, err := net.Listen("unix", consoleSock)
	if err != nil {
		return err
	}
	defer ln.Close()
	_ = os.Chmod(consoleSock, 0o600)
	_ = ln.(*net.UnixListener).SetDeadline(time.Now().Add(consoleWait))
	c, err := ln.Accept()
	if err != nil {
		return errors.New("the local console did not attach")
	}
	if uid, err := peerUID(c); err != nil || uid != 0 {
		_ = c.Close()
		return errors.New("console peer is not root")
	}
	d.con = c
	go func() {
		r := bufio.NewReader(c)
		for {
			line, err := readLine(r, pair.MaxLine)
			if err != nil {
				d.stop("the local console closed")
				return
			}
			var a answer
			if json.Unmarshal(line, &a) == nil {
				select {
				case d.answers <- a.Allow:
				default:
				}
			}
		}
	}()
	return nil
}

func (d *daemon) event(e event) {
	d.conMu.Lock()
	defer d.conMu.Unlock()
	if d.con != nil {
		_ = d.con.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = writeJSON(d.con, e)
	}
}

func peerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a unix socket")
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *syscall.Ucred
	var cerr error
	if err := rc.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if cerr != nil {
		return -1, cerr
	}
	return int(cred.Uid), nil
}

// beacon announces the session on the local link until ctx ends: IPv6 link-local
// multicast, hop limit 1, no loopback.
func (d *daemon) beacon(ctx context.Context) error {
	c, err := net.ListenUDP("udp6", &net.UDPAddr{IP: d.link.LinkLocal.AsSlice(), Zone: d.link.Name})
	if err != nil {
		return err
	}
	defer c.Close()
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	_ = rc.Control(func(fd uintptr) {
		for _, o := range [][2]int{{syscall.IPV6_MULTICAST_HOPS, 1}, {syscall.IPV6_MULTICAST_IF, d.link.Index}, {syscall.IPV6_MULTICAST_LOOP, 0}} {
			if e := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, o[0], o[1]); e != nil && serr == nil {
				serr = e
			}
		}
	})
	if serr != nil {
		return serr
	}
	dst := &net.UDPAddr{IP: net.ParseIP(pair.BeaconGroup), Port: pair.BeaconPort, Zone: d.link.Name}
	msg := pair.Beacon{Host: pair.HostName(d.sess.ID()), FP: d.hostKey.Fingerprint()}.Marshal()
	t := time.NewTicker(beaconEvery)
	defer t.Stop()
	fails := 0
	for {
		if _, err := c.WriteToUDP(msg, dst); err != nil {
			if fails++; fails > 30 {
				return err
			}
		} else {
			fails = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (d *daemon) acceptPairing() {
	for {
		c, err := d.pairLn.Accept()
		if err != nil {
			return
		}
		d.handlePair(c) // one at a time: a second operator waits or times out
	}
}

func (d *daemon) handlePair(c net.Conn) {
	defer c.Close()
	from := c.RemoteAddr().String()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	r := bufio.NewReader(c)
	line, err := readLine(r, pair.MaxLine)
	var req pair.Request
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	if err != nil {
		d.log.Printf("pairing request from %s: malformed", from)
		_ = writeJSON(c, pair.ErrorReply(pair.ErrProtocol))
		return
	}
	opKey, err := d.sess.Verify(time.Now(), req)
	if err != nil {
		left := pair.MaxAttempts - d.sess.Attempts()
		d.log.Printf("pairing request from %s: %v (wrong codes so far: %d of %d)", from, err, d.sess.Attempts(), pair.MaxAttempts)
		_ = writeJSON(c, pair.ErrorReply(err))
		switch {
		case errors.Is(err, pair.ErrLocked):
			d.event(event{Ev: "locked", Addr: from})
			d.stop("locked after " + strconv.Itoa(pair.MaxAttempts) + " wrong codes")
		case errors.Is(err, pair.ErrBadMAC):
			d.event(event{Ev: "attempt", Addr: from, Left: left})
		}
		return
	}
	d.log.Printf("pairing request from %s with the correct code; operator key %s; asking the local operator", from, opKey.Fingerprint())
	if err := writeJSON(c, d.sess.Confirm(req)); err != nil {
		d.sess.Decide(req, false)
		d.stop("pairing connection lost")
		return
	}
	for len(d.answers) > 0 { // drop anything typed before the question
		<-d.answers
	}
	d.event(event{Ev: "confirm", FP: opKey.Fingerprint(), Addr: from})
	allow := false
	select {
	case allow = <-d.answers:
	case <-time.After(decisionWait):
		d.log.Printf("no local answer within %s: refused", decisionWait)
	}
	if allow {
		if err := d.startSSH(opKey); err != nil {
			d.log.Printf("cannot start the pairing sshd: %v", err)
			d.event(event{Ev: "progress", Line: "cannot start the pairing sshd: " + err.Error()})
			allow = false
		}
	}
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	_ = writeJSON(c, d.sess.Decide(req, allow))
	d.stopBeacon()
	_ = d.pairLn.Close()
	if !allow {
		d.log.Printf("pairing with %s REFUSED by the local operator", opKey.Fingerprint())
		d.stop("pairing refused")
		return
	}
	d.stMu.Lock()
	d.opFP, d.lastRPC = opKey.Fingerprint(), time.Now()
	d.stMu.Unlock()
	d.log.Printf("paired with operator key %s from %s (allowed by the local operator)", opKey.Fingerprint(), from)
	d.event(event{Ev: "paired", FP: opKey.Fingerprint(), Addr: from})
}

func (d *daemon) timers() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		d.stMu.Lock()
		paired, last, installed := d.opFP != "", d.lastRPC, d.installed
		d.stMu.Unlock()
		switch {
		case !paired && time.Now().After(d.sess.Expires()) && len(d.answers) == 0 && !d.pendingDecision():
			d.event(event{Ev: "expired"})
			d.stop("the pairing code expired")
		case paired && installed && time.Since(last) > afterInstall:
			d.stop("install finished; session closed")
		case paired && time.Since(last) > pairedIdle:
			d.stop("idle for " + pairedIdle.String())
		}
	}
}

// pendingDecision: a verified request is waiting for the local answer (expiry waits for it).
func (d *daemon) pendingDecision() bool { return d.sess.Pending() }

// startSSH creates the pairing user, installs the operator's key for this session only,
// opens the RPC socket and starts the restricted sshd on the link-local address.
func (d *daemon) startSSH(opKey pair.SSHKey) error {
	if _, err := user.Lookup(pair.PairUser); err != nil {
		if out, err := exec.Command("useradd", "-r", "-U", "-M", "-d", homeDir, "-s", "/bin/sh", "-p", "*", pair.PairUser).CombinedOutput(); err != nil { // #nosec G204 -- fixed program and fixed arguments
			return fmt.Errorf("useradd: %v: %s", err, out)
		}
	} else if out, err := exec.Command("usermod", "-p", "*", "-d", homeDir, pair.PairUser).CombinedOutput(); err != nil { // #nosec G204 -- fixed program and fixed arguments
		return fmt.Errorf("usermod: %v: %s", err, out)
	}
	u, err := user.Lookup(pair.PairUser)
	if err != nil {
		return err
	}
	d.uid, _ = strconv.Atoi(u.Uid)
	d.gid, _ = strconv.Atoi(u.Gid)
	if err := os.MkdirAll(filepath.Join(homeDir, "payload"), 0o700); err != nil {
		return err
	}
	for _, p := range []string{homeDir, filepath.Join(homeDir, "payload")} {
		if err := os.Chown(p, d.uid, d.gid); err != nil {
			return err
		}
		_ = os.Chmod(p, 0o700) // #nosec G302 -- a directory, 0700
	}
	ak := fmt.Sprintf("restrict,command=\"%s rpc\" %s\n", selfPath, opKey.String())
	if err := os.WriteFile(filepath.Join(sshDir, "authorized_keys"), []byte(ak), 0o644); err != nil { // #nosec G306 -- sshd reads authorized_keys as the pairing user; it holds a public key only
		return err
	}

	_ = os.Remove(rpcSock)
	ln, err := net.Listen("unix", rpcSock)
	if err != nil {
		return err
	}
	if err := os.Chown(rpcSock, 0, d.gid); err != nil {
		_ = ln.Close()
		return err
	}
	_ = os.Chmod(rpcSock, 0o660) // #nosec G302 -- the pairing user (group) must reach the RPC socket; peer uid is checked
	d.rpcLn = ln
	go d.acceptRPC()

	cfg := filepath.Join(privDir, "sshd_config")
	if err := os.WriteFile(cfg, []byte(sshdConfig(d.link)), 0o600); err != nil {
		return err
	}
	sshd := sshdPath()
	if out, err := exec.Command(sshd, "-t", "-f", cfg).CombinedOutput(); err != nil { // #nosec G204 -- sshd from a fixed path list, a config this program wrote
		return fmt.Errorf("sshd -t: %v: %s", err, out)
	}
	cmd := exec.Command(sshd, "-D", "-f", cfg, "-E", filepath.Join(logDir, "sshd-"+d.sess.ID()+".log")) // #nosec G204 -- sshd from a fixed path list, a config this program wrote
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	d.sshd = cmd
	time.Sleep(500 * time.Millisecond)
	if cmd.ProcessState != nil {
		return errors.New("sshd exited at once")
	}
	d.log.Printf("pairing sshd listening on [%s]:%d for %s only", d.link.LinkLocal, pair.SSHPort, pair.PairUser)
	return nil
}

func sshdPath() string {
	for _, p := range []string{"/usr/bin/sshd", "/usr/sbin/sshd"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "sshd"
}

// sshdConfig is the complete configuration of the pairing sshd. It includes nothing from
// /etc/ssh: one user, one key type, public key only, no forwarding of any kind, no TTY,
// and every session forced through the RPC wrapper.
func sshdConfig(l pair.Link) string {
	return fmt.Sprintf(`# river-pair-announce: the pairing session's sshd (generated; RAM only)
Port %d
AddressFamily inet6
ListenAddress %s
HostKey %s/ssh_host_ed25519_key
HostKeyAlgorithms ssh-ed25519
PidFile %s/sshd.pid
AuthorizedKeysFile %s/authorized_keys
AuthorizedPrincipalsFile none
AuthorizedKeysCommand none
AllowUsers %s
AuthenticationMethods publickey
PubkeyAuthentication yes
PubkeyAcceptedAlgorithms ssh-ed25519
PasswordAuthentication no
KbdInteractiveAuthentication no
HostbasedAuthentication no
PermitRootLogin no
PermitEmptyPasswords no
UsePAM no
StrictModes yes
DisableForwarding yes
AllowAgentForwarding no
AllowTcpForwarding no
AllowStreamLocalForwarding no
X11Forwarding no
PermitTunnel no
PermitTTY no
PermitUserRC no
PermitUserEnvironment no
GatewayPorts no
ForceCommand %s rpc
MaxAuthTries 2
MaxSessions 2
MaxStartups 4:50:8
LoginGraceTime 20
ClientAliveInterval 15
ClientAliveCountMax 8
PrintMotd no
LogLevel VERBOSE
`, pair.SSHPort, l.LinkLocal.String(), privDir, privDir, sshDir, pair.PairUser, selfPath)
}

func (d *daemon) acceptRPC() {
	for {
		c, err := d.rpcLn.Accept()
		if err != nil {
			return
		}
		go d.handleRPC(c)
	}
}

func (d *daemon) handleRPC(c net.Conn) {
	defer c.Close()
	exit := func(code int) { _ = pair.WriteFrame(c, pair.FrameExit, []byte(strconv.Itoa(code))) }
	out := pair.FrameWriter{W: c}
	if uid, err := peerUID(c); err != nil || uid != d.uid {
		d.log.Printf("rpc: refused a peer that is not %s", pair.PairUser)
		return
	}
	r := bufio.NewReader(c)
	line, err := readLine(r, pair.MaxCommand+64)
	var req struct {
		Cmd string `json:"cmd"`
	}
	if err == nil {
		err = json.Unmarshal(line, &req)
	}
	if err != nil {
		exit(2)
		return
	}
	rpc, err := pair.ParseRPC(req.Cmd)
	if err != nil || rpc.Verb == "rsync" {
		d.log.Printf("rpc: refused %q", req.Cmd)
		fmt.Fprintf(out, "river-pair-announce: %v\n", pair.ErrCommand)
		exit(126)
		return
	}
	if !d.rpcMu.TryLock() {
		fmt.Fprintln(out, "river-pair-announce: busy: another command is running")
		exit(75)
		return
	}
	defer d.rpcMu.Unlock()
	d.stMu.Lock()
	d.lastRPC = time.Now()
	d.stMu.Unlock()
	d.log.Printf("rpc: %s", rpc.String())
	d.event(event{Ev: "rpc", Line: rpc.String()})
	switch rpc.Verb {
	case "hello":
		ver, _ := os.ReadFile("/etc/runink-os-version")
		fmt.Fprintf(out, "session %s\nversion %s\n", pair.HostName(d.sess.ID()), strings.TrimSpace(string(ver)))
		exit(0)
	case "plan":
		exit(d.plan(out, rpc.Lab))
	case "stage-payload":
		exit(d.stagePayload(out, rpc.Name))
	case "install":
		exit(d.install(out, r, rpc))
	case "reboot", "finish":
		d.stMu.Lock()
		busy := d.installing
		d.stMu.Unlock()
		if busy {
			fmt.Fprintln(out, "river-pair-announce: the install is still running")
			exit(75)
			return
		}
		fmt.Fprintf(out, "%s: session closed%s\n", pair.HostName(d.sess.ID()), map[bool]string{true: "; rebooting", false: ""}[rpc.Verb == "reboot"])
		exit(0)
		_ = c.Close()
		if rpc.Verb == "reboot" {
			d.stop("reboot")
		} else {
			d.stop("finished by the operator")
		}
	}
}

func (d *daemon) run(out, errw io.Writer, name string, args ...string) int {
	cmd := exec.Command(name, args...) // #nosec G204 -- callers pass fixed programs (river-hwprobe, river-plan, river-payloadpack) and fixed/validated arguments
	cmd.Stdout, cmd.Stderr = out, errw
	cmd.Env = append(os.Environ(), "PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin")
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(errw, "river-pair-announce: %s: %v\n", name, err)
		return 1
	}
	return 0
}

// plan probes this machine and makes the install plan the same way runink-install does
// (installer/lib/hwplan.sh), prints it, then a RIVER-PAIR-DISKS line and the disks it
// erases (river-plan --list-disks). Exit: river-plan's verdict (0 ok, 3 refused).
func (d *daemon) plan(out io.Writer, lab bool) int {
	d.stMu.Lock()
	if d.installing || d.installed {
		d.stMu.Unlock()
		fmt.Fprintln(out, "river-pair-announce: already installing or installed")
		return 1
	}
	d.planned = false
	d.stMu.Unlock()
	probe, plan := filepath.Join(privDir, "probe.json"), filepath.Join(privDir, "plan.json")
	var pb bytes.Buffer
	if rc := d.run(&pb, out, "river-hwprobe", "--json"); rc != 0 {
		_, _ = out.Write(pb.Bytes())
		return 1
	}
	if err := os.WriteFile(probe, pb.Bytes(), 0o600); err != nil {
		return 1
	}
	args := []string{"--probe", probe}
	if _, err := os.Stat(manifestTiers); err == nil {
		args = append(args, "--manifest", manifestTiers)
		if _, err := os.Stat(manifestLock); err == nil {
			args = append(args, "--lock", manifestLock)
		}
	} else {
		fmt.Fprintln(out, "(no models manifest on this medium: planning hardware and storage only)")
		args = append(args, "--no-models")
	}
	if lab {
		args = append(args, "--lab")
	}
	var planJSON bytes.Buffer
	rc := d.run(&planJSON, out, "river-plan", append(args, "--json")...)
	if rc != 0 && rc != 3 {
		_, _ = out.Write(planJSON.Bytes())
		return 1
	}
	if err := os.WriteFile(plan, planJSON.Bytes(), 0o600); err != nil {
		return 1
	}
	verdict := d.run(out, out, "river-plan", "--plan-file", plan)
	if verdict != 0 && verdict != 3 {
		return 1
	}
	fmt.Fprintln(out, "RIVER-PAIR-DISKS")
	if rc := d.run(out, out, "river-plan", "--plan-file", plan, "--list-disks"); rc != 0 {
		return 1
	}
	d.stMu.Lock()
	d.planned, d.verdict = true, verdict
	d.stMu.Unlock()
	return verdict
}

// stagePayload hands a payload the operator rsync'd into payload/NAME to the installer.
// river-payloadpack is the medium's payload tool; a medium without it (a base image)
// received the files but cannot stage them, and says so.
func (d *daemon) stagePayload(out io.Writer, name string) int {
	src := filepath.Join(homeDir, "payload", name)
	if fi, err := os.Lstat(src); err != nil || !fi.IsDir() {
		fmt.Fprintf(out, "river-pair-announce: no payload %s received\n", name)
		return 1
	}
	dest := filepath.Join(payloadDest, name)
	if err := os.MkdirAll(payloadDest, 0o700); err != nil {
		return 1
	}
	tool, err := exec.LookPath("river-payloadpack")
	if err != nil {
		for _, p := range []string{"/usr/local/bin/river-payloadpack"} {
			if _, e := os.Stat(p); e == nil {
				tool, err = p, nil
			}
		}
	}
	if err != nil {
		fmt.Fprintf(out, "payload %s: received, NOT staged: river-payloadpack is not on this medium\n", name)
		d.log.Printf("payload %s received; river-payloadpack absent, not staged", name)
		return 0
	}
	rc := d.run(out, out, tool, "stage", "--src", src, "--dest", dest)
	d.log.Printf("payload %s staged to %s: exit %d", name, dest, rc)
	return rc
}

// install runs runink-autoinstall with the plan made by `plan`, the serials the operator
// typed, and the secrets read from stdin into memory-only files.
func (d *daemon) install(out io.Writer, in *bufio.Reader, rpc pair.RPC) int {
	sec, err := pair.ReadSecrets(in)
	if err != nil {
		fmt.Fprintln(out, "river-pair-announce:", err)
		return 2
	}
	d.stMu.Lock()
	switch {
	case !d.planned:
		d.stMu.Unlock()
		fmt.Fprintln(out, "river-pair-announce: run plan first")
		return 1
	case d.verdict != 0:
		d.stMu.Unlock()
		fmt.Fprintln(out, "river-pair-announce: the plan is refused")
		return 1
	case d.installing || d.installed:
		d.stMu.Unlock()
		fmt.Fprintln(out, "river-pair-announce: already installing or installed")
		return 1
	}
	d.installing = true
	d.stMu.Unlock()
	defer func() {
		d.stMu.Lock()
		d.installing = false
		d.stMu.Unlock()
	}()

	args := []string{"--plan-file", filepath.Join(privDir, "plan.json")}
	for _, s := range rpc.Serials {
		args = append(args, "--yes-i-have-checked-serial="+s)
	}
	// Secrets: anonymous memory files at fixed descriptors (/dev/fd/N) in the installer's
	// process tree. They exist nowhere else and vanish when the install exits.
	extra := make([]*os.File, 8) // fds 3..10; 3..8 stay closed
	if sec.ModelsPassphrase != "" {
		f, err := pair.MemFile("models-passphrase", []byte(sec.ModelsPassphrase+"\n"))
		if err != nil {
			fmt.Fprintln(out, "river-pair-announce:", err)
			return 1
		}
		defer f.Close()
		extra[6] = f
		args = append(args, "--models-passphrase-file", "/dev/fd/9")
	}
	if len(sec.AdminKeys) > 0 {
		f, err := pair.MemFile("admin-keys", []byte(strings.Join(sec.AdminKeys, "\n")+"\n"))
		if err != nil {
			fmt.Fprintln(out, "river-pair-announce:", err)
			return 1
		}
		defer f.Close()
		extra[7] = f
		args = append(args, "--admin-authorized-keys", "/dev/fd/10")
	}
	pool, be, host := rpc.Pool, rpc.BE, rpc.Hostname
	if pool == "" {
		pool = "zriver"
	}
	if be == "" {
		be = "runink"
	}
	if host == "" {
		host = "runink"
	}
	args = append(args, pool, be, host)
	d.log.Printf("install: serials %s, pool %s, be %s, hostname %s, models passphrase %s, admin keys %d",
		strings.Join(rpc.Serials, ","), pool, be, host, map[bool]string{true: "given", false: "none"}[sec.ModelsPassphrase != ""], len(sec.AdminKeys))
	d.event(event{Ev: "progress", Line: "install started by the operator (pool " + pool + ", hostname " + host + ")"})

	pr, pw, err := os.Pipe()
	if err != nil {
		return 1
	}
	cmd := exec.Command("runink-autoinstall", args...) // #nosec G204 -- fixed program; arguments are validated by pair.ParseRPC (serials, names) or fixed
	cmd.Env = append(os.Environ(), "PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin")
	cmd.Stdout, cmd.Stderr, cmd.ExtraFiles = pw, pw, extra
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		fmt.Fprintln(out, "river-pair-announce:", err)
		return 1
	}
	_ = pw.Close()
	done := d.relay(out, pr)
	err = cmd.Wait()
	_ = pr.Close()
	rc := 0
	if err != nil {
		rc = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			rc = ee.ExitCode()
		}
	}
	if rc == 0 && !done {
		rc = 1
	}
	d.log.Printf("install finished: exit %d", rc)
	if rc == 0 {
		d.stMu.Lock()
		d.installed, d.lastRPC = true, time.Now()
		d.stMu.Unlock()
		d.event(event{Ev: "progress", Line: "install complete: waiting for the operator to reboot this machine"})
	} else {
		d.event(event{Ev: "progress", Line: fmt.Sprintf("install FAILED (exit %d)", rc)})
	}
	return rc
}

// relay copies the installer's output to the operator unchanged and tells the local
// console the steps, the result and the recovery key (never the log).
func (d *daemon) relay(out io.Writer, r io.Reader) (done bool) {
	buf := make([]byte, 32<<10)
	var line []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = out.Write(buf[:n]) // the operator may have gone; the install goes on regardless
			for _, b := range buf[:n] {
				if b != '\n' && b != '\r' {
					if len(line) < 4096 {
						line = append(line, b)
					}
					continue
				}
				l := string(line)
				line = line[:0]
				switch {
				case strings.HasPrefix(l, "===> "), strings.HasPrefix(l, "AUTOINSTALL"):
					d.event(event{Ev: "progress", Line: l})
					if strings.HasPrefix(l, "AUTOINSTALL-DONE") {
						done = true
					}
				case pair.RecoveryKeyLine.MatchString(l):
					d.event(event{Ev: "recovery", Line: strings.TrimSpace(l)})
				case strings.Contains(l, "RIVER ZFS RECOVERY KEY"):
					d.event(event{Ev: "progress", Line: strings.TrimSpace(l)})
				}
			}
		}
		if err != nil {
			return done
		}
	}
}

// teardown ends the session: no announcement, no listener, no sshd, no key, no user, no
// staged payload, no host key. Only the session log (RAM) remains.
func (d *daemon) teardown(why string) {
	if d.stopBeacon != nil {
		d.stopBeacon()
	}
	if d.pairLn != nil {
		_ = d.pairLn.Close()
	}
	if d.rpcLn != nil {
		_ = d.rpcLn.Close()
	}
	if d.sshd != nil && d.sshd.Process != nil {
		_ = syscall.Kill(-d.sshd.Process.Pid, syscall.SIGTERM)
		_ = d.sshd.Process.Kill()
		_ = d.sshd.Wait()
	}
	if d.uid > 0 {
		killUID(d.uid)
	}
	_ = os.Remove(filepath.Join(sshDir, "authorized_keys"))
	if _, err := user.Lookup(pair.PairUser); err == nil {
		_ = exec.Command("userdel", pair.PairUser).Run() // #nosec G204 -- fixed program and fixed user name
	}
	for _, p := range []string{privDir, sshDir, homeDir, rpcSock} {
		_ = os.RemoveAll(p)
	}
	d.log.Printf("session ended: %s", why)
	d.event(event{Ev: "teardown", Why: why})
	if d.con != nil {
		time.Sleep(200 * time.Millisecond)
		_ = d.con.Close()
	}
	_ = os.Remove(consoleSock)
	_ = os.Remove(stateDir)
}

// killUID ends every process of the pairing user (its sshd sessions, a running rsync).
func killUID(uid int) {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/status")
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) >= 2 && f[0] == "Uid:" && f[1] == strconv.Itoa(uid) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}
