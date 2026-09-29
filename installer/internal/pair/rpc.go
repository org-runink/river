// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package pair

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// The restricted RPC. The pairing sshd forces every session through
// `river-pair-announce rpc`, which reads the operator's command from SSH_ORIGINAL_COMMAND and
// accepts only this grammar (no shell ever sees it):
//
//	hello                              session id, live-system version
//	plan [--lab]                       probe + plan on the target; prints the plan and the disks
//	stage-payload NAME                 stage payload/NAME (rsync'd before) with river-payloadpack
//	install --serial S [--serial S...] [--pool P] [--be B] [--hostname H]
//	                                   runink-autoinstall with the plan made by `plan`; secrets
//	                                   arrive on stdin (EncodeSecrets), never on disk
//	reboot                             tear the session down and reboot the target
//	finish                             tear the session down, no reboot
//	rsync --server -<flags> [--delete] . payload/NAME/
//	                                   the receiving end of `rsync -rt --delete`, into the pairing
//	                                   user's own RAM staging dir
const MaxCommand = 1024

// RPC is a parsed command.
type RPC struct {
	Verb     string
	Lab      bool
	Serials  []string
	Pool     string
	BE       string
	Hostname string
	Name     string   // stage-payload / rsync: the payload name
	Argv     []string // rsync: the exact server argv to exec
}

var (
	tokenRe    = regexp.MustCompile(`^[A-Za-z0-9._/=:,@+-]+$`)
	serialRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	poolRe     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	nameRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	rsyncFlags = regexp.MustCompile(`^-[rtvz]*e\.[A-Za-z]+$`)
)

// ErrCommand is returned for any command outside the grammar.
var ErrCommand = errors.New("command not allowed")

// ParseRPC validates an SSH_ORIGINAL_COMMAND against the grammar.
func ParseRPC(cmd string) (RPC, error) {
	if len(cmd) > MaxCommand {
		return RPC{}, ErrCommand
	}
	argv := strings.Split(cmd, " ")
	for _, a := range argv {
		if !tokenRe.MatchString(a) {
			return RPC{}, ErrCommand
		}
	}
	bad := func(why string) (RPC, error) { return RPC{}, fmt.Errorf("%w: %s", ErrCommand, why) }
	r := RPC{Verb: argv[0]}
	args := argv[1:]
	switch r.Verb {
	case "hello", "reboot", "finish":
		if len(args) != 0 {
			return bad(r.Verb + " takes no arguments")
		}
	case "plan":
		switch {
		case len(args) == 0:
		case len(args) == 1 && args[0] == "--lab":
			r.Lab = true
		default:
			return bad("plan takes only --lab")
		}
	case "stage-payload":
		if len(args) != 1 || !validName(args[0]) {
			return bad("stage-payload NAME")
		}
		r.Name = args[0]
	case "install":
		for i := 0; i < len(args); i += 2 {
			if i+1 >= len(args) {
				return bad(args[i] + " needs a value")
			}
			v := args[i+1]
			switch args[i] {
			case "--serial":
				if !serialRe.MatchString(v) {
					return bad("bad serial")
				}
				r.Serials = append(r.Serials, v)
			case "--pool":
				if r.Pool != "" || !poolRe.MatchString(v) {
					return bad("bad pool name")
				}
				r.Pool = v
			case "--be":
				if r.BE != "" || !poolRe.MatchString(v) {
					return bad("bad boot-environment name")
				}
				r.BE = v
			case "--hostname":
				if r.Hostname != "" || !hostnameRe.MatchString(v) {
					return bad("bad hostname")
				}
				r.Hostname = v
			default:
				return bad("unknown install option " + args[i])
			}
		}
		if len(r.Serials) == 0 {
			return bad("install needs --serial for every disk the plan erases")
		}
	case "rsync":
		return parseRsync(argv)
	default:
		return bad("unknown verb " + r.Verb)
	}
	return r, nil
}

func validName(s string) bool { return nameRe.MatchString(s) && !strings.Contains(s, "..") }

// parseRsync accepts only the receiving side of `rsync -rt [--delete]` into payload/NAME/:
// no --sender (nothing is ever read back), no long option but --delete, no links, devices,
// owners or permissions, and a destination inside the staging dir.
func parseRsync(argv []string) (RPC, error) {
	bad := func(why string) (RPC, error) { return RPC{}, fmt.Errorf("%w: rsync: %s", ErrCommand, why) }
	if len(argv) < 5 || argv[1] != "--server" || !rsyncFlags.MatchString(argv[2]) {
		return bad("only `rsync -rt [--delete]` to this target is allowed")
	}
	rest := argv[3:]
	if rest[0] == "--delete" {
		rest = rest[1:]
	}
	if len(rest) != 2 || rest[0] != "." {
		return bad("unexpected arguments")
	}
	dst, ok := strings.CutPrefix(rest[1], "payload/")
	dst = strings.TrimSuffix(dst, "/")
	if !ok || !validName(dst) {
		return bad("the destination must be payload/NAME/")
	}
	// --munge-links: should a link ever arrive, it is stored harmless (rsync's own guard).
	out := append([]string{"rsync", "--server", argv[2], "--munge-links"}, argv[3:]...)
	return RPC{Verb: "rsync", Name: dst, Argv: out}, nil
}

