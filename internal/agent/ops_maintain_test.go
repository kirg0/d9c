package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/kirg0/d9c/internal/docker"
)

func TestNetworkCreateOptions(t *testing.T) {
	tests := []struct {
		name     string
		args     Args
		wantErr  string // substring of the usage message; "" = valid
		wantOpts docker.NetworkCreateOptions
	}{
		{"name only", Args{"name": " edge-net "}, "", docker.NetworkCreateOptions{Name: "edge-net"}},
		{
			"full",
			Args{"name": "app", "driver": "macvlan", "subnet": "172.28.0.0/16", "gateway": "172.28.0.1"},
			"", docker.NetworkCreateOptions{Name: "app", Driver: "macvlan", Subnet: "172.28.0.0/16", Gateway: "172.28.0.1"},
		},
		{"ipv6", Args{"name": "v6", "subnet": "fd00::/64", "gateway": "fd00::1"}, "",
			docker.NetworkCreateOptions{Name: "v6", Subnet: "fd00::/64", Gateway: "fd00::1"}},
		{"empty name", Args{"name": "  "}, "empty network name", docker.NetworkCreateOptions{}},
		{"bad subnet", Args{"name": "a", "subnet": "172.28.0.0"}, "invalid subnet", docker.NetworkCreateOptions{}},
		{"bad gateway", Args{"name": "a", "subnet": "10.0.0.0/8", "gateway": "x"}, "invalid gateway", docker.NetworkCreateOptions{}},
		{"gateway without subnet", Args{"name": "a", "gateway": "10.0.0.1"}, "needs --subnet", docker.NetworkCreateOptions{}},
		{"gateway outside subnet", Args{"name": "a", "subnet": "10.0.0.0/24", "gateway": "10.0.1.1"}, "outside subnet", docker.NetworkCreateOptions{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := networkCreateOptions(tt.args)
			if tt.wantErr == "" {
				if err != nil || opts != tt.wantOpts {
					t.Errorf("opts = %+v (%v), want %+v", opts, err, tt.wantOpts)
				}
				return
			}
			if e := Classify(err); e == nil || e.Code != CodeUsage || !strings.Contains(e.Message, tt.wantErr) {
				t.Errorf("err = %+v, want usage %q", e, tt.wantErr)
			}
		})
	}
}

