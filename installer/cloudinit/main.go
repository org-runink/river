// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Command river-cloud-init brings a Runink River cloud image up on its instance: per-instance
// identity, the metadata server's SSH keys and hostname, the root pool grown to the attached
// disk, and the encrypted data datasets created (first boot) or unlocked (every boot) with
// the per-instance data key. It replaces a cloud guest agent with one Go standard-library
// binary and three s6 oneshots. Design: docs/CLOUD-IMAGES.md.
//
//	river-cloud-init identity   s6 river-cloud-identity (no network): machine-id, SSH host
//	                            keys, ZFS hostid
//	river-cloud-init boot       s6 river-cloud-init (after the network): metadata, hostname,
//	                            SSH keys, grow, data datasets; rc-local waits for it
//	river-cloud-init late       s6 river-cloud-late: initramfs for the new hostid, pool
//	                            reguid, the model payload, the instance's install plan
//	river-cloud-init watch      s6 river-cloud-watch (longrun): re-applies the SSH keys whenever
//	                            instance or project metadata changes (wait_for_change)
//	river-cloud-init startup    s6 river-cloud-startup (base profile): runs the `startup-script` metadata value
//	                            once per boot, detached; output in /var/log/river-cloud-startup.log
//	river-cloud-init reseal     re-wrap the data key with the configured provider (rotation)
//	river-cloud-init status     print /run/river-cloud/status
//
// The profile comes from RIVER_CLOUD_PROFILE in cloud.env: "node" (the default; everything
// above) or "base" (a general-purpose image: identity, hostname, per-account SSH keys, the startup script and
// disk growth only; no data datasets, no enrollment, no cluster address; docs/cloud/gcp.md).
//
// Exit status: 0 done, 1 failed (boot: the data datasets are NOT available, so rc-local and
// the k0s node do not start), 2 usage.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/org-runink/river/installer/internal/cloud"
)

const (
	stateDir   = "/var/lib/river-cloud" // on the boot environment (unencrypted): no secrets
	runDir     = "/run/river-cloud"
	statusFile = runDir + "/status"
	cloudEnv   = "/etc/river-cloud/cloud.env"
	logFile    = "/var/log/river-cloud-init.log"
	authKeys   = "/etc/ssh/authorized_keys.d/runink"
	adminUser  = "runink"
	modelsMP   = "/var/lib/tide/models/shared"
	shareDir   = "/usr/local/share/runink"
)

// dataset is one encrypted data dataset under <pool>/data, the encryption root.
type dataset struct {
	name, mountpoint string
	props            []string
	mode             os.FileMode // applied to the mountpoint after migration (0 = keep)
}

// dataDatasets, parents before children. Everything a node accumulates or keeps secret.
var dataDatasets = []dataset{
	{"etc-runink", "/etc/runink", nil, 0o700},
	{"node-state", "/var/lib/runink", nil, 0o700},
	{"home", "/home", nil, 0},
	{"state", "/var/lib/tide", []string{"com.sun:auto-snapshot=false"}, 0},
	{"models", modelsMP, []string{"com.sun:auto-snapshot=false", "recordsize=1M", "compression=off", "atime=off"}, 0},
	{"containers", "/var/lib/k0s", []string{"com.sun:auto-snapshot=false"}, 0},
}

var logger *log.Logger

func main() {
	_ = os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin")
	var sinks []io.Writer = []io.Writer{os.Stderr}
	if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		sinks = append(sinks, f)
	}
	logger = log.New(io.MultiWriter(sinks...), "river-cloud-init: ", log.LstdFlags|log.LUTC)
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "identity":
		err = identity()
	case "boot":
		err = boot()
	case "late":
		err = late()
	case "watch":
		err = watch()
	case "startup":
		err = startup()
	case "reseal":
		err = reseal()
	case "status":
		b, e := os.ReadFile(statusFile)
		if e == nil {
			_, _ = os.Stdout.Write(b)
		}
		err = e
	default:
		usage()
	}
	if err != nil {
		logger.Printf("%s: FAILED: %v", os.Args[1], err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: river-cloud-init identity|boot|late|watch|startup|reseal|status")
	os.Exit(2)
}

// --- helpers ------------------------------------------------------------------------------

func run(stdin []byte, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) // #nosec G204 -- fixed tool names from this file; arguments are passed as argv, never through a shell
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- system directories (/etc, /run) that must stay world-traversable
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil { // #nosec G703 G306 -- paths are constants of this file; the mode is chosen per file by the caller
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// setStatus records key=value in the status file (read by the cloud test and operators).
func setStatus(key, value string) {
	_ = os.MkdirAll(runDir, 0o755) // #nosec G301 -- /run/river-cloud holds only the non-secret status file
	b, _ := os.ReadFile(statusFile)
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" && !strings.HasPrefix(l, key+"=") {
			lines = append(lines, l)
		}
	}
	lines = append(lines, key+"="+value)
	sort.Strings(lines)
	_ = writeFileAtomic(statusFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	logger.Printf("%s=%s", key, value)
}

// readCloudEnv reads /etc/river-cloud/cloud.env (KEY=value lines written at image build).
func readCloudEnv() map[string]string {
	env := map[string]string{}
	b, err := os.ReadFile(cloudEnv)
	if err != nil {
		return env
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if k, v, ok := strings.Cut(l, "="); ok {
			env[k] = strings.Trim(v, `"`)
		}
	}
	return env
}

// The image profiles (RIVER_CLOUD_PROFILE in cloud.env).
const (
	profileNode = "node" // the default: a cluster node with encrypted data datasets
	profileBase = "base" // a general-purpose base image: no data datasets, no enrollment
)

// profile reads RIVER_CLOUD_PROFILE. An unknown value is an error, never a guess: a node
// image read as a base image would boot without unlocking its data.
func profile(env map[string]string) (string, error) {
	switch p := env["RIVER_CLOUD_PROFILE"]; p {
	case "", profileNode:
		return profileNode, nil
	case profileBase:
		return profileBase, nil
	default:
		return "", fmt.Errorf("RIVER_CLOUD_PROFILE=%q in %s: want %s or %s", p, cloudEnv, profileNode, profileBase)
	}
}

// rootIsZFS says whether / is a ZFS boot environment.
func rootIsZFS() bool { _, err := rootPool(); return err == nil }

// --- watch ------------------------------------------------------------------------------------

// watch is the s6 longrun river-cloud-watch: it long-polls the metadata server and re-applies
// the SSH keys whenever instance or project metadata changes, so a key added with
// `gcloud compute instances add-metadata` (or by `gcloud compute ssh`) works without a
// reboot, and a removed key stops working within one round trip.
func watch() error {
	md := metadataClient(readCloudEnv())
	return watchLoop(context.Background(), md, func(ctx context.Context) error { return sshKeys(ctx, md) }, 60, 2*time.Second)
}

// watchLoop calls apply once at start and again after every metadata change, until ctx ends.
// A failed wait or apply is retried with a capped exponential backoff starting at retry; the
// ETag only advances once apply has succeeded, so a failed apply is retried, never skipped.
func watchLoop(ctx context.Context, md *cloud.Metadata, apply func(context.Context) error, timeoutSec int, retry time.Duration) error {
	etag, backoff := "", retry
	for ctx.Err() == nil {
		wctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec+30)*time.Second)
		tag, err := md.WaitForChange(wctx, etag, timeoutSec)
		if err == nil && tag != etag {
			if err = apply(wctx); err == nil {
				logger.Printf("watch: metadata at etag %s applied", tag)
				etag = tag
			}
		}
		cancel()
		if err == nil {
			backoff = retry
			continue
		}
		logger.Printf("watch: %v (retrying in %s)", err, backoff)
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
	return nil
}

