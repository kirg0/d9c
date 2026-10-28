package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

func TestCodeExitCode(t *testing.T) {
	tests := []struct {
		code Code
		want int
	}{
		{CodeInternal, 1},
		{CodeUsage, 2},
		{CodeNotFound, 3},
		{CodeConnection, 4},
		{CodeForbidden, 5},
		{CodeAuthRequired, 6},
		{CodeConfirmationRequired, 7},
		{CodeTimeout, 9},
		{Code("bogus"), 1},
	}
	for _, tt := range tests {
		if got := tt.code.ExitCode(); got != tt.want {
			t.Errorf("%q.ExitCode() = %d, want %d", tt.code, got, tt.want)
		}
	}
}

func TestClassify(t *testing.T) {
	own := Errorf(CodeForbidden, "nope")
	tests := []struct {
		name     string
		err      error
		want     Code
		wantHint bool
	}{
		{"own error", own, CodeForbidden, false},
		{"wrapped own error", fmt.Errorf("ctx: %w", own), CodeForbidden, false},
		{"host key mismatch", errors.New("ssh: handshake failed: knownhosts: key mismatch"), CodeConnection, true},
		{"unknown host key", fmt.Errorf("SSH tunnel: ssh: handshake failed: %w: h:22", docker.ErrUnknownHostKey), CodeConnection, true},
		{"ssh auth", errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain"), CodeAuthRequired, true},
		{"socket path", fmt.Errorf("%w: /var/run/x.sock: no such file", docker.ErrSocketPath), CodeConnection, true},
		{"dns", errors.New("dial tcp: lookup nohost: no such host"), CodeConnection, true},
		{"not found", errors.New("Error response from daemon: No such container: abc"), CodeNotFound, true},
		{"refused", errors.New("dial tcp 10.0.0.1:2375: connect: connection refused"), CodeConnection, true},
		{"eof", fmt.Errorf("read: %w", io.EOF), CodeConnection, true},
		{"other", errors.New("conflict: unable to remove repository reference"), CodeInternal, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.err)
			if got.Code != tt.want {
				t.Fatalf("Classify(%v).Code = %q, want %q", tt.err, got.Code, tt.want)
			}
			if docker.IsUnknownHostKeyError(tt.err) && !strings.Contains(got.Hint, "not in ~/.ssh/known_hosts") {
				t.Errorf("unknown host key got the mismatch hint: %q", got.Hint)
			}
			if (got.Hint != "") != tt.wantHint {
				t.Errorf("hint = %q, wantHint %v", got.Hint, tt.wantHint)
			}
			if !errors.Is(got, tt.err) && !errors.Is(tt.err, got) {
				t.Errorf("classified error lost its cause")
			}
		})
	}
	if Classify(nil) != nil {
		t.Error("Classify(nil) != nil")
	}
}

// TestClassifyMessage checks the message/hint the agent sees: no daemon
// preamble, and a FriendlyError's recommendation moved into the hint while
// the code still follows the raw cause.
func TestClassifyMessage(t *testing.T) {
	noImage := &docker.FriendlyError{Msg: "image not found on the host", Hint: "pull it first",
		Cause: errors.New("Error response from daemon: No such image: nginx:x")}
	gone := &docker.FriendlyError{Msg: "container not found",
		Cause: errors.New("Error response from daemon: No such container: web")}
	conflict := &docker.FriendlyError{Msg: "the image is used by a container", Hint: "run rm -f",
		Cause: errors.New("conflict: unable to remove repository reference (must force)")}
	tests := []struct {
		name     string
		err      error
		wantCode Code
		wantMsg  string
		wantHint string
	}{
		{"daemon prefix dropped", errors.New("Error response from daemon: No such container: web"), CodeNotFound,
			"No such container: web", "the object may have been removed by another client; list the resources again"},
		{"wrapped daemon prefix dropped", fmt.Errorf("inspect: %w", errors.New("Error response from daemon: boom")), CodeInternal,
			"inspect: boom", ""},
		{"friendly hint replaces generic", noImage, CodeNotFound, "image not found on the host", "pull it first"},
		{"wrapped friendly keeps context", fmt.Errorf("run web: %w", noImage), CodeNotFound, "run web: image not found on the host", "pull it first"},
		{"friendly without hint keeps generic", gone, CodeNotFound, "container not found",
			"the object may have been removed by another client; list the resources again"},
		{"friendly conflict", conflict, CodeInternal, "the image is used by a container", "run rm -f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.err)
			if got.Code != tt.wantCode || got.Message != tt.wantMsg || got.Hint != tt.wantHint {
				t.Errorf("Classify = {%q, %q, %q}\nwant       {%q, %q, %q}",
					got.Code, got.Message, got.Hint, tt.wantCode, tt.wantMsg, tt.wantHint)
			}
			if !errors.Is(got, tt.err) {
				t.Error("classified error lost its cause")
			}
		})
	}
}

func TestErrorJSONAndString(t *testing.T) {
	cause := errors.New("boom")
	e := &Error{Code: CodeNotFound, Message: "no such container: web", Hint: "list again", Err: cause}
	b, err := json.Marshal(ErrorEnvelope{Error: e})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"error":{"code":"not_found","message":"no such container: web","hint":"list again"}}`
	if string(b) != want {
		t.Errorf("json = %s\nwant   %s", b, want)
	}
	if got := e.Error(); got != "no such container: web (hint: list again)" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(e, cause) {
		t.Error("Unwrap does not expose the cause")
	}

	bare := Errorf(CodeUsage, "bad %s", "flag")
	b, _ = json.Marshal(ErrorEnvelope{Error: bare})
	if string(b) != `{"error":{"code":"usage","message":"bad flag"}}` {
		t.Errorf("json without hint = %s", b)
	}
	if bare.Error() != "bad flag" {
		t.Errorf("Error() without hint = %q", bare.Error())
	}
	hinted := bare.WithHint("h")
	if bare.Hint != "" || hinted.Hint != "h" || hinted.Code != CodeUsage {
		t.Errorf("WithHint mutated the receiver or lost fields: %+v / %+v", bare, hinted)
	}
}
