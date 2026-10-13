// Package runtime runs the stack's containers. Runtime is the role every
// container operation goes through; Docker Compose is its implementation.
package runtime

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/inayayousfi/selfnook/internal/shell"
)

// Runtime starts, stops, and inspects the stack's services.
type Runtime interface {
	// Ready waits until the runtime can run containers.
	Ready() error
	// Reachable reports, without waiting, whether the runtime answers.
	Reachable() (bool, error)
	// Prepare brings the runtime's own resources, such as the network, into
	// the state the stack expects, before services start.
	Prepare() error
	// Check validates the stack's definition without running anything.
	Check() error
	// Up starts services without their dependencies. RemoveOrphans also
	// removes containers of services the definition no longer lists.
	Up(services []string, removeOrphans bool) error
	// Start starts the existing containers of services by name, as they are.
	Start(services ...string) error
	// Stop stops the containers of services by name and keeps them.
	// Services without a container are skipped.
	Stop(services ...string) error
	// Remove deletes the containers of services. Services without a
	// container are skipped.
	Remove(services ...string) error
	Restart(service string) error
	// Status shows every service's state.
	Status() error
	// RunOnce runs a service's command once and removes its container.
	RunOnce(service string, args ...string) error
	// Exec runs a command inside a running service and collects its output.
	Exec(service string, args ...string) (shell.Result, error)
	// Health is the health check state of a service, such as "healthy".
	Health(service string) (string, error)
	// State is the state of a service's container, such as "running" or
	// "exited". It is empty when the container does not exist.
	State(service string) (string, error)
	// Logs are the last lines a service's container printed, oldest first.
	Logs(service string, lines int) ([]string, error)
	// Image describes the image a service's container runs. It is empty
	// when the container does not exist.
	Image(service string) (Image, error)
}

// Image identifies the image a container runs.
type Image struct {
	// Digests are the registry digests it was pulled by, such as sha256:12ab....
	Digests []string
	// Label is the image's org.opencontainers.image.version label.
	Label string
}

// Options are what a runtime needs from the stack.
type Options struct {
	Shell shell.Shell
	// Env returns the values the services' definitions read. It is called
	// for every command that reads them, so values saved in between are used.
	Env func() map[string]string
	// Sleep waits between readiness checks.
	Sleep func(time.Duration)
}

// Factory builds a runtime.
type Factory struct {
	// Name identifies the runtime in the state file.
	Name string
	New  func(Options) Runtime
}

// Project is the Compose project, network, and container name prefix.
const Project = "selfnook"

// NetworkSubnet is the stack network's address range. Docker numbers
// containers without a fixed address from AutomaticRange only, so the rest
// of the subnet holds fixed addresses that no other container can take.
const (
	NetworkSubnet  = "172.31.250.0/24"
	AutomaticRange = "172.31.250.128/25"
)

// Container is the name of the container that runs service.
func Container(service string) string { return Project + "-" + service }

// Docker runs the stack with Docker Compose.
var Docker = Factory{
	Name: "docker",
	New: func(o Options) Runtime {
		if o.Sleep == nil {
			o.Sleep = time.Sleep
		}
		return &docker{shell: o.Shell, env: o.Env, sleep: o.Sleep}
	},
}

type docker struct {
	shell shell.Shell
	env   func() map[string]string
	sleep func(time.Duration)
}

func (d *docker) compose(args ...string) error {
	_, err := d.shell.Run(shell.Cmd{Args: append([]string{"docker", "compose"}, args...), Check: true, Env: d.env()})
	return err
}

// capture runs a docker command that reads no Compose file, so it needs
// none of the stack's values.
func (d *docker) capture(args ...string) (shell.Result, error) {
	return d.shell.Run(shell.Cmd{Args: args, Capture: true})
}

// dockerChecks is how many times Ready checks, 2 seconds apart: five minutes.
const dockerChecks = 150

// Ready waits while Docker is starting. Docker refusing access is not
// fixed by waiting, so it fails at once with Docker's own message.
func (d *docker) Ready() error {
	if _, ok := shell.Found("docker"); !ok {
		return errors.New("Docker is not installed. Follow https://docs.docker.com/get-docker/.")
	}
	last := ""
	for range dockerChecks {
		info, err := d.capture("docker", "info")
		if err != nil {
			return err
		}
		last = firstLine(info.Stderr)
		if strings.Contains(strings.ToLower(info.Stderr), "permission denied") {
			return errors.New("Docker refused access: " + last)
		}
		if info.Code == 0 {
			version, err := d.capture("docker", "compose", "version")
			if err != nil {
				return err
			}
			if version.Code == 0 {
				return nil
			}
			return errors.New("Docker Compose is unavailable. Follow https://docs.docker.com/compose/install/.")
		}
		d.sleep(2 * time.Second)
	}
	if last == "" {
		return errors.New("Docker did not become ready within five minutes.")
	}
	return errors.New("Docker did not become ready within five minutes. Its last message: " + last)
}

