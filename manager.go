package appmod

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	shutdown "github.com/efureev/go-shutdown/v3"
)

// HealthChecker is an optional capability of a module. Modules registered in a
// [Manager] that implement it are probed by [Manager.Health].
type HealthChecker interface {
	// HealthCheck reports whether the module is healthy. A non-nil error means
	// the module is unhealthy.
	HealthCheck(ctx context.Context) error
}

// node is a registered module together with its dependencies. The name is the
// map key, so it is not repeated here.
type node struct {
	module AppModule
	deps   []string
}

// managerState is the lifecycle state of a [Manager]. It exists to make Start
// non-reentrant; module lifecycle is tracked separately by [State].
type managerState int32

const (
	// managerCreated is a manager that has never been started.
	managerCreated managerState = iota
	// managerStarting means Start is currently bringing modules up.
	managerStarting
	// managerRunning means Start completed successfully.
	managerRunning
	// managerStopped means Stop has run (or Start rolled itself back).
	managerStopped
)

// healthProbe pairs a module name with its [HealthChecker] view, so [Manager.Health]
// can collect everything it needs under the mutex and probe without holding it.
type healthProbe struct {
	name    string
	checker HealthChecker
}

// Manager orchestrates a set of named [AppModule]s connected by dependencies.
//
// Modules are registered with [Manager.Register] together with the names of the
// modules they depend on. [Manager.Start] initializes them in dependency
// (topological) order and [Manager.Stop] tears them down in the reverse one;
// both run the modules of a layer concurrently. A dependency cycle is reported
// as [ErrDependencyCycle].
//
// A Manager is safe for concurrent use by multiple goroutines.
type Manager struct {
	mu    sync.Mutex
	nodes map[string]*node

	// started holds the names of successfully started modules in start
	// completion order; Stop tears them down in reverse.
	started []string

	// state guards against a re-entrant Start; see [ErrAlreadyStarted].
	state managerState

	logger          *slog.Logger
	shutdownTimeout time.Duration
	signals         []os.Signal
	force           bool

	// sh owns the signal handling and the shutdown broadcast. It is created in
	// NewManager rather than in Run because AppContext hands its observation side
	// to every module at injection time, which happens before Run.
	sh *shutdown.Shutdown
	// ran guards the single-use shutdown sequence; see [ErrAlreadyRun].
	ran bool

	// appCtx is the shared context (Registry + Logger) injected into
	// every ContextAware module before Start.
	appCtx *AppContext
}

// ManagerOption configures a [Manager] created with [NewManager].
type ManagerOption func(*Manager)

// WithLogger sets the structured logger used to report lifecycle events. By
// default a no-op logger is used.
func WithLogger(logger *slog.Logger) ManagerOption {
	return func(m *Manager) { m.logger = logger }
}

// WithShutdownTimeout sets the maximum duration allowed for [Manager.Run] to
// stop all modules after a shutdown signal. A non-positive value means no
// timeout.
func WithShutdownTimeout(d time.Duration) ManagerOption {
	return func(m *Manager) { m.shutdownTimeout = d }
}

// WithSignals replaces the OS signals [Manager.Run] reacts to. The default is
// SIGINT, SIGTERM and SIGQUIT. Passing none keeps the default.
func WithSignals(sigs ...os.Signal) ManagerOption {
	return func(m *Manager) { m.signals = sigs }
}

// WithForceOnSecondSignal controls what happens when a watched signal arrives
// while the modules are still being torn down.
//
// When enabled — the default — the process terminates immediately with exit code
// 128+signum. That is what the OS would have done had the process not handled
// signals at all, and it is the operator's only way out of a teardown that hung:
// the signal handler is installed, so a second Ctrl-C would otherwise go to a
// channel nobody reads.
//
// Disable it only when the teardown must never be interrupted, and note that
// this leaves SIGKILL as the only way to stop a module that ignores its context.
func WithForceOnSecondSignal(v bool) ManagerOption {
	return func(m *Manager) { m.force = v }
}

