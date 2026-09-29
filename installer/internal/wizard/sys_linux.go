// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/org-runink/river/installer/internal/hw"
)

// LiveSystem runs the existing installer pieces on the live medium.
type LiveSystem struct {
	Lib         string // /usr/local/lib/runink-install
	RunDir      string // private 0700 scratch on /run
	EditionsDir string // /usr/share/river/installer/editions
	Manifest    string // /usr/local/share/runink/models.tiers
	Lock        string // /usr/local/share/runink/models.lock
	KioskUser   string // the kiosk user (its keyboard follows the installer's)
	DesktopUser string // the live desktop user (workstation), "" on a server
	Logf        func(string, ...any)
}

func (s *LiveSystem) logf(f string, a ...any) {
	if s.Logf != nil {
		s.Logf(f, a...)
	}
}

// run executes a command and returns its stdout; stderr is folded into the error.
func run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed installer tools
	if env != nil {
		cmd.Env = env
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return out.Bytes(), fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return out.Bytes(), nil
}

// ---- network -------------------------------------------------------------------------------

const netStateFile = "/run/river/net-state.json"

func readNetState() (NetState, error) {
	b, err := os.ReadFile(netStateFile)
	if err != nil {
		return NetState{}, err
	}
	return parseNetState(b)
}

