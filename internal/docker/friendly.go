package docker

import "errors"

// FriendlyError is a raw daemon/runtime error rewritten into readable text
// with an optional recommendation. Error() renders "Msg — Hint" for the UI;
// the agent contract takes Msg and Hint as separate fields. The raw error
// stays reachable through Unwrap, so errors.Is/As and IsNotFound still see
// what the daemon actually said.
type FriendlyError struct {
	Msg   string
	Hint  string
	Cause error
}

// Error implements the error interface.
func (e *FriendlyError) Error() string {
	if e.Hint == "" {
		return e.Msg
	}
	return e.Msg + " — " + e.Hint
}

// Unwrap exposes the raw error the message was derived from.
func (e *FriendlyError) Unwrap() error { return e.Cause }

// friendly builds a *FriendlyError over cause.
func friendly(cause error, msg, hint string) error {
	return &FriendlyError{Msg: msg, Hint: hint, Cause: cause}
}

// AsFriendly returns the first *FriendlyError in err's chain, if any.
func AsFriendly(err error) (*FriendlyError, bool) {
	var fe *FriendlyError
	if errors.As(err, &fe) {
		return fe, true
	}
	return nil, false
}