// NewManager creates a [Manager] configured with the given options.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{nodes: make(map[string]*node), force: true}
	for _, opt := range opts {
		opt(m)
	}
	if m.logger == nil {
		m.logger = discardLogger
	}

	m.sh = shutdown.New(
		shutdown.WithLogger(m.logger),
		shutdown.WithTimeout(m.shutdownTimeout),
		shutdown.WithSignals(m.signals...),
		shutdown.WithForceOnSecondSignal(m.force),
	)

	m.appCtx = &AppContext{
		Registry: NewRegistry(),
		Logger:   m.logger,
		shutdown: m.sh.Context(),
	}

	return m
}

// Registry returns the shared [Registry] injected into the manager's modules.
func (m *Manager) Registry() *Registry { return m.appCtx.Registry }

// Register adds a module under the given name, declaring the names of the
// modules it depends on. Dependencies may be registered before or after the
// dependent module; they are validated when [Manager.Start] is called.
//
// It returns [ErrEmptyName] for an empty name, [ErrNilModule] for a nil module
// and [ErrDuplicateModule] if the name is already taken.
func (m *Manager) Register(name string, module AppModule, deps ...string) error {
	if name == "" {
		return ErrEmptyName
	}
	if module == nil {
		return ErrNilModule
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.nodes[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateModule, name)
	}
	m.nodes[name] = &node{module: module, deps: slices.Clone(deps)}

	return nil
}

// Start initializes every registered module in dependency order.
//
// Independent modules within the same dependency layer are started
// concurrently. If any module fails to start (or the context is canceled),
// Start rolls back through [Manager.Stop] — reverse layer order, concurrent
// within a layer — and returns the cause joined with any teardown error via
// [errors.Join].
//
// Start is not re-entrant: calling it on a manager that is already starting or
// running returns [ErrAlreadyStarted] without touching the running modules. The
// guard is what makes a stray or concurrent second Start harmless — without it
// the second call would fail on the already-initialized modules, treat that as a
// startup failure and roll back, stopping the modules the first Start had
// successfully brought up.
//
// After [Manager.Stop] the manager can be started again.
func (m *Manager) Start(ctx context.Context) error {
	if err := m.beginStart(); err != nil {
		return err
	}

	layers, err := m.plan()
	if err != nil {
		m.setState(managerStopped)
		return err
	}

	m.injectContext()

	for _, layer := range layers {
		if err := ctx.Err(); err != nil {
			return m.abort(ctx, err)
		}
		if err := m.startLayer(ctx, layer); err != nil {
			return m.abort(ctx, err)
		}
	}

	m.setState(managerRunning)

	return nil
}

// beginStart claims the manager for a start attempt, or reports why it cannot be
// claimed. It also refuses to start while modules from a previous run are still
// up (a teardown aborted by its context leaves them in m.started).
func (m *Manager) beginStart() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.state {
	case managerCreated, managerStopped:
	default:
		return ErrAlreadyStarted
	}
	if len(m.started) > 0 {
		return fmt.Errorf("%w: %d module(s) from a previous run are still started", ErrAlreadyStarted, len(m.started))
	}
	m.state = managerStarting

	return nil
}

// setState atomically updates the manager lifecycle state.
func (m *Manager) setState(s managerState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state = s
}

// injectContext hands the shared [AppContext] to every registered module that
// implements [ContextAware], so modules can reach the shared [Registry] and
// logger before they are started.
func (m *Manager) injectContext() {
	m.mu.Lock()
	modules := make([]AppModule, 0, len(m.nodes))
	for _, n := range m.nodes {
		modules = append(modules, n.module)
	}
	m.mu.Unlock()

	for _, mod := range modules {
		if ca, ok := mod.(ContextAware); ok {
			ca.SetAppContext(m.appCtx)
		}
	}
}

