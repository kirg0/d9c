package agent

import (
	"fmt"
	"strings"
)

// HostResult is one host's outcome of a read operation run on several hosts
// at once (fan-out): that host's envelope fields, or its error.
type HostResult struct {
	Host      string `json:"host"`
	Runtime   string `json:"runtime,omitempty"`
	Data      any    `json:"data,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Hint      string `json:"hint,omitempty"`
	Error     *Error `json:"error,omitempty"`
}

// FanoutResult combines the per-host outcomes of operation op: every host
// answered → the list as Data; some failed → the list marked Partial; all
// failed → one error carrying the first host's code. Truncated is set when
// any host's data was cut.
func FanoutResult(op string, results []HostResult) (Result, error) {
	res := Result{Data: results}
	var failed []HostResult
	for _, r := range results {
		if r.Error != nil {
			failed = append(failed, r)
		}
		res.Truncated = res.Truncated || r.Truncated
	}
	switch {
	case len(failed) == 0:
		if res.Truncated {
			res.Hint = "some hosts' data was cut; see data[].hint"
		}
		return res, nil
	case len(failed) < len(results):
		res.Partial = true
		res.Hint = fmt.Sprintf("%d of %d hosts failed; see data[].error", len(failed), len(results))
		return res, nil
	}
	if len(failed) == 1 {
		e := *failed[0].Error
		e.Message = failed[0].Host + ": " + e.Message
		return Result{}, &e
	}
	details := make([]string, 0, maxFailureDetails)
	for _, r := range failed[:min(len(failed), maxFailureDetails)] {
		details = append(details, r.Host+": "+r.Error.Message)
	}
	if len(failed) > maxFailureDetails {
		details = append(details, fmt.Sprintf("and %d more", len(failed)-maxFailureDetails))
	}
	first := failed[0].Error
	return Result{}, &Error{
		Code:    first.Code,
		Message: fmt.Sprintf("%s failed on all %d hosts: %s", op, len(failed), strings.Join(details, "; ")),
		Hint:    first.Hint,
		Err:     first,
	}
}
