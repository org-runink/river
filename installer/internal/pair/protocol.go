// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package pair

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

// Fixed ports and the beacon group. None of them is taken from a beacon: an operator
// connects only to the SOURCE address of an announcement, on these ports.
const (
	BeaconGroup = "ff02::7269:7672" // link-local scope (ff02::/16), unregistered
	BeaconPort  = 47653             // UDP: the announcement
	PairPort    = 47654             // TCP: the pairing endpoint
	SSHPort     = 47655             // TCP: the pairing session's restricted sshd
	PairUser    = "river-pair"      // the only account the pairing sshd admits

	CodeTTL     = 15 * time.Minute // a code is good for this long after it is shown
	MaxAttempts = 5                // wrong codes before pairing locks
	NonceLen    = 16
	MaxLine     = 4096 // longest pairing-protocol line either side reads
)

// Session errors. They are what a pairing request gets back, so callers match on them.
var (
	ErrExpired  = errors.New("expired")  // the code is older than CodeTTL
	ErrLocked   = errors.New("locked")   // MaxAttempts wrong codes
	ErrBadMAC   = errors.New("rejected") // wrong code (or a forged request)
	ErrUsed     = errors.New("used")     // the code already paired (or was refused): one use
	ErrBusy     = errors.New("busy")     // a pairing is waiting for the local operator
	ErrProtocol = errors.New("malformed request")
)

// Request is what the operator sends to the pairing endpoint (one JSON line).
type Request struct {
	V       int    `json:"v"`
	Session string `json:"session"` // the short id from the announcement
	OpKey   string `json:"op_key"`  // the operator's ephemeral ssh-ed25519 public key
	Nonce   string `json:"nonce"`   // base64, NonceLen bytes, fresh per request
	MAC     string `json:"mac"`     // base64 HMAC-SHA256, see RequestMAC
}

// Reply is what the target sends back: first a "confirm" (it knew the code too, here is its
// host key), then, after the local operator answered, "accepted" or "refused". "error"
// replies carry no MAC.
type Reply struct {
	V       int    `json:"v"`
	Status  string `json:"status"` // confirm | accepted | refused | error
	Error   string `json:"error,omitempty"`
	HostKey string `json:"host_key,omitempty"`
	User    string `json:"user,omitempty"`
	MAC     string `json:"mac,omitempty"`
}

func codeKey(code string) []byte {
	h := sha256.Sum256([]byte("river-pair/v1 key\n" + code))
	return h[:]
}