// startLayer initializes all modules in a layer concurrently and joins their
// errors. Successfully started modules are appended to m.started.
func (m *Manager) startLayer(ctx context.Context, layer []string) error {
	var (
		wg   sync.WaitGroup
		emu  sync.Mutex
		errs []error
	)

	for _, name := range layer {
		m.mu.Lock()
		n := m.nodes[name]
		m.mu.Unlock()

		wg.Add(1)
		go func() {
			defer wg.Done()

			m.logger.InfoContext(ctx, "starting module", "module", name)
			if err := n.module.Init(ctx); err != nil {
				m.logger.ErrorContext(ctx, "module failed to start", "module", name, "error", err)
				emu.Lock()
				errs = append(errs, fmt.Errorf("appmod: module %q failed to start: %w", name, err))
				emu.Unlock()

				return
			}

			m.mu.Lock()
			m.started = append(m.started, name)
			m.mu.Unlock()
		}()
	}

	wg.Wait()

	return errors.Join(errs...)
}

// abort stops everything that started so far (using a non-cancelable context so
// that cleanup is not skipped) and returns the original cause, joined with any
// teardown error.
func (m *Manager) abort(ctx context.Context, cause error) error {
	if err := m.Stop(context.WithoutCancel(ctx)); err != nil {
		return errors.Join(cause, err)
	}

	return cause
}

// Stop tears down all started modules in the reverse of their start order. Every
// module is attempted even if an earlier one fails; the errors are joined.
//
// Stop honors context cancellation between modules: if ctx is canceled (for
// example, when [Manager.Run] hits its shutdown timeout), Stop aborts before
// the next module instead of spinning forever, so it does not keep running in a
// detached goroutine. The names of the modules that were not torn down are
// retained so a subsequent Stop can resume the teardown; until that teardown
// completes, [Manager.Start] refuses to run with [ErrAlreadyStarted].
//
// Teardown mirrors startup: modules are grouped into the same dependency layers
// [Manager.Start] uses, walked in reverse, and the modules within a layer are
// stopped concurrently. A serial teardown made shutdown cost the sum of every
// module's teardown instead of the maximum per layer, which is what blows a
// [WithShutdownTimeout] budget on an application whose modules are mostly
// independent.
func (m *Manager) Stop(ctx context.Context) error {
	// Tell the observers before touching anything: a module watching
	// AppContext().Done() must learn that the application is going down whether
	// it was Run that decided so or a direct Stop — including the Stop that rolls
	// back a failed Start.
	m.sh.End()

	m.mu.Lock()
	started := slices.Clone(m.started)
	m.state = managerStopped
	m.mu.Unlock()

	if len(started) == 0 {
		return nil
	}

	layers := m.stopLayers(started)

	var errs []error
	for _, layer := range layers {
		if err := ctx.Err(); err != nil {
			// Context canceled (e.g. shutdown timeout): stop iterating so the
			// teardown does not leak as a detached goroutine. Whatever has not
			// been torn down is still in m.started, so a later Stop resumes there.
			m.logger.ErrorContext(ctx, "teardown aborted by context",
				"error", err, "remaining", len(m.startedNames()))
			errs = append(errs, fmt.Errorf("appmod: teardown aborted: %w", err))

			return errors.Join(errs...)
		}

		errs = append(errs, m.stopLayer(ctx, layer)...)
	}

	return errors.Join(errs...)
}

// stopLayers groups started into teardown layers: the dependency layers used by
// [Manager.Start], reversed, so that dependents are torn down before the modules
// they depend on and everything within a layer is independent.
//
// If the graph is no longer plannable — a module registered after Start can
// introduce a cycle or an unknown dependency — it falls back to the reverse of
// the start order with one module per layer, which is always safe.
func (m *Manager) stopLayers(started []string) [][]string {
	reverseSerial := func() [][]string {
		out := make([][]string, 0, len(started))
		for i := len(started) - 1; i >= 0; i-- {
			out = append(out, []string{started[i]})
		}

		return out
	}

	layers, err := m.plan()
	if err != nil {
		return reverseSerial()
	}

	pending := make(map[string]struct{}, len(started))
	for _, name := range started {
		pending[name] = struct{}{}
	}

	out := make([][]string, 0, len(layers))
	for i := len(layers) - 1; i >= 0; i-- {
		var layer []string
		for _, name := range layers[i] {
			if _, ok := pending[name]; ok {
				layer = append(layer, name)
				delete(pending, name)
			}
		}
		if len(layer) > 0 {
			out = append(out, layer)
		}
	}

	// Defensive: anything started but missing from the plan is torn down last,
	// one at a time, so no started module is silently left running.
	for i := len(started) - 1; i >= 0; i-- {
		if _, ok := pending[started[i]]; ok {
			out = append(out, []string{started[i]})
		}
	}

	return out
}

