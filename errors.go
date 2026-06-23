package appmod

import "errors"

// Lifecycle errors returned by [BaseAppModule].
var (
	// ErrAlreadyInitialized is returned by Init when the module has already
	// been initialized and not yet destroyed.
	ErrAlreadyInitialized = errors.New("appmod: module already initialized")
	// ErrNotInitialized is returned by Destroy when the module has not been
	// initialized (or has already been destroyed).
	ErrNotInitialized = errors.New("appmod: module is not initialized")
)
