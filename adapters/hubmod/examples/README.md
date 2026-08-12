# hubmod examples

Runnable programs that show an application built on the adapter behaving over time — orders arriving, handlers reacting,
the bus being torn down last. Each is a self-contained `main`
package; run it from the adapter directory (`adapters/hubmod`), not from the repository root:
this is a separate Go module.

They complement the `Example` function in [`example_test.go`](../example_test.go): that one shows the **shape of the
API** in a handful of lines, which is what belongs on pkg.go.dev. These show what the wiring buys you.

| Example              | What it demonstrates                                                                                                                                                                                                                                                                                                                                                                        |
|----------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [`orders`](./orders) | The whole adapter in one application: `NewModule` owning the bus, `Require` taking it from the registry in a start hook, `SubscribeModule` scoping subscriptions to a module, msghub's synchronous and queued delivery side by side on one topic, a mid-run module restart that is *not* subscribed twice, and a teardown that drains and closes the hub after every publisher has stopped. |

## Running

```sh
cd adapters/hubmod
go run ./examples/orders
```

## Notes

**The output is deterministic.** Every run prints exactly the same bytes, and that is not an accident of timing. Two
things arrange it:

- `Manager` starts and stops the modules of one dependency layer in parallel goroutines, so
  `inventory` and `mailer` subscribe — and later shut down — in an unpredictable order. Only one of them prints per
  event inline (`inventory`, which is `msghub.Synchronous()`), and only one prints at teardown (`mailer`).
- The queued subscriber runs on its own goroutine, so the driver calls `bus.Drain(ctx)` after each order and lets it
  catch up before the next one is placed.

If an example ever prints its lines in a different order on two runs, that is a bug in the example or in the package,
not noise to be ignored.

**No logger is wired.** `appmod.NewManager` defaults to a discard logger, so nothing but the example's own `fmt` output
reaches stdout — no timestamps, no durations.
