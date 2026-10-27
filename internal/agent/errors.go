package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kirg0/d9c/internal/docker"
)

// Code is the machine-readable error class of the agent contract. Each code
// maps to a fixed process exit status (see ExitCode) so a shell-driven agent
// can branch on `$?` without parsing stderr.
type Code string

const (
	// CodeInternal is any failure not covered by a more specific code.
	CodeInternal Code = "internal"
	// CodeUsage is a bad invocation: unknown operation, bad or missing argument.
	CodeUsage Code = "usage"
	// CodeNotFound means the targeted object does not exist (anymore).
	CodeNotFound Code = "not_found"
	// CodeConnection means the daemon/host is unreachable or rejected the
	// transport (DNS, refused, SSH host key mismatch, bad socket path).
	CodeConnection Code = "connection"
	// CodeForbidden means the operation's tier exceeds the policy ceiling.
	CodeForbidden Code = "forbidden"
	// CodeAuthRequired means the host needs credentials the agent cannot
	// supply interactively.
	CodeAuthRequired Code = "auth_required"
	// CodeConfirmationRequired means a destructive operation was asked for
	// without explicit confirmation; the caller must show the plan to the user.
	CodeConfirmationRequired Code = "confirmation_required"
)

// exitCodes is the documented code → exit status table (0 is success).
var exitCodes = map[Code]int{
	CodeInternal:             1,
	CodeUsage:                2,
	CodeNotFound:             3,
	CodeConnection:           4,
	CodeForbidden:            5,
	CodeAuthRequired:         6,
	CodeConfirmationRequired: 7,
}

// ExitCode returns the process exit status for c (1 for an unknown code).
func (c Code) ExitCode() int {
	if n, ok := exitCodes[c]; ok {
		return n
	}
	return 1
}

// Error is the structured failure of an operation. It serializes as the
// `error` object of the stderr contract: {"error":{"code","message","hint"}}.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	// Hint is an actionable next step for the agent (how to fix or widen
	// the request); empty when there is nothing useful to add.
	Hint string `json:"hint,omitempty"`
	// Err is the underlying cause, kept for errors.Is/As; never serialized.
	Err error `json:"-"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Hint == "" {
		return e.Message
	}
	return e.Message + " (hint: " + e.Hint + ")"
}

// Unwrap exposes the underlying cause.
func (e *Error) Unwrap() error { return e.Err }

// ErrorEnvelope is the top-level JSON document written to stderr on failure.
type ErrorEnvelope struct {
	Error *Error `json:"error"`
}

// Errorf builds an *Error with a formatted message and no hint.
func Errorf(code Code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// WithHint returns a copy of e carrying hint.
func (e *Error) WithHint(hint string) *Error {
	c := *e
	c.Hint = hint
	return &c
}

// daemonPrefix is the Docker SDK's preamble on every API error; it carries no
// information for an agent, so Classify drops it from the message.
const daemonPrefix = "Error response from daemon: "

// Classify turns any error from a handler or backend into an *Error with the
// matching code and an English hint. An *Error anywhere in the chain is
// returned as is; nil stays nil. A docker.FriendlyError in the chain is
// reused: its message replaces the raw text, its recommendation becomes the
// hint, and the code still follows the raw cause (so a reworded "no such
// container" stays not_found).
func Classify(err error) *Error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	msg := message(err)
	fe, isFriendly := docker.AsFriendly(err)
	wrap := func(code Code, hint string) *Error {
		if isFriendly && fe.Hint != "" {
			hint = fe.Hint
		}
		return &Error{Code: code, Message: msg, Hint: hint, Err: err}
	}
	switch {
	case docker.IsUnknownHostKeyError(err):
		return wrap(CodeConnection, "the host is not in ~/.ssh/known_hosts; ask the user to verify its key "+
			"(connect once with ssh or the d9c TUI) — agent mode never accepts host keys automatically")
	case docker.IsHostKeyError(err):
		return wrap(CodeConnection, "the host key does not match ~/.ssh/known_hosts; "+
			"verify the host and fix known_hosts manually (agent mode never accepts host keys automatically)")
	case isAuthError(err):
		return wrap(CodeAuthRequired, "set D9C_SSH_PASSWORD or configure an SSH key/agent for this host")
	case docker.IsSocketError(err):
		return wrap(CodeConnection, "check the unix:// socket path passed with -H")
	case docker.IsHostNotFoundError(err):
		return wrap(CodeConnection, "check the host address; the name does not resolve")
	case docker.IsNotFound(err):
		return wrap(CodeNotFound, "the object may have been removed by another client; list the resources again")
	case docker.IsConnectionError(err):
		return wrap(CodeConnection, "check that the daemon is running and reachable from this machine")
	default:
		return wrap(CodeInternal, "")
	}
}

// message renders err for the agent: without the daemon preamble, and with a
// FriendlyError's recommendation cut out of the text (it goes to the hint).
func message(err error) string {
	msg := err.Error()
	if fe, ok := docker.AsFriendly(err); ok && fe.Hint != "" {
		msg = strings.Replace(msg, fe.Error(), fe.Msg, 1)
	}
	return strings.ReplaceAll(msg, daemonPrefix, "")
}

// isAuthError reports whether err is an SSH authentication failure ("ssh:
// handshake failed: ssh: unable to authenticate, attempted methods …") — the
// host is reachable but needs credentials the agent was not given.
func isAuthError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "no supported methods remain")
}