// parseNetState maps river.net-state/v1 to what the UI shows.
func parseNetState(b []byte) (NetState, error) {
	var st struct {
		State    string `json:"state"`
		Method   string `json:"method"`
		Hostname string `json:"hostname"`
		Links    []struct {
			Name  string   `json:"name"`
			Up    bool     `json:"up"`
			Addrs []string `json:"addrs"`
		} `json:"links"`
		Routes []struct {
			Family string `json:"family"`
			Iface  string `json:"iface"`
		} `json:"routes"`
		Checks struct {
			Internet4 struct{ OK bool } `json:"internet4"`
			Internet6 struct{ OK bool } `json:"internet6"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return NetState{}, err
	}
	ns := NetState{State: st.State, Method: st.Method, Hostname: st.Hostname,
		Internet4: st.Checks.Internet4.OK, Internet6: st.Checks.Internet6.OK}
	for _, r := range st.Routes {
		if r.Family == "ipv4" {
			ns.IPv4 = true
		}
		if r.Family == "ipv6" {
			ns.IPv6 = true
		}
		if ns.Iface == "" {
			ns.Iface = r.Iface
		}
	}
	for _, l := range st.Links {
		if l.Name == ns.Iface {
			ns.Addrs = l.Addrs
		}
		if strings.HasPrefix(l.Name, "wl") {
			ns.HasWifi = true
		}
	}
	ns.Wifi = strings.HasPrefix(ns.Iface, "wl") || ns.Method == "wifi"
	ns.Wired = ns.Iface != "" && !ns.Wifi
	if _, err := os.Stat("/sys/class/ieee80211"); err == nil {
		if e, _ := os.ReadDir("/sys/class/ieee80211"); len(e) > 0 {
			ns.HasWifi = true
		}
	}
	return ns, nil
}

func (s *LiveSystem) NetAuto(ctx context.Context) (NetState, error) {
	if _, err := run(ctx, nil, "river-netsetup", "--auto", "--wait", "20"); err != nil {
		s.logf("river-netsetup --auto: %v", err)
	}
	return readNetState()
}

func (s *LiveSystem) NetCheck(ctx context.Context) (NetState, error) {
	if _, err := run(ctx, nil, "river-netsetup", "--check"); err != nil {
		return NetState{}, err
	}
	return readNetState()
}

func (s *LiveSystem) WifiScan(ctx context.Context) ([]WifiNet, error) {
	_, _ = run(ctx, nil, "nmcli", "radio", "wifi", "on")
	out, err := run(ctx, nil, "nmcli", "-t", "-e", "yes", "-f", "SSID,SIGNAL,SECURITY", "device", "wifi", "list", "--rescan", "yes")
	if err != nil {
		return nil, err
	}
	return parseWifiList(string(out)), nil
}

// parseWifiList reads nmcli's terse, escaped output (":" and "\" escaped by a backslash).
func parseWifiList(out string) []WifiNet {
	best := map[string]WifiNet{}
	for _, line := range strings.Split(out, "\n") {
		var fields []string
		var cur strings.Builder
		esc := false
		for _, r := range line {
			switch {
			case esc:
				cur.WriteRune(r)
				esc = false
			case r == '\\':
				esc = true
			case r == ':':
				fields = append(fields, cur.String())
				cur.Reset()
			default:
				cur.WriteRune(r)
			}
		}
		fields = append(fields, cur.String())
		if len(fields) < 3 || fields[0] == "" {
			continue
		}
		sig, _ := strconv.Atoi(fields[1])
		n := WifiNet{SSID: fields[0], Signal: sig, Secure: fields[2] != "" && fields[2] != "--"}
		if old, ok := best[n.SSID]; !ok || n.Signal > old.Signal {
			best[n.SSID] = n
		}
	}
	var nets []WifiNet
	for _, n := range best {
		nets = append(nets, n)
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].Signal > nets[j].Signal })
	return nets
}

func (s *LiveSystem) WifiConnect(ctx context.Context, ssid, psk string) (NetState, error) {
	f, err := os.CreateTemp(s.RunDir, "wifi-*.conf")
	if err != nil {
		return NetState{}, err
	}
	name := f.Name()
	defer os.Remove(name)
	cfg := "[network]\nmethod = wifi\n\n[wifi]\nssid = " + ssid + "\n"
	if psk != "" {
		cfg += "psk = " + psk + "\n"
	}
	_ = f.Chmod(0o600)
	if _, err := f.WriteString(cfg); err != nil {
		_ = f.Close()
		return NetState{}, err
	}
	_ = f.Close()
	if _, err := run(ctx, nil, "river-netsetup", "--config", name, "--wait", "30"); err != nil {
		return NetState{}, errors.New("river-netsetup --config failed") // its message may quote the SSID
	}
	ns, err := readNetState()
	if err == nil && ns.State == "offline" {
		return ns, errors.New("still offline")
	}
	return ns, err
}

// ---- probe and plan ------------------------------------------------------------------------

func (s *LiveSystem) Probe(ctx context.Context) ([]byte, error) {
	return run(ctx, nil, "river-hwprobe", "--json")
}

func (s *LiveSystem) scratch(name string, b []byte) (string, error) {
	p := filepath.Join(s.RunDir, name)
	return p, os.WriteFile(p, b, 0o600)
}

func (s *LiveSystem) Plan(ctx context.Context, probe []byte, lab bool, profile string, models bool) ([]byte, bool, error) {
	pf, err := s.scratch("probe.json", probe)
	if err != nil {
		return nil, false, err
	}
	args := []string{"--probe", pf}
	switch {
	case profile == "workstation":
		args = append(args, "--profile", "workstation", "--no-models")
	case models && fileExists(s.Manifest):
		args = append(args, "--manifest", s.Manifest)
		if fileExists(s.Lock) {
			args = append(args, "--lock", s.Lock)
		}
	default:
		args = append(args, "--no-models")
	}
	if lab {
		args = append(args, "--lab")
	}
	args = append(args, "--json")
	out, err := run(ctx, nil, "river-plan", args...)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 3 {
		return out, true, nil
	}
	return out, false, err
}

func (s *LiveSystem) Resolve(ctx context.Context, plan []byte, ids []string) (map[string]string, error) {
	probe, err := s.Probe(ctx)
	if err != nil {
		return nil, err
	}
	pf, err := s.scratch("resolve-probe.json", probe)
	if err != nil {
		return nil, err
	}
	plf, err := s.scratch("resolve-plan.json", plan)
	if err != nil {
		return nil, err
	}
	args := []string{"--plan-file", plf, "--probe", pf, "--env"}
	for _, id := range ids {
		args = append(args, "--confirm", id)
	}
	out, err := run(ctx, nil, "river-plan", args...)
	if err != nil {
		return nil, err
	}
	return parseShAssignments(string(out))
}

// parseShAssignments reads river-plan --env: KEY='value' lines, ' written as '\”.
func parseShAssignments(s string) (map[string]string, error) {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			return nil, fmt.Errorf("bad line %q", line)
		}
		k, v := line[:i], line[i+1:]
		if len(v) < 2 || v[0] != '\'' || v[len(v)-1] != '\'' {
			return nil, fmt.Errorf("bad value for %s", k)
		}
		m[k] = strings.ReplaceAll(v[1:len(v)-1], `'\''`, `'`)
	}
	return m, nil
}

// ---- steps ----------------------------------------------------------------------------------

func (s *LiveSystem) HasStep(step string) bool { return fileExists(filepath.Join(s.Lib, step+".sh")) }

func (s *LiveSystem) RunStep(ctx context.Context, step string, env []string, stdin io.Reader, out func(string)) error {
	cmd := exec.Command("sh", filepath.Join(s.Lib, step+".sh")) // #nosec G204 -- the image's own steps
	cmd.Env = env
	cmd.Dir = "/"
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		// rsync --info=progress2 rewrites its line with \r: split on both.
		sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			for i, b := range data {
				if b == '\n' || b == '\r' {
					return i + 1, data[:i], nil
				}
			}
			if atEOF && len(data) > 0 {
				return len(data), data, nil
			}
			return 0, nil, nil
		})
		for sc.Scan() {
			if t := strings.TrimRight(sc.Text(), " "); t != "" {
				out(t)
			}
		}
		_, _ = io.Copy(io.Discard, pr)
		close(done)
	}()
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	var err error
	select {
	case err = <-waitErr:
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		err = <-waitErr
	}
	_ = pw.Close()
	<-done
	return err
}

