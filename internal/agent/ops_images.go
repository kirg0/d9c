package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kirg0/d9c/internal/docker"
)

// Registry credentials for `image push` come only from the environment, like
// the SSH password: an agent never types or stores them.
const (
	envRegistryUser     = "D9C_REGISTRY_USERNAME"
	envRegistryPassword = "D9C_REGISTRY_PASSWORD"
)

// resolveImage finds the image ref names among list: an exact tag (a bare
// repository means its :latest tag), an exact ID, or a unique ID prefix (with
// or without "sha256:").
func resolveImage(list []docker.Image, ref string) (docker.Image, *Error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return docker.Image{}, Errorf(CodeUsage, "empty image reference")
	}
	tagged := ref
	if !strings.Contains(lastPathElem(ref), ":") && !strings.Contains(ref, "@") {
		tagged = ref + ":latest"
	}
	for _, img := range list {
		for _, t := range imageTags(img) {
			if t == ref || t == tagged {
				return img, nil
			}
		}
	}
	id := strings.TrimPrefix(ref, "sha256:")
	var hits []docker.Image
	for _, img := range list {
		if img.ID == id {
			return img, nil
		}
		if strings.HasPrefix(img.ID, id) || (len(id) > len(img.ID) && strings.HasPrefix(id, img.ID)) {
			hits = append(hits, img)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return docker.Image{}, Errorf(CodeNotFound, "no such image: %s", ref).
			WithHint("list the images with `d9c images`")
	default:
		return docker.Image{}, Errorf(CodeUsage, "image prefix %q is ambiguous (%d images)", ref, len(hits)).
			WithHint("use a longer ID prefix or a repository:tag")
	}
}

// lastPathElem is the part of an image reference after its last "/", where a
// ":" means a tag rather than a registry port.
func lastPathElem(ref string) string {
	return ref[strings.LastIndex(ref, "/")+1:]
}

// imageTags splits the "a:1, b:2" tag list of an image; untagged images have
// none.
func imageTags(img docker.Image) []string {
	var out []string
	for t := range strings.SplitSeq(img.Tags, ",") {
		if t = strings.TrimSpace(t); t != "" && !strings.Contains(t, "<none>") {
			out = append(out, t)
		}
	}
	return out
}

// imageLabel names an image in plan steps: "nginx:1.25 (a08c488a9779, 187 MB)".
func imageLabel(img docker.Image) string {
	name := img.Tags
	if len(imageTags(img)) == 0 {
		name = "untagged image"
	}
	return fmt.Sprintf("%s (%s, %s)", name, img.ID, img.Size)
}

// lookupImage lists the images and resolves ref.
func lookupImage(env *Env, op, ref string) (docker.Image, error) {
	if env.Backend == nil {
		return docker.Image{}, errNoBackend(op)
	}
	list, err := env.Backend.ListImages()
	if err != nil {
		return docker.Image{}, err
	}
	img, e := resolveImage(list, ref)
	if e != nil {
		return docker.Image{}, e
	}
	return img, nil
}

// usersOf lists the containers (all states) created from img.
func usersOf(b docker.Backend, img docker.Image) ([]string, error) {
	list, err := b.ListContainers(true)
	if err != nil {
		return nil, err
	}
	tags := imageTags(img)
	var out []string
	for _, c := range list {
		if c.Image == img.ID || strings.HasPrefix(strings.TrimPrefix(c.Image, "sha256:"), img.ID) || containsTag(tags, c.Image) {
			out = append(out, c.Name)
		}
	}
	return out, nil
}

func containsTag(tags []string, image string) bool {
	if !strings.Contains(lastPathElem(image), ":") {
		image += ":latest"
	}
	return slices.Contains(tags, image)
}

// HistoryLayer is one layer of `image history`, newest first.
type HistoryLayer struct {
	Created string `json:"created"`
	Size    string `json:"size"`
	Command string `json:"command"`
}

// ImageHistoryData is the `image history` output. Engines whose history is
// not in Docker's layout return it as text in Report instead of Layers.
type ImageHistoryData struct {
	Image  string         `json:"image"`
	Layers []HistoryLayer `json:"layers"`
	Report string         `json:"report,omitempty"`
}

// ItemsKey implements Lister: the layers are the items.
func (ImageHistoryData) ItemsKey() string { return "layers" }

