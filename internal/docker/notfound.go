package docker

import (
	"regexp"
	"strings"

	"github.com/docker/docker/errdefs"
)

// notFoundPhrases are lower-case fragments the backends use to say the target
// object no longer exists: the Docker daemon ("No such container: x", "get x:
// no such volume"), nerdctl (same wording after normalizeNerdctlErr), crictl
// (gRPC NotFound / "could not find container"), the compose layer and the
// fake backend.
var notFoundPhrases = []string{
	"no such container",
	"no such image",
	"no such network",
	"no such volume",
	"no such compose project",
	"no such object",
	"no containers found for compose deployment",
	"code = notfound",
	"could not find container",
	"could not find pod",
	"id does not exist",
}

// notFoundObjectRe matches "network x not found" (Docker network rm) and the
// quoted nerdctl/crictl variants (`volume "x" not found`, `container "x" not found`).
var notFoundObjectRe = regexp.MustCompile(`\b(container|image|network|volume|pod|sandbox)\s+\S+\s+not found`)

// IsNotFound reports whether err says the targeted object no longer exists —
// typically because another client removed it after the table was last
// refreshed. It recognizes typed Docker errdefs.ErrNotFound (anywhere in the
// wrap chain) and the textual variants of the CLI-driven backends. Callers
// should only apply it to operations on an existing row: a pull of a missing
// image is also a daemon "not found", but means something else.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errdefs.IsNotFound(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, p := range notFoundPhrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return notFoundObjectRe.MatchString(msg)
}