func (s *LiveSystem) PrepareRetry(ctx context.Context, pool string) error {
	_, _ = run(ctx, nil, "umount", "-R", "/mnt")
	if _, err := run(ctx, nil, "zpool", "list", "-H", pool); err == nil {
		if _, err := run(ctx, nil, "zpool", "export", "-f", pool); err != nil {
			return err
		}
	}
	return nil
}

// ---- recovery key to a removable drive ----------------------------------------------------------

func (s *LiveSystem) Removable(ctx context.Context, exclude []string) ([]USBDrive, error) {
	b, err := s.Probe(ctx)
	if err != nil {
		return nil, err
	}
	var p hw.Probe
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, x := range exclude {
		skip[x] = true
	}
	var out []USBDrive
	for _, d := range p.Disks {
		if d.BootMedia || d.ReadOnly || !(d.Removable || d.Transport == "usb") || d.SizeBytes == 0 ||
			skip[d.Name] || skip[d.Path] || skip[d.ConfirmID] {
			continue
		}
		if fsPartition(ctx, d.Path) == "" {
			continue // nothing to write a file to; the installer never formats a drive for this
		}
		gbs := d.SizeBytes / 1e9
		if gbs > 1<<31 {
			gbs = 1 << 31
		}
		out = append(out, USBDrive{Dev: d.Path, Model: d.Model, SizeGB: int(gbs)}) // #nosec G115 -- clamped above
	}
	return out, nil
}

// fsPartition returns the first partition (or the whole device) with a filesystem the key file
// can go on.
func fsPartition(ctx context.Context, dev string) string {
	out, err := run(ctx, nil, "lsblk", "-rno", "PATH,FSTYPE", dev)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			switch f[1] {
			case "vfat", "exfat", "ext4", "ext3", "ext2", "ntfs", "ntfs3", "btrfs", "xfs":
				return f[0]
			}
		}
	}
	return ""
}

