package hubmod_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/efureev/appmod/adapters/hubmod"
	"github.com/efureev/appmod/v4"
	"github.com/efureev/msghub/v3"
)

type UserCreated struct{ ID string }

var Users = msghub.NewTopic[UserCreated]("user.created")

func quiet() msghub.Option {
	return msghub.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func ctxOf(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	return ctx
}

// The adapter must satisfy the appmod contracts it claims, from outside the
// package as a consumer sees it.
func TestModuleIsAnAppModule(t *testing.T) {
	m := hubmod.NewModule(msghub.New(quiet()))

	var _ appmod.AppModule = m
	var _ appmod.ContextAware = m

	if m.Name() != "hub" {
		t.Errorf("Name() = %q, want %q", m.Name(), "hub")
	}
	if m.State() != appmod.StateCreated {
		t.Errorf("State() = %v, want Created", m.State())
	}
}

func TestNewModulePanicsOnNilHub(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewModule(nil) did not panic")
		}
		if err, ok := r.(error); !ok || !errors.Is(err, hubmod.ErrNilHub) {
			t.Fatalf("panicked with %v, want ErrNilHub", r)
		}
	}()

	hubmod.NewModule(nil)
}

// The point of the module: the hub is published for other modules to find, and
// torn down when the application stops.
func TestModuleProvidesAndTearsDown(t *testing.T) {
	ctx := ctxOf(t)

	h := msghub.New(quiet())
	mgr := appmod.NewManager()

	if err := mgr.Register("hub", hubmod.NewModule(h)); err != nil {
		t.Fatal(err)
	}

	var seen atomic.Int32
	consumer := newConsumer(&seen)
	if err := mgr.Register("consumer", consumer, "hub"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// The consumer found the hub through the registry during its start.
	if consumer.hub != h {
		t.Fatal("the consumer did not receive the hub from the registry")
	}

	if err := msghub.Publish(ctx, h, Users, UserCreated{ID: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", seen.Load())
	}

	if err := mgr.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	// The hub is closed, and the contract is gone from the registry.
	if err := msghub.Publish(ctx, h, Users, UserCreated{ID: "2"}); !errors.Is(err, msghub.ErrClosed) {
		t.Errorf("Publish after Stop = %v, want ErrClosed", err)
	}
	if _, err := hubmod.Require(mgr.Registry()); !errors.Is(err, appmod.ErrProviderNotFound) {
		t.Errorf("Require after Stop = %v, want ErrProviderNotFound", err)
	}
}

// Teardown drains before closing, so work already queued still runs.
func TestModuleDrainsBeforeClosing(t *testing.T) {
	ctx := ctxOf(t)

	h := msghub.New(quiet(), msghub.WithQueueSize(64))
	mgr := appmod.NewManager()

	var handled atomic.Int32
	release := make(chan struct{})

	sub, err := msghub.Subscribe(h, Users, func(context.Context, UserCreated) error {
		<-release
		handled.Add(1)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if err := mgr.Register("hub", hubmod.NewModule(h)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	for range 5 {
		if err := msghub.Publish(ctx, h, Users, UserCreated{ID: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	stopped := make(chan error, 1)
	go func() { stopped <- mgr.Stop(ctx) }()

	close(release)

	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if handled.Load() != 5 {
		t.Fatalf("handled %d events, want 5 — the queue was not drained", handled.Load())
	}
}

// WithoutDrain abandons the queue instead of waiting for it.
//
// The handler here honors its context, which is what makes the difference
// observable: Close cancels it, so the invocation already running returns at
// once and teardown does not hang on it. What WithoutDrain skips is the wait for
// the four events still queued behind it — with a drain, Stop would sit there
// until the teardown context expired.
func TestWithoutDrain(t *testing.T) {
	ctx := ctxOf(t)

	h := msghub.New(quiet(), msghub.WithQueueSize(64))
	mgr := appmod.NewManager()

	release := make(chan struct{})
	defer close(release)

	var completed atomic.Int32
	sub, err := msghub.Subscribe(h, Users, func(hctx context.Context, _ UserCreated) error {
		select {
		case <-release:
			completed.Add(1)
		case <-hctx.Done():
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if err := mgr.Register("hub", hubmod.NewModule(h, hubmod.WithoutDrain())); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	for range 5 {
		if err := msghub.Publish(ctx, h, Users, UserCreated{ID: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now()
	if err := mgr.Stop(ctx); err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}

	// The teardown context allows 10s; a module that drained would have used
	// all of it and then failed.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Stop took %v — it waited for the queue despite WithoutDrain", elapsed)
	}
	if completed.Load() != 0 {
		t.Fatalf("%d handlers ran to completion, want 0 — nothing was released", completed.Load())
	}
}

// With the default, teardown waits for the queue and fails when it cannot
// finish inside the teardown context.
func TestDrainReportsTimeout(t *testing.T) {
	h := msghub.New(quiet(), msghub.WithQueueSize(64))
	mgr := appmod.NewManager()

	block := make(chan struct{})
	defer close(block)

	sub, err := msghub.Subscribe(h, Users, func(hctx context.Context, _ UserCreated) error {
		select {
		case <-block:
		case <-hctx.Done():
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if err := mgr.Register("hub", hubmod.NewModule(h)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	for range 5 {
		if err := msghub.Publish(context.Background(), h, Users, UserCreated{ID: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := mgr.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop = %v, want DeadlineExceeded", err)
	}
}

// Two hubs in one application: the registry keys by type, so only one may take
// the `*msghub.Hub` contract.
func TestWithoutRegistry(t *testing.T) {
	ctx := ctxOf(t)

	primary, secondary := msghub.New(quiet()), msghub.New(quiet())
	mgr := appmod.NewManager()

	if err := mgr.Register("hub", hubmod.NewModule(primary)); err != nil {
		t.Fatal(err)
	}

	second := hubmod.NewModule(secondary, hubmod.WithoutRegistry()).
		Configure(appmod.WithConfig(appmod.NewConfig("hub-metrics", "v3")))
	if err := mgr.Register("hub-metrics", second); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start = %v — the second hub claimed the contract", err)
	}
	defer func() { _ = mgr.Stop(ctx) }()

	got, err := hubmod.Require(mgr.Registry())
	if err != nil {
		t.Fatal(err)
	}
	if got != primary {
		t.Error("the registry holds the wrong hub")
	}
	if second.Hub() != secondary {
		t.Error("Hub() returned the wrong hub")
	}
}

// Registering two hubs without WithoutRegistry is a wiring mistake, and it is
// reported rather than silently ignored.
func TestDuplicateProviderIsReported(t *testing.T) {
	ctx := ctxOf(t)

	mgr := appmod.NewManager()

	if err := mgr.Register("hub", hubmod.NewModule(msghub.New(quiet()))); err != nil {
		t.Fatal(err)
	}
	second := hubmod.NewModule(msghub.New(quiet())).
		Configure(appmod.WithConfig(appmod.NewConfig("hub2", "v3")))
	if err := mgr.Register("hub2", second); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Start(ctx); !errors.Is(err, appmod.ErrDuplicateProvider) {
		t.Fatalf("Start = %v, want ErrDuplicateProvider", err)
	}
}

// --------------------------------------------------------------- SubscribeModule

// The reason SubscribeModule exists: a module that restarts must not end up
// subscribed twice.
func TestSubscribeModuleUnsubscribesOnDestroy(t *testing.T) {
	ctx := ctxOf(t)

	h := msghub.New(quiet())
	defer func() { _ = h.Close(ctx) }()

	mgr := appmod.NewManager()
	if err := mgr.Register("hub", hubmod.NewModule(h, hubmod.WithoutDrain())); err != nil {
		t.Fatal(err)
	}

	var seen atomic.Int32
	consumer := newConsumer(&seen)
	if err := mgr.Register("consumer", consumer, "hub"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := msghub.Publish(ctx, h, Users, UserCreated{ID: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 1 {
		t.Fatalf("after start: %d deliveries, want 1", seen.Load())
	}

	// Destroy just the consumer, then bring it back: a leaked subscription
	// would make the next event arrive twice.
	if err := consumer.Destroy(ctx); err != nil {
		t.Fatal(err)
	}

	seen.Store(0)
	if err := msghub.Publish(ctx, h, Users, UserCreated{ID: "2"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 0 {
		t.Fatalf("after destroy: %d deliveries, want 0", seen.Load())
	}

	if err := consumer.Init(ctx); err != nil {
		t.Fatal(err)
	}

	seen.Store(0)
	if err := msghub.Publish(ctx, h, Users, UserCreated{ID: "3"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 1 {
		t.Fatalf("after restart: %d deliveries, want 1", seen.Load())
	}
}

func TestSubscribeModuleArgumentChecks(t *testing.T) {
	h := msghub.New(quiet())
	defer func() { _ = h.Close(context.Background()) }()

	fn := func(context.Context, UserCreated) error { return nil }

	if err := hubmod.SubscribeModule(nil, h, Users, fn); !errors.Is(err, hubmod.ErrNilModule) {
		t.Errorf("nil module = %v, want ErrNilModule", err)
	}

	base := appmod.New()
	if err := hubmod.SubscribeModule(base, nil, Users, fn); !errors.Is(err, hubmod.ErrNilHub) {
		t.Errorf("nil hub = %v, want ErrNilHub", err)
	}

	// No AppContext yet: subscribing from a constructor instead of a start hook.
	if err := hubmod.SubscribeModule(base, h, Users, fn); !errors.Is(err, hubmod.ErrNoAppContext) {
		t.Errorf("no app context = %v, want ErrNoAppContext", err)
	}

	base.SetAppContext(&appmod.AppContext{Registry: appmod.NewRegistry()})
	if err := hubmod.SubscribeModule(base, h, Users, nil); !errors.Is(err, msghub.ErrNilHandler) {
		t.Errorf("nil handler = %v, want msghub.ErrNilHandler", err)
	}
}

func TestProvideAndRequire(t *testing.T) {
	reg := appmod.NewRegistry()
	h := msghub.New(quiet())
	defer func() { _ = h.Close(context.Background()) }()

	if err := hubmod.Provide(reg, nil); !errors.Is(err, hubmod.ErrNilHub) {
		t.Errorf("Provide(nil) = %v, want ErrNilHub", err)
	}
	if _, err := hubmod.Require(reg); !errors.Is(err, appmod.ErrProviderNotFound) {
		t.Errorf("Require before Provide = %v, want ErrProviderNotFound", err)
	}

	if err := hubmod.Provide(reg, h); err != nil {
		t.Fatal(err)
	}

	got, err := hubmod.Require(reg)
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Error("Require returned a different hub")
	}
}

// --------------------------------------------------------------- helper module

// consumerModule takes the hub from the registry and subscribes with a
// lifecycle-scoped subscription — the pattern the package exists to support.
type consumerModule struct {
	appmod.BaseAppModule

	hub  *msghub.Hub
	seen *atomic.Int32
}

func (m *consumerModule) init() {
	m.SetConfig(appmod.NewConfig("consumer", "v1"))

	m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
		h, err := hubmod.Require(m.AppContext().Registry)
		if err != nil {
			return err
		}
		m.hub = h

		return hubmod.SubscribeModule(&m.BaseAppModule, h, Users,
			func(context.Context, UserCreated) error {
				m.seen.Add(1)

				return nil
			})
	})
}

func newConsumer(seen *atomic.Int32) *consumerModule {
	m := &consumerModule{seen: seen}
	m.init()

	return m
}