// rootPool is the pool of the dataset mounted at /.
func rootPool() (string, error) {
	out, err := run(nil, "findmnt", "-n", "-o", "SOURCE", "/")
	if err != nil {
		return "", err
	}
	src := strings.TrimSpace(out)
	pool, _, ok := strings.Cut(src, "/")
	if !ok || !strings.Contains(src, "/ROOT/") {
		return "", fmt.Errorf("root %q is not a ZFS boot environment", src)
	}
	return pool, nil
}

func zfsExists(ds string) bool {
	_, err := run(nil, "zfs", "list", "-H", "-o", "name", ds)
	return err == nil
}

// legacyStateDir is where older images mounted the node-state datasets; /var/lib/tide
// replaced it.
const legacyStateDir = "/var/lib/core"

// movedMountpoint maps a mountpoint under the old node-state directory to the same place
// under /var/lib/tide; ok is false for any other path.
func movedMountpoint(cur string) (string, bool) {
	if cur == legacyStateDir || strings.HasPrefix(cur, legacyStateDir+"/") {
		return "/var/lib/tide" + strings.TrimPrefix(cur, legacyStateDir), true
	}
	return "", false
}

// migrateLegacyMountpoints moves the mountpoint PROPERTY of a data dataset an older image
// created at /var/lib/core to its current path. It copies nothing: the dataset is the same,
// mounted elsewhere. A dataset is left alone unless its property is exactly the old path of
// its current mountpoint and no dataset under root already uses the new path.
func migrateLegacyMountpoints(root string) {
	listing, _ := run(nil, "zfs", "list", "-H", "-o", "mountpoint", "-r", root)
	for _, d := range dataDatasets {
		ds := root + "/" + d.name
		want, ok := movedMountpoint(zfsGet("mountpoint", ds))
		if !ok || want != d.mountpoint {
			continue
		}
		if slices.Contains(strings.Fields(listing), want) {
			logger.Printf("%s: mounted at the old path but %s is already in use; left as is", ds, want)
			continue
		}
		if _, err := run(nil, "zfs", "set", "mountpoint="+want, ds); err != nil {
			logger.Printf("%s: moving its mountpoint to %s: %v", ds, want, err)
			continue
		}
		logger.Printf("%s: mountpoint moved to %s (older image)", ds, want)
	}
}

func zfsGet(prop, ds string) string {
	out, err := run(nil, "zfs", "get", "-H", "-o", "value", prop, ds)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func metadataClient(env map[string]string) *cloud.Metadata {
	m := &cloud.Metadata{}
	if b := env["RIVER_METADATA_BASE"]; b != "" {
		m.Base = b
	}
	return m
}

// --- identity -------------------------------------------------------------------------------

// identity gives the instance what must be unique per machine and was cleared from the
// image before capture (build/cloud-image.sh, installer step 85-cloud-finalize).
func identity() error {
	if b, _ := os.ReadFile("/etc/machine-id"); len(bytes.TrimSpace(b)) != 32 {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		if err := writeFileAtomic("/etc/machine-id", []byte(hex.EncodeToString(id)+"\n"), 0o444); err != nil {
			return err
		}
		logger.Print("machine-id generated")
	}
	if !exists("/etc/hostid") && rootIsZFS() {
		// A per-instance ZFS hostid. The initramfs still carries the image's until `late`
		// rebuilds it; zfs_force=1 on the command line covers the one boot in between.
		if _, err := run(nil, "zgenhostid"); err != nil {
			return err
		}
		if err := writeFileAtomic(stateDir+"/initramfs-stale", nil, 0o600); err != nil {
			return err
		}
		if err := writeFileAtomic(stateDir+"/reguid-pending", nil, 0o600); err != nil {
			return err
		}
		logger.Print("ZFS hostid generated; initramfs rebuild and pool reguid pending")
	}
	if _, err := run(nil, "ssh-keygen", "-A"); err != nil {
		return err
	}
	return nil
}

// --- boot -------------------------------------------------------------------------------------

func boot() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	env := readCloudEnv()
	setStatus("cloud", env["RIVER_CLOUD"])
	md := metadataClient(env)
	wctx, wcancel := context.WithTimeout(ctx, 5*time.Minute)
	id, err := md.Wait(wctx, 2*time.Second)
	wcancel()
	if err != nil {
		setStatus("metadata", "unreachable")
		// Without metadata there are no SSH keys and no key-provider config; the data
		// datasets may still unlock from a provider that needs no metadata (vTPM, later).
		logger.Printf("WARNING: %v", err)
	} else {
		setStatus("metadata", "ok")
	}
	prev, _ := os.ReadFile(stateDir + "/instance-id")
	first := err == nil && strings.TrimSpace(string(prev)) != id
	setStatus("first_boot_on_instance", fmt.Sprint(first))

	if err == nil {
		if e := setHostname(ctx, md); e != nil {
			logger.Printf("hostname: %v", e)
		}
		if e := sshKeys(ctx, md); e != nil {
			logger.Printf("ssh keys: %v", e)
		}
	}
	prof, perr := profile(env)
	if perr != nil {
		return perr
	}
	setStatus("profile", prof)
	pool, err2 := rootPool()
	if prof == profileBase {
		// A general-purpose image: grow the root pool when / is a ZFS boot environment, and
		// stop there. Nothing waits on data datasets, so the boot never fails closed here.
		if err2 != nil {
			setStatus("grow", "skipped-not-zfs")
			logger.Printf("grow: %v", err2)
		} else if e := grow(pool); e != nil {
			setStatus("grow", "failed")
			logger.Printf("grow: %v", e)
		}
		if id != "" {
			if e := writeFileAtomic(stateDir+"/instance-id", []byte(id+"\n"), 0o600); e != nil {
				return e
			}
		}
		if err == nil {
			healthLine("ready", "Runink River base image up")
		} else {
			healthLine("failed", "metadata server not reachable: no SSH keys")
		}
		return nil
	}
	if err2 != nil {
		return err2
	}
	if e := grow(pool); e != nil {
		setStatus("grow", "failed")
		logger.Printf("grow: %v", e)
	}
	if e := nodeAddress(env); e != nil {
		logger.Printf("node address: %v", e)
	}
	if err := data(ctx, md, pool); err != nil {
		setStatus("data", "locked")
		healthLine("failed", "data datasets locked: "+err.Error())
		return err
	}
	if err == nil {
		if e := enrollment(ctx, md, env); e != nil {
			setStatus("enrollment", "failed")
			logger.Printf("enrollment: %v", e)
		}
	}
	if id != "" {
		if e := writeFileAtomic(stateDir+"/instance-id", []byte(id+"\n"), 0o600); e != nil {
			return e
		}
	}
	return nil
}

