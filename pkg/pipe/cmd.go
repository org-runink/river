package pipe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Command is one stage of a command pipeline: a program, its arguments and, optionally,
// its own environment and working directory. It is a description; [Run] starts it.
type Command struct {
	Name string   // the program, looked up in PATH like the shell would
	Args []string // its arguments, never passed through a shell
	Dir  string   // working directory; empty means the caller's
	// Env adds KEY=VALUE entries on top of the caller's environment. The shell idiom
	// `FOO=1 cmd` is Command{Name: "cmd", Env: []string{"FOO=1"}}.
	Env []string
}

// Cmd is shorthand for Command{Name: name, Args: args}.
func Cmd(name string, args ...string) Command { return Command{Name: name, Args: args} }

// String renders the command the way it would read in a script, for logs and errors.
func (c Command) String() string {
	parts := append([]string{c.Name}, c.Args...)
	for i, p := range parts {
		if p == "" || strings.ContainsAny(p, " \t\n'\"$`\\|&;<>()*?[]#~") {
			parts[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

// StageError is how a failed stage reports itself: which command, where it sat in the
// pipeline, its exit error, and the last lines it wrote to stderr.
type StageError struct {
	Index  int
	Cmd    Command
	Err    error
	Stderr string
}

func (e *StageError) Error() string {
	msg := fmt.Sprintf("stage %d (%s): %v", e.Index+1, e.Cmd, e.Err)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

func (e *StageError) Unwrap() error { return e.Err }

// IO wires a pipeline to the outside world: what the first stage reads, where the last
// stage writes, and where stderr goes in addition to the error's own tail. Nil fields mean
// no stdin, discard stdout, and keep stderr only for errors.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes cmds as one pipeline, cmds[0] | cmds[1] | ..., and waits for every stage.
// It returns nil only when every stage exits 0 (pipefail); otherwise it returns the
// earliest failing stage's *StageError, joined with any later ones.
func Run(ctx context.Context, stdio IO, cmds ...Command) error {
	if len(cmds) == 0 {
		return errors.New("pipe: Run needs at least one command")
	}
	procs := make([]*exec.Cmd, len(cmds))
	tails := make([]*tailBuffer, len(cmds))
	var closers []io.Closer
	closeAll := func() {
		for _, c := range closers {
			_ = c.Close()
		}
		closers = nil
	}

	for i, c := range cmds {
		p := exec.CommandContext(ctx, c.Name, c.Args...)
		p.Dir = c.Dir
		if len(c.Env) > 0 {
			p.Env = append(os.Environ(), c.Env...)
		}
		tails[i] = newTailBuffer(4096)
		if stdio.Stderr != nil {
			p.Stderr = io.MultiWriter(stdio.Stderr, tails[i])
		} else {
			p.Stderr = tails[i]
		}
		procs[i] = p
	}
	procs[0].Stdin = stdio.Stdin
	if stdio.Stdout != nil {
		procs[len(procs)-1].Stdout = stdio.Stdout
	}
	// os.Pipe, not io.Pipe: the kernel carries the bytes between the two processes, so
	// no goroutine has to copy them and a stage that exits early gives the one before
	// it EPIPE, exactly as in the shell.
	for i := 0; i < len(procs)-1; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			closeAll()
			return fmt.Errorf("pipe: %w", err)
		}
		procs[i].Stdout = w
		procs[i+1].Stdin = r
		closers = append(closers, r, w)
	}

	started := 0
	var startErr error
	for i, p := range procs {
		if err := p.Start(); err != nil {
			startErr = &StageError{Index: i, Cmd: cmds[i], Err: err}
			break
		}
		started++
	}
	// The parent's copies of the pipe ends must close, or a reader never sees EOF.
	closeAll()

	errs := make([]error, len(procs))
	var wg sync.WaitGroup
	for i := 0; i < started; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := procs[i].Wait(); err != nil {
				errs[i] = &StageError{Index: i, Cmd: cmds[i], Err: err, Stderr: tails[i].Tail()}
			}
		}(i)
	}
	wg.Wait()
	if startErr != nil {
		return startErr
	}
	return errors.Join(errs...)
}

// Output runs the pipeline and returns what its last stage wrote to stdout, like `$(...)`
// in a script, but without stripping trailing newlines; use [strings.TrimSpace] for that.
func Output(ctx context.Context, stdin io.Reader, cmds ...Command) ([]byte, error) {
	var out bytes.Buffer
	err := Run(ctx, IO{Stdin: stdin, Stdout: &out}, cmds...)
	return out.Bytes(), err
}

// tailBuffer keeps the last max bytes written to it: a failing command's stderr can be
// megabytes, and the error only needs the end of it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) Tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}
