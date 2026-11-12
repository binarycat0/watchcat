// Package worker runs background tasks from the SQLite queue.
//
// Producers ("planners") add tasks: the PR planner enqueues a pr.check for
// every PR that is due. The Pool claims ready tasks and runs the Handler
// registered for each task's kind, each in its own goroutine (Go's lightweight
// threads; think asyncio tasks that can block on I/O without await). Handlers
// can enqueue follow-up tasks, which is how a PR check fans out into Slack
// messages now and review/agent hand-offs later.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/binarycat0/watchcat/internal/core"
)

// Handler runs one task. Returning an error retries the task (with backoff)
// until it runs out of attempts.
type Handler interface {
	Handle(ctx context.Context, t core.Task) error
}

// HandlerFunc lets a plain function be a Handler, like http.HandlerFunc.
type HandlerFunc func(ctx context.Context, t core.Task) error

func (f HandlerFunc) Handle(ctx context.Context, t core.Task) error { return f(ctx, t) }

// Planner adds recurring work to the queue on every tick.
type Planner func(ctx context.Context, now time.Time) error

type Pool struct {
	Store       *core.Store
	Handlers    map[string]Handler // task kind -> handler
	Planners    []Planner
	Tick        time.Duration // how often to plan and look for ready tasks
	MaxParallel int
	TaskTimeout time.Duration
	RetryDelay  func(attempt int) time.Duration // nil = Backoff
	Log         *slog.Logger

	once    sync.Once
	poke    chan struct{}
	mu      sync.Mutex // guards running; Go has no GIL, so shared maps need a lock
	running map[int64]core.Task
	wg      sync.WaitGroup
}

func (p *Pool) init() {
	// sync.Once makes this safe to call from several goroutines.
	p.once.Do(func() {
		p.poke = make(chan struct{}, 1)
		p.running = map[int64]core.Task{}
		if p.MaxParallel <= 0 {
			p.MaxParallel = 4
		}
		if p.TaskTimeout <= 0 {
			p.TaskTimeout = 2 * time.Minute
		}
		if p.Log == nil {
			p.Log = slog.Default()
		}
		if p.RetryDelay == nil {
			p.RetryDelay = Backoff
		}
	})
}

// Poke asks the pool to look for work now instead of at the next tick. It
// never blocks: if a poke is already pending, this one is dropped.
func (p *Pool) Poke() {
	p.init()
	select {
	case p.poke <- struct{}{}:
	default:
	}
}

// Running returns the tasks currently executing.
func (p *Pool) Running() []core.Task {
	p.init()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]core.Task, 0, len(p.running))
	for _, t := range p.running {
		out = append(out, t)
	}
	return out
}

// Run blocks until ctx is cancelled, then waits for running tasks.
// context.Context is Go's cancellation signal, passed explicitly down the call
// chain; cancelling it is how the daemon tells everything to stop.
func (p *Pool) Run(ctx context.Context) {
	p.init()
	if n, err := p.Store.RequeueRunning(ctx); err != nil {
		p.Log.Error("requeue interrupted tasks", "err", err)
	} else if n > 0 {
		p.Log.Info("requeued tasks interrupted by the last shutdown", "count", n)
	}
	ticker := time.NewTicker(p.Tick)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		now := time.Now()
		for _, plan := range p.Planners {
			if err := plan(ctx, now); err != nil && ctx.Err() == nil {
				p.Log.Error("planner", "err", err)
			}
		}
		p.dispatch(ctx)
		if now.Sub(lastPrune) > time.Hour {
			p.Store.PruneTasks(ctx, now.Add(-7*24*time.Hour))
			lastPrune = now
		}
		// select waits on several channels at once, like asyncio.wait(FIRST_COMPLETED).
		select {
		case <-ctx.Done():
			p.wg.Wait()
			return
		case <-ticker.C:
		case <-p.poke:
		}
	}
}

// dispatch claims ready tasks until the queue is empty or all slots are busy.
func (p *Pool) dispatch(ctx context.Context) {
	for {
		p.mu.Lock()
		full := len(p.running) >= p.MaxParallel
		p.mu.Unlock()
		if full || ctx.Err() != nil {
			return
		}
		t, ok, err := p.Store.ClaimTask(ctx, time.Now())
		if err != nil {
			if ctx.Err() == nil {
				p.Log.Error("claim task", "err", err)
			}
			return
		}
		if !ok {
			return
		}
		p.mu.Lock()
		p.running[t.ID] = t
		p.mu.Unlock()
		p.wg.Add(1)
		go p.runOne(ctx, t) // "go f()" starts f in a new goroutine
	}
}

// errNoHandler is permanent: retrying won't register a handler.
var errNoHandler = errors.New("no handler for task kind")

func (p *Pool) runOne(ctx context.Context, t core.Task) {
	defer p.wg.Done()
	defer func() {
		p.mu.Lock()
		delete(p.running, t.ID)
		p.mu.Unlock()
		p.Poke() // a slot freed up; pick up anything that was waiting
	}()

	err := p.handle(ctx, t)
	// Use a fresh context so a shutdown mid-task still records the outcome.
	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err == nil {
		if err := p.Store.CompleteTask(wctx, t.ID); err != nil {
			p.Log.Error("complete task", "task", t.ID, "err", err)
		}
		return
	}
	if ctx.Err() != nil {
		// Interrupted by shutdown: leave it "running" so the next start requeues it.
		return
	}
	if errors.Is(err, errNoHandler) {
		t.Attempts = t.MaxAttempts
	}
	p.Log.Warn("task failed", "task", t.ID, "kind", t.Kind, "attempt", t.Attempts, "of", t.MaxAttempts, "err", err)
	if err := p.Store.FailTask(wctx, t, err, time.Now().Add(p.RetryDelay(t.Attempts))); err != nil {
		p.Log.Error("record task failure", "task", t.ID, "err", err)
	}
}

// handle runs the handler with a timeout and turns a panic into an error, so
// one buggy task can't take the daemon down.
func (p *Pool) handle(ctx context.Context, t core.Task) (err error) {
	h, ok := p.Handlers[t.Kind]
	if !ok {
		return fmt.Errorf("%w %q", errNoHandler, t.Kind)
	}
	defer func() {
		if r := recover(); r != nil { // recover() is Go's "except" for panics
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	tctx, cancel := context.WithTimeout(ctx, p.TaskTimeout)
	defer cancel()
	return h.Handle(tctx, t)
}

// Value is a setting read at the moment it's needed, so changes from the
// settings page apply without restarting the daemon. Generic over T, like
// Callable[[], T] in Python.
type Value[T any] func() T

// Fixed wraps a constant as a Value, for tests and settings that never change.
func Fixed[T any](v T) Value[T] { return func() T { return v } }

// Backoff is the delay before retry number n: 30s, 1m, 2m, ... capped at 30m.
func Backoff(attempt int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempt && d < 30*time.Minute; i++ {
		d *= 2
	}
	return min(d, 30*time.Minute)
}