func setHostname(ctx context.Context, md *cloud.Metadata) error {
	fqdn, err := md.Get(ctx, "instance/hostname")
	if err != nil {
		return err
	}
	fqdn = strings.TrimSpace(fqdn)
	short, _, _ := strings.Cut(fqdn, ".")
	if !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(short) {
		return fmt.Errorf("refusing hostname %q", fqdn)
	}
	if err := writeFileAtomic("/etc/hostname", []byte(short+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile("/proc/sys/kernel/hostname", []byte(short), 0o644); err != nil { // #nosec G306 -- a procfs control file, not a regular file
		return err
	}
	// A managed /etc/hosts line for the instance's own names.
	b, _ := os.ReadFile("/etc/hosts")
	var keep []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.HasSuffix(l, "# river-cloud-init") {
			keep = append(keep, l)
		}
	}
	names := short
	if fqdn != short {
		names = fqdn + " " + short
	}
	keep = append(keep, "127.0.1.1\t"+names+"\t# river-cloud-init")
	if err := writeFileAtomic("/etc/hosts", []byte(strings.Join(keep, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	setStatus("hostname", short)
	return nil
}

// managedHeader marks a key file river-cloud-init wrote, so it removes only its own files.
const managedHeader = "# Written by river-cloud-init from instance metadata (ssh-keys); rewritten on every change.\n"

// authKeysDir is where sshd looks for per-account keys (AuthorizedKeysFile in 05-river-cloud.conf).
const authKeysDir = "/etc/ssh/authorized_keys.d"

// metadataSSHKeys returns the ssh-keys text of instance and (unless blocked) project metadata.
// Anything but a value or "not found" is an error, so a metadata hiccup never reads as "no
// keys" and never removes the keys already installed.
func metadataSSHKeys(ctx context.Context, md *cloud.Metadata) (string, error) {
	var text []string
	get := func(p string) (string, bool, error) {
		v, err := md.Get(ctx, p)
		if errors.Is(err, cloud.ErrNotFound) {
			return "", false, nil
		}
		return v, err == nil, err
	}
	for _, p := range []string{"instance/attributes/ssh-keys", "instance/attributes/sshKeys"} {
		v, ok, err := get(p)
		if err != nil {
			return "", err
		}
		if ok {
			text = append(text, v)
		}
	}
	block, _, err := get("instance/attributes/block-project-ssh-keys")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(strings.ToLower(block)) != "true" {
		v, ok, err := get("project/attributes/ssh-keys")
		if err != nil {
			return "", err
		}
		if ok {
			text = append(text, v)
		}
	}
	return strings.Join(text, "\n"), nil
}

// sshKeys installs the metadata server's keys. On a node image every key goes to the one admin
// account (runink); on a base image each key goes to the account named in its `ssh-keys` line
// (userKeys). The files live on the boot environment (public keys, root-owned 0644) and sshd
// reads them through AuthorizedKeysFile (etc/ssh/sshd_config.d/05-river-cloud.conf). They are
// rewritten at every boot and, where the river-cloud-watch longrun runs, on every metadata
// change, so a key removed from metadata stops working then.
func sshKeys(ctx context.Context, md *cloud.Metadata) error {
	text, err := metadataSSHKeys(ctx, md)
	if err != nil {
		return err
	}
	if p, _ := profile(readCloudEnv()); p == profileBase {
		return userKeys(authKeysDir, cloud.ParseSSHKeysByUser(text, time.Now()), ensureUser)
	}
	keys := cloud.ParseSSHKeys(text, time.Now())
	body := managedHeader
	for _, k := range keys {
		body += k + "\n"
	}
	if err := writeFileAtomic(authKeys, []byte(body), 0o644); err != nil {
		return err
	}
	setStatus("ssh_keys", fmt.Sprint(len(keys)))
	return nil
}

// userKeys writes dir/<user> for every account in byUser (creating the account first, with
// ensure, when it is missing) and removes the key files it wrote earlier for accounts that
// are no longer listed. A file without managedHeader is never touched. An account that cannot
// be created gets no key file; the others are still written, and the error comes back.
func userKeys(dir string, byUser map[string][]string, ensure func(string) error) error {
	users := make([]string, 0, len(byUser))
	for u := range byUser {
		users = append(users, u)
	}
	sort.Strings(users)
	var written []string
	var failed []string
	nkeys := 0
	for _, u := range users {
		if err := ensure(u); err != nil {
			logger.Printf("user %s: %v", u, err)
			failed = append(failed, u)
			continue
		}
		body := managedHeader + strings.Join(byUser[u], "\n") + "\n"
		if err := writeFileAtomic(filepath.Join(dir, u), []byte(body), 0o644); err != nil {
			return err
		}
		written = append(written, u)
		nkeys += len(byUser[u])
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n := e.Name()
		if slices.Contains(written, n) || strings.HasSuffix(n, ".new") || !e.Type().IsRegular() {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil && strings.HasPrefix(string(b), managedHeader) {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
	setStatus("ssh_keys", fmt.Sprint(nkeys))
	setStatus("ssh_users", strings.Join(written, ","))
	if len(failed) > 0 {
		return fmt.Errorf("no account for %s", strings.Join(failed, ", "))
	}
	return nil
}

// ensureUser creates the account when the system has none of that name: a home directory, bash,
// no password (the password field is "*", so no password can ever match), and the wheel group
// when there is one (78-cloud-target gives wheel sudo on a base image, as Compute Engine's guest
// environment does for the users in metadata).
func ensureUser(name string) error {
	_, err := user.Lookup(name)
	if err == nil {
		return nil
	}
	var unknown user.UnknownUserError
	if !errors.As(err, &unknown) {
		return err
	}
	args := []string{"-m", "-s", "/bin/bash", "-p", "*"}
	if _, gerr := user.LookupGroup("wheel"); gerr == nil {
		args = append(args, "-G", "wheel")
	}
	if _, err := run(nil, "useradd", append(args, name)...); err != nil {
		return err
	}
	logger.Printf("account %s created (no password)", name)
	return nil
}

// --- startup-script -----------------------------------------------------------------------------

const (
	startupAttr = "startup-script"
	startupPath = stateDir + "/startup-script"
	startupLog  = "/var/log/river-cloud-startup.log"
)

// startup is the s6 oneshot river-cloud-startup's job (detached, like late): once per boot, run
// the `startup-script` metadata value (instance, else project) as root. Only the base profile
// runs it; the node profile never executes metadata. The script's output goes to startupLog,
// never to this program's log, and the script itself is deleted when it ends.
func startup() error {
	env := readCloudEnv()
	p, err := profile(env)
	if err != nil {
		return err
	}
	if p != profileBase {
		setStatus("startup_script", "not-enabled")
		return nil
	}
	ctx := context.Background()
	md := metadataClient(env)
	wctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	_, err = md.Wait(wctx, 2*time.Second)
	cancel()
	if err != nil {
		setStatus("startup_script", "metadata-unreachable")
		return nil // the boot goes on; nothing is run that was not read
	}
	script, err := md.Attr(ctx, startupAttr)
	if errors.Is(err, cloud.ErrNotFound) {
		setStatus("startup_script", "none")
		return nil
	}
	if err != nil {
		setStatus("startup_script", "read-failed")
		return err
	}
	code, err := runStartupScript(script, startupPath, startupLog)
	if err != nil {
		setStatus("startup_script", "failed-to-start")
		return err
	}
	setStatus("startup_script", fmt.Sprintf("ran-exit-%d", code))
	return nil
}

// runStartupScript writes script to path (0700), runs it (directly when it begins with a
// shebang, else with /bin/sh), appends its output to logPath (0600), deletes it, and returns the
// exit code. An error means it could not be started.
func runStartupScript(script, path, logPath string) (int, error) {
	if err := writeFileAtomic(path, []byte(script), 0o700); err != nil {
		return -1, err
	}
	defer os.Remove(path)
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- a constant path of this program
	if err != nil {
		return -1, err
	}
	defer lf.Close()
	fmt.Fprintf(lf, "=== startup-script start %s (%d bytes)\n", time.Now().UTC().Format(time.RFC3339), len(script))
	cmd := exec.Command("/bin/sh", path) // #nosec G204 -- the script is the instance owner's own metadata, run as root by design (docs/cloud/edition.md)
	if strings.HasPrefix(script, "#!") {
		cmd = exec.Command(path) // #nosec G204 -- as above
	}
	cmd.Stdout, cmd.Stderr = lf, lf
	err = cmd.Run()
	code := 0
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		return -1, err
	}
	fmt.Fprintf(lf, "=== startup-script exit %d\n", code)
	return code, nil
}

// grow extends the root pool's partition to the end of its disk and the pool with it.
func grow(pool string) error {
	out, err := run(nil, "zpool", "status", "-P", "-L", pool)
	if err != nil {
		return err
	}
	var devs []string
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "/dev/") {
			devs = append(devs, f)
		}
	}
	if len(devs) != 1 {
		return fmt.Errorf("pool %s has %d devices; only a single-disk pool is grown", pool, len(devs))
	}
	p, err := cloud.ReadPartition("/sys", devs[0])
	if err != nil {
		return err
	}
	if !p.NeedsGrow() {
		setStatus("grow", "not-needed")
		return nil
	}
	info, err := run(nil, "sgdisk", "-i", fmt.Sprint(p.Number), p.Disk)
	if err != nil {
		return err
	}
	in, err := cloud.ParseSgdiskInfo(info)
	if err != nil {
		return err
	}
	if in.FirstLBA != p.Start {
		return fmt.Errorf("sgdisk says partition %d starts at %d, sysfs says %d: not touching it", p.Number, in.FirstLBA, p.Start)
	}
	if _, err := run(nil, "sgdisk", "-e", p.Disk); err != nil { // backup GPT header to the new end
		return err
	}
	if _, err := run(nil, "sgdisk", cloud.SgdiskGrowArgs(p.Disk, p.Number, in)...); err != nil {
		return err
	}
	// Tell the kernel the partition's new size (BLKPG; works while the pool is in use).
	if _, err := run(nil, "partx", "-u", "-n", fmt.Sprint(p.Number), p.Disk); err != nil {
		return err
	}
	if _, err := run(nil, "zpool", "online", "-e", pool, devs[0]); err != nil {
		return err
	}
	size, _ := run(nil, "zpool", "list", "-H", "-o", "size", pool)
	setStatus("grow", "grown-to-"+strings.TrimSpace(size))
	return nil
}

// nodeAddress makes sure the node's cluster address (a ULA on the river0 dummy interface,
// configured by NetworkManager) is present: k0s advertises it and kubelet uses it as the node
// IP, so the IPv6-only cluster runs on an instance whose NIC has only IPv4.
func nodeAddress(env map[string]string) error {
	addr := env["RIVER_NODE_ADDR"]
	if addr == "" {
		return nil
	}
	for i := 0; i < 30; i++ {
		out, _ := run(nil, "ip", "-6", "addr", "show", "dev", "river0")
		if strings.Contains(out, addr+"/") {
			setStatus("node_addr", addr)
			return nil
		}
		time.Sleep(time.Second)
	}
	// NetworkManager did not bring it up: add it directly, so the cluster can still start.
	_, _ = run(nil, "ip", "link", "add", "river0", "type", "dummy")
	if _, err := run(nil, "ip", "link", "set", "river0", "up"); err != nil {
		return err
	}
	if _, err := run(nil, "ip", "-6", "addr", "replace", addr+"/64", "dev", "river0"); err != nil {
		return err
	}
	setStatus("node_addr", addr+" (added by river-cloud-init)")
	return nil
}

// --- the data datasets and their key ---------------------------------------------------------

// The instance-metadata contract (instance attribute first, then project; docs/CLOUD-IMAGES.md,
// "Metadata contract").
// #nosec G101 -- metadata attribute NAMES, not credentials
const (
	attrKeySecret     = "runink-key-secret"               // secret id or name: the data-key wrapping secret
	attrModelsSecret  = "runink-models-passphrase-secret" // secret id or name: model payload passphrase
	attrModelsPass    = "runink-models-passphrase"        // the passphrase itself (weaker)
	attrEnrollment    = "runink-enrollment"               // an enrollment.env body (no secrets)
	attrEnrollSecrets = "runink-enrollment-secrets"       // comma-separated secret ids, enrollment.env fragments
	attrDataDevice    = "runink-data-device-name"         // device_name of an attached data disk
	attrSMEndpoint    = "runink-secretmanager-endpoint"   // test hook, http://169.254.169.254/... only

	dataPoolName = "zriver-data"     // the pool on a data disk
	propRole     = "org.runink:role" // "cloud-data" on the data encryption root
	propKeyPfx   = "org.runink:key." // + provider name: the sealed data key (base64), stored WITH the data
	roleData     = "cloud-data"
)

// secretClient is a Secret Manager client for this instance, with the test endpoint hook.
func secretClient(ctx context.Context, md *cloud.Metadata) *cloud.SecretManager {
	sm := &cloud.SecretManager{Meta: md}
	if ep, err := md.Attr(ctx, attrSMEndpoint); err == nil && strings.TrimSpace(ep) != "" {
		ep = strings.TrimSpace(ep)
		if cloud.CheckEndpoint(ep) == nil {
			sm.Endpoint = ep
		} else {
			logger.Printf("ignoring %s %q", attrSMEndpoint, ep)
		}
	}
	return sm
}

// secretName resolves a secret id ("name") or full resource name against the project.
func secretName(ctx context.Context, md *cloud.Metadata, s string) (string, error) {
	project := ""
	if !strings.HasPrefix(strings.TrimSpace(s), "projects/") {
		p, err := md.Get(ctx, "project/project-id")
		if err != nil {
			return "", fmt.Errorf("project id: %w", err)
		}
		project = strings.TrimSpace(p)
	}
	return cloud.SecretResource(project, s)
}

func providers(ctx context.Context, md *cloud.Metadata, aad string) []cloud.KeyProvider {
	sm := secretClient(ctx, md)
	secret := ""
	if s, err := md.Attr(ctx, attrKeySecret); err == nil && strings.TrimSpace(s) != "" {
		if r, err := secretName(ctx, md, s); err == nil {
			secret = r
		} else {
			logger.Printf("%s: %v", attrKeySecret, err)
		}
	}
	return []cloud.KeyProvider{
		&cloud.TPMProvider{},
		&cloud.SecretManagerProvider{SM: sm, Secret: secret, AAD: aad},
	}
}

// dataRoot finds the data encryption root on any imported pool (by its role property).
func dataRoot() string {
	out, err := run(nil, "zfs", "get", "-H", "-o", "name,value", "-t", "filesystem", "-s", "local", propRole)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[1] == roleData {
			return f[0]
		}
	}
	return ""
}

// dataPool picks where the data datasets live: the boot pool, or a pool on the attached
// disk named by runink-data-device-name (created on a blank disk, imported when the disk
// already carries one, e.g. after the boot disk was replaced by a newer image).
func dataPool(ctx context.Context, md *cloud.Metadata, bootPool string) (string, error) {
	name, err := md.Attr(ctx, attrDataDevice)
	name = strings.TrimSpace(name)
	if err != nil || name == "" {
		return bootPool, nil
	}
	dev, err := cloud.FindDataDisk("/sys", "/dev", name)
	if err != nil {
		return "", fmt.Errorf("%s=%s: %w (not falling back to the boot disk)", attrDataDevice, name, err)
	}
	setStatus("data_disk", dev)
	if _, err := run(nil, "zpool", "list", "-H", "-o", "name", dataPoolName); err == nil {
		_, _ = run(nil, "zpool", "online", "-e", dataPoolName, dev) // a resized data disk
		return dataPoolName, nil
	}
	// -f: the pool was last imported by another instance (a replaced boot disk).
	if _, err := run(nil, "zpool", "import", "-N", "-f", dataPoolName); err == nil {
		return dataPoolName, nil
	}
	// Only a BLANK disk is formatted: no filesystem or partition-table signature at all.
	if out, err := run(nil, "blkid", "-p", "-o", "export", dev); err == nil || strings.TrimSpace(out) != "" {
		return "", fmt.Errorf("refusing to format %s (%s): it is not blank and holds no %s pool", dev, name, dataPoolName)
	}
	if parts, _ := filepath.Glob(filepath.Join("/sys/class/block", filepath.Base(dev), filepath.Base(dev)+"*")); len(parts) > 0 {
		return "", fmt.Errorf("refusing to format %s (%s): it has partitions", dev, name)
	}
	if _, err := run(nil, "zpool", "create", "-o", "ashift=12", "-o", "autoexpand=on", "-o", "autotrim=on",
		"-O", "compression=zstd", "-O", "atime=off", "-O", "xattr=sa", "-O", "acltype=posixacl",
		"-O", "mountpoint=none", "-O", "canmount=off", dataPoolName, dev); err != nil {
		return "", err
	}
	logger.Printf("data disk %s (%s): pool %s created", dev, name, dataPoolName)
	return dataPoolName, nil
}

func data(ctx context.Context, md *cloud.Metadata, bootPool string) error {
	root := dataRoot()
	if root == "" {
		pool, err := dataPool(ctx, md, bootPool)
		if err != nil {
			return err
		}
		// Importing a data disk's pool may have brought an existing root with it.
		if root = dataRoot(); root == "" {
			root = pool + "/data"
			if zfsExists(root) {
				return fmt.Errorf("%s exists without the %s=%s property; not touching it", root, propRole, roleData)
			}
			return createData(ctx, providers(ctx, md, "river-cloud/"+root), root)
		}
	}
	setStatus("data_root", root)
	if zfsGet("keystatus", root) != "available" {
		key, via, err := unseal(ctx, providers(ctx, md, "river-cloud/"+root), root)
		if err != nil {
			return err
		}
		if _, err := run([]byte(hex.EncodeToString(key)), "zfs", "load-key", root); err != nil {
			return err
		}
		setStatus("unlock_provider", via)
	} else {
		setStatus("unlock_provider", "already-loaded")
	}
	migrateLegacyMountpoints(root)
	if _, err := run(nil, "zfs", "mount", "-a"); err != nil {
		logger.Printf("zfs mount -a: %v", err)
	}
	for _, d := range dataDatasets {
		if zfsGet("mounted", root+"/"+d.name) != "yes" {
			return fmt.Errorf("%s/%s is not mounted", root, d.name)
		}
	}
	setStatus("data", "unlocked")
	return nil
}

// blobs returns the sealed data keys stored on the encryption root, by provider.
func blobs(root string) map[string][]byte {
	out, _ := run(nil, "zfs", "get", "-H", "-o", "property,value", "-s", "local", "all", root)
	m := map[string][]byte{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) != 2 || !strings.HasPrefix(f[0], propKeyPfx) {
			continue
		}
		if b, err := base64.StdEncoding.DecodeString(f[1]); err == nil {
			m[strings.TrimPrefix(f[0], propKeyPfx)] = b
		}
	}
	return m
}

func unseal(ctx context.Context, ps []cloud.KeyProvider, root string) ([]byte, string, error) {
	bs := blobs(root)
	if len(bs) == 0 {
		return nil, "", fmt.Errorf("no sealed data key on %s", root)
	}
	var errs []string
	for _, p := range ps {
		b, ok := bs[p.Name()]
		if !ok {
			continue
		}
		var key []byte
		var err error
		for attempt := 0; attempt < 5; attempt++ { // the network may still be settling
			if key, err = p.Unseal(ctx, b); err == nil || errors.Is(err, cloud.ErrUnavailable) {
				break
			}
			time.Sleep(time.Duration(2<<attempt) * time.Second)
		}
		if err == nil && len(key) == 32 {
			return key, p.Name(), nil
		}
		errs = append(errs, p.Name()+": "+fmt.Sprint(err))
	}
	return nil, "", fmt.Errorf("no key provider could unseal the data key: %s", strings.Join(errs, "; "))
}

// seal wraps key with the first provider that works and proves the blob reopens.
func seal(ctx context.Context, ps []cloud.KeyProvider, key []byte) ([]byte, string, error) {
	var errs []string
	for _, p := range ps {
		b, err := p.Seal(ctx, key)
		if err != nil {
			errs = append(errs, p.Name()+": "+err.Error())
			continue
		}
		back, err := p.Unseal(ctx, b)
		if err != nil || !bytes.Equal(back, key) {
			errs = append(errs, p.Name()+": sealed blob does not reopen")
			continue
		}
		return b, p.Name(), nil
	}
	return nil, "", fmt.Errorf("no key provider can seal a data key (set the %s metadata attribute; docs/CLOUD-IMAGES.md): %s",
		attrKeySecret, strings.Join(errs, "; "))
}

// createData makes the per-instance data key, seals it with the first provider that works,
// proves the blob opens, and only then creates the encrypted datasets and moves the image's
// content for those paths into them. The sealed key is stored as a user property of the
// encryption root, so it travels with the data (a data disk moved to a new boot disk keeps
// it). A node that cannot seal its key does not create an encryption root it could never
// reopen: it stops here (fail closed; SSH still works for the admin to configure a provider
// and run `sudo river-cloud-init boot`).
func createData(ctx context.Context, ps []cloud.KeyProvider, root string) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	sealed, via, err := seal(ctx, ps, key)
	if err != nil {
		return err
	}
	if _, err := run([]byte(hex.EncodeToString(key)), "zfs", "create", "-o", "encryption=aes-256-gcm", "-o", "keyformat=hex",
		"-o", "keylocation=prompt", "-o", "canmount=off", "-o", "mountpoint=none",
		"-o", propRole+"="+roleData, "-o", propKeyPfx+via+"="+base64.StdEncoding.EncodeToString(sealed), root); err != nil {
		return err
	}
	if zfsGet("encryption", root) != "aes-256-gcm" {
		return fmt.Errorf("%s was created without encryption", root)
	}
	// appfs: the parent for per-application roots a downstream payload creates; they inherit
	// the data key. Not mounted itself.
	if _, err := run(nil, "zfs", "create", "-o", "canmount=off", "-o", "mountpoint=none", root+"/appfs"); err != nil {
		return err
	}
	for _, d := range dataDatasets {
		if err := migrate(root, d); err != nil {
			return err
		}
	}
	_, _ = run(nil, "/usr/local/bin/river-perms")
	setStatus("data_root", root)
	setStatus("unlock_provider", via)
	setStatus("data", "created")
	return nil
}

// migrate creates <root>/<name>, copies what the image has at its mountpoint into it, empties
// the (unencrypted) original, and mounts the dataset in its place.
func migrate(root string, d dataset) error {
	ds := root + "/" + d.name
	args := []string{"create", "-o", "canmount=noauto", "-o", "mountpoint=" + d.mountpoint}
	for _, p := range d.props {
		args = append(args, "-o", p)
	}
	if _, err := run(nil, "zfs", append(args, ds)...); err != nil {
		return err
	}
	tmp := filepath.Join(runDir, "migrate", d.name)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	if _, err := run(nil, "mount", "-t", "zfs", "-o", "zfsutil", ds, tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(d.mountpoint, 0o755); err != nil { // #nosec G301 -- a mountpoint such as /home; the dataset mounted on it sets its own mode
		return err
	}
	_, cpErr := run(nil, "rsync", "-aHAX", "--numeric-ids", d.mountpoint+"/", tmp+"/")
	if _, err := run(nil, "umount", tmp); err != nil {
		return err
	}
	if cpErr != nil {
		return cpErr
	}
	// The originals are image content (nothing secret: the image carries none); removing them
	// keeps a stale copy from reappearing if the dataset is ever unmounted.
	entries, _ := os.ReadDir(d.mountpoint)
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(d.mountpoint, e.Name())); err != nil {
			return err
		}
	}
	if _, err := run(nil, "zfs", "set", "canmount=on", ds); err != nil {
		return err
	}
	if _, err := run(nil, "zfs", "mount", ds); err != nil {
		return err
	}
	if d.mode != 0 {
		_ = os.Chmod(d.mountpoint, d.mode)
	}
	logger.Printf("%s: encrypted, mounted at %s", ds, d.mountpoint)
	return nil
}

