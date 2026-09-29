package pipe

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"
)

// Pipeline carries one run's shared state: its context, which the first error cancels, and
// that first error. Every stage started from it stops when the context is done, so a
// failing stage never leaves the others blocked on a channel.
type Pipeline struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
	err    error
}

// New starts a data pipeline bound to ctx.
func New(ctx context.Context) *Pipeline {
	ctx, cancel := context.WithCancel(ctx)
	return &Pipeline{ctx: ctx, cancel: cancel}
}

// Context is the pipeline's context: done once any stage fails or the caller cancels.
func (p *Pipeline) Context() context.Context { return p.ctx }

func (p *Pipeline) fail(err error) {
	if err == nil {
		return
	}
	p.once.Do(func() {
		p.err = err
		p.cancel()
	})
}

func (p *Pipeline) goStage(f func() error) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.fail(f())
	}()
}

// Wait blocks until every stage has returned and reports the first error, or the caller's
// cancellation. Call it once, after the pipeline's output has been consumed.
func (p *Pipeline) Wait() error {
	p.wg.Wait()
	defer p.cancel()
	if p.err != nil {
		return p.err
	}
	if err := context.Cause(p.ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// send delivers v unless the pipeline is cancelled first.
func send[T any](ctx context.Context, ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	case <-ctx.Done():
		return false
	}
}

// Source starts a stage that produces values: gen calls emit for each one and returns an
// error to fail the pipeline. emit returns false once the pipeline is cancelled, and gen
// should return then.
func Source[T any](p *Pipeline, gen func(ctx context.Context, emit func(T) bool) error) <-chan T {
	out := make(chan T)
	p.goStage(func() error {
		defer close(out)
		return gen(p.ctx, func(v T) bool { return send(p.ctx, out, v) })
	})
	return out
}

// Slice is a Source over fixed values.
func Slice[T any](p *Pipeline, vs ...T) <-chan T {
	return Source(p, func(ctx context.Context, emit func(T) bool) error {
		for _, v := range vs {
			if !emit(v) {
				return nil
			}
		}
		return nil
	})
}

// Lines is a Source over r's lines, without their line endings: the `while read -r line`
// of a script. Lines longer than 1 MiB fail the pipeline.
func Lines(p *Pipeline, r io.Reader) <-chan string {
	return Source(p, func(ctx context.Context, emit func(string) bool) error {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if !emit(sc.Text()) {
				return nil
			}
		}
		return sc.Err()
	})
}

// Map is a stage that transforms each value in order. An error from f fails the pipeline.
func Map[In, Out any](p *Pipeline, in <-chan In, f func(context.Context, In) (Out, error)) <-chan Out {
	out := make(chan Out)
	p.goStage(func() error {
		defer close(out)
		for v := range in {
			r, err := f(p.ctx, v)
			if err != nil {
				return err
			}
			if !send(p.ctx, out, r) {
				return nil
			}
		}
		return nil
	})
	return out
}

// Filter is a stage that passes on the values keep accepts, in order: a script's grep.
func Filter[T any](p *Pipeline, in <-chan T, keep func(T) bool) <-chan T {
	out := make(chan T)
	p.goStage(func() error {
		defer close(out)
		for v := range in {
			if keep(v) && !send(p.ctx, out, v) {
				return nil
			}
		}
		return nil
	})
	return out
}

// ParallelMap runs f on up to workers values at once: the `xargs -P` of a script. Output
// order is not input order; follow it with a sort, or use Map, when order matters.
func ParallelMap[In, Out any](p *Pipeline, in <-chan In, workers int, f func(context.Context, In) (Out, error)) <-chan Out {
	if workers < 1 {
		workers = 1
	}
	out := make(chan Out)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		p.goStage(func() error {
			defer wg.Done()
			for v := range in {
				r, err := f(p.ctx, v)
				if err != nil {
					return err
				}
				if !send(p.ctx, out, r) {
					return nil
				}
			}
			return nil
		})
	}
	go func() { wg.Wait(); close(out) }()
	return out
}

// Collect reads every value, waits for the pipeline, and returns the values and the
// pipeline's error.
func Collect[T any](p *Pipeline, in <-chan T) ([]T, error) {
	var vs []T
	for v := range in {
		vs = append(vs, v)
	}
	return vs, p.Wait()
}

// Drain runs sink on every value, then waits for the pipeline. An error from sink fails
// the pipeline and is returned.
func Drain[T any](p *Pipeline, in <-chan T, sink func(T) error) error {
	for v := range in {
		if err := sink(v); err != nil {
			p.fail(err)
			for range in { // let the upstream stages see the cancellation and exit
			}
			break
		}
	}
	return p.Wait()
}