func (s *LiveSystem) SaveToUSB(ctx context.Context, dev, name string, content []byte) error {
	part := fsPartition(ctx, dev)
	if part == "" {
		return errors.New("no filesystem on " + dev)
	}
	mnt := filepath.Join(s.RunDir, "usb")
	if err := os.MkdirAll(mnt, 0o700); err != nil {
		return err
	}
	if _, err := run(ctx, nil, "mount", "-o", "nosuid,nodev,noexec", part, mnt); err != nil {
		return err
	}
	defer func() { _, _ = run(context.Background(), nil, "umount", mnt) }()
	f, err := os.OpenFile(filepath.Join(mnt, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ---- keyboard, editions, medium ------------------------------------------------------------------

func (s *LiveSystem) ApplyKeyboard(l layout) error {
	var errs []string
	if _, err := exec.LookPath("loadkeys"); err == nil {
		if _, err := run(context.Background(), nil, "loadkeys", l.Keymap); err != nil {
			errs = append(errs, err.Error())
		}
	}
	// The kiosk reads its XKB layout at start (river-kiosk); restarting it keeps the page state
	// (the UI reconnects).
	env := "XKB_DEFAULT_LAYOUT=" + l.XKB + "\nXKB_DEFAULT_VARIANT=" + l.Variant + "\n"
	if err := os.WriteFile(filepath.Join(s.RunDir, "kiosk.env"), []byte(env), 0o600); err != nil {
		errs = append(errs, err.Error())
	} else {
		_ = os.WriteFile(filepath.Join(s.RunDir, "kiosk.restart"), nil, 0o600)
	}
	// The live desktop (workstation): Plasma's layout list, reloaded in the running session.
	if u := s.DesktopUser; u != "" {
		if _, err := exec.LookPath("kwriteconfig6"); err == nil {
			uid := userID(u)
			benv := []string{"HOME=/home/" + u, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/" + uid + "/bus",
				"PATH=/usr/bin:/bin"}
			for _, kv := range [][2]string{{"LayoutList", l.XKB}, {"VariantList", l.Variant}, {"Use", "true"}} {
				_, _ = run(context.Background(), benv, "runuser", "-u", u, "--", "kwriteconfig6", "--file", "kxkbrc",
					"--group", "Layout", "--key", kv[0], kv[1])
			}
			_, _ = run(context.Background(), benv, "runuser", "-u", u, "--", "dbus-send", "--session",
				"--type=signal", "/Layouts", "org.kde.keyboard.reloadConfig")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func userID(name string) string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) > 2 && p[0] == name {
			return p[2]
		}
	}
	return ""
}

// cmdlineArg is the value of <name>= on the kernel command line. label= is the live medium's
// volume label; river.edition= names the running edition on a medium that carries several
// under one label (docs/BUILD.md, "One medium, several editions").
func cmdlineArg(name string) string {
	b, _ := os.ReadFile("/proc/cmdline")
	for _, f := range strings.Fields(string(b)) {
		if strings.HasPrefix(f, name+"=") {
			return strings.TrimPrefix(f, name+"=")
		}
	}
	return ""
}

func (s *LiveSystem) Editions() []Edition {
	eds, skipped := LoadEditionsFor(s.EditionsDir, cmdlineArg("label"), cmdlineArg("river.edition"), func(label string) bool {
		return fileExists("/dev/disk/by-label/" + label)
	})
	for _, m := range skipped {
		s.logf("edition descriptor skipped: %s", m)
	}
	return eds
}

var mediumRoots = []string{"/run/initramfs/live", "/run/archiso/bootmnt", "/run/artix/bootmnt", "/run/miso/bootmnt", "/bootmnt"}

func (s *LiveSystem) MediumPayloads() bool {
	for _, r := range mediumRoots {
		if m, _ := filepath.Glob(filepath.Join(r, "river-*", "MANIFEST")); len(m) > 0 {
			return true
		}
		if m, _ := filepath.Glob(filepath.Join(r, "river-*", "*", "MANIFEST")); len(m) > 0 {
			return true
		}
	}
	return false
}

func (s *LiveSystem) Reboot() error {
	syscall.Sync()
	_, err := run(context.Background(), nil, "reboot")
	return err
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---- the installed node's first boot ----------------------------------------------------------

// NodeSystem is the first boot on an installed server.
type NodeSystem struct {
	Marker string // /var/lib/runink/firstboot-ui
	RunDir string
	Logf   func(string, ...any)
}

func (n *NodeSystem) logf(f string, a ...any) {
	if n.Logf != nil {
		n.Logf(f, a...)
	}
}

// MarkerValues reads the KEY=VALUE lines 77-firstboot-ui wrote.
func (n *NodeSystem) MarkerValues() map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(n.Marker)
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(line, '='); i > 0 && !strings.HasPrefix(line, "#") {
			m[line[:i]] = line[i+1:]
		}
	}
	return m
}

func (n *NodeSystem) Net(ctx context.Context) (NetState, error) {
	_, _ = run(ctx, nil, "river-netsetup", "--check")
	return readNetState()
}

// SyncClock asks the configured NTP servers (/etc/runink/ntp-servers, one per line), or the
// default gateway, for the time (SNTP, RFC 4330) and steps the clock to it.
func (n *NodeSystem) SyncClock(ctx context.Context) (string, error) {
	var servers []string
	if b, err := os.ReadFile("/etc/runink/ntp-servers"); err == nil {
		for _, l := range strings.Fields(string(b)) {
			servers = append(servers, l)
		}
	}
	if gw := defaultGateway4(); gw != "" {
		servers = append(servers, gw)
	}
	for _, srv := range servers {
		t, err := sntp(ctx, srv)
		if err != nil {
			continue
		}
		tv := syscall.NsecToTimeval(t.UnixNano())
		if err := syscall.Settimeofday(&tv); err != nil {
			return "", err
		}
		_, _ = run(ctx, nil, "hwclock", "--systohc", "--utc")
		return srv, nil
	}
	return "", errors.New("no time server answered")
}

func defaultGateway4() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Fields(sc.Text())
		if len(p) > 2 && p[1] == "00000000" && p[2] != "00000000" {
			v, err := strconv.ParseUint(p[2], 16, 32)
			if err == nil {
				ip := make(net.IP, 4)
				binary.LittleEndian.PutUint32(ip, uint32(v))
				return ip.String()
			}
		}
	}
	return ""
}