// stopLayer tears down every module of a layer concurrently and returns their
// errors in layer order, so the joined result does not depend on scheduling.
func (m *Manager) stopLayer(ctx context.Context, layer []string) []error {
	var wg sync.WaitGroup

	errs := make([]error, len(layer))
	for i, name := range layer {
		m.mu.Lock()
		n := m.nodes[name]
		m.mu.Unlock()

		// Defensive: a started module always has a node, since Register only ever
		// adds. Kept so that adding an Unregister later cannot turn this into a
		// nil dereference.
		if n == nil {
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			m.logger.InfoContext(ctx, "stopping module", "module", name)
			if err := n.module.Destroy(ctx); err != nil {
				m.logger.ErrorContext(ctx, "module failed to stop", "module", name, "error", err)
				errs[i] = fmt.Errorf("appmod: module %q failed to stop: %w", name, err)
			}
			// Drop it whether or not Destroy succeeded: either way this Stop is
			// done with it, and a module left in the set would be retried forever.
			m.forget(name)
		}()
	}
	wg.Wait()

	return slices.DeleteFunc(errs, func(err error) bool { return err == nil })
}

// forget drops a module from the started set once this Stop is done with it.
//
// Removing them one by one, rather than clearing the set upfront and putting the
// leftovers back on abort, keeps m.started truthful at every instant: a teardown
// abandoned mid-flight — which is what a shutdown timeout does — leaves exactly
// the modules that are still up, and both the resumed Stop and the timeout
// diagnostic read it directly.
func (m *Manager) forget(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.started = slices.DeleteFunc(m.started, func(n string) bool { return n == name })
}

// Run starts all modules, then blocks until the context is canceled, one of the
// watched signals arrives (see [WithSignals]) or [Manager.Shutdown] is called,
// after which it gracefully stops every module.
//
// The signal handling and the bounded teardown are delegated to
// github.com/efureev/go-shutdown. When the teardown does not finish within
// [WithShutdownTimeout], Run returns an error wrapping [ErrShutdownTimeout],
// annotated with the modules that were still running.
//
// Run is single use: the underlying shutdown sequence runs exactly once, so a
// second call returns [ErrAlreadyRun] rather than silently returning the first
// result without stopping anything. A manager driven through [Manager.Start] and
// [Manager.Stop] can still be restarted.
func (m *Manager) Run(ctx context.Context) error {
	if err := m.beginRun(); err != nil {
		return err
	}

	if err := m.Start(ctx); err != nil {
		return err
	}

	// One hook, not one per module: go-shutdown could drive the layered teardown
	// itself (LIFO with Parallel groups), but that would make Run and Stop two
	// separate teardown implementations, free to drift apart. Stop stays the only
	// one.
	m.sh.Add("modules", m.Stop)

	return m.describeTimeout(m.sh.Wait(ctx))
}

// beginRun claims the single-use shutdown sequence for this call.
func (m *Manager) beginRun() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ran {
		return ErrAlreadyRun
	}
	m.ran = true

	return nil
}

// describeTimeout names the modules that were still running when the shutdown
// budget ran out. go-shutdown can only report the hook it was given — "modules"
// — which says nothing useful; Stop puts whatever it did not tear down back into
// m.started, so the detail is available here.
func (m *Manager) describeTimeout(err error) error {
	if err == nil || !errors.Is(err, ErrShutdownTimeout) {
		return err
	}

	m.mu.Lock()
	remaining := slices.Clone(m.started)
	m.mu.Unlock()

	if len(remaining) == 0 {
		return err
	}

	return fmt.Errorf("%w (modules still running: %s)", err, strings.Join(remaining, ", "))
}

