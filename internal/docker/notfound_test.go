package docker

import (
	"errors"
	"fmt"
	"testing"

	"github.com/docker/docker/errdefs"
)

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed errdefs", errdefs.NotFound(errors.New("boom")), true},
		{"typed errdefs wrapped", fmt.Errorf("stop container: %w", errdefs.NotFound(errors.New("boom"))), true},
		{"docker container", errors.New("Error response from daemon: No such container: abc123"), true},
		{"docker image", errors.New("Error response from daemon: No such image: nginx:old"), true},
		{"docker volume", errors.New("Error response from daemon: get data: no such volume"), true},
		{"docker network", errors.New("Error response from daemon: network backend not found"), true},
		{"nerdctl network quoted", errors.New(`network "backend" not found`), true},
		{"nerdctl volume quoted", errors.New(`volume "data" not found`), true},
		{"crictl grpc", errors.New(`rpc error: code = NotFound desc = could not find container "abc"`), true},
		{"crictl id", errors.New("container with ID starting with abc not found: ID does not exist"), true},
		{"compose gone", errors.New(`no containers found for compose deployment "/srv/app"`), true},
		{"fake compose", errors.New("no such compose project: shop"), true},
		{"conflict", errors.New("Error response from daemon: conflict: unable to remove repository reference"), false},
		{"connection", errors.New("dial tcp 10.0.0.1:2375: connection refused"), false},
		{"dns", errors.New("lookup nohost: no such host"), false},
		{"missing file", errors.New("ls: /x: No such file or directory"), false},
		{"exec binary", errors.New(`exec: "bash": executable file not found in $PATH`), false},
		{"pull manifest", errors.New("manifest for nginx:nope not found: manifest unknown"), false},
		{"friendly copy", errors.New("container not found"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotFound(tt.err); got != tt.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
