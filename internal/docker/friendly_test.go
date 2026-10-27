package docker

import (
	"errors"
	"fmt"
	"testing"
)

func TestFriendlyError(t *testing.T) {
	cause := errors.New("raw daemon text")
	tests := []struct {
		name string
		err  *FriendlyError
		want string
	}{
		{"msg only", &FriendlyError{Msg: "container not found", Cause: cause}, "container not found"},
		{"msg and hint", &FriendlyError{Msg: "invalid port format", Hint: "use host:container", Cause: cause}, "invalid port format — use host:container"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
			if !errors.Is(tt.err, cause) {
				t.Error("FriendlyError does not unwrap to its cause")
			}
		})
	}
}

func TestAsFriendly(t *testing.T) {
	fe := &FriendlyError{Msg: "m"}
	if got, ok := AsFriendly(fmt.Errorf("ctx: %w", fe)); !ok || got != fe {
		t.Errorf("AsFriendly(wrapped) = %v, %v; want the friendly error", got, ok)
	}
	if _, ok := AsFriendly(errors.New("plain")); ok {
		t.Error("AsFriendly(plain) = true")
	}
	if _, ok := AsFriendly(nil); ok {
		t.Error("AsFriendly(nil) = true")
	}
}

// TestFriendlyHelpersSplitHint pins that each helper keeps the raw cause and
// separates the recommendation from the message (the agent contract reads
// them as distinct fields; the UI text is unchanged).
func TestFriendlyHelpersSplitHint(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantMsg  string
		wantHint string
	}{
		{"run no image", friendlyRunErr(errors.New("No such image: x")), "image not found on the host", "pull it first (:pull in the Images view)"},
		{"run name taken", friendlyRunErr(errors.New(`Conflict. The container name "/a" is already in use`)), "container name is taken", "pick another or remove the old container"},
		{"run port", friendlyRunErr(errors.New("invalid containerPort: x")), "invalid port format", "use host:container, e.g. 8080:80"},
		{"rmi child", friendlyImageRemoveErr(errors.New("image has dependent child")), "the image has dependent images", "remove them first or run prune"},
		{"rmi force", friendlyImageRemoveErr(errors.New("(must force)")), "the image is used by a container", "run rm -f or remove the container first"},
		{"cp gone", friendlyCopyErr(errors.New("No such container: a")), "container not found", ""},
		{"ls missing", friendlyListErr("/x", "ls: /x: No such file or directory\n"), "path /x not found", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fe, ok := AsFriendly(tt.err)
			if !ok {
				t.Fatalf("%v is not a FriendlyError", tt.err)
			}
			if fe.Msg != tt.wantMsg || fe.Hint != tt.wantHint {
				t.Errorf("Msg, Hint = %q, %q; want %q, %q", fe.Msg, fe.Hint, tt.wantMsg, tt.wantHint)
			}
			if fe.Cause == nil {
				t.Error("FriendlyError lost its cause")
			}
		})
	}
}