func mac(code, label string, fields ...string) string {
	m := hmac.New(sha256.New, codeKey(code))
	_, _ = io.WriteString(m, "river-pair/v1 "+label)
	for _, f := range fields {
		_, _ = io.WriteString(m, "\n"+f)
	}
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func macEqual(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

// RequestMAC binds the operator's key to the code AND to the target it saw announced (its
// session id and host key fingerprint), so a request cannot be replayed to another target.
func RequestMAC(code, session, hostFP, opKey, nonce string) string {
	return mac(code, "request", session, hostFP, opKey, nonce)
}

// ConfirmMAC proves to the operator that the target knows the code too, and binds the host
// key the target presents to this request.
func ConfirmMAC(code, session, hostKey, opKey, nonce string) string {
	return mac(code, "confirm", session, hostKey, opKey, nonce)
}

// VerdictMAC authenticates the local operator's answer.
func VerdictMAC(code, status, session, opKey, nonce string) string {
	return mac(code, "verdict", status, session, opKey, nonce)
}

// NewNonce returns a fresh base64 nonce.
func NewNonce() (string, error) {
	var b [NonceLen]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

// NewRequest builds a request for a target (session id + host key fingerprint from its
// announcement), using the canonical code the operator typed.
func NewRequest(code, session, hostFP string, opKey SSHKey) (Request, error) {
	n, err := NewNonce()
	if err != nil {
		return Request{}, err
	}
	k := opKey.String()
	return Request{V: 1, Session: session, OpKey: k, Nonce: n, MAC: RequestMAC(code, session, hostFP, k, n)}, nil
}

// VerifyConfirm checks a "confirm" reply on the operator's side: authentic under the code,
// for this request, and presenting the host key whose fingerprint the announcement carried.
func VerifyConfirm(code, hostFP string, req Request, r Reply) (SSHKey, error) {
	if r.Status != "confirm" {
		return SSHKey{}, errors.New("unexpected reply " + r.Status)
	}
	hk, err := ParseSSHKey(r.HostKey)
	if err != nil {
		return SSHKey{}, err
	}
	if !macEqual(r.MAC, ConfirmMAC(code, req.Session, hk.String(), req.OpKey, req.Nonce)) {
		return SSHKey{}, errors.New("the target's reply is not authentic under this code")
	}
	if hk.Fingerprint() != hostFP {
		return SSHKey{}, errors.New("the target's host key does not match its announcement")
	}
	return hk, nil
}

// VerifyVerdict checks the final "accepted"/"refused" reply.
func VerifyVerdict(code string, req Request, r Reply) (accepted bool, err error) {
	if r.Status != "accepted" && r.Status != "refused" {
		return false, errors.New("unexpected reply " + r.Status)
	}
	if !macEqual(r.MAC, VerdictMAC(code, r.Status, req.Session, req.OpKey, req.Nonce)) {
		return false, errors.New("the target's verdict is not authentic under this code")
	}
	return r.Status == "accepted", nil
}

// Session is the target's pairing state for one code. It is safe for concurrent use.
type Session struct {
	mu       sync.Mutex
	id       string
	code     string
	hostKey  SSHKey
	created  time.Time
	attempts int
	locked   bool
	used     bool
	pending  bool
}

// NewSession starts the pairing window for code at now.
func NewSession(id, code string, hostKey SSHKey, now time.Time) *Session {
	return &Session{id: id, code: code, hostKey: hostKey, created: now}
}

// ID is the session's short id.
func (s *Session) ID() string { return s.id }

// Code is the canonical code (shown on the local console only).
func (s *Session) Code() string { return s.code }

// Expires is when the code stops being accepted.
func (s *Session) Expires() time.Time { return s.created.Add(CodeTTL) }

// Attempts is the number of wrong codes so far.
func (s *Session) Attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// Pending reports whether a verified request is waiting for the local operator's answer.
func (s *Session) Pending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending
}

// Locked reports whether MaxAttempts wrong codes locked pairing.
func (s *Session) Locked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked
}

// Verify checks a request at time now. On success the session is pending the local
// operator's answer and the returned key is the operator's; call Decide next. A wrong MAC
// counts an attempt; the fifth locks the session for good.
func (s *Session) Verify(now time.Time, req Request) (SSHKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.used:
		return SSHKey{}, ErrUsed
	case s.locked:
		return SSHKey{}, ErrLocked
	case !now.Before(s.created.Add(CodeTTL)):
		return SSHKey{}, ErrExpired
	case s.pending:
		return SSHKey{}, ErrBusy
	}
	nonce, err := base64.StdEncoding.DecodeString(req.Nonce)
	if req.V != 1 || req.Session != s.id || err != nil || len(nonce) != NonceLen || req.MAC == "" {
		return SSHKey{}, ErrProtocol
	}
	opKey, err := ParseSSHKey(req.OpKey)
	if err != nil || opKey.String() != req.OpKey {
		return SSHKey{}, ErrProtocol
	}
	if !macEqual(req.MAC, RequestMAC(s.code, s.id, s.hostKey.Fingerprint(), req.OpKey, req.Nonce)) {
		s.attempts++
		if s.attempts >= MaxAttempts {
			s.locked = true
			return SSHKey{}, ErrLocked
		}
		return SSHKey{}, ErrBadMAC
	}
	s.pending = true
	return opKey, nil
}

// Confirm is the reply to a verified request.
func (s *Session) Confirm(req Request) Reply {
	hk := s.hostKey.String()
	return Reply{V: 1, Status: "confirm", HostKey: hk, MAC: ConfirmMAC(s.code, s.id, hk, req.OpKey, req.Nonce)}
}

// Decide records the local operator's answer and returns the reply for the operator. The
// code is spent either way: a refused pairing needs a new session and a new code.
func (s *Session) Decide(req Request, allow bool) Reply {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = false
	s.used = true
	st := "refused"
	if allow {
		st = "accepted"
	}
	r := Reply{V: 1, Status: st, MAC: VerdictMAC(s.code, st, s.id, req.OpKey, req.Nonce)}
	if allow {
		r.User = PairUser
	}
	return r
}

// ErrorReply is the unauthenticated reply for a refused request.
func ErrorReply(err error) Reply {
	e := ErrProtocol.Error()
	for _, k := range []error{ErrExpired, ErrLocked, ErrBadMAC, ErrUsed, ErrBusy} {
		if errors.Is(err, k) {
			e = k.Error()
		}
	}
	return Reply{V: 1, Status: "error", Error: e}
}

// HostName is the name a session announces: river-install-<short id>.
func HostName(id string) string { return "river-install-" + strings.ToLower(id) }
