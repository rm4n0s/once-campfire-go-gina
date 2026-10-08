package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/gina"
)

// start runs r on a gina system of its own, like the front server does.
func start(t *testing.T, r *Runner) {
	t.Helper()
	var spec gina.SystemSpec
	if err := r.Install(&spec); err != nil {
		t.Fatal(err)
	}
	sys, err := gina.NewSystem(spec, gina.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Attach(sys); err != nil {
		t.Fatal(err)
	}
	sys.Start(gina.RunOptions{})
	t.Cleanup(func() { r.Close(time.Second); sys.Stop(); sys.Close() })
}

func TestIndependentQueuesAndShutdown(t *testing.T) {
	// Two job shards: a job that blocks one shard leaves the other free.
	r := New(2, "slow", "fast")
	start(t, r)
	entered := make(chan struct{})
	release := make(chan struct{})
	fast := make(chan struct{})
	r.Enqueue("slow", func(context.Context) error { close(entered); <-release; return nil })
	<-entered
	// The wake-up goes to the shard that is not blocked once the counter has turned.
	for range 2 {
		r.Enqueue("fast", func(context.Context) error {
			select {
			case <-fast:
			default:
				close(fast)
			}
			return nil
		})
	}
	select {
	case <-fast:
	case <-time.After(time.Second):
		t.Fatal("slow job blocked independent queue")
	}
	close(release)
	r.Close(time.Second)
	if r.Enqueue("fast", func(context.Context) error { return nil }) {
		t.Fatal("accepted work after shutdown")
	}
}

func TestJobsRunInOrderOnOneShard(t *testing.T) {
	r := New(1, "work")
	start(t, r)
	var got []int
	for i := range 50 {
		r.Enqueue("work", func(context.Context) error { got = append(got, i); return nil })
	}
	r.Close(5 * time.Second)
	if len(got) != 50 {
		t.Fatalf("ran %d of 50 jobs", len(got))
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("job %d ran at position %d", v, i)
		}
	}
}

func TestPanicAndErrorDoNotStopTheWorker(t *testing.T) {
	r := New(1, "work")
	start(t, r)
	var ran atomic.Int32
	r.Enqueue("work", func(context.Context) error { panic("boom") })
	r.Enqueue("work", func(context.Context) error { return context.Canceled })
	r.Enqueue("work", func(context.Context) error { ran.Add(1); return nil })
	r.Close(5 * time.Second)
	if ran.Load() != 1 {
		t.Fatal("job after a failing one did not run")
	}
}

func TestConcurrentShutdown(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "drain"
		if timeout {
			name = "timeout"
		}
		t.Run(name, func(t *testing.T) {
			r := New(1, "work")
			start(t, r)
			release := make(chan struct{})
			entered := make(chan struct{})
			r.Enqueue("work", func(ctx context.Context) error {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil
			})
			<-entered
			closed := make(chan time.Duration, 2)
			begin := time.Now()
			for range 2 {
				go func() { r.Close(300 * time.Millisecond); closed <- time.Since(begin) }()
			}
			if !timeout {
				time.AfterFunc(50*time.Millisecond, func() { close(release) })
			}
			for range 2 {
				elapsed := <-closed
				if timeout && elapsed < 250*time.Millisecond {
					t.Fatalf("shutdown took %s; want the 300ms timeout", elapsed)
				}
				if !timeout && elapsed > 250*time.Millisecond {
					t.Fatalf("shutdown took %s; want the drain", elapsed)
				}
			}
			if timeout {
				close(release)
			}
			if r.Enqueue("work", func(context.Context) error { return nil }) {
				t.Fatal("accepted work after concurrent shutdown")
			}
		})
	}
}

func TestShutdownDrainsDependentJobs(t *testing.T) {
	r := New(2, "parent", "child")
	start(t, r)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	child := make(chan struct{})
	r.Enqueue("parent", func(context.Context) error {
		close(entered)
		<-release
		if !r.Enqueue("child", func(context.Context) error { close(child); return nil }) {
			t.Error("dependent job dropped")
		}
		return nil
	})
	<-entered
	go func() { r.Close(time.Second); close(done) }()
	close(release)
	<-done
	select {
	case <-child:
	default:
		t.Fatal("dependent job did not finish")
	}
}

func TestWithoutASystemWorkRunsInline(t *testing.T) {
	r := New(1, "work")
	ran := false
	if !r.Enqueue("work", func(context.Context) error { ran = true; return nil }) || !ran {
		t.Fatal("work did not run on the calling thread")
	}
}