// enrollment stages /etc/runink/enrollment.env from metadata for runink-firstboot.sh (run
// by rc.local) until the node is enrolled; with no enrollment metadata a node gets the
// single-node k0s role once. All or nothing: a secret that cannot be read leaves nothing
// staged, and the next boot tries again.
func enrollment(ctx context.Context, md *cloud.Metadata, env map[string]string) error {
	const staged, sentinel, k0sEnv = "/etc/runink/enrollment.env", "/var/lib/runink/.enrolled", "/etc/runink/k0s.env"
	if exists(sentinel) || exists(staged) {
		return nil
	}
	addr := env["RIVER_NODE_ADDR"]
	body, _ := md.Attr(ctx, attrEnrollment)
	list, _ := md.Attr(ctx, attrEnrollSecrets)
	var ids []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			ids = append(ids, s)
		}
	}
	if strings.TrimSpace(body) != "" || len(ids) > 0 {
		text := "# Staged by river-cloud-init from instance metadata; runink-firstboot.sh consumes and removes it.\n"
		if strings.TrimSpace(body) != "" {
			text += strings.TrimRight(body, "\n") + "\n"
		}
		sm := secretClient(ctx, md)
		for _, id := range ids {
			name, err := secretName(ctx, md, id)
			if err != nil {
				return err
			}
			b, _, err := sm.Access(ctx, name)
			if err != nil {
				return fmt.Errorf("%s: %w", attrEnrollSecrets, err)
			}
			text += "# from secret " + id + "\n" + strings.TrimRight(string(b), "\n") + "\n"
		}
		if addr != "" {
			// The cloud node's cluster address, unless the enrollment pins another one.
			text += fmt.Sprintf("K0S_KUBELET_EXTRA_ARGS=\"${K0S_KUBELET_EXTRA_ARGS:---node-ip=%s}\"\n", addr)
		}
		if err := writeFileAtomic(staged, []byte(text), 0o600); err != nil {
			return err
		}
		setStatus("enrollment", fmt.Sprintf("staged (metadata body: %v, secrets: %d)", strings.TrimSpace(body) != "", len(ids)))
		return nil
	}
	if exists(k0sEnv) || addr == "" {
		return nil
	}
	// Not enrolled: a single-node cluster on the node address, with the image's own CoreDNS
	// (the same settings runink-firstboot.sh derives for an enrolled node).
	k0s := fmt.Sprintf("K0S_ROLE=single\nK0S_ENABLE_WORKER=1\nK0S_KUBELET_EXTRA_ARGS=\"--node-ip=%s --cluster-dns=%s\"\nK0S_EXTRA_ARGS=\"--disable-components=coredns\"\n",
		addr, envOr(env, "RIVER_CLUSTER_DNS", "fd00:10:96::a"))
	if err := writeFileAtomic(k0sEnv, []byte(k0s), 0o600); err != nil {
		return err
	}
	dnsUpstreams()
	setStatus("enrollment", "none (single-node defaults)")
	return nil
}