func (d *docker) Reachable() (bool, error) {
	if _, ok := shell.Found("docker"); !ok {
		return false, nil
	}
	info, err := d.capture("docker", "info")
	return err == nil && info.Code == 0, err
}

// firstLine is the first non-empty line of text.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// retiredProject is the Compose project name that earlier versions used.
const retiredProject = "media-stack"

// Prepare removes the containers and network of an install made under the
// earlier project name, which would otherwise hold the ports and the address
// range, then removes the stack when its network uses other address ranges,
// so Compose recreates it with the expected one. Configuration and media
// folders are kept.
func (d *docker) Prepare() error {
	containers, err := d.capture("docker", "ps", "--all", "--quiet", "--filter", "label=com.docker.compose.project="+retiredProject)
	if err != nil {
		return err
	}
	network, err := d.capture("docker", "network", "inspect", retiredProject)
	if err != nil {
		return err
	}
	if strings.TrimSpace(containers.Stdout) != "" || network.Code == 0 {
		if err := d.compose("--project-name", retiredProject, "down", "--remove-orphans"); err != nil {
			return err
		}
		if _, err := d.capture("docker", "image", "rm", retiredProject+"/vpn-country"); err != nil {
			return err
		}
	}
	result, err := d.capture("docker", "network", "inspect", Project, "--format", "{{range .IPAM.Config}}{{.Subnet}} {{.IPRange}} {{end}}")
	if err != nil {
		return err
	}
	if result.Code == 0 && strings.TrimSpace(result.Stdout) != NetworkSubnet+" "+AutomaticRange {
		return d.compose("down", "--remove-orphans")
	}
	return nil
}

func (d *docker) Check() error { return d.compose("config", "--quiet") }

func (d *docker) Up(services []string, removeOrphans bool) error {
	args := []string{"up", "-d", "--no-deps"}
	if removeOrphans {
		args = append(args, "--remove-orphans")
	}
	return d.compose(append(args, services...)...)
}

func (d *docker) Start(services ...string) error {
	return d.byName("start", services)
}

func (d *docker) Stop(services ...string) error {
	return d.byName("stop", services)
}

func (d *docker) Remove(services ...string) error {
	return d.byName("rm", services, "--force")
}

// byName runs docker command on the containers of services that exist.
// Naming containers, rather than Compose services, also reaches a container
// that another tool recreated under the same name.
func (d *docker) byName(command string, services []string, flags ...string) error {
	args := append([]string{"docker", command}, flags...)
	found := false
	for _, service := range services {
		state, err := d.State(service)
		if err != nil {
			return err
		}
		if state != "" {
			args = append(args, Container(service))
			found = true
		}
	}
	if !found {
		return nil
	}
	// Docker echoes each container name; only its error message is shown.
	result, err := d.capture(args...)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("Command '%s' failed: %s", strings.Join(args, " "), firstLine(result.Stderr))
	}
	return nil
}

func (d *docker) Restart(service string) error { return d.compose("restart", service) }

func (d *docker) Status() error { return d.compose("ps") }

func (d *docker) RunOnce(service string, args ...string) error {
	return d.compose(append([]string{"run", "--rm", service}, args...)...)
}

func (d *docker) Exec(service string, args ...string) (shell.Result, error) {
	return d.capture(append([]string{"docker", "exec", Container(service)}, args...)...)
}

func (d *docker) Health(service string) (string, error) {
	result, err := d.capture("docker", "inspect", Container(service), "--format", "{{.State.Health.Status}}")
	if err != nil || result.Code != 0 {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (d *docker) State(service string) (string, error) {
	result, err := d.capture("docker", "inspect", Container(service), "--format", "{{.State.Status}}")
	if err != nil || result.Code != 0 {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (d *docker) Logs(service string, lines int) ([]string, error) {
	result, err := d.capture("docker", "logs", "--tail", strconv.Itoa(lines), Container(service))
	if err != nil || result.Code != 0 {
		return nil, err
	}
	output := strings.TrimSpace(result.Stdout + result.Stderr)
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

func (d *docker) Image(service string) (Image, error) {
	id, err := d.capture("docker", "inspect", Container(service), "--format", "{{.Image}}")
	if err != nil || id.Code != 0 {
		return Image{}, err
	}
	result, err := d.capture("docker", "image", "inspect", strings.TrimSpace(id.Stdout), "--format",
		`{{range .RepoDigests}}{{.}} {{end}}|{{index .Config.Labels "org.opencontainers.image.version"}}`)
	if err != nil || result.Code != 0 {
		return Image{}, err
	}
	digests, label, _ := strings.Cut(strings.TrimSpace(result.Stdout), "|")
	image := Image{Label: strings.TrimSpace(label)}
	for _, reference := range strings.Fields(digests) {
		if _, digest, ok := strings.Cut(reference, "@"); ok {
			image.Digests = append(image.Digests, digest)
		}
	}
	return image, nil
}