// Shutdown triggers the shutdown sequence a [Manager.Run] is waiting on, as if a
// signal had arrived. It is non-blocking and safe to call more than once, from
// any goroutine.
func (m *Manager) Shutdown() { m.sh.End() }

// ExitCode reports the process exit code matching how the shutdown was
// triggered: 128+signum when a signal did it — what a shell reports for a
// process killed by that signal — and 0 otherwise.
//
// Cleanup errors are deliberately not taken into account; whether a failed
// teardown should change the exit status is the application's decision.
//
//	if err := mgr.Run(ctx); err != nil {
//		logger.Error("shutdown failed", "error", err)
//	}
//	os.Exit(mgr.ExitCode())
func (m *Manager) ExitCode() int { return m.sh.ExitCode() }

// Health probes every started module that implements [HealthChecker] and joins
// the errors of the unhealthy ones. It returns nil when all probed modules are
// healthy (or none implement [HealthChecker]).
// The probes are collected under the mutex and executed without it: a
// HealthCheck is user code and may block, so it must not be run while holding
// the lock. Note that it is the probes that are copied out, not the node map —
// reading m.nodes after unlocking would race with a concurrent
// [Manager.Register] and crash the process with "concurrent map read and map
// write", which no recover can catch.
func (m *Manager) Health(ctx context.Context) error {
	m.mu.Lock()
	probes := make([]healthProbe, 0, len(m.started))
	for _, name := range m.started {
		n, ok := m.nodes[name]
		if !ok {
			// Defensive, as in stopLayer: unreachable while Register is the only
			// way to populate the map.
			continue
		}
		if hc, ok := n.module.(HealthChecker); ok {
			probes = append(probes, healthProbe{name: name, checker: hc})
		}
	}
	m.mu.Unlock()

	var errs []error
	for _, p := range probes {
		if err := p.checker.HealthCheck(ctx); err != nil {
			errs = append(errs, fmt.Errorf("appmod: module %q is unhealthy: %w", p.name, err))
		}
	}

	return errors.Join(errs...)
}

// startedNames returns the names of the modules currently considered started.
func (m *Manager) startedNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.started)
}

// Modules returns the names of all registered modules, sorted lexicographically.
func (m *Manager) Modules() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Sorted(maps.Keys(m.nodes))
}

// plan validates the dependency graph and returns the modules grouped into
// dependency layers: every module in layer i depends only on modules in layers
// < i, so modules within a layer can be started concurrently.
//
// It returns [ErrUnknownDependency] if a module depends on an unregistered name
// and [ErrDependencyCycle] if the graph contains a cycle.
func (m *Manager) plan() ([][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateDeps(); err != nil {
		return nil, err
	}

	indeg := make(map[string]int, len(m.nodes))
	dependents := make(map[string][]string, len(m.nodes))
	for name, n := range m.nodes {
		indeg[name] = len(n.deps)
		for _, dep := range n.deps {
			dependents[dep] = append(dependents[dep], name)
		}
	}

	current := make([]string, 0)
	for name, deg := range indeg {
		if deg == 0 {
			current = append(current, name)
		}
	}
	slices.Sort(current)

	var (
		layers    [][]string
		processed int
	)
	for len(current) > 0 {
		layers = append(layers, current)

		var next []string
		for _, name := range current {
			processed++
			for _, dep := range dependents[name] {
				indeg[dep]--
				if indeg[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		slices.Sort(next)
		current = next
	}

	if processed != len(m.nodes) {
		return nil, ErrDependencyCycle
	}

	return layers, nil
}

// validateDeps checks that every declared dependency refers to a registered
// module. The caller must hold m.mu.
func (m *Manager) validateDeps() error {
	var errs []error
	for name, n := range m.nodes {
		for _, dep := range n.deps {
			if _, ok := m.nodes[dep]; !ok {
				errs = append(errs, fmt.Errorf("%w: module %q depends on %q", ErrUnknownDependency, name, dep))
			}
		}
	}

	return errors.Join(errs...)
}
