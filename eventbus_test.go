package appmod

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type evtA struct{ n int }
type evtB struct{}

func TestEventBusPublishSubscribe(t *testing.T) {
	bus := NewEventBus()

	var got int
	_, err := Subscribe(bus, func(_ context.Context, e evtA) error {
		got = e.n
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() = %v, want nil", err)
	}

	if err := Publish(t.Context(), bus, evtA{n: 42}); err != nil {
		t.Fatalf("Publish() = %v, want nil", err)
	}
	if got != 42 {
		t.Errorf("subscriber got %d, want 42", got)
	}
}

func TestEventBusTypeIsolation(t *testing.T) {
	bus := NewEventBus()

	var aCalls, bCalls int
	_, _ = Subscribe(bus, func(_ context.Context, _ evtA) error { aCalls++; return nil })
	_, _ = Subscribe(bus, func(_ context.Context, _ evtB) error { bCalls++; return nil })

	if err := Publish(t.Context(), bus, evtA{}); err != nil {
		t.Fatalf("Publish(evtA) = %v", err)
	}
	if aCalls != 1 || bCalls != 0 {
		t.Errorf("aCalls=%d bCalls=%d, want 1 and 0", aCalls, bCalls)
	}

	// Publishing a type with no subscribers must be a no-op.
	type evtC struct{}
	if err := Publish(t.Context(), bus, evtC{}); err != nil {
		t.Errorf("Publish(evtC) = %v, want nil", err)
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	bus := NewEventBus()

	var order []int
	for i := range 3 {
		_, _ = Subscribe(bus, func(_ context.Context, _ evtA) error {
			order = append(order, i)
			return nil
		})
	}

	if err := Publish(t.Context(), bus, evtA{}); err != nil {
		t.Fatalf("Publish() = %v", err)
	}
	if len(order) != 3 || order[0] != 0 || order[1] != 1 || order[2] != 2 {
		t.Errorf("delivery order = %v, want [0 1 2]", order)
	}
}

func TestEventBusUnsubscribe(t *testing.T) {
	bus := NewEventBus()

	var calls int
	unsub, _ := Subscribe(bus, func(_ context.Context, _ evtA) error { calls++; return nil })

	_ = Publish(t.Context(), bus, evtA{})
	unsub()
	unsub() // idempotent
	_ = Publish(t.Context(), bus, evtA{})

	if calls != 1 {
		t.Errorf("calls = %d, want 1 (second publish should not reach unsubscribed handler)", calls)
	}
}

func TestEventBusSubscriberError(t *testing.T) {
	bus := NewEventBus()
	sentinel := errors.New("boom")

	_, _ = Subscribe(bus, func(_ context.Context, _ evtA) error { return sentinel })
	var second bool
	_, _ = Subscribe(bus, func(_ context.Context, _ evtA) error { second = true; return nil })

	err := Publish(t.Context(), bus, evtA{})
	if !errors.Is(err, sentinel) {
		t.Errorf("Publish() = %v, want to wrap sentinel", err)
	}
	if !second {
		t.Error("second subscriber should still run after the first one errors")
	}
}

func TestEventBusSubscriberPanic(t *testing.T) {
	bus := NewEventBus()

	_, _ = Subscribe(bus, func(_ context.Context, _ evtA) error { panic("kaboom") })

	err := Publish(t.Context(), bus, evtA{})
	if err == nil {
		t.Fatal("Publish() = nil, want an error from the recovered panic")
	}
}

func TestEventBusClosed(t *testing.T) {
	bus := NewEventBus()
	_, _ = Subscribe(bus, func(_ context.Context, _ evtA) error { return nil })

	bus.Close()

	if _, err := Subscribe(bus, func(_ context.Context, _ evtA) error { return nil }); !errors.Is(err, ErrBusClosed) {
		t.Errorf("Subscribe after Close = %v, want ErrBusClosed", err)
	}
	if err := Publish(t.Context(), bus, evtA{}); !errors.Is(err, ErrBusClosed) {
		t.Errorf("Publish after Close = %v, want ErrBusClosed", err)
	}
}

func TestEventBusNil(t *testing.T) {
	if _, err := Subscribe[evtA](nil, func(_ context.Context, _ evtA) error { return nil }); !errors.Is(err, ErrNilBus) {
		t.Errorf("Subscribe(nil) = %v, want ErrNilBus", err)
	}
	if err := Publish(t.Context(), (*EventBus)(nil), evtA{}); !errors.Is(err, ErrNilBus) {
		t.Errorf("Publish(nil) = %v, want ErrNilBus", err)
	}

	bus := NewEventBus()
	if _, err := Subscribe[evtA](bus, nil); !errors.Is(err, ErrNilSubscriber) {
		t.Errorf("Subscribe(nil fn) = %v, want ErrNilSubscriber", err)
	}
}

func TestEventBusConcurrent(t *testing.T) {
	bus := NewEventBus()

	var count atomic.Int64
	_, _ = Subscribe(bus, func(_ context.Context, _ evtA) error {
		count.Add(1)
		return nil
	})

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = Publish(t.Context(), bus, evtA{})
		}()
		go func() {
			defer wg.Done()
			unsub, _ := Subscribe(bus, func(_ context.Context, _ evtA) error { return nil })
			unsub()
		}()
	}
	wg.Wait()

	if count.Load() == 0 {
		t.Error("expected the persistent subscriber to be invoked at least once")
	}
}

