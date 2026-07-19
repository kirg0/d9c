package docker

import (
	"encoding/json"
	"fmt"
	"strings"

	"d9c/internal/i18n"

	"github.com/docker/go-units"
)

// ── images ──────────────────────────────────────────────────────────────────

// criImage is the subset of `crictl images -o json` we use.
type criImage struct {
	ID       string    `json:"id"`
	RepoTags []string  `json:"repoTags"`
	Size     criNumber `json:"size"`
}

// parseCRIImages decodes the `crictl images -o json` envelope.
func parseCRIImages(out string) ([]criImage, error) {
	var doc struct {
		Images []criImage `json:"images"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("parse crictl images: %w", err)
	}
	return doc.Images, nil
}

// ListImages lists CRI images. CRI reports no creation time, so Created stays
// zero; size arrives as raw bytes and is humanized for the table.
func (b *criBackend) ListImages() ([]Image, error) {
	out, err := b.run("images", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	rows, err := parseCRIImages(out)
	if err != nil {
		return nil, err
	}
	result := make([]Image, 0, len(rows))
	for _, r := range rows {
		result = append(result, Image{
			ID:   shortID(strings.TrimPrefix(r.ID, "sha256:")),
			Tags: criImageTags(r.RepoTags),
			Size: criImageSize(r.Size),
		})
	}
	return result, nil
}

// criImageTags picks the display tag; untagged images collapse to "<none>".
func criImageTags(tags []string) string {
	if len(tags) == 0 {
		return "<none>"
	}
	return tags[0]
}

// criImageSize renders CRI's raw byte count ("187553280") as a human size.
func criImageSize(n criNumber) string {
	bytes := n.int64()
	if bytes <= 0 {
		return "-"
	}
	return units.HumanSize(float64(bytes))
}

// InspectImage renders `crictl inspecti` as YAML.
func (b *criBackend) InspectImage(id string) (*InspectResult, error) {
	out, err := b.run("inspecti", id)
	if err != nil {
		return nil, fmt.Errorf("inspect image: %w", err)
	}
	y, err := jsonToYAML(out)
	if err != nil {
		return nil, fmt.Errorf("inspect image: %w", err)
	}
	return &InspectResult{Name: id, RawYAML: y}, nil
}

// RemoveImage removes an image. CRI's RemoveImage has no force flag (it must
// not fail on images used by stopped containers per spec), so force is ignored.
func (b *criBackend) RemoveImage(id string, _ bool) error {
	_, err := b.run("rmi", id)
	return friendlyImageRemoveErr(err)
}

func (b *criBackend) PullImage(ref string) error {
	if _, err := b.run("pull", ref); err != nil {
		return fmt.Errorf("pull image: %w", err)
	}
	return nil
}

// PruneImages removes all images not used by any container (`crictl rmi
// --prune`).
func (b *criBackend) PruneImages() (int, error) {
	out, err := b.run("rmi", "--prune")
	if err != nil {
		return 0, fmt.Errorf("prune images: %w", err)
	}
	return countDeleted(out), nil
}

func (b *criBackend) TagImage(string, string) error {
	return errCRIUnsupported(i18n.T("тегирование образов", "image tagging"))
}

func (b *criBackend) PushImage(string, RegistryAuth) (<-chan string, func(), error) {
	return nil, nil, errCRIUnsupported(i18n.T("push образов", "image push"))
}

func (b *criBackend) BuildImage(string, string) (<-chan string, func(), error) {
	return nil, nil, errCRIUnsupported(i18n.T("сборка образов", "image build"))
}

func (b *criBackend) ImageHistory(string) (*InspectResult, error) {
	return nil, errCRIUnsupported(i18n.T("история слоёв", "layer history"))
}

// ── networks / volumes (not modeled by CRI) ─────────────────────────────────

// ListNetworks returns an empty list: CRI delegates networking to CNI and
// exposes no network objects. The section degrades to an empty table instead
// of an error banner.
func (b *criBackend) ListNetworks() ([]Network, error) { return []Network{}, nil }

func (b *criBackend) InspectNetwork(string) (*InspectResult, error) {
	return nil, errCRIUnsupported(i18n.T("сети", "networks"))
}
func (b *criBackend) RemoveNetwork(string) error {
	return errCRIUnsupported(i18n.T("сети", "networks"))
}
func (b *criBackend) CreateNetwork(NetworkCreateOptions) error {
	return errCRIUnsupported(i18n.T("сети", "networks"))
}

// ListVolumes returns an empty list: CRI has no volume objects (mounts are
// declared per container by the orchestrator).
func (b *criBackend) ListVolumes() ([]Volume, error) { return []Volume{}, nil }

func (b *criBackend) InspectVolume(string) (*InspectResult, error) {
	return nil, errCRIUnsupported(i18n.T("тома", "volumes"))
}
func (b *criBackend) RemoveVolume(string) error {
	return errCRIUnsupported(i18n.T("тома", "volumes"))
}
func (b *criBackend) CreateVolume(VolumeCreateOptions) error {
	return errCRIUnsupported(i18n.T("тома", "volumes"))
}
func (b *criBackend) PruneVolumes() (int, error) {
	return 0, errCRIUnsupported(i18n.T("тома", "volumes"))
}
