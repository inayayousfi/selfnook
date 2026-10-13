package app

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/runtime"
	"github.com/inayayousfi/selfnook/internal/state"
)

// CompareVersions orders dotted versions such as 4.0.19.2979 by their
// numbers, returning -1, 0, or 1. A part that is not a number compares as text.
func CompareVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(left), len(right)) {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		ln, lerr := strconv.Atoi(l)
		rn, rerr := strconv.Atoi(r)
		switch {
		case lerr == nil && rerr == nil && ln != rn:
			if ln < rn {
				return -1
			}
			return 1
		case (lerr != nil || rerr != nil) && l != r:
			if l < r {
				return -1
			}
			return 1
		}
	}
	return 0
}

var serviceLine = regexp.MustCompile(`^  ([A-Za-z0-9_.-]+):\s*$`)
var imageLine = regexp.MustCompile(`^\s+image:\s*\S+@(sha256:[0-9a-f]+)`)

// PinnedDigest returns the digest that compose.yaml in fragment pins for
// service, or "" when the service has no pinned image.
func PinnedDigest(fragment fs.FS, service string) string {
	if fragment == nil {
		return ""
	}
	content, err := fs.ReadFile(fragment, "compose.yaml")
	if err != nil {
		return ""
	}
	current := ""
	for _, line := range strings.Split(string(content), "\n") {
		if match := serviceLine.FindStringSubmatch(line); match != nil {
			current = match[1]
			continue
		}
		if current != service {
			continue
		}
		if match := imageLine.FindStringSubmatch(line); match != nil {
			return match[1]
		}
	}
	return ""
}

// PinnedVersion is what the program knows before a container runs: the
// version of the digest the Compose fragment pins.
func PinnedVersion(fragment fs.FS, image Image) state.Version {
	digest := PinnedDigest(fragment, image.Service)
	return state.Version{Version: image.Digests[digest], Source: state.FromDigest, Digest: digest}
}

// Detect finds the version of the image a running container uses: from its
// digest, then from its version label, then by calling ask, which is nil
// when the application cannot tell its version.
// The version stays empty when none of them tells it.
func Detect(image Image, found runtime.Image, ask func() (string, error)) state.Version {
	result := state.Version{Label: found.Label}
	if len(found.Digests) > 0 {
		result.Digest = found.Digests[0]
	}
	for _, digest := range found.Digests {
		if version, ok := image.Digests[digest]; ok {
			result.Digest, result.Version, result.Source = digest, version, state.FromDigest
			return result
		}
	}
	if version, ok := image.Labels[found.Label]; ok && found.Label != "" {
		result.Version, result.Source = version, state.FromLabel
		return result
	}
	if ask != nil {
		if version, err := ask(); err == nil && version != "" {
			result.Version, result.Source = version, state.FromApp
		}
	}
	return result
}

// AskVersion asks a running service for its version: it fetches url inside
// the container, with the given request headers, and reads the field named
// field from the JSON answer.
func AskVersion(e *Env, service, url, field string, headers ...string) (string, error) {
	result, err := e.Runtime.ExecInput(service, CurlInput(headers, ""), "curl", "-fsS", "--config", "-", url)
	if err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("%s did not answer its version request.", service)
	}
	var answer map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &answer); err != nil {
		return "", fmt.Errorf("%s answered its version request with unreadable data.", service)
	}
	version, _ := answer[field].(string)
	return version, nil
}
