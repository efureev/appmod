// Command hooks demonstrates the advanced hook features of appmod.
//
// It shows how to:
//   - build a module with the functional-options constructor appmod.New;
//   - attach a structured slog logger that reports lifecycle events;
//   - register named, prioritized hooks via AddHook and control their order;
//   - remove a previously registered hook with RemoveHook;
//   - inspect a failure through the typed *appmod.HookError (phase/index/name);
//   - rely on automatic rollback: when a start hook fails, the teardown hooks
//     of the hooks that already ran are executed in reverse and the module
//     ends up in StateFailed.
//
// Run it with:
//
//	go run ./examples/hooks
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/efureev/appmod/v2"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// New builds a module from functional options: configuration, logger and an
	// initial set of named, prioritized hooks.
	mod := appmod.New(
		appmod.WithConfig(appmod.NewConfig("worker", "v2.0.0")),
		appmod.WithModuleLogger(logger),
		// Lower Priority runs first, so "config" (10) runs before "pool" (20).
		appmod.WithHook(appmod.PhaseBeforeStart, appmod.Hook{
			Name:     "pool",
			Priority: 20,
			Run: func(_ context.Context, m appmod.HookModule) error {
				fmt.Printf("  -> opening connection pool for %s\n", m.Name())
				return nil
			},
		}),
		appmod.WithHook(appmod.PhaseBeforeStart, appmod.Hook{
			Name:     "config",
			Priority: 10,
			Run: func(_ context.Context, m appmod.HookModule) error {
				fmt.Printf("  -> loading config for %s\n", m.Name())
				return nil
			},
		}),
	)

	// Hooks can also be added imperatively after construction.
	mod.AddHook(appmod.PhaseBeforeStart, appmod.Hook{
		Name:     "temporary",
		Priority: 30,
		Run: func(_ context.Context, _ appmod.HookModule) error {
			fmt.Println("  -> this hook will be removed before Init")
			return nil
		},
	})
	// ... and removed again by name.
	removed := mod.RemoveHook(appmod.PhaseBeforeStart, "temporary")
	fmt.Println("temporary hook removed:", removed)

	// A matching teardown hook so rollback has something to compensate.
	mod.BeforeDestroy(func(_ context.Context, m appmod.HookModule) error {
		fmt.Printf("  -> closing connection pool for %s\n", m.Name())
		return nil
	})

	fmt.Println("\n== successful init ==")
	if err := mod.Init(context.Background()); err != nil {
		fmt.Println("unexpected init failure:", err)
		return
	}
	fmt.Println("state:", mod.State())
	_ = mod.Destroy(context.Background())

	// Now demonstrate a failing hook and the typed HookError it produces.
	failingHookError()
}

// failingHookError builds a module whose AfterStart hook fails and shows how the
// returned error can be inspected programmatically via *appmod.HookError, and
// how the automatic rollback runs the teardown hook of the already-started
// BeforeStart hook.
func failingHookError() {
	fmt.Println("\n== failing init with rollback ==")

	errBoom := errors.New("downstream unavailable")

	mod := appmod.New(appmod.WithConfig(appmod.NewConfig("flaky", "v0.1.0")))
	mod.BeforeStart(func(_ context.Context, _ appmod.HookModule) error {
		fmt.Println("  -> acquired resource")
		return nil
	})
	mod.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
		return errBoom
	})
	mod.BeforeDestroy(func(_ context.Context, _ appmod.HookModule) error {
		fmt.Println("  -> rollback released the acquired resource")
		return nil
	})

	err := mod.Init(context.Background())
	fmt.Println("init error:", err)
	fmt.Println("final state:", mod.State()) // Failed

	var hookErr *appmod.HookError
	if errors.As(err, &hookErr) {
		fmt.Printf("typed error -> phase=%s index=%d module=%q\n", hookErr.Phase, hookErr.Index, hookErr.Module)
	}
	fmt.Println("errors.Is(err, errBoom):", errors.Is(err, errBoom))
}
