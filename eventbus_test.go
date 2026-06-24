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

	if err := bus.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

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