// String renders an RPC back as a command line (the operator's side builds commands with it).
func (r RPC) String() string {
	a := []string{r.Verb}
	switch r.Verb {
	case "plan":
		if r.Lab {
			a = append(a, "--lab")
		}
	case "stage-payload":
		a = append(a, r.Name)
	case "install":
		for _, s := range r.Serials {
			a = append(a, "--serial", s)
		}
		for _, kv := range [][2]string{{"--pool", r.Pool}, {"--be", r.BE}, {"--hostname", r.Hostname}} {
			if kv[1] != "" {
				a = append(a, kv[0], kv[1])
			}
		}
	}
	return strings.Join(a, " ")
}

// Secrets travel on the install command's stdin and are held in memory on the target.
type Secrets struct {
	ModelsPassphrase string
	AdminKeys        []string // authorized_keys lines for the runink admin
}

const maxSecrets = 64 << 10

// EncodeSecrets writes the stdin frame of `install`.
func EncodeSecrets(w io.Writer, s Secrets) error {
	b := &strings.Builder{}
	enc := base64.StdEncoding.EncodeToString
	if s.ModelsPassphrase != "" {
		fmt.Fprintf(b, "models-passphrase %s\n", enc([]byte(s.ModelsPassphrase)))
	}
	for _, k := range s.AdminKeys {
		fmt.Fprintf(b, "admin-key %s\n", enc([]byte(k)))
	}
	b.WriteString("end\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// ReadSecrets reads the frame EncodeSecrets wrote, up to its "end" line.
func ReadSecrets(r io.Reader) (Secrets, error) {
	var s Secrets
	sc := bufio.NewScanner(io.LimitReader(r, maxSecrets))
	sc.Buffer(make([]byte, 0, 4096), maxSecrets)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		if k == "end" {
			return s, nil
		}
		d, err := base64.StdEncoding.DecodeString(v)
		if err != nil || strings.ContainsAny(string(d), "\n\r\x00") {
			return Secrets{}, errors.New("secrets: malformed line")
		}
		switch k {
		case "models-passphrase":
			s.ModelsPassphrase = string(d)
		case "admin-key":
			if _, err := ParseAuthorizedKey(string(d)); err != nil {
				return Secrets{}, fmt.Errorf("secrets: admin key: %w", err)
			}
			s.AdminKeys = append(s.AdminKeys, string(d))
		default:
			return Secrets{}, errors.New("secrets: unknown field " + k)
		}
	}
	return Secrets{}, errors.New("secrets: no end line")
}

// ParseAuthorizedKey accepts one public key line for the installed node's admin: an
// OpenSSH key type and base64 blob, optionally a comment. Options are refused.
func ParseAuthorizedKey(line string) (string, error) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return "", errors.New("not a public key line")
	}
	switch f[0] {
	case "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
	default:
		return "", errors.New("unsupported key type " + f[0])
	}
	if _, err := base64.StdEncoding.DecodeString(f[1]); err != nil {
		return "", errors.New("bad key data")
	}
	return strings.Join(f, " "), nil
}

// Frames on the RPC socket from the daemon to the wrapper.
const (
	FrameOut  = 'o' // bytes for the operator's stdout
	FrameExit = 'x' // exit status, decimal
	maxFrame  = 1 << 20
)

// WriteFrame writes one frame.
func WriteFrame(w io.Writer, typ byte, data []byte) error {
	var h [5]byte
	h[0] = typ
	binary.BigEndian.PutUint32(h[1:], uint32(len(data))) // #nosec G115 -- FrameWriter splits writes at maxFrame (1 MiB); ReadFrame refuses larger
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[1:])
	if n > maxFrame {
		return 0, nil, errors.New("frame too large")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return h[0], b, err
}

// FrameWriter turns writes into FrameOut frames.
type FrameWriter struct{ W io.Writer }

func (f FrameWriter) Write(p []byte) (int, error) {
	for off := 0; off < len(p); off += maxFrame {
		end := min(off+maxFrame, len(p))
		if err := WriteFrame(f.W, FrameOut, p[off:end]); err != nil {
			return off, err
		}
	}
	return len(p), nil
}

// RecoveryKeyLine matches the grouped recovery key line 10-disk-zfs prints.
var RecoveryKeyLine = regexp.MustCompile(`^\s*([0-9a-f]{8} ){7}[0-9a-f]{8}\s*$`)