func sntp(ctx context.Context, server string) (time.Time, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "udp", net.JoinHostPort(server, "123"))
	if err != nil {
		return time.Time{}, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	req := make([]byte, 48)
	req[0] = 0x23 // LI 0, version 4, mode 3 (client)
	if _, err := c.Write(req); err != nil {
		return time.Time{}, err
	}
	resp := make([]byte, 48)
	if _, err := io.ReadFull(c, resp); err != nil {
		return time.Time{}, err
	}
	if resp[0]&0x07 != 4 || resp[1] == 0 { // mode server, stratum set
		return time.Time{}, errors.New("not a server reply")
	}
	secs := binary.BigEndian.Uint32(resp[40:44])
	frac := binary.BigEndian.Uint32(resp[44:48])
	const ntpEpoch = 2208988800
	return time.Unix(int64(secs)-ntpEpoch, int64(frac)*1e9>>32), nil
}

func (n *NodeSystem) Firewall(ctx context.Context) (bool, error) {
	if _, err := run(ctx, nil, "nft", "list", "table", "inet", "runink_fw"); err == nil {
		return true, nil
	}
	if fileExists("/usr/local/bin/runink-fw") {
		_, _ = run(ctx, nil, "/usr/local/bin/runink-fw", "apply")
	}
	_, err := run(ctx, nil, "nft", "list", "table", "inet", "runink_fw")
	return err == nil, err
}

func (n *NodeSystem) HasK0s() bool { return fileExists("/usr/bin/k0s") }