// namedEvent is an interface deliberately implemented by evtA, to cover
// subscriptions keyed by an interface type.
type namedEvent interface{ eventName() string }

func (evtA) eventName() string { return "evtA" }

// TestEventBusPublishThroughInterface covers events that reach Publish through
// an interface variable. reflect reports the static type of the parameter, so
// keying by T alone looked up the interface bucket, delivered to nobody and
// still returned nil — losing the event without a trace.
func TestEventBusPublishThroughInterface(t *testing.T) {
	t.Run("DeliveredByDynamicType", func(t *testing.T) {
		bus := NewEventBus()

		var got atomic.Int32
		if _, err := Subscribe(bus, func(_ context.Context, e evtA) error {
			got.Add(int32(e.n))
			return nil
		}); err != nil {
			t.Fatalf("Subscribe() = %v, want nil", err)
		}

		var ev any = evtA{n: 7}
		if err := Publish(t.Context(), bus, ev); err != nil {
			t.Fatalf("Publish() = %v, want nil", err)
		}
		if got.Load() != 7 {
			t.Errorf("subscriber received %d, want 7 (event published through any was dropped)", got.Load())
		}
	})

	t.Run("InterfaceSubscriberStillWorks", func(t *testing.T) {
		bus := NewEventBus()

		var iface, concrete atomic.Int32
		if _, err := Subscribe(bus, func(_ context.Context, _ namedEvent) error {
			iface.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Subscribe() = %v, want nil", err)
		}
		if _, err := Subscribe(bus, func(_ context.Context, _ evtA) error {
			concrete.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Subscribe() = %v, want nil", err)
		}

		// Published as the interface: both the dynamic-type subscriber and the
		// interface subscriber must fire, each exactly once.
		var ev namedEvent = evtA{n: 1}
		if err := Publish(t.Context(), bus, ev); err != nil {
			t.Fatalf("Publish() = %v, want nil", err)
		}
		if iface.Load() != 1 {
			t.Errorf("interface subscriber called %d time(s), want 1", iface.Load())
		}
		if concrete.Load() != 1 {
			t.Errorf("concrete subscriber called %d time(s), want 1", concrete.Load())
		}
	})

	t.Run("ConcreteTypeUnaffected", func(t *testing.T) {
		bus := NewEventBus()

		var got atomic.Int32
		if _, err := Subscribe(bus, func(_ context.Context, _ evtB) error {
			got.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Subscribe() = %v, want nil", err)
		}
		if err := Publish(t.Context(), bus, evtB{}); err != nil {
			t.Fatalf("Publish() = %v, want nil", err)
		}
		if err := Publish(t.Context(), bus, evtA{n: 1}); err != nil {
			t.Fatalf("Publish() = %v, want nil", err)
		}
		if got.Load() != 1 {
			t.Errorf("subscriber called %d time(s), want 1", got.Load())
		}
	})

	t.Run("NilInterfaceEvent", func(t *testing.T) {
		bus := NewEventBus()

		var got atomic.Int32
		if _, err := Subscribe(bus, func(_ context.Context, _ any) error {
			got.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Subscribe() = %v, want nil", err)
		}

		var ev any
		if err := Publish(t.Context(), bus, ev); err != nil {
			t.Fatalf("Publish(nil) = %v, want nil", err)
		}
		if got.Load() != 1 {
			t.Errorf("any-subscriber called %d time(s), want 1", got.Load())
		}
	})
}

// TestSubscribeModuleUnsubscribesOnDestroy pins the fix for a subscription that
// outlived its module: a plain Subscribe leaves the handler on the bus, so a
// module stopped and started again ends up subscribed twice and every event is
// delivered twice, growing by one delivery per restart.
func TestSubscribeModuleUnsubscribesOnDestroy(t *testing.T) {
	t.Run("SurvivesRestart", func(t *testing.T) {
		mgr := NewManager()

		var hits atomic.Int32
		mod := New(WithConfig(NewConfig("sub", "v1")))
		mod.AfterStart(func(_ context.Context, _ HookModule) error {
			return SubscribeModule(mod, func(_ context.Context, _ evtA) error {
				hits.Add(1)
				return nil
			})
		})

		if err := mgr.Register("sub", mod); err != nil {
			t.Fatalf("Register() = %v", err)
		}
		if err := mgr.Start(t.Context()); err != nil {
			t.Fatalf("Start() = %v, want nil", err)
		}
		if err := mgr.Stop(t.Context()); err != nil {
			t.Fatalf("Stop() = %v, want nil", err)
		}
		if err := mgr.Start(t.Context()); err != nil {
			t.Fatalf("re-Start() = %v, want nil", err)
		}
		t.Cleanup(func() { _ = mgr.Stop(context.WithoutCancel(t.Context())) })

		if err := Publish(t.Context(), mgr.EventBus(), evtA{n: 1}); err != nil {
			t.Fatalf("Publish() = %v, want nil", err)
		}
		if got := hits.Load(); got != 1 {
			t.Errorf("handler invoked %d time(s) after one restart, want exactly 1", got)
		}
	})

	t.Run("GoneAfterDestroy", func(t *testing.T) {
		mgr := NewManager()

		var hits atomic.Int32
		mod := New(WithConfig(NewConfig("sub", "v1")))
		mod.AfterStart(func(_ context.Context, _ HookModule) error {
			return SubscribeModule(mod, func(_ context.Context, _ evtA) error {
				hits.Add(1)
				return nil
			})
		})

		if err := mgr.Register("sub", mod); err != nil {
			t.Fatalf("Register() = %v", err)
		}
		if err := mgr.Start(t.Context()); err != nil {
			t.Fatalf("Start() = %v, want nil", err)
		}
		if err := mgr.Stop(t.Context()); err != nil {
			t.Fatalf("Stop() = %v, want nil", err)
		}

		if err := Publish(t.Context(), mgr.EventBus(), evtA{n: 1}); err != nil {
			t.Fatalf("Publish() = %v, want nil", err)
		}
		if got := hits.Load(); got != 0 {
			t.Errorf("handler invoked %d time(s) after Destroy, want 0", got)
		}
	})

	t.Run("Errors", func(t *testing.T) {
		if err := SubscribeModule(nil, func(_ context.Context, _ evtA) error { return nil }); !errors.Is(err, ErrNilModule) {
			t.Errorf("SubscribeModule(nil) = %v, want %v", err, ErrNilModule)
		}
		// No Manager, so no AppContext was ever injected.
		orphan := New(WithConfig(NewConfig("orphan", "v1")))
		if err := SubscribeModule(orphan, func(_ context.Context, _ evtA) error { return nil }); !errors.Is(err, ErrNoAppContext) {
			t.Errorf("SubscribeModule(no context) = %v, want %v", err, ErrNoAppContext)
		}
	})
}

// TestEventBusPublishContextCanceled covers the early exit: delivery stops at
// the first cancellation check and the context error is reported, so a canceled
// publish is not silently treated as a successful one.
func TestEventBusPublishContextCanceled(t *testing.T) {
	bus := NewEventBus()

	var calls atomic.Int32
	for range 3 {
		if _, err := Subscribe(bus, func(_ context.Context, _ evtA) error {
			calls.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Subscribe() = %v, want nil", err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := Publish(ctx, bus, evtA{n: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("Publish(canceled) = %v, want to wrap %v", err, context.Canceled)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("%d subscriber(s) ran on a canceled context, want 0", got)
	}
}

// TestSubscribeModuleFailures covers the paths where the subscription cannot be
// established, and the rollback path where it must be undone again.
func TestSubscribeModuleFailures(t *testing.T) {
	t.Run("PropagatesSubscribeError", func(t *testing.T) {
		mgr := NewManager()
		mod := New(WithConfig(NewConfig("m", "v1")))
		if err := mgr.Register("m", mod); err != nil {
			t.Fatalf("Register() = %v", err)
		}
		if err := mgr.Start(t.Context()); err != nil {
			t.Fatalf("Start() = %v, want nil", err)
		}
		t.Cleanup(func() { _ = mgr.Stop(context.WithoutCancel(t.Context())) })

		// A nil handler is rejected by Subscribe; SubscribeModule must pass that
		// through rather than registering a cleanup for a subscription that does
		// not exist.
		if err := SubscribeModule[evtA](mod, nil); !errors.Is(err, ErrNilSubscriber) {
			t.Errorf("SubscribeModule(nil handler) = %v, want %v", err, ErrNilSubscriber)
		}

		mgr.EventBus().Close()
		if err := SubscribeModule(mod, func(_ context.Context, _ evtA) error { return nil }); !errors.Is(err, ErrBusClosed) {
			t.Errorf("SubscribeModule(closed bus) = %v, want %v", err, ErrBusClosed)
		}
	})

	t.Run("UnsubscribesOnRollback", func(t *testing.T) {
		mgr := NewManager()

		var hits atomic.Int32
		mod := New(WithConfig(NewConfig("m", "v1")))
		mod.BeforeStart(func(_ context.Context, _ HookModule) error {
			return SubscribeModule(mod, func(_ context.Context, _ evtA) error {
				hits.Add(1)
				return nil
			})
		})
		mod.AfterStart(func(_ context.Context, _ HookModule) error {
			return errors.New("too late")
		})

		if err := mgr.Register("m", mod); err != nil {
			t.Fatalf("Register() = %v", err)
		}
		if err := mgr.Start(t.Context()); err == nil {
			t.Fatal("Start() = nil, want error")
		}
		if got := mod.State(); got != StateFailed {
			t.Errorf("State() = %v, want %v", got, StateFailed)
		}

		if err := Publish(t.Context(), mgr.EventBus(), evtA{n: 1}); err != nil {
			t.Fatalf("Publish() = %v, want nil", err)
		}
		if got := hits.Load(); got != 0 {
			t.Errorf("handler invoked %d time(s) after a rolled-back start, want 0", got)
		}
	})
}
