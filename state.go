package appmod

import "fmt"

// State is the lifecycle state of a [BaseAppModule].
//
// The normal flow is:
//
//	Created → Initializing → Running → Destroying → Destroyed
//
// If a hook aborts Init (or the context is canceled during start), the module
// ends up in StateFailed after the automatic rollback (see [BaseAppModule.Init]).
type State int32

const (
	// StateCreated is the initial state of a freshly created module.
	StateCreated State = iota
	// StateInitializing means Init is currently running start hooks.
	StateInitializing
	// StateRunning means the module has been successfully initialized.
	StateRunning
	// StateDestroying means Destroy is currently running teardown hooks.
	StateDestroying
	// StateDestroyed means the module has been successfully destroyed.
	StateDestroyed
	// StateFailed means Init failed; any acquired resources were rolled back.
	StateFailed
)

// String implements [fmt.Stringer].
func (s State) String() string {
	switch s {
	case StateCreated:
		return "Created"
	case StateInitializing:
		return "Initializing"
	case StateRunning:
		return "Running"
	case StateDestroying:
		return "Destroying"
	case StateDestroyed:
		return "Destroyed"
	case StateFailed:
		return "Failed"
	default:
		return fmt.Sprintf("State(%d)", int32(s))
	}
}
