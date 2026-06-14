package docker

import "fmt"

// disconnectedBackend is a stand-in Backend used when no Docker connection is
// established. Every operation fails with a "not connected" error, letting the
// UI start in the hosts view (to pick or add a host) without nil-checks in the
// model. A successful :connect replaces it with a real backend.
type disconnectedBackend struct{ reason error }

// NewDisconnected returns a Backend whose every operation reports that no
// connection is available, wrapping reason when provided.
func NewDisconnected(reason error) Backend { return &disconnectedBackend{reason: reason} }

func (b *disconnectedBackend) err() error {
	if b.reason != nil {
		return fmt.Errorf("not connected: %w", b.reason)
	}
	return fmt.Errorf("not connected — select a host in the hosts view")
}

func (b *disconnectedBackend) ListContainers(bool) ([]Container, error) { return nil, b.err() }
func (b *disconnectedBackend) InspectContainer(string) (*InspectResult, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) StartContainer(string) error        { return b.err() }
func (b *disconnectedBackend) StopContainer(string) error         { return b.err() }
func (b *disconnectedBackend) RestartContainer(string) error      { return b.err() }
func (b *disconnectedBackend) RemoveContainer(string, bool) error { return b.err() }
func (b *disconnectedBackend) KillContainer(string, string) error { return b.err() }
func (b *disconnectedBackend) ContainerLogs(string, LogOptions) (<-chan string, func(), error) {
	return nil, nil, b.err()
}
func (b *disconnectedBackend) ContainerStats([]string) (map[string]ContainerStats, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) ExecInteractive(string, []string) (ExecSession, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) RunContainer(RunOptions) error { return b.err() }
func (b *disconnectedBackend) RunInteractive(ExecRunOptions) (ExecSession, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) ListPath(string, string) ([]FileEntry, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) CopyFromContainer(string, string, string) error { return b.err() }
func (b *disconnectedBackend) CopyToContainer(string, string, string) error   { return b.err() }
func (b *disconnectedBackend) ListImages() ([]Image, error)                   { return nil, b.err() }
func (b *disconnectedBackend) InspectImage(string) (*InspectResult, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) RemoveImage(string, bool) error { return b.err() }
func (b *disconnectedBackend) TagImage(string, string) error  { return b.err() }
func (b *disconnectedBackend) PushImage(string, RegistryAuth) (<-chan string, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) BuildImage(string, string) (<-chan string, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) ImageHistory(string) (*InspectResult, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) ListNetworks() ([]Network, error) { return nil, b.err() }
func (b *disconnectedBackend) InspectNetwork(string) (*InspectResult, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) RemoveNetwork(string) error               { return b.err() }
func (b *disconnectedBackend) CreateNetwork(NetworkCreateOptions) error { return b.err() }
func (b *disconnectedBackend) ListVolumes() ([]Volume, error)           { return nil, b.err() }
func (b *disconnectedBackend) InspectVolume(string) (*InspectResult, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) RemoveVolume(string) error              { return b.err() }
func (b *disconnectedBackend) CreateVolume(VolumeCreateOptions) error { return b.err() }
func (b *disconnectedBackend) PruneVolumes() (int, error)             { return 0, b.err() }
func (b *disconnectedBackend) PullImage(string) error                 { return b.err() }
func (b *disconnectedBackend) PruneImages() (int, error)              { return 0, b.err() }
func (b *disconnectedBackend) ListComposeProjects() ([]ComposeProject, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) ListComposeContainers(string) ([]Container, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) InspectComposeProject(string) (*InspectResult, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) ComposeLogs(string, LogOptions) (<-chan string, func(), error) {
	return nil, nil, b.err()
}
func (b *disconnectedBackend) ComposeUp(string) (<-chan string, error)   { return nil, b.err() }
func (b *disconnectedBackend) ComposePull(string) (<-chan string, error) { return nil, b.err() }
func (b *disconnectedBackend) ComposeDown(string) (<-chan string, error) { return nil, b.err() }
func (b *disconnectedBackend) ComposeConfig(string) (string, error) {
	return "", b.err()
}
func (b *disconnectedBackend) ReadComposeFile(string) (string, string, error) {
	return "", "", b.err()
}
func (b *disconnectedBackend) WriteComposeFile(string, string) error { return b.err() }
func (b *disconnectedBackend) CreateComposeFile(string, string) (<-chan string, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) BackupComposeProject(string) (string, error) {
	return "", b.err()
}
func (b *disconnectedBackend) RestoreComposeProject(string, string) (<-chan string, error) {
	return nil, b.err()
}
func (b *disconnectedBackend) SystemDF() (*InspectResult, error)      { return nil, b.err() }
func (b *disconnectedBackend) SystemPrune() (string, error)           { return "", b.err() }
func (b *disconnectedBackend) Ping() error                            { return b.err() }
func (b *disconnectedBackend) Info() (HostSummary, error)             { return HostSummary{}, b.err() }
func (b *disconnectedBackend) ComposeStart(string) error              { return b.err() }
func (b *disconnectedBackend) ComposeStop(string) error               { return b.err() }
func (b *disconnectedBackend) ComposeRestart(string) error            { return b.err() }
func (b *disconnectedBackend) ComposePause(string) error              { return b.err() }
func (b *disconnectedBackend) ComposeUnpause(string) error            { return b.err() }
func (b *disconnectedBackend) ComposeRemove(string) error             { return b.err() }
func (b *disconnectedBackend) Events() (<-chan string, func(), error) { return nil, nil, b.err() }
func (b *disconnectedBackend) Close()                                 {}
