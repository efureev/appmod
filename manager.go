package appmod

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"syscall"
	"time"

	shutdown "github.com/efureev/go-shutdown/v2"
)

// HealthChecker is an optional capability of a module. Modules registered in a
// [Manager] that implement it are probed by [Manager.Health].
type HealthChecker interface {
	// HealthCheck reports whether the module is healthy. A non-nil error means
	// the module is unhealthy.
	HealthCheck(ctx context.Context) error
}

// node is a registered module together with its dependencies.
type node struct {
	name   string
	module AppModule
	deps   []string
}

// Manager orchestrates a set of named [AppModule]s connected by dependencies.
//
// Modules are registered with [Manager.Register] together with the names of the
// modules they depend on. [Manager.Start] initializes them in dependency
// (topological) order, starting independent modules concurrently, and
// [Manager.Stop] tears them down in the reverse order. A dependency cycle is
// reported as [ErrDependencyCycle].
//
// A Manager is safe for concurrent use by multiple goroutines.
type Manager struct {
	mu    sync.Mutex
	nodes map[string]*node

	// started holds the names of successfully started modules in start
	// completion order; Stop tears them down in reverse.
	started []string

	logger          *slog.Logger
	shutdownTimeout time.Duration
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

// NewManager creates a [Manager] configured with the given options.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{nodes: make(map[string]*node)}
	for _, opt := range opts {
		opt(m)
	}
	if m.logger == nil {
		m.logger = slog.New(slog.DiscardHandler)
	}

	return m
}

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
	m.nodes[name] = &node{name: name, module: module, deps: slices.Clone(deps)}

	return nil
}

// Start initializes every registered module in dependency order.
//
// Independent modules within the same dependency layer are started
// concurrently. If any module fails to start (or the context is canceled),
// Start rolls back by stopping the modules that already started, in reverse
// order, and returns the cause joined with any teardown error via
// [errors.Join].
func (m *Manager) Start(ctx context.Context) error {
	layers, err := m.plan()
	if err != nil {
		return err
	}

	for _, layer := range layers {
		if err := ctx.Err(); err != nil {
			return m.abort(ctx, err)
		}
		if err := m.startLayer(ctx, layer); err != nil {
			return m.abort(ctx, err)
		}
	}

	return nil
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
// retained so a subsequent Stop can resume the teardown.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	started := slices.Clone(m.started)
	m.started = nil
	m.mu.Unlock()

	var errs []error
	for i := len(started) - 1; i >= 0; i-- {
		name := started[i]

		if err := ctx.Err(); err != nil {
			// Context canceled (e.g. shutdown timeout): stop iterating so the
			// teardown does not leak as a detached goroutine. Put the modules
			// that were not stopped back so a later Stop can finish the job.
			m.logger.ErrorContext(ctx, "teardown aborted by context", "error", err, "remaining", i+1)
			m.mu.Lock()
			m.started = append(slices.Clone(started[:i+1]), m.started...)
			m.mu.Unlock()
			errs = append(errs, fmt.Errorf("appmod: teardown aborted: %w", err))

			return errors.Join(errs...)
		}

		m.mu.Lock()
		n := m.nodes[name]
		m.mu.Unlock()

		m.logger.InfoContext(ctx, "stopping module", "module", name)
		if err := n.module.Destroy(ctx); err != nil {
			m.logger.ErrorContext(ctx, "module failed to stop", "module", name, "error", err)
			errs = append(errs, fmt.Errorf("appmod: module %q failed to stop: %w", name, err))
		}
	}

	return errors.Join(errs...)
}

// Run starts all modules and then blocks until the context is canceled or an
// interrupt/termination signal (SIGINT, SIGTERM) is received, after which it
// gracefully stops every module. If a shutdown timeout was configured (see
// [WithShutdownTimeout]), Stop is bounded by it and a non-positive value means
// no timeout.
//
// The graceful-shutdown sequence (signal handling and the bounded teardown) is
// delegated to github.com/efureev/go-shutdown. When the teardown does not
// finish within the configured timeout, Run returns [shutdown.ErrShutdownTimeout].
func (m *Manager) Run(ctx context.Context) error {
	if err := m.Start(ctx); err != nil {
		return err
	}

	sd := shutdown.New().
		SetLogger(slogShutdownLogger{m.logger}).
		SetTimeout(m.shutdownTimeout).
		OnDestroy(func(ctx context.Context) error {
			m.logger.Info("shutdown signal received, stopping modules")
			return m.Stop(ctx)
		})

	return sd.WaitContext(ctx, syscall.SIGINT, syscall.SIGTERM)
}

// slogShutdownLogger adapts a *slog.Logger to the shutdown.Logger interface so
// the orchestrator can report the shutdown sequence through the same structured
// logger used for the rest of the lifecycle.
type slogShutdownLogger struct{ l *slog.Logger }

func (s slogShutdownLogger) Trace(args ...any) { s.l.Debug(fmt.Sprint(args...)) }
func (s slogShutdownLogger) Info(args ...any)  { s.l.Info(fmt.Sprint(args...)) }

// Health probes every started module that implements [HealthChecker] and joins
// the errors of the unhealthy ones. It returns nil when all probed modules are
// healthy (or none implement [HealthChecker]).
func (m *Manager) Health(ctx context.Context) error {
	m.mu.Lock()
	started := slices.Clone(m.started)
	nodes := m.nodes
	m.mu.Unlock()

	var errs []error
	for _, name := range started {
		hc, ok := nodes[name].module.(HealthChecker)
		if !ok {
			continue
		}
		if err := hc.HealthCheck(ctx); err != nil {
			errs = append(errs, fmt.Errorf("appmod: module %q is unhealthy: %w", name, err))
		}
	}

	return errors.Join(errs...)
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