// historyLineRE is one line of the Docker backend's history text:
// "2006-01-02 15:04  <size>  <command>".
var historyLineRE = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2})\s{2,}(\S+(?: \S+)?)\s{2,}(.*)$`)

// parseHistory splits the history text into layers, or returns ok=false when
// a line does not follow the Docker layout.
func parseHistory(text string) ([]HistoryLayer, bool) {
	layers := []HistoryLayer{}
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		m := historyLineRE.FindStringSubmatch(l)
		if m == nil {
			return nil, false
		}
		layers = append(layers, HistoryLayer{Created: m[1], Size: m[2], Command: m[3]})
	}
	return layers, true
}

func imageHistoryOp() Op {
	return Op{
		Name:    "image history",
		Summary: "Show the layers of an image: when each was created, its size and the build step that made it.",
		Args: Object(map[string]*Schema{
			"image": String("Image ID, ID prefix or repository:tag."),
		}, "image"),
		Tier:      TierRead,
		Untrusted: true,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			img, err := lookupImage(env, "image history", args.String("image"))
			if err != nil {
				return Result{}, err
			}
			res, err := env.Backend.ImageHistory(img.ID)
			if err != nil {
				return Result{}, err
			}
			out := ImageHistoryData{Image: args.String("image"), Layers: []HistoryLayer{}}
			if layers, ok := parseHistory(res.RawYAML); ok {
				out.Layers = layers
			} else {
				out.Report = strings.TrimSpace(res.RawYAML)
			}
			return Result{Data: out}, nil
		},
	}
}

// ImageTagData is the `image tag` output.
type ImageTagData struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Target string `json:"target"`
}

func imageTagOp() Op {
	return Op{
		Name:    "image tag",
		Summary: "Add a repository:tag to an existing image (an existing tag of that name moves to it).",
		Args: Object(map[string]*Schema{
			"source": String("Image ID, ID prefix or repository:tag to tag."),
			"target": String("New reference, e.g. registry.example.com/team/app:1.4."),
		}, "source", "target"),
		Tier: TierSafe,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			img, target, err := checkImageTag(env, args)
			if err != nil {
				return Result{}, err
			}
			if err := env.Backend.TagImage(args.String("source"), target); err != nil {
				return Result{}, err
			}
			return Result{Data: ImageTagData{Source: args.String("source"), ID: img.ID, Target: target}}, nil
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			img, target, err := checkImageTag(env, args)
			if err != nil {
				return nil, err
			}
			step := "tag " + imageLabel(img) + " as " + target
			if list, err := env.Backend.ListImages(); err == nil {
				if cur, e := resolveImage(list, target); e == nil && cur.ID != img.ID {
					step += "; the tag moves off image " + cur.ID
				}
			}
			return []string{step}, nil
		},
	}
}

// checkImageTag resolves the source image and validates the target reference.
func checkImageTag(env *Env, args Args) (docker.Image, string, error) {
	target := strings.TrimSpace(args.String("target"))
	if target == "" || strings.ContainsAny(target, " \t") {
		return docker.Image{}, "", Errorf(CodeUsage, "image tag: invalid target reference %q", target)
	}
	img, err := lookupImage(env, "image tag", args.String("source"))
	return img, target, err
}

// ImageActionData is the output of `image push` / `image build`: the engine's
// progress lines (the newest maxProgressLines of them).
type ImageActionData struct {
	Action string   `json:"action"`
	Image  string   `json:"image"`
	Output []string `json:"output"`
}

// ItemsKey implements Lister: the progress lines are the items.
func (ImageActionData) ItemsKey() string { return "output" }

// imageResult turns a drained image stream into the operation result.
func imageResult(p progress, action, image string) (Result, error) {
	r, err := p.result(action, image)
	if err != nil {
		return r, err
	}
	r.Data = ImageActionData{Action: action, Image: image, Output: p.lines}
	return r, nil
}

// registryAuth reads the push credentials for ref from the environment.
func registryAuth(ref string) docker.RegistryAuth {
	return docker.RegistryAuth{
		Registry: docker.RegistryFromRef(ref),
		Username: os.Getenv(envRegistryUser),
		Password: os.Getenv(envRegistryPassword),
	}
}

func imagePushOp() Op {
	return Op{
		Name: "image push",
		Summary: "Push an image to its registry. Credentials come from " + envRegistryUser + "/" + envRegistryPassword +
			" when set; otherwise the push is anonymous.",
		Args: Object(map[string]*Schema{
			"image": String("repository:tag to push; the registry is the host part of it (Docker Hub without one)."),
		}, "image"),
		Tier: TierSafe,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			ref := strings.TrimSpace(args.String("image"))
			if _, err := lookupImage(env, "image push", ref); err != nil {
				return Result{}, err
			}
			env.report("pushing " + ref)
			ch, stop, err := env.Backend.PushImage(ref, registryAuth(ref))
			if err != nil {
				return Result{}, err
			}
			prog, err := collectProgress(ctx, ch, stop, env.report)
			if err != nil {
				return Result{}, err
			}
			return imageResult(prog, "image push", ref)
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			ref := strings.TrimSpace(args.String("image"))
			img, err := lookupImage(env, "image push", ref)
			if err != nil {
				return nil, err
			}
			auth := registryAuth(ref)
			reg, who := auth.Registry, "anonymously"
			if reg == "" {
				reg = "docker.io"
			}
			if auth.Username != "" {
				who = "as " + auth.Username
			}
			return []string{fmt.Sprintf("push %s to %s %s", imageLabel(img), reg, who)}, nil
		},
	}
}

// checkBuildContext fails unless dir is an existing local directory with a
// Dockerfile, and returns its absolute path.
func checkBuildContext(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", Errorf(CodeUsage, "image build: bad context directory %q", dir)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return "", Errorf(CodeNotFound, "build context %q is not a directory", dir).
			WithHint("the path is local to the machine running d9c; its files are sent to the engine")
	}
	if _, err := os.Stat(filepath.Join(abs, "Dockerfile")); err != nil {
		return "", Errorf(CodeNotFound, "no Dockerfile in %s", abs)
	}
	return abs, nil
}

func imageBuildOp() Op {
	return Op{
		Name: "image build",
		Summary: "Build an image from the Dockerfile in a local directory; the directory is sent to the engine as the " +
			"build context, and the Dockerfile's RUN steps execute on the host.",
		Args: Object(map[string]*Schema{
			"dir": String("Local directory holding the Dockerfile and the build context."),
			"tag": String("repository:tag for the new image; untagged when omitted.").WithShort("t"),
		}, "dir"),
		Tier:      TierSafe,
		Untrusted: true,
		Handler: func(ctx context.Context, env *Env, args Args) (Result, error) {
			if env.Backend == nil {
				return Result{}, errNoBackend("image build")
			}
			dir, err := checkBuildContext(args.String("dir"))
			if err != nil {
				return Result{}, err
			}
			tag := strings.TrimSpace(args.String("tag"))
			env.report("building " + dir)
			ch, stop, err := env.Backend.BuildImage(dir, tag)
			if err != nil {
				return Result{}, err
			}
			prog, err := collectProgress(ctx, ch, stop, env.report)
			if err != nil {
				return Result{}, err
			}
			if tag == "" {
				tag = "untagged"
			}
			return imageResult(prog, "image build", tag)
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			dir, err := checkBuildContext(args.String("dir"))
			if err != nil {
				return nil, err
			}
			steps := []string{"send " + dir + " to the engine as the build context and run its Dockerfile"}
			if tag := strings.TrimSpace(args.String("tag")); tag != "" {
				steps = append(steps, "tag the result as "+tag)
			}
			return steps, nil
		},
	}
}

// ImageRemoveData is the `image rm` output.
type ImageRemoveData struct {
	Image string `json:"image"`
	ID    string `json:"id"`
}

func imageRmOp() Op {
	return Op{
		Name: "image rm",
		Summary: "Remove an image, or one tag of it when named by repository:tag and other tags remain. " +
			"Refuses an image a container uses unless --force.",
		Args: Object(map[string]*Schema{
			"image": String("Image ID, ID prefix or repository:tag."),
			"force": Boolean("Remove even when a container uses the image.").WithShort("f"),
		}, "image"),
		Tier: TierDestructive,
		Handler: func(_ context.Context, env *Env, args Args) (Result, error) {
			ref := strings.TrimSpace(args.String("image"))
			img, err := lookupImage(env, "image rm", ref)
			if err != nil {
				return Result{}, err
			}
			if err := env.Backend.RemoveImage(ref, args.Bool("force")); err != nil {
				return Result{}, err
			}
			return Result{Data: ImageRemoveData{Image: ref, ID: img.ID}}, nil
		},
		Plan: func(_ context.Context, env *Env, args Args) ([]string, error) {
			ref := strings.TrimSpace(args.String("image"))
			img, err := lookupImage(env, "image rm", ref)
			if err != nil {
				return nil, err
			}
			step := "remove " + imageLabel(img)
			if tags := imageTags(img); len(tags) > 1 && ref != img.ID {
				step = "untag " + ref + " from image " + img.ID + " (its other tags stay)"
			}
			users, err := usersOf(env.Backend, img)
			if err != nil {
				return nil, err
			}
			steps := []string{step}
			if len(users) > 0 {
				use := "used by containers: " + strings.Join(users, ", ")
				if !args.Bool("force") {
					use += " — the engine refuses without --force"
				}
				steps = append(steps, use)
			}
			return steps, nil
		},
	}
}