func TestNetworkCreate(t *testing.T) {
	fake := docker.NewFakeBackend()
	r, err := invokeWrite(fake, "network create", Args{"name": "edge-net", "subnet": "172.28.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	d := r.Data.(NetworkEntry)
	if d.Name != "edge-net" || d.ID == "" || d.Driver != "bridge" {
		t.Errorf("data = %+v, want the read-back network", d)
	}
	if _, ok := findNetwork(fake.Networks, "edge-net"); !ok {
		t.Error("network not created")
	}
	if e := Classify(func() error { _, err := invokeWrite(fake, "network create", Args{"name": "edge-net"}); return err }()); e.Code != CodeUsage || !strings.Contains(e.Message, "already exists") {
		t.Errorf("duplicate: %+v", e)
	}
}

func TestVolumeCreate(t *testing.T) {
	fake := docker.NewFakeBackend()
	r, err := invokeWrite(fake, "volume create", Args{"name": "uploads"})
	if err != nil {
		t.Fatal(err)
	}
	if d := r.Data.(VolumeEntry); d.Name != "uploads" || d.Driver != "local" {
		t.Errorf("data = %+v, want the read-back volume", d)
	}
	if e := Classify(func() error { _, err := invokeWrite(fake, "volume create", Args{"name": "uploads"}); return err }()); e.Code != CodeUsage || !strings.Contains(e.Message, "already exists") {
		t.Errorf("duplicate: %+v", e)
	}
	if e := Classify(func() error { _, err := invokeWrite(fake, "volume create", Args{"name": " "}); return err }()); e.Code != CodeUsage {
		t.Errorf("empty name: %+v", e)
	}
}

// createSpy fails CreateNetwork/CreateVolume and, optionally, the listing.
type createSpy struct {
	*docker.FakeBackend
	createErr, listErr error
}

func (s *createSpy) CreateNetwork(docker.NetworkCreateOptions) error { return s.createErr }
func (s *createSpy) CreateVolume(docker.VolumeCreateOptions) error   { return s.createErr }
func (s *createSpy) ListNetworks() ([]docker.Network, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.FakeBackend.ListNetworks()
}

func TestCreateEngineErrors(t *testing.T) {
	spy := &createSpy{FakeBackend: docker.NewFakeBackend(), createErr: errors.New("Error response from daemon: plugin \"weave\" not found")}
	for _, op := range []string{"network create", "volume create"} {
		_, err := invokeWrite(spy, op, Args{"name": "x"})
		if e := Classify(err); e == nil || !strings.Contains(e.Message, `plugin "weave"`) || strings.Contains(e.Message, "Error response") {
			t.Errorf("%s: %+v", op, e)
		}
	}
	spy.listErr = errors.New("dial tcp 10.0.0.1:2375: connect: connection refused")
	if e := Classify(func() error { _, err := invokeWrite(spy, "network create", Args{"name": "x"}); return err }()); e.Code != CodeConnection {
		t.Errorf("listing failure: %+v, want connection", e)
	}
}

func TestPruneOps(t *testing.T) {
	fake := docker.NewFakeBackend()
	wantImages := 0
	for _, im := range fake.Images {
		if strings.Contains(im.Tags, "<none>") {
			wantImages++
		}
	}
	wantVolumes := len(fake.Volumes)

	r, err := invokeWrite(fake, "image prune", nil)
	if err != nil || r.Data != (PruneData{Pruned: "images", Deleted: wantImages}) {
		t.Errorf("image prune = %+v (%v), want %d deleted", r.Data, err, wantImages)
	}
	r, err = invokeWrite(fake, "volume prune", nil)
	if err != nil || r.Data != (PruneData{Pruned: "volumes", Deleted: wantVolumes}) {
		t.Errorf("volume prune = %+v (%v), want %d deleted", r.Data, err, wantVolumes)
	}
	r, err = invokeWrite(fake, "system prune", nil)
	if d, ok := r.Data.(SystemPruneData); err != nil || !ok || !strings.HasPrefix(d.Summary, "prune: containers ") {
		t.Errorf("system prune = %+v (%v)", r.Data, err)
	}
}

// pruneSpy fails every prune with err; SystemPrune also returns summary.
type pruneSpy struct {
	*docker.FakeBackend
	summary string
	err     error
}

func (s *pruneSpy) PruneImages() (int, error)        { return 0, s.err }
func (s *pruneSpy) PruneVolumes() (int, error)       { return 0, s.err }
func (s *pruneSpy) SystemPrune() (string, error)     { return s.summary, s.err }
func (s *pruneSpy) withErr(err error) *pruneSpy      { s.err = err; return s }
func (s *pruneSpy) withSummary(sum string) *pruneSpy { s.summary = sum; return s }

func TestPruneErrors(t *testing.T) {
	daemonErr := errors.New("Error response from daemon: a prune operation is already running")
	for _, op := range []string{"image prune", "volume prune"} {
		_, err := invokeWrite((&pruneSpy{FakeBackend: docker.NewFakeBackend()}).withErr(daemonErr), op, nil)
		if e := Classify(err); e == nil || e.Message != "a prune operation is already running" {
			t.Errorf("%s: %+v", op, e)
		}
	}

	spy := (&pruneSpy{FakeBackend: docker.NewFakeBackend()}).
		withSummary("prune: containers 2, images 1 — reclaimed 3.0 MB").
		withErr(errors.New("networks: " + daemonErr.Error()))
	_, err := invokeWrite(spy, "system prune", nil)
	want := "system prune failed: networks: a prune operation is already running; " +
		"completed steps: prune: containers 2, images 1 — reclaimed 3.0 MB"
	if e := Classify(err); e.Code != CodeInternal || e.Message != want {
		t.Errorf("partial system prune: %+v", e)
	}

	spy = (&pruneSpy{FakeBackend: docker.NewFakeBackend()}).withErr(errors.New("system prune: ssh: connection refused"))
	if e := Classify(func() error { _, err := invokeWrite(spy, "system prune", nil); return err }()); e.Message != "system prune failed: system prune: ssh: connection refused" {
		t.Errorf("no summary: %+v", e)
	}
}
