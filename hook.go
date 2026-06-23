package appmod

import (
	"fmt"
	"strconv"
)

// Phase identifies a lifecycle phase a hook is attached to.
type Phase int32

const (
	// PhaseBeforeStart hooks run before the module is started (during Init).
	PhaseBeforeStart Phase = iota
	// PhaseAfterStart hooks run after the module is started (during Init).
	PhaseAfterStart
	// PhaseBeforeDestroy hooks run before the module is destroyed (during Destroy).
	PhaseBeforeDestroy
	// PhaseAfterDestroy hooks run after the module is destroyed (during Destroy).
	PhaseAfterDestroy
)

// String implements [fmt.Stringer].
func (p Phase) String() string {
	switch p {
	case PhaseBeforeStart:
		return "BeforeStart"
	case PhaseAfterStart:
		return "AfterStart"
	case PhaseBeforeDestroy:
		return "BeforeDestroy"
	case PhaseAfterDestroy:
		return "AfterDestroy"
	default:
		return fmt.Sprintf("Phase(%d)", int32(p))
	}
}

// Hook is a named, prioritized lifecycle hook.
//
// Within a phase, hooks run in ascending Priority order; hooks with the same
// priority keep their registration order (the ordering is stable). A Name is
// optional but makes the hook removable via [BaseAppModule.RemoveHook] and
// improves diagnostics (it appears in [HookError] and logs).
type Hook struct {
	// Name optionally identifies the hook for removal and diagnostics.
	Name string
	// Priority orders hooks within a phase (lower runs first; default 0).
	Priority int
	// Run is the hook function. A nil Run is skipped.
	Run HookFunc
}

// HookError is returned when a lifecycle hook fails (or panics). It carries the
// phase, the index of the hook within that phase, its optional name and the
// owning module name so failures can be diagnosed programmatically.
type HookError struct {
	// Phase is the lifecycle phase the failing hook belongs to.
	Phase Phase
	// Index is the position of the hook within its phase (after ordering).
	Index int
	// Name is the optional hook name (empty for anonymous hooks).
	Name string
	// Module is the name of the module the hook is attached to.
	Module string
	// Err is the underlying error returned (or recovered) from the hook.
	Err error
}

// Error implements the error interface.
func (e *HookError) Error() string {
	id := "#" + strconv.Itoa(e.Index)
	if e.Name != "" {
		id = strconv.Quote(e.Name)
	}
	if e.Module != "" {
		return fmt.Sprintf("appmod: module %q: %s hook %s failed: %v", e.Module, e.Phase, id, e.Err)
	}

	return fmt.Sprintf("appmod: %s hook %s failed: %v", e.Phase, id, e.Err)
}

// Unwrap returns the underlying hook error so [errors.Is] / [errors.As] work.
func (e *HookError) Unwrap() error { return e.Err }