func envOr(env map[string]string, k, def string) string {
	if v := env[k]; v != "" {
		return v
	}
	return def
}

// dnsUpstreams points the image's CoreDNS at the instance's resolvers through the NAT64
// prefix (pods are IPv6-only), as runink-firstboot.sh does for an enrolled node.
func dnsUpstreams() {
	const manifest = "/var/lib/k0s/manifests/runink-coredns/coredns.yaml"
	rc, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return
	}
	var ups []string
	for _, l := range strings.Split(string(rc), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[0] != "nameserver" || strings.HasPrefix(f[1], "127.") || f[1] == "::1" {
			continue
		}
		if strings.Contains(f[1], ":") {
			ups = append(ups, f[1])
		} else {
			ups = append(ups, "64:ff9b::"+f[1])
		}
	}
	m, err := os.ReadFile(manifest)
	if err != nil || len(ups) == 0 {
		return
	}
	re := regexp.MustCompile(`(?m)^(\s*)forward \. .*$`)
	out := re.ReplaceAll(m, []byte("${1}forward . "+strings.Join(ups, " ")))
	if err := os.WriteFile(manifest, out, 0o644); err == nil { // #nosec G703 G306 -- the image's CoreDNS manifest (constant path), read by k0s
		logger.Printf("cluster DNS upstreams: %s", strings.Join(ups, " "))
	}
}

