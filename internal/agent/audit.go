package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// AuditFileName is the audit log's file name; frontends keep it next to the
// config file (~/.d9c/agent-audit.log by default).
const AuditFileName = "agent-audit.log"

// Audit outcomes.
const (
	AuditOK      = "ok"
	AuditPartial = "partial"
	AuditPlanned = "planned" // a dry run: nothing changed
	AuditError   = "error"
)

// AuditEntry is one line of the audit log: a write operation an agent asked
// for, whatever came of it (refused, planned, done, failed).
type AuditEntry struct {
	Time time.Time `json:"time"`
	// Host is the saved host, Docker context or URL the command addressed.
	Host string `json:"host"`
	Op   string `json:"op"`
	Tier Tier   `json:"tier"`
	// Targets names what the operation acted on: the resolved containers of
	// a lifecycle command, else the project, image, name or container given.
	Targets []string `json:"targets,omitempty"`
	// Args are the arguments as the frontend received them.
	Args    Args   `json:"args,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
	Yes     bool   `json:"yes,omitempty"`
	Result  string `json:"result"`
	Code    Code   `json:"code,omitempty"`
	Message string `json:"error,omitempty"`
}

// NewAuditEntry describes one invocation of op for the audit log from its
// outcome: res on success, err (classified) on failure.
func NewAuditEntry(now time.Time, host string, op Op, args Args, call Call, res Result, err error) AuditEntry {
	e := AuditEntry{
		Time:   now.UTC(),
		Host:   host,
		Op:     op.Name,
		Tier:   op.Tier,
		Args:   args,
		DryRun: call.DryRun,
		Yes:    call.Confirmed,
	}
	switch {
	case err != nil:
		ae := Classify(err)
		e.Result, e.Code, e.Message = AuditError, ae.Code, ae.Message
	case call.DryRun:
		e.Result = AuditPlanned
	case res.Partial:
		e.Result = AuditPartial
	default:
		e.Result = AuditOK
	}
	e.Targets = auditTargets(args, res)
	return e
}

// auditTargetArgs are the argument names that name what an operation touches.
var auditTargetArgs = []string{"targets", "container", "project", "image", "name", "file"}

// auditTargets lists what an invocation acted on: the per-container outcomes
// of a lifecycle command when it ran, else the naming arguments (and filters).
func auditTargets(args Args, res Result) []string {
	if data, ok := res.Data.(ActionData); ok {
		out := make([]string, 0, len(data.Targets))
		for _, t := range data.Targets {
			label := targetLabel(t)
			if t.ID != "" {
				label += " (" + t.ID + ")"
			}
			if t.Result != outcomeOK {
				label += ": " + t.Result
			}
			out = append(out, label)
		}
		return out
	}
	var out []string
	for _, name := range auditTargetArgs {
		switch v := args[name].(type) {
		case string:
			if v != "" {
				out = append(out, v)
			}
		case []string:
			out = append(out, v...)
		}
	}
	if f, ok := args["filter"].([]string); ok {
		for _, s := range f {
			out = append(out, "filter "+s)
		}
	}
	return slices.Clip(out)
}

// AuditLog appends AuditEntry lines (JSON, one per line) to a file shared by
// every d9c process of the user.
type AuditLog struct {
	f *os.File
}

// OpenAuditLog opens (creating it and its directory if needed) the audit log
// at path for appending. Frontends open it before running a write operation
// and refuse the operation when that fails: writes must leave a trail.
func OpenAuditLog(path string) (*AuditLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, auditError(path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, auditError(path, err)
	}
	return &AuditLog{f: f}, nil
}

func auditError(path string, err error) *Error {
	return &Error{
		Code:    CodeInternal,
		Message: fmt.Sprintf("cannot open the audit log %s: %v", path, err),
		Hint:    "write commands are refused without an audit trail; ask the user to fix the file or its directory",
		Err:     err,
	}
}

// Record appends e as one line. The line goes out in a single write, so
// entries of concurrent d9c processes do not interleave.
func (l *AuditLog) Record(e AuditEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = l.f.Write(append(b, '\n'))
	return err
}

// Close closes the file.
func (l *AuditLog) Close() error { return l.f.Close() }
