// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/org-runink/river/guide/bundle"
)

// State of one step.
type State string

const (
	Pending  State = "pending"
	Proposed State = "proposed"
	Running  State = "running"
	Done     State = "done"
	Failed   State = "failed"
	Skipped  State = "skipped"
)

// Actor is who asked for a transition.
type Actor struct {
	Remote bool   // true for an issue-comment tag
	Login  string // GitHub login for remote actors; "console" locally
}

// Local is the console operator.
var Local = Actor{Login: "console"}

func (a Actor) String() string {
	if a.Remote {
		return "@" + a.Login + " (remote)"
	}
	return "console"
}

// Step is a StepDef plus its live state.
type Step struct {
	*bundle.StepDef
	Private bool
	State   State
	Output  string // captured command output; LOCAL ONLY, never posted remotely
	Exit    int
	By      string
}

// Errors returned by transitions. They are policy, so callers match on them.
var (
	ErrUnknownStep     = errors.New("no such step")
	ErrNotProposed     = errors.New("step has not been proposed at the console")
	ErrRemoteDisabled  = errors.New("remote approvals are not enabled at the console")
	ErrDestructive     = errors.New("destructive steps are never run by river-guide")
	ErrNotRunnable     = errors.New("this step has no command river-guide may run")
	ErrRemoteForbidden = errors.New("only the console operator may do that")
	ErrBusy            = errors.New("another step is proposed or running")
)

// Runner executes a step's command. Tests replace it.
type Runner func(ctx context.Context, argv []string) (out string, exit int, err error)

// ExecRunner runs argv directly (no shell) with a timeout and a bounded output buffer.
func ExecRunner(timeout time.Duration) Runner {
	return func(ctx context.Context, argv []string) (string, int, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- argv comes from the verified bundle step table, run without a shell; destructive steps are refused before this
		cmd.Stdout, cmd.Stderr = &limited{w: &buf, n: 64 << 10}, &limited{w: &buf, n: 64 << 10}
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return buf.String(), ee.ExitCode(), nil
		}
		if err != nil {
			return buf.String(), -1, err
		}
		return buf.String(), 0, nil
	}
}

type limited struct {
	w *bytes.Buffer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.w.Len(); room > 0 {
		if len(p) > room {
			l.w.Write(p[:room])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}

// Machine is the install step state machine. It mirrors the guide: the steps are the
// bundles' step directives, in bundle order (Runink River first, then any platform bundle).
type Machine struct {
	mu             sync.Mutex
	steps          []*Step
	remoteApproval bool
	run            Runner
}

// NewMachine builds a machine over the given bundles.
func NewMachine(run Runner, bundles ...*bundle.Bundle) *Machine {
	m := &Machine{run: run}
	for _, b := range bundles {
		m.Append(b)
	}
	return m
}

// Append adds a bundle's steps (used when a platform bundle arrives after sign-in).
func (m *Machine) Append(b *bundle.Bundle) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range b.Steps {
		m.steps = append(m.steps, &Step{StepDef: d, Private: b.Private, State: Pending})
	}
}

// SetRemoteApproval is a CONSOLE-only switch.
func (m *Machine) SetRemoteApproval(on bool) {
	m.mu.Lock()
	m.remoteApproval = on
	m.mu.Unlock()
}

// RemoteApproval reports the switch.
func (m *Machine) RemoteApproval() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.remoteApproval
}

// Steps returns a snapshot.
func (m *Machine) Steps() []Step {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Step, len(m.steps))
	for i, s := range m.steps {
		out[i] = *s
	}
	return out
}

func (m *Machine) find(id string) *Step {
	for _, s := range m.steps {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Current is the proposed or running step, if any.
func (m *Machine) Current() (Step, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.steps {
		if s.State == Proposed || s.State == Running {
			return *s, true
		}
	}
	return Step{}, false
}

// Next proposes the first pending step. Console only.
func (m *Machine) Next(a Actor) (Step, error) {
	if a.Remote {
		return Step{}, ErrRemoteForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.steps {
		if s.State == Proposed || s.State == Running {
			return *s, nil
		}
	}
	for _, s := range m.steps {
		if s.State == Pending {
			s.State = Proposed
			return *s, nil
		}
	}
	return Step{}, errors.New("all steps are finished")
}

// Approve runs a PROPOSED runnable step. A remote actor may approve only when the console
// enabled remote approvals, and never a destructive step. Nobody may make river-guide run
// a destructive step: the operator runs it at the console and marks it done.
func (m *Machine) Approve(ctx context.Context, id string, a Actor) (Step, error) {
	m.mu.Lock()
	s := m.find(id)
	switch {
	case s == nil:
		m.mu.Unlock()
		return Step{}, ErrUnknownStep
	case s.State != Proposed:
		m.mu.Unlock()
		return *s, ErrNotProposed
	case s.Kind == bundle.KindDestructive:
		m.mu.Unlock()
		return *s, ErrDestructive
	case !s.Kind.Runnable():
		m.mu.Unlock()
		return *s, ErrNotRunnable
	case a.Remote && !m.remoteApproval:
		m.mu.Unlock()
		return *s, ErrRemoteDisabled
	}
	argv, err := bundle.SplitArgs(s.Run)
	if err != nil || len(argv) == 0 {
		m.mu.Unlock()
		return *s, fmt.Errorf("step %s: bad command: %v", id, err)
	}
	s.State, s.By = Running, a.String()
	m.mu.Unlock()

	out, exit, err := m.run(ctx, argv)

	m.mu.Lock()
	defer m.mu.Unlock()
	s.Output, s.Exit = out, exit
	if err != nil || exit != 0 {
		s.State = Failed
		if err != nil {
			s.Output = strings.TrimSpace(s.Output + "\n" + err.Error())
		}
	} else {
		s.State = Done
	}
	return *s, nil
}

// MarkDone records that the operator completed an info or destructive step themselves.
// Console only: a remote actor cannot attest to something that happened at the machine.
func (m *Machine) MarkDone(id string, a Actor) (Step, error) {
	return m.settle(id, a, Done)
}

// Skip skips a step. Console only.
func (m *Machine) Skip(id string, a Actor) (Step, error) {
	return m.settle(id, a, Skipped)
}

// Retry returns a failed step to proposed. Console only.
func (m *Machine) Retry(id string, a Actor) (Step, error) {
	if a.Remote {
		return Step{}, ErrRemoteForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.find(id)
	if s == nil {
		return Step{}, ErrUnknownStep
	}
	if s.State != Failed {
		return *s, fmt.Errorf("step %s is %s, not failed", id, s.State)
	}
	s.State = Proposed
	return *s, nil
}

func (m *Machine) settle(id string, a Actor, to State) (Step, error) {
	if a.Remote {
		return Step{}, ErrRemoteForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.find(id)
	if s == nil {
		return Step{}, ErrUnknownStep
	}
	if s.State == Running {
		return *s, ErrBusy
	}
	if to == Done && s.Kind.Runnable() && s.State != Failed {
		// A runnable step is done when its command succeeded, not when someone says so —
		// except after a failure, where the operator may have fixed things by hand.
		return *s, fmt.Errorf("step %s runs a command: approve it instead", id)
	}
	s.State, s.By = to, a.String()
	return *s, nil
}

// Progress counts done+skipped over total, split by public/private.
func (m *Machine) Progress() (pubDone, pubTotal, privDone, privTotal int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.steps {
		fin := s.State == Done || s.State == Skipped
		if s.Private {
			privTotal++
			if fin {
				privDone++
			}
		} else {
			pubTotal++
			if fin {
				pubDone++
			}
		}
	}
	return
}
