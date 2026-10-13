package stack

import (
	"slices"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
)

// kept is a container that runs another image than the one this release
// pins: a version the user chose, or the version an earlier release pinned.
// Start runs it by name as it is, and update asks whether to replace it.
type kept struct {
	app     *app.App
	service string
	// running and shipped are the versions of the container's image and of
	// the pinned image.
	running, shipped string
}

func (k kept) describe() string {
	return k.app.Title + " " + k.running + " (this release ships " + k.shipped + ")"
}

// keptContainers lists the containers of apps that run another image than
// the pinned one. Services without a container or without a pinned image
// are not listed: Compose creates and starts them.
func (ses *session) keptContainers(apps []*app.App) ([]kept, error) {
	var list []kept
	for _, a := range apps {
		for _, image := range a.Images {
			pinned := app.PinnedDigest(a.Compose, image.Service)
			if pinned == "" {
				continue
			}
			found, err := ses.runtime.Image(image.Service)
			if err != nil {
				return nil, err
			}
			if len(found.Digests) == 0 && found.Label == "" {
				continue
			}
			if slices.Contains(found.Digests, pinned) {
				continue
			}
			running := app.Detect(image, found, nil).Version
			if running == "" {
				running = found.Label
			}
			if running == "" {
				running = "an unknown version"
			}
			shipped := image.Digests[pinned]
			if shipped == "" {
				shipped = pinned
			}
			list = append(list, kept{app: a, service: image.Service, running: running, shipped: shipped})
		}
	}
	return list, nil
}

// keptServices lists the services of list.
func keptServices(list []kept) []string {
	var services []string
	for _, k := range list {
		services = append(services, k.service)
	}
	return services
}

// reportKept tells the user which containers start as they are.
func (ses *session) reportKept(list []kept) {
	if len(list) == 0 {
		return
	}
	var names []string
	for _, k := range list {
		names = append(names, k.describe())
	}
	ses.UI.Say("Started as they are, because they run another version than this release: " + strings.Join(names, ", ") + ". Run '" + Command + " update' to choose whether to replace them.")
}
