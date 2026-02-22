package issues

import (
	"fmt"
)

var validTransitions = map[State]map[State]bool{
	StateTodo: {
		StateInProgress: true,
		StateBlocked:    true,
		StateCanceled:   true,
	},
	StateInProgress: {
		StateBlocked:  true,
		StateDone:     true,
		StateTodo:     true,
		StateCanceled: true,
	},
	StateBlocked: {
		StateTodo:       true,
		StateInProgress: true,
		StateCanceled:   true,
	},
	StateDone: {
		StateTodo: true,
	},
	StateCanceled: {
		StateTodo: true,
	},
}

func IsValidState(s State) bool {
	_, ok := validTransitions[s]
	return ok
}

func ValidateTransition(from, to State) error {
	if from == to {
		return nil
	}
	next, ok := validTransitions[from]
	if !ok || !next[to] {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidStateTransition, from, to)
	}
	return nil
}
