// Package jobs runs the bounded, independent queues used by the Rust port, on gina
// shards of their own: no goroutines.
//
// Install adds `concurrency` job shards to the gina system. Each hosts one worker
// isolate per kind of job. Enqueue puts the work on its kind's queue (shared
// memory, a mutex) and sends one wake-up message to one of the kind's workers,
// which takes the oldest job from the queue and runs it. A job may block (SQLite,
// an outbound HTTP call, ffmpeg): that stalls its shard, which is why job shards
// run nothing else, never an HTTP handler.
//
// A Runner that was never attached to a running system (an in-process test with
// no front server) has no shards to run on, so Enqueue runs the work on the
// calling thread.
package jobs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rm4n0s/gina"
)

const (
	queueCap = 1024 // pending jobs per kind; more are dropped

	typeBase gina.TypeID = 230 // one isolate type per kind: 230, 231, ...
	tagJob   gina.Tag    = gina.TagUserBase
)

type (
	Work   func(context.Context) error
	Runner struct {
		kinds  []string
		index  map[string]int
		shards int

		sys     atomic.Pointer[gina.System]
		workers [][]gina.Handle // [kind][job shard]
		turn    atomic.Uint32   // spreads wake-ups over the job shards

		mu      sync.Mutex
		queues  [][]Work // [kind], oldest first
		pending int      // queued plus running
		closed  bool
		changed chan struct{}
		done    chan struct{}
		ctx     context.Context
		cancel  context.CancelFunc
	}
)

// New makes a runner with one queue per kind, served by `concurrency` job shards.
func New(concurrency int, kinds ...string) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{
		kinds:   kinds,
		index:   make(map[string]int, len(kinds)),
		shards:  max(1, concurrency),
		queues:  make([][]Work, len(kinds)),
		changed: make(chan struct{}, 1),
		done:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	for i, kind := range kinds {
		r.index[kind] = i
	}
	return r
}

// Install appends the job shards to spec. Call it after the shards that must come
// before them (the last shard of the spec is what a later extension takes for its
// service shard) and before gina.NewSystem.
func (r *Runner) Install(spec *gina.SystemSpec) error {
	spec.PoolSlots = max(spec.PoolSlots, 1<<15) // room for the wake-ups queued per shard
	first := len(spec.Shards)
	spec.Shards = append(spec.Shards, make([]gina.ShardSpec, r.shards)...)
	r.workers = make([][]gina.Handle, len(r.kinds))
	for i := range r.kinds {
		id := typeBase + gina.TypeID(i)
		spec.Types = append(spec.Types, gina.RegisterType(id,
			gina.TypeOptions{SlotCount: 1, MailboxCapacity: queueCap + 8}, nil, r.worker(i)))
		for s := range r.shards {
			shard := first + s
			spec.Shards[shard].Boot = append(spec.Shards[shard].Boot,
				gina.SpawnSpec{Type: id, Group: gina.GroupRoot, Restart: gina.RestartPermanent})
			r.workers[i] = append(r.workers[i], gina.MakeHandle(uint8(shard), id, 0, 1))
		}
	}
	return nil
}

// Attach connects the runner to the running system; until then Enqueue runs work
// inline.
func (r *Runner) Attach(sys *gina.System) error {
	r.sys.Store(sys)
	return nil
}

func (r *Runner) worker(kind int) gina.Handler[struct{}] {
	return func(_ *struct{}, _ *gina.Ctx, m *gina.Message) gina.Effect {
		switch m.Tag {
		case tagJob:
			r.runNext(kind)
		case gina.TagShutdown:
			return gina.Done()
		}
		return gina.WaitMessage()
	}
}

// runNext runs the oldest queued job of a kind. Every Enqueue sends one wake-up,
// so there is a job for every wake-up unless the runner was closed with work left.
func (r *Runner) runNext(kind int) {
	r.mu.Lock()
	if len(r.queues[kind]) == 0 {
		r.mu.Unlock()
		return
	}
	work := r.queues[kind][0]
	r.queues[kind][0] = nil
	r.queues[kind] = r.queues[kind][1:]
	abandoned := r.closed
	r.mu.Unlock()
	if !abandoned {
		run(r.ctx, r.kinds[kind], work)
	}
	r.finish()
}

func (r *Runner) finish() {
	r.mu.Lock()
	r.pending--
	r.mu.Unlock()
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func run(ctx context.Context, kind string, work Work) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("background job panic", "kind", kind, "error", p)
		}
	}()
	if err := work(ctx); err != nil {
		slog.Error("background job failed", "kind", kind, "error", err)
	}
}

func (r *Runner) Enqueue(kind string, work Work) bool {
	i, ok := r.index[kind]
	if !ok {
		panic("unregistered job kind: " + kind)
	}
	sys := r.sys.Load()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	if sys == nil {
		r.mu.Unlock()
		run(r.ctx, kind, work)
		return true
	}
	if len(r.queues[i]) >= queueCap {
		r.mu.Unlock()
		slog.Error("background job queue full; job dropped", "kind", kind)
		return false
	}
	r.queues[i] = append(r.queues[i], work)
	r.pending++
	r.mu.Unlock()
	to := r.workers[i][int(r.turn.Add(1))%r.shards]
	if sent := sys.SendExternal(to, tagJob, nil); sent != gina.SendOK {
		// The wake-up never left: take back a job (any, they are interchangeable
		// until a worker has it) so nothing waits for a wake-up that will not come.
		r.mu.Lock()
		if n := len(r.queues[i]); n > 0 {
			r.queues[i][n-1] = nil
			r.queues[i] = r.queues[i][:n-1]
			r.pending--
		}
		r.mu.Unlock()
		slog.Error("background job not delivered; job dropped", "kind", kind, "result", sent)
		return false
	}
	return true
}

// The HTTP server stops accepting requests before Close. Queued work can still
// enqueue dependent work (a banned message's attachment purge, for example).
// changed wakes one drain waiter; done broadcasts shutdown to all closers.
// Close waits for the queued and running jobs, so the system must still be running.
func (r *Runner) Close(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return
		}
		if r.pending == 0 {
			r.closed = true
			close(r.done)
			r.mu.Unlock()
			r.cancel()
			return
		}
		r.mu.Unlock()
		select {
		case <-r.changed:
		case <-r.done:
			return
		case <-timer.C:
			r.mu.Lock()
			if r.closed {
				r.mu.Unlock()
				return
			}
			r.closed = true
			close(r.done)
			r.mu.Unlock()
			r.cancel()
			slog.Warn("background jobs abandoned at shutdown")
			return
		}
	}
}

// DrainTimeout is how long the extension waits for jobs when the front server stops.
const DrainTimeout = 10 * time.Second

// Extension is the Runner as a component of the front server's gina system
// (front.Extension): its job shards are installed with the HTTP servers, and
// stopping the server drains the queues first.
func (r *Runner) Extension() *Extension { return &Extension{r} }

type Extension struct{ r *Runner }

func (e *Extension) Install(spec *gina.SystemSpec) error { return e.r.Install(spec) }
func (e *Extension) Attach(sys *gina.System) error       { return e.r.Attach(sys) }
func (e *Extension) Close()                              { e.r.Close(DrainTimeout) }
