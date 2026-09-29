// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type recRunner struct {
	calls [][]string
	exit  int
}

func (r *recRunner) run(_ context.Context, argv []string) (string, int, error) {
	r.calls = append(r.calls, argv)
	return "ok\n", r.exit, nil
}

func proposeUntil(t *testing.T, m *Machine, id string) Step {
	t.Helper()
	for {
		st, err := m.Next(Local)
		if err != nil {
			t.Fatalf("never reached %s: %v", id, err)
		}
		if st.ID == id {
			return st
		}
		if st.Kind.Runnable() {
			if _, err := m.Approve(context.Background(), st.ID, Local); err != nil {
				t.Fatal(err)
			}
		} else if _, err := m.MarkDone(st.ID, Local); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStepMachinePolicy(t *testing.T) {
	r := &recRunner{}
	m := NewMachine(r.run, river(t))
	remote := Actor{Remote: true, Login: "alice"}
	ctx := context.Background()

	if _, err := m.Next(remote); !errors.Is(err, ErrRemoteForbidden) {
		t.Fatalf("remote next: %v", err)
	}
	// Network first: the first step is the read-only network check, never destructive.
	st, _ := m.Next(Local)
	if st.ID != "network" || st.State != Proposed || !st.Kind.Runnable() {
		t.Fatalf("first step %+v", st)
	}
	if st, err := m.Approve(ctx, "network", Local); err != nil || st.State != Done {
		t.Fatalf("network step: %+v %v", st, err)
	}
	if !reflect.DeepEqual(r.calls[0], []string{"river-netsetup", "--check"}) {
		t.Fatalf("network step ran %v", r.calls[0])
	}
	st, _ = m.Next(Local)
	if st.ID != "live-session" || st.State != Proposed {
		t.Fatalf("second step %+v", st)
	}
	if _, err := m.MarkDone("live-session", remote); !errors.Is(err, ErrRemoteForbidden) {
		t.Fatal("remote may not attest a step")
	}
	if _, err := m.MarkDone("live-session", Local); err != nil {
		t.Fatal(err)
	}

	st = proposeUntil(t, m, "firmware")
	if _, err := m.Approve(ctx, "firmware", remote); !errors.Is(err, ErrRemoteDisabled) {
		t.Fatalf("remote approve while disabled: %v", err)
	}
	m.SetRemoteApproval(true)
	st, err := m.Approve(ctx, "firmware", remote)
	if err != nil || st.State != Done {
		t.Fatalf("remote approve: %+v %v", st, err)
	}
	if !reflect.DeepEqual(r.calls[len(r.calls)-1], []string{"ls", "/sys/firmware/efi"}) {
		t.Fatalf("ran %v, want the bundle's argv exactly", r.calls[len(r.calls)-1])
	}
	if _, err := m.Approve(ctx, "ipv6-address", remote); !errors.Is(err, ErrNotProposed) {
		t.Fatalf("approving an unproposed step: %v", err)
	}

	st = proposeUntil(t, m, "install")
	n := len(r.calls)
	for _, who := range []Actor{Local, remote} {
		if _, err := m.Approve(ctx, "install", who); !errors.Is(err, ErrDestructive) {
			t.Fatalf("%s approved a destructive step: %v", who, err)
		}
	}
	if len(r.calls) != n {
		t.Fatal("a destructive step reached the runner")
	}
	if _, err := m.MarkDone("install", Local); err != nil {
		t.Fatal(err)
	}
}

func TestFailedStepRetry(t *testing.T) {
	r := &recRunner{exit: 2}
	m := NewMachine(r.run, river(t))
	m.MarkDone("live-session", Local) // not proposed yet: settle allows attesting any info step
	proposeUntil(t, m, "firmware")
	st, _ := m.Approve(context.Background(), "firmware", Local)
	if st.State != Failed || st.Exit != 2 {
		t.Fatalf("%+v", st)
	}
	if _, err := m.Retry("firmware", Actor{Remote: true, Login: "x"}); !errors.Is(err, ErrRemoteForbidden) {
		t.Fatal("remote retry")
	}
	if st, _ = m.Retry("firmware", Local); st.State != Proposed {
		t.Fatalf("%+v", st)
	}
	r.exit = 0
	if st, _ = m.Approve(context.Background(), "firmware", Local); st.State != Done {
		t.Fatalf("%+v", st)
	}
	pd, pt, _, xt := m.Progress()
	// network (failed under exit 2, so not counted), live-session and firmware
	if pd != 2 || pt != len(river(t).Steps) || xt != 0 {
		t.Fatalf("progress %d/%d private %d", pd, pt, xt)
	}
}
