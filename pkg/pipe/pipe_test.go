package pipe

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRunJoinsStages(t *testing.T) {
	out, err := Output(context.Background(), strings.NewReader("b\na\nc\na\n"),
		Cmd("sort"), Cmd("uniq"), Cmd("tr", "a-z", "A-Z"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "A\nB\nC\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunIsPipefail(t *testing.T) {
	// The middle stage fails but the last one succeeds: a plain shell pipeline would call
	// this a success; pipefail and Run must not.
	err := Run(context.Background(), IO{},
		Cmd("printf", "x\n"), Command{Name: "sh", Args: []string{"-c", "cat >/dev/null; echo boom >&2; exit 3"}}, Cmd("cat"))
	var se *StageError
	if !errors.As(err, &se) {
		t.Fatalf("want a *StageError, got %v", err)
	}
	if se.Index != 1 || !strings.Contains(se.Stderr, "boom") {
		t.Fatalf("wrong stage or stderr: %+v", se)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("exit code not carried: %v", err)
	}
}

func TestRunMissingProgram(t *testing.T) {
	err := Run(context.Background(), IO{}, Cmd("printf", "x"), Cmd("definitely-not-a-program-river"))
	var se *StageError
	if !errors.As(err, &se) || se.Index != 1 {
		t.Fatalf("want stage 2 start error, got %v", err)
	}
}

func TestRunHonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Run(ctx, IO{}, Cmd("sleep", "10"), Cmd("cat"))
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("cancelled pipeline: err=%v after %v", err, time.Since(start))
	}
}

func TestEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	out, err := Output(context.Background(), nil, Command{Name: "sh", Args: []string{"-c", `printf "%s %s" "$RIVER_T" "$(pwd)"`}, Env: []string{"RIVER_T=ok"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if want := "ok " + dir; string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestCommandString(t *testing.T) {
	if got, want := Cmd("echo", "a b", "it's").String(), `echo 'a b' 'it'\''s'`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestDataPipeline(t *testing.T) {
	p := New(context.Background())
	nums := Lines(p, strings.NewReader("1\n2\n3\n4\n5\n6\n"))
	even := Filter(p, nums, func(s string) bool { return s[0]%2 == 0 })
	sq := Map(p, even, func(_ context.Context, s string) (string, error) { return s + s, nil })
	got, err := Collect(p, sq)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "22,44,66" {
		t.Fatalf("got %v", got)
	}
}

func TestParallelMapAndErrorCancels(t *testing.T) {
	p := New(context.Background())
	src := Slice(p, 1, 2, 3, 4, 5, 6, 7, 8)
	out := ParallelMap(p, src, 4, func(_ context.Context, v int) (int, error) { return v * 10, nil })
	got, err := Collect(p, out)
	if err != nil {
		t.Fatal(err)
	}
	sort.Ints(got)
	if fmt.Sprint(got) != "[10 20 30 40 50 60 70 80]" {
		t.Fatalf("got %v", got)
	}

	// A failing stage must stop an endless source rather than hang it.
	p = New(context.Background())
	endless := Source(p, func(ctx context.Context, emit func(int) bool) error {
		for i := 0; ; i++ {
			if !emit(i) {
				return nil
			}
		}
	})
	boom := errors.New("boom")
	failing := Map(p, endless, func(_ context.Context, v int) (int, error) {
		if v == 5 {
			return 0, boom
		}
		return v, nil
	})
	done := make(chan error, 1)
	go func() { _, err := Collect(p, failing); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("want boom, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline did not stop after a stage failed")
	}
}

func TestDrainSinkError(t *testing.T) {
	p := New(context.Background())
	src := Slice(p, "a", "b", "c")
	stop := errors.New("stop")
	err := Drain(p, src, func(s string) error {
		if s == "b" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("want stop, got %v", err)
	}
}
