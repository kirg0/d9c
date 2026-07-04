package docker

import (
	"errors"
	"strings"
	"testing"
)

// TestDisconnectedBackend walks every Backend method and checks each one fails
// with the "not connected" error (or the documented neutral value).
func TestDisconnectedBackend(t *testing.T) {
	b := NewDisconnected(nil)

	checks := []struct {
		name string
		err  func() error
	}{
		{"ListContainers", func() error { _, err := b.ListContainers(true); return err }},
		{"InspectContainer", func() error { _, err := b.InspectContainer("id"); return err }},
		{"StartContainer", func() error { return b.StartContainer("id") }},
		{"StopContainer", func() error { return b.StopContainer("id") }},
		{"RestartContainer", func() error { return b.RestartContainer("id") }},
		{"RemoveContainer", func() error { return b.RemoveContainer("id", true) }},
		{"KillContainer", func() error { return b.KillContainer("id", "KILL") }},
		{"ContainerLogs", func() error { _, _, err := b.ContainerLogs("id", LogOptions{}); return err }},
		{"ContainerStats", func() error { _, err := b.ContainerStats([]string{"id"}); return err }},
		{"ExecInteractive", func() error { _, err := b.ExecInteractive("id", nil); return err }},
		{"RunContainer", func() error { return b.RunContainer(RunOptions{}) }},
		{"RunInteractive", func() error { _, err := b.RunInteractive(ExecRunOptions{}); return err }},
		{"ListPath", func() error { _, err := b.ListPath("id", "/"); return err }},
		{"CopyFromContainer", func() error { return b.CopyFromContainer("id", "/a", "b") }},
		{"CopyToContainer", func() error { return b.CopyToContainer("id", "a", "/b") }},
		{"ListImages", func() error { _, err := b.ListImages(); return err }},
		{"InspectImage", func() error { _, err := b.InspectImage("id"); return err }},
		{"RemoveImage", func() error { return b.RemoveImage("id", false) }},
		{"TagImage", func() error { return b.TagImage("a", "b") }},
		{"PushImage", func() error { _, _, err := b.PushImage("ref", RegistryAuth{}); return err }},
		{"BuildImage", func() error { _, _, err := b.BuildImage(".", "t"); return err }},
		{"ImageHistory", func() error { _, err := b.ImageHistory("id"); return err }},
		{"ListNetworks", func() error { _, err := b.ListNetworks(); return err }},
		{"InspectNetwork", func() error { _, err := b.InspectNetwork("id"); return err }},
		{"RemoveNetwork", func() error { return b.RemoveNetwork("id") }},
		{"CreateNetwork", func() error { return b.CreateNetwork(NetworkCreateOptions{}) }},
		{"ListVolumes", func() error { _, err := b.ListVolumes(); return err }},
		{"InspectVolume", func() error { _, err := b.InspectVolume("id"); return err }},
		{"RemoveVolume", func() error { return b.RemoveVolume("id") }},
		{"CreateVolume", func() error { return b.CreateVolume(VolumeCreateOptions{}) }},
		{"PruneVolumes", func() error { _, err := b.PruneVolumes(); return err }},
		{"PullImage", func() error { return b.PullImage("ref") }},
		{"PruneImages", func() error { _, err := b.PruneImages(); return err }},
		{"ListComposeProjects", func() error { _, err := b.ListComposeProjects(); return err }},
		{"ListComposeContainers", func() error { _, err := b.ListComposeContainers("p"); return err }},
		{"InspectComposeProject", func() error { _, err := b.InspectComposeProject("p"); return err }},
		{"ComposeLogs", func() error { _, _, err := b.ComposeLogs("p", LogOptions{}); return err }},
		{"ComposeUp", func() error { _, _, err := b.ComposeUp("p"); return err }},
		{"ComposePull", func() error { _, _, err := b.ComposePull("p"); return err }},
		{"ComposeDown", func() error { _, _, err := b.ComposeDown("p"); return err }},
		{"ComposeConfig", func() error { _, err := b.ComposeConfig("p"); return err }},
		{"ReadComposeFile", func() error { _, _, err := b.ReadComposeFile("p"); return err }},
		{"WriteComposeFile", func() error { return b.WriteComposeFile("p", "x") }},
		{"CreateComposeFile", func() error { _, _, err := b.CreateComposeFile("d", "x"); return err }},
		{"BackupComposeProject", func() error { _, err := b.BackupComposeProject("p"); return err }},
		{"RestoreComposeProject", func() error { _, _, err := b.RestoreComposeProject("p", "f"); return err }},
		{"SystemDF", func() error { _, err := b.SystemDF(); return err }},
		{"SystemPrune", func() error { _, err := b.SystemPrune(); return err }},
		{"Ping", func() error { return b.Ping() }},
		{"Info", func() error { _, err := b.Info(); return err }},
		{"ComposeStart", func() error { return b.ComposeStart("p") }},
		{"ComposeStop", func() error { return b.ComposeStop("p") }},
		{"ComposeRestart", func() error { return b.ComposeRestart("p") }},
		{"ComposePause", func() error { return b.ComposePause("p") }},
		{"ComposeUnpause", func() error { return b.ComposeUnpause("p") }},
		{"ComposeRemove", func() error { return b.ComposeRemove("p") }},
		{"Events", func() error { _, _, err := b.Events(); return err }},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			err := c.err()
			if err == nil || !strings.Contains(err.Error(), "not connected") {
				t.Errorf("%s: err = %v, want 'not connected'", c.name, err)
			}
		})
	}

	if b.Runtime() != RuntimeUnknown {
		t.Error("Runtime should be unknown while disconnected")
	}
	if b.SupportsHostCompose() {
		t.Error("SupportsHostCompose should be false while disconnected")
	}
	b.Close() // must be a no-op
}

func TestDisconnectedWrapsReason(t *testing.T) {
	cause := errors.New("dial tcp: refused")
	b := NewDisconnected(cause)
	err := b.Ping()
	if !errors.Is(err, cause) {
		t.Errorf("Ping err = %v, want it to wrap the reason", err)
	}
}