// --- late: after rc-local has been released -------------------------------------------------

func late() error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	env := readCloudEnv()
	pool, err := rootPool()
	if err != nil {
		return err
	}
	if exists(stateDir + "/reguid-pending") {
		// Every instance from one image starts with the same pool GUID; give this one its own.
		if _, err := run(nil, "zpool", "reguid", pool); err != nil {
			logger.Printf("zpool reguid: %v", err)
		} else {
			_ = os.Remove(stateDir + "/reguid-pending")
			setStatus("pool_reguid", "done")
		}
	}
	if exists(stateDir + "/initramfs-stale") {
		// Bake the per-instance hostid (and anything else identity-bound) into the initramfs.
		if _, err := run(nil, "mkinitcpio", "-P"); err != nil {
			logger.Printf("mkinitcpio -P: %v", err)
		} else {
			_ = os.Remove(stateDir + "/initramfs-stale")
			setStatus("initramfs", "rebuilt")
		}
	}
	if p, _ := profile(env); p == profileBase {
		return nil // no data datasets, models or enrollment on a base image
	}
	md := metadataClient(env)
	if root := dataRoot(); root == "" || zfsGet("keystatus", root) != "available" {
		healthLine("failed", "data datasets locked")
		return errors.New("data datasets are locked; nothing to do")
	}
	// Readiness runs beside the model unpacking: a node is ready without its models.
	ready := make(chan struct{})
	go func() { defer close(ready); health(ctx) }()
	if err := models(ctx, md, pool); err != nil {
		logger.Printf("models: %v", err)
	}
	if !exists("/etc/runink/install-plan.json") {
		if err := installPlan(); err != nil {
			logger.Printf("install plan: %v", err)
		}
	}
	<-ready
	return nil
}

