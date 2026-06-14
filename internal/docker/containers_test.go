package docker

import (
	"errors"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
)

func TestFormatPorts(t *testing.T) {
	tests := []struct {
		name  string
		ports []types.Port
		want  string
	}{
		{
			name:  "empty",
			ports: nil,
			want:  "",
		},
		{
			name:  "single private",
			ports: []types.Port{{PrivatePort: 80, Type: "tcp"}},
			want:  "80/tcp",
		},
		{
			name:  "public mapping",
			ports: []types.Port{{PublicPort: 8080, PrivatePort: 80, Type: "tcp"}},
			want:  "8080->80/tcp",
		},
		{
			name: "deduplication",
			ports: []types.Port{
				{PublicPort: 8080, PrivatePort: 80, Type: "tcp"},
				{PublicPort: 8080, PrivatePort: 80, Type: "tcp"},
			},
			want: "8080->80/tcp",
		},
		{
			name: "multiple distinct",
			ports: []types.Port{
				{PublicPort: 80, PrivatePort: 80, Type: "tcp"},
				{PublicPort: 443, PrivatePort: 443, Type: "tcp"},
			},
			want: "80->80/tcp, 443->443/tcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatPorts(tt.ports)
			if got != tt.want {
				t.Errorf("formatPorts() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFriendlyRunErr(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // substring of the rewritten message
	}{
		{"missing image", "Error response from daemon: No such image: nginx:9.9", "pull"},
		{"name conflict", `Error response from daemon: Conflict. The container name "/web" is already in use by container abc`, "имя контейнера занято"},
		{"bad port", "Error response from daemon: invalid containerPort: abc", "host:container"},
		{"other passes wrapped", "Error response from daemon: mounts denied", "create container"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := friendlyRunErr(errors.New(tt.in))
			if got == nil || !strings.Contains(got.Error(), tt.want) {
				t.Errorf("friendlyRunErr = %v, want substring %q", got, tt.want)
			}
		})
	}
	if friendlyRunErr(nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestFakeRunContainer(t *testing.T) {
	t.Run("runs from a local image", func(t *testing.T) {
		f := NewFakeBackend()
		before := len(f.Containers)
		err := f.RunContainer(RunOptions{
			Image: "nginx:1.25",
			Name:  "web2",
			Ports: []string{"8081:80"},
			Env:   []string{"MODE=test"},
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if len(f.Containers) != before+1 {
			t.Fatalf("container count = %d, want %d", len(f.Containers), before+1)
		}
		got := f.Containers[len(f.Containers)-1]
		if got.Name != "web2" || got.State != "running" || got.Image != "nginx:1.25" {
			t.Errorf("created container = %+v, want web2/running/nginx:1.25", got)
		}
	})

	t.Run("generates a name when empty", func(t *testing.T) {
		f := NewFakeBackend()
		if err := f.RunContainer(RunOptions{Image: "nginx:1.25"}); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := f.Containers[len(f.Containers)-1].Name; got == "" {
			t.Error("expected a generated container name")
		}
	})

	t.Run("missing image gets a pull hint", func(t *testing.T) {
		f := NewFakeBackend()
		err := f.RunContainer(RunOptions{Image: "ghost:0.0"})
		if err == nil || !strings.Contains(err.Error(), "pull") {
			t.Errorf("err = %v, want pull hint", err)
		}
	})

	t.Run("duplicate name rejected", func(t *testing.T) {
		f := NewFakeBackend()
		err := f.RunContainer(RunOptions{Image: "nginx:1.25", Name: "web"})
		if err == nil || !strings.Contains(err.Error(), "занято") {
			t.Errorf("err = %v, want name-conflict hint", err)
		}
	})

	t.Run("empty image rejected", func(t *testing.T) {
		f := NewFakeBackend()
		if err := f.RunContainer(RunOptions{}); err == nil {
			t.Error("expected error for empty image")
		}
	})
}

func TestParseHealth(t *testing.T) {
	tests := []struct{ status, want string }{
		{"Up 2 hours (healthy)", "healthy"},
		{"Up 5 minutes (unhealthy)", "unhealthy"},
		{"Up 3 seconds (health: starting)", "starting"},
		{"Up 2 hours", ""},
		{"Exited (0) 1 hour ago", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := parseHealth(tt.status); got != tt.want {
			t.Errorf("parseHealth(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}
