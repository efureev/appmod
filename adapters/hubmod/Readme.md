# hubmod

`github.com/efureev/appmod/adapters/hubmod`

Ties a [msghub](https://github.com/efureev/msghub) event bus to the
[appmod](../..) module lifecycle.

This is a **separate Go module**. Importing `appmod` does not pull `msghub` in, and
importing `msghub` does not pull `appmod` in; only a program that wants both depends on
both. That is why the directory has its own `go.mod` and its own release tags
(`adapters/hubmod/vX.Y.Z`), independent of the major version of the module above it.

## Why it exists

`appmod` ships no event bus. Answering a request and broadcasting a fact are different
mechanisms with different failure modes — one returns an error to the caller, the other queues
and may drop — so `appmod` keeps a single extension point, the `Registry`, and lets an
application publish a bus through it like any other capability.

That leaves three pieces of wiring, and they are what this package provides.

| | |
|---|---|
| `NewModule` | Owns the hub and gives it a lifecycle: published to the registry on start, drained and closed on teardown, in dependency order. |
| `SubscribeModule` | Scopes a subscription to a module, so a module that restarts is not subscribed twice. |
| `Provide` / `Require` | Publish and take the hub through the shared `appmod.Registry`. |

## Install

```bash
go get github.com/efureev/appmod/adapters/hubmod
```

## Usage

Register the bus as a module, and depend on it from everything that uses it — the ordering
then follows from the dependency graph:

```go
bus := msghub.New()
mgr := appmod.NewManager()

must(mgr.Register("bus", hubmod.NewModule(bus)))
must(mgr.Register("cache", newCache(), "bus"))
must(mgr.Register("api", newAPI(), "bus", "cache"))

must(mgr.Start(ctx))
```

A module takes the hub from the registry in a start hook and subscribes:

```go
m.AfterStart(func(_ context.Context, _ appmod.HookModule) error {
	bus, err := hubmod.Require(m.AppContext().Registry)
	if err != nil {
		return err
	}

	return hubmod.SubscribeModule(&m.BaseAppModule, bus, UserCreated,
		func(_ context.Context, ev User) error {
			// react
			return nil
		})
})
```

Prefer `SubscribeModule` to calling `msghub.Subscribe` directly. `Subscribe` hands back a handle
you must store and remember to close; when that does not happen the handler outlives the
module, so a module stopped and started again is subscribed twice and every event is delivered
twice — growing by one delivery per restart, while the stale handler keeps a live reference to
the destroyed module.

A runnable version of the whole wiring is in [`example_test.go`](example_test.go).

## Options

| Option | Effect |
|---|---|
| `WithoutDrain()` | Teardown abandons whatever is still queued instead of waiting for it. Use it for streams where a late delivery is worth less than a prompt shutdown. |
| `WithoutRegistry()` | The module does not publish the hub through the registry. Needed for a second hub: the registry keys by type, so only one may hold the `*msghub.Hub` contract. |

`Configure` applies ordinary `appmod.Option`s to the module — renaming it, for instance, which
an application running more than one hub will want:

```go
metrics := hubmod.NewModule(metricsBus, hubmod.WithoutRegistry()).
	Configure(appmod.WithConfig(appmod.NewConfig("hub-metrics", "v3")))
```

## Teardown

By default the module drains the hub and then closes it, from an `AfterDestroy` hook. That
ordering is deliberate: `appmod` runs BeforeDestroy hooks, then cleanups, then AfterDestroy, and
a failing BeforeDestroy puts the module back into `Running` with its cleanups unrun. Shutting
the hub down last means the registry entry is already revoked by then, and a drain that times
out is reported from a module that did stop rather than leaving a half-stopped one behind.

Both the drain and the close are bounded by the teardown context, so `Manager.WithShutdownTimeout`
governs how long a slow handler may hold up the shutdown. Handlers should honor the context
they are given: `Close` cancels it, and a handler that ignores it delays the teardown until the
timeout.

## Development

`msghub` is pinned by version and resolves from the proxy like any other dependency. `appmod`
is wired to the sources next to it:

```
replace github.com/efureev/appmod/v4 => ../..
```

That replace is committed on purpose, and not only because `appmod v4.0.0` is not tagged yet.
An adapter should be tested against the `appmod` it ships beside — otherwise a change in the
package above can break it while CI, building against an older release, stays green. A
`replace` in a module's own `go.mod` is ignored when that module is a dependency, so nothing
about it reaches anyone importing the adapter.

```bash
go build ./... && go vet ./... && go test ./... -race
```

The root module's `go test ./...` does not reach this directory: a nested module is invisible
to the parent's package patterns. CI runs it as a separate `adapters` job.
