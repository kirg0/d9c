package docker

import (
	"testing"

	"github.com/docker/docker/api/types"
)

func TestDetectRuntime(t *testing.T) {
	mkComponents := func(names ...string) []types.ComponentVersion {
		cs := make([]types.ComponentVersion, 0, len(names))
		for _, n := range names {
			cs = append(cs, types.ComponentVersion{Name: n})
		}
		return cs
	}
	tests := []struct {
		name string
		ver  types.Version
		want Runtime
	}{
		{
			name: "docker engine",
			ver:  types.Version{Components: mkComponents("Engine", "containerd", "runc")},
			want: RuntimeDocker,
		},
		{
			name: "podman via component",
			ver:  types.Version{Components: mkComponents("Podman Engine", "Conmon", "OCI Runtime (crun)")},
			want: RuntimePodman,
		},
		{
			name: "podman via platform name",
			ver: types.Version{
				Platform:   struct{ Name string }{Name: "Podman Engine"},
				Components: mkComponents("Conmon"),
			},
			want: RuntimePodman,
		},
		{
			name: "case-insensitive match",
			ver:  types.Version{Components: mkComponents("podman engine")},
			want: RuntimePodman,
		},
		{
			name: "empty version defaults to docker",
			ver:  types.Version{},
			want: RuntimeDocker,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectRuntime(tt.ver); got != tt.want {
				t.Errorf("detectRuntime() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Runtime() on the auxiliary backends must report sensible defaults: the fake
// is Docker unless told otherwise, and the disconnected stub is unknown.
func TestRuntimeDefaults(t *testing.T) {
	if got := NewFakeBackend().Runtime(); got != RuntimeDocker {
		t.Errorf("fake default runtime = %q, want docker", got)
	}
	if got := (&FakeBackend{RuntimeKind: RuntimePodman}).Runtime(); got != RuntimePodman {
		t.Errorf("fake with RuntimeKind=podman = %q, want podman", got)
	}
	if got := NewDisconnected(nil).Runtime(); got != RuntimeUnknown {
		t.Errorf("disconnected runtime = %q, want unknown", got)
	}
}

func TestRuntimeLabel(t *testing.T) {
	tests := []struct {
		r    Runtime
		want string
	}{
		{RuntimePodman, "podman"},
		{RuntimeDocker, "docker"},
		{RuntimeUnknown, "docker"},
	}
	for _, tt := range tests {
		if got := tt.r.Label(); got != tt.want {
			t.Errorf("Runtime(%q).Label() = %q, want %q", tt.r, got, tt.want)
		}
	}
}