// models unpacks the encrypted model payload the image carries (<pool>/payload, AES-256-GCM
// ciphertext) into the encrypted <pool>/data/models, verifies it, and destroys the payload.
func models(ctx context.Context, md *cloud.Metadata, pool string) error {
	payloadDS := pool + "/payload"
	lock := shareDir + "/models.lock"
	manifest := shareDir + "/models.manifest"
	if !zfsExists(payloadDS) {
		if entries, _ := os.ReadDir(modelsMP); len(entries) > 0 {
			setStatus("models", "installed")
		} else {
			setStatus("models", "none (image carries no payload)")
		}
		return nil
	}
	payload := zfsGet("mountpoint", payloadDS)
	if !exists(payload + "/MANIFEST") {
		return fmt.Errorf("%s has no MANIFEST", payload)
	}
	pass, src, err := modelsPassphrase(ctx, md)
	if err != nil {
		setStatus("models", "deferred ("+err.Error()+")")
		return nil
	}
	if entries, _ := os.ReadDir(modelsMP); len(entries) > 0 {
		if _, err := run(nil, "river-modelpack", "verify", "--dir", modelsMP, "--lock", lock); err == nil {
			setStatus("models", "installed")
			_, _ = run(nil, "zfs", "destroy", "-r", payloadDS)
			return nil
		}
		return fmt.Errorf("%s is not empty and does not match %s; left as is", modelsMP, lock)
	}
	setStatus("models", "unpacking")
	if err := os.MkdirAll(runDir, 0o755); err != nil { // #nosec G301 -- see setStatus; the passphrase file below is created 0600
		return err
	}
	pf, err := os.CreateTemp(runDir, "models-pass.")
	if err != nil {
		return err
	}
	defer os.Remove(pf.Name())
	if err := pf.Chmod(0o600); err != nil {
		return err
	}
	if _, err := pf.WriteString(pass + "\n"); err != nil {
		return err
	}
	if err := pf.Close(); err != nil {
		return err
	}
	start := time.Now()
	if _, err := run(nil, "river-modelpack", "unpack", "--payload", payload, "--lock", lock,
		"--dest", modelsMP, "--passphrase-file", pf.Name()); err != nil {
		cleanDir(modelsMP)
		setStatus("models", "failed")
		return err
	}
	if exists(manifest) {
		cmd := exec.Command("sha256sum", "--quiet", "-c", manifest)
		cmd.Dir = modelsMP
		if out, err := cmd.CombinedOutput(); err != nil {
			cleanDir(modelsMP)
			setStatus("models", "failed")
			return fmt.Errorf("sha256sum -c %s: %v: %s", manifest, err, out)
		}
	}
	_, _ = run(nil, "chmod", "-R", "a+rX", modelsMP)
	if _, err := run(nil, "zfs", "destroy", "-r", payloadDS); err != nil {
		logger.Printf("zfs destroy %s: %v", payloadDS, err)
	}
	_ = os.Remove(payload) // the empty mountpoint left on the boot environment
	setStatus("models", fmt.Sprintf("installed (passphrase from %s, %s)", src, time.Since(start).Round(time.Second)))
	return nil
}