func (n *NodeSystem) K0sReady(ctx context.Context) (bool, string, error) {
	out, err := run(ctx, nil, "k0s", "kubectl", "--kubeconfig", "/var/lib/k0s/pki/admin.conf", "get", "nodes", "--no-headers")
	if err != nil {
		return false, "starting", nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && f[1] == "Ready" {
			return true, "node " + f[0] + " Ready", nil
		}
	}
	return false, "node not Ready yet", nil
}

const hooksDir = "/usr/local/lib/runink/firstboot.d"

func (n *NodeSystem) Hooks() []string {
	ents, _ := os.ReadDir(hooksDir)
	var out []string
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func (n *NodeSystem) HookStatus(name string) string {
	b, _ := os.ReadFile(filepath.Join("/run/runink/firstboot-status", filepath.Base(name)))
	return strings.TrimSpace(string(b))
}

func (n *NodeSystem) HookDone(name string) bool {
	return fileExists(filepath.Join("/var/lib/runink/firstboot.d", filepath.Base(name)+".done"))
}

func (n *NodeSystem) RetryHooks() error {
	cmd := exec.Command("/usr/local/bin/river-firstboot-hooks")
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin", "RIVER_FIRSTBOOT_K0S_WAIT=60"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (n *NodeSystem) PagesDir() string { return "/run/runink/firstboot-pages" }

func (n *NodeSystem) Ready(ctx context.Context) Ready {
	m := n.MarkerValues()
	r := Ready{Title: m["EDITION_TITLE"], AdminUser: m["ADMIN_USER"]}
	r.Hostname, _ = os.Hostname()
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}
		for _, p := range []string{"cni", "kube", "veth", "flannel", "vxlan", "nat64", "tun", "docker", "lxc", "cali"} {
			if strings.HasPrefix(i.Name, p) {
				goto next
			}
		}
		if addrs, err := i.Addrs(); err == nil {
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.IsGlobalUnicast() {
					r.Addresses = append(r.Addresses, ipn.IP.String())
				}
			}
		}
	next:
	}
	for _, k := range []string{"ed25519", "ecdsa", "rsa"} {
		out, err := run(ctx, nil, "ssh-keygen", "-lf", "/etc/ssh/ssh_host_"+k+"_key.pub")
		if err == nil {
			r.Fingerprints = append(r.Fingerprints, strings.TrimSpace(string(out)))
		}
	}
	return r
}

// Finish removes the first-boot marker (the hooks runner stops waiting for pages), stops the
// kiosk, and removes the kiosk's packages and user, so the node keeps nothing that draws
// pixels (forbidden.closure).
func (n *NodeSystem) Finish(ctx context.Context) error {
	m := n.MarkerValues()
	if err := os.Remove(n.Marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.WriteFile(filepath.Join(n.RunDir, "kiosk.stop"), nil, 0o600)
	time.Sleep(3 * time.Second)
	// The kiosk exits on kiosk.stop; make sure nothing of the kiosk user is left running, or
	// userdel refuses ("user is currently used by process").
	for i := 0; i < 20; i++ {
		if _, err := run(ctx, nil, "pgrep", "-u", "river-kiosk"); err != nil {
			break
		}
		sig := "-TERM"
		if i >= 10 {
			sig = "-KILL"
		}
		_, _ = run(ctx, nil, "pkill", sig, "-u", "river-kiosk")
		time.Sleep(500 * time.Millisecond)
	}
	var errs []string
	if pk := strings.Fields(m["KIOSK_PKGS"]); len(pk) > 0 {
		args := append([]string{"-Rns", "--noconfirm"}, pk...)
		if out, err := run(ctx, nil, "pacman", args...); err != nil {
			errs = append(errs, err.Error())
		} else {
			n.logf("kiosk packages removed: %d lines of pacman output", bytes.Count(out, []byte("\n")))
		}
	}
	if userID("river-kiosk") != "" {
		if _, err := run(ctx, nil, "userdel", "-r", "river-kiosk"); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
