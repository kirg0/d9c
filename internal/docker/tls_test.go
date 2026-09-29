package docker

import (
	"path/filepath"
	"testing"

	"github.com/kirg0/d9c/internal/config"
)

// A CA alone (as some Docker contexts ship) switches the TCP client to TLS —
// proven by the missing CA file now failing construction; with no TLS
// material the plain client builds fine.
func TestNewTCPBackendTLSSelection(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "ca.pem")
	if _, err := newTCPBackend(&config.Config{Host: "tcp://127.0.0.1:2376", TLSCACert: missing}); err == nil {
		t.Error("CA-only config must enable TLS (and fail on the missing CA file)")
	}
	b, err := newTCPBackend(&config.Config{Host: "tcp://127.0.0.1:2375"})
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}