func cleanDir(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// modelsPassphrase: the secret named by runink-models-passphrase-secret (read like the key
// secret), else the value of runink-models-passphrase.
func modelsPassphrase(ctx context.Context, md *cloud.Metadata) (string, string, error) {
	if s, err := md.Attr(ctx, attrModelsSecret); err == nil && strings.TrimSpace(s) != "" {
		name, err := secretName(ctx, md, s)
		if err != nil {
			return "", "", err
		}
		b, _, err := secretClient(ctx, md).Access(ctx, name)
		if err != nil {
			return "", "", err
		}
		return firstLine(b), "secret", nil
	}
	if v, err := md.Attr(ctx, attrModelsPass); err == nil && strings.TrimSpace(v) != "" {
		return firstLine([]byte(v)), "metadata", nil
	}
	return "", "", errors.New("no " + attrModelsSecret + " or " + attrModelsPass + " metadata")
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(s)
}

// installPlan records the install plan for THIS instance's hardware (the image was built on
// another machine). The storage section describes the pool the image brought, not a layout
// to create; its verdict is kept as river-plan gives it.
func installPlan() error {
	probe := filepath.Join(runDir, "probe.json")
	out, err := run(nil, "river-hwprobe", "--json")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(probe, []byte(out), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("river-plan", "--probe", probe, "--manifest", shareDir+"/models.tiers", "--lock", shareDir+"/models.lock", "--json") // #nosec G204 -- fixed binary and constant paths
	plan, err := cmd.Output()
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 3) {
		return err
	}
	if err := writeFileAtomic("/etc/runink/install-plan.json", plan, 0o600); err != nil {
		return err
	}
	setStatus("install_plan", "written")
	return nil
}

// --- reseal --------------------------------------------------------------------------------

// reseal re-wraps the data key with the currently configured provider and secret version
// (after rotating the key secret) and replaces that provider's blob on the encryption root.
func reseal() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	md := metadataClient(readCloudEnv())
	root := dataRoot()
	if root == "" {
		return errors.New("no data encryption root on this instance")
	}
	ps := providers(ctx, md, "river-cloud/"+root)
	key, _, err := unseal(ctx, ps, root)
	if err != nil {
		return err
	}
	b, via, err := seal(ctx, ps, key)
	if err != nil {
		return err
	}
	if _, err := run(nil, "zfs", "set", propKeyPfx+via+"="+base64.StdEncoding.EncodeToString(b), root); err != nil {
		return err
	}
	logger.Printf("data key resealed with %s", via)
	return nil
}

// k0sPerms re-asserts the shadow-grade modes once the k0s node is Ready. k0s creates
// /var/lib/k0s/pki/admin.conf 0640, and its worker resets /etc/k0s to 0755 when it starts,
// both AFTER the boot-time river-perms pass (on every node; it shows on a cloud image because
// its k0s starts on first boot). health() calls it before it reports ready.
func k0sPerms() {
	if out, err := run(nil, "/usr/local/bin/river-perms"); err != nil {
		logger.Printf("river-perms: %v", err)
	} else {
		logger.Printf("%s", strings.TrimSpace(out))
	}
}

// Serial-console health contract (docs/CLOUD-IMAGES.md, "Health on the serial console").
// A deployment test reads the instance's serial port output:
//
//	RUNINK-HEALTH ready (Runink River node ready)    enrollment done (if any was staged),
//	                                                  data unlocked, the k0s node Ready, and
//	                                                  every payload readiness hook passing
//	RUNINK-HEALTH failed <reason>                    it will not become ready this boot
const readyHooks = "/usr/local/share/runink/core/ready.d"

func healthLine(state, detail string) {
	line := "RUNINK-HEALTH " + state
	if detail != "" {
		line += " (" + detail + ")"
	}
	setStatus("health", state)
	logger.Print(line)
	if f, err := os.OpenFile("/dev/console", os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_, _ = fmt.Fprintf(f, "\r\n%s\r\n", line)
		_ = f.Close()
	}
}

// health waits (at most 30 minutes) for the node to be ready, then prints the ready line.
func health(ctx context.Context) {
	deadline := time.Now().Add(30 * time.Minute)
	wait := func(what string, ok func() bool) bool {
		for !ok() {
			if time.Now().After(deadline) || ctx.Err() != nil {
				healthLine("failed", what+" not reached within 30 minutes")
				return false
			}
			time.Sleep(10 * time.Second)
		}
		return true
	}
	// Enrollment: when one was staged, runink-firstboot.sh consumes it and leaves the sentinel.
	if exists("/etc/runink/enrollment.env") && !wait("enrollment", func() bool { return exists("/var/lib/runink/.enrolled") }) {
		return
	}
	if !wait("k0s node Ready", k0sReady) {
		return
	}
	k0sPerms()
	// A downstream payload may delay readiness: every executable in ready.d must exit 0.
	hooks, _ := filepath.Glob(readyHooks + "/*")
	for _, h := range hooks {
		if st, err := os.Stat(h); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		name := filepath.Base(h)
		if !wait("payload readiness hook "+name, func() bool {
			hctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			return exec.CommandContext(hctx, h).Run() == nil // #nosec G204 -- executables of the image's own payload tree
		}) {
			return
		}
	}
	healthLine("ready", "Runink River node ready")
}

func k0sReady() bool {
	out, err := run(nil, "k0s", "kubectl", "get", "nodes", "-o",
		`jsonpath={range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}`)
	return err == nil && strings.Contains(out, "True")
}
