package issues

import "errors"

var (
	ErrInvalidInput           = errors.New("invalid input")
	ErrNotFound               = errors.New("not found")
	ErrConflict               = errors.New("conflict")
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrDepthExceeded          = errors.New("depth exceeded")
	ErrCycleDetected          = errors.New("cycle detected")
)
