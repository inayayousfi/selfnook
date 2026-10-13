// Package stack runs the commands: it walks the registry to set up, start,
// stop, and inspect every app.
package stack

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
	"github.com/inayayousfi/selfnook/internal/shell"
	"github.com/inayayousfi/selfnook/internal/state"
)

// Command is the program name shown in instructions.
const Command = "selfnook"

// runtimeRole is the name under which the state file records the runtime.
const runtimeRole = "runtime"

// Stack is one installation: the folder holding config/ and the generated
// Compose files, and the state file holding every saved value.
type Stack struct {
	Root string
	// StatePath is the state file.
	StatePath string
	Apps      []*app.App
	// Runtimes are the container runtimes, the first being the default.
	Runtimes []runtime.Factory
	Shell    shell.Shell
	UI       flow.UI
	// Program is the executable that automatic start runs.
	Program string
	// Sleep waits between checks. Tests replace it.
	Sleep func(time.Duration)
	// Fetch downloads a file. Nil downloads over HTTPS; tests replace it.
	Fetch func(url string) ([]byte, error)
}

// fetch downloads url with Fetch, or over HTTPS when Fetch is nil.
func (s *Stack) fetch(url string) ([]byte, error) {
	if s.Fetch != nil {
		return s.Fetch(url)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, response.Status)
	}
	return io.ReadAll(response.Body)
}

func (s *Stack) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

// session is one command's view of the installation: the saved state, the
// roles resolved from it, and the version each app runs.
type session struct {
	*Stack
	saved   *state.State
	runtime runtime.Runtime
	// versions holds each app's version for choosing its implementation:
	// the pinned one until its containers run, then the detected one.
	versions map[string]string
	warned   map[string]bool
	sets     map[string]*role.Set
	steps    bool
}

var errNoSetup = errors.New("Run '" + Command + " setup' first.")

// open reads the state file, moving an earlier .env into it first. found
// is false when neither existed.
func (s *Stack) open() (*session, bool, error) {
	saved, found, err := state.Read(s.StatePath)
	if err != nil {
		return nil, false, err
	}
	if !found {
		if saved, found, err = s.importEnv(); err != nil {
			return nil, false, err
		}
	}
	ses := &session{Stack: s, saved: saved, versions: map[string]string{}, warned: map[string]bool{}}
	for _, a := range s.Apps {
		ses.versions[a.Name] = a.Pinned()
	}
	factory := s.Runtimes[0]
	if name := saved.Roles[runtimeRole]; name != "" {
		index := slices.IndexFunc(s.Runtimes, func(f runtime.Factory) bool { return f.Name == name })
		if index < 0 {
			return nil, false, fmt.Errorf("The saved settings name an unknown container runtime: %s.", name)
		}
		factory = s.Runtimes[index]
	}
	saved.Roles[runtimeRole] = factory.Name
	ses.runtime = factory.New(runtime.Options{Shell: s.Shell, Env: ses.composeEnv, Sleep: s.sleep})
	return ses, found, nil
}

func (ses *session) save() error { return state.Write(ses.StatePath, ses.saved) }

// Done and Complete record guided setup steps in the state file.
func (ses *session) Done(step string) bool { return ses.saved.Done(step) }

func (ses *session) Complete(step string) error {
	ses.saved.Complete(step)
	return ses.save()
}

// roleNames lists every role an app provides, in registry order.
func (ses *session) roleNames() []role.Info {
	var infos []role.Info
	for _, a := range ses.Apps {
		for _, info := range a.Provides(ses.versions[a.Name]) {
			if !slices.ContainsFunc(infos, func(i role.Info) bool { return i.Name == info.Name }) {
				infos = append(infos, info)
			}
		}
	}
	return infos
}

func (ses *session) providers(name string) []*app.App {
	var found []*app.App
	for _, a := range ses.Apps {
		if slices.ContainsFunc(a.Provides(ses.versions[a.Name]), func(i role.Info) bool { return i.Name == name }) {
			found = append(found, a)
		}
	}
	return found
}

// assignRoles makes sure every role has the app that fills it. A role with
// one provider gets it. A role with several is asked when ask is set, and
// otherwise stops the command until setup chooses it.
func (ses *session) assignRoles(ask bool) error {
	for _, info := range ses.roleNames() {
		candidates := ses.providers(info.Name)
		if slices.ContainsFunc(candidates, func(a *app.App) bool { return a.Name == ses.saved.Roles[info.Name] }) {
			continue
		}
		if len(candidates) == 1 {
			ses.saved.Roles[info.Name] = candidates[0].Name
			continue
		}
		if !ask {
			return errNoSetup
		}
		if err := ses.chooseProvider(info, candidates); err != nil {
			return err
		}
	}
	return nil
}

// chooseProvider asks which app fills a role, listing each candidate's options.
func (ses *session) chooseProvider(info role.Info, candidates []*app.App) error {
	var options []flow.Option
	owners := map[string]*app.App{}
	for _, a := range candidates {
		for _, option := range a.Options {
			value := a.Name + "/" + option.Value
			owners[value] = a
			options = append(options, flow.Option{Value: value, Label: option.Label, Detail: option.Detail})
		}
	}
	answers, err := ses.UI.Ask(flow.Screen{
		Title:  info.Title,
		Fields: []flow.Field{{Key: "provider", Prompt: info.Prompt, Kind: flow.Choice, Default: options[0].Value, Options: options}},
	})
	if err != nil {
		return err
	}
	chosen := owners[answers["provider"]]
	ses.saved.Roles[info.Name] = chosen.Name
	if chosen.Choose != nil {
		_, option, _ := strings.Cut(answers["provider"], "/")
		chosen.Choose(app.ValuesFor(ses.saved, chosen), option)
	}
	return ses.save()
}

// active lists the apps that run: those that provide no role, and those
// assigned to a role they provide.
func (ses *session) active() []*app.App {
	var apps []*app.App
	for _, a := range ses.Apps {
		provides := a.Provides(ses.versions[a.Name])
		if len(provides) == 0 || slices.ContainsFunc(provides, func(i role.Info) bool { return ses.saved.Roles[i.Name] == a.Name }) {
			apps = append(apps, a)
		}
	}
	return apps
}

// impl is the implementation for the app's current version. When none
// covers that version, it warns once and uses the built-in one.
func (ses *session) impl(a *app.App) *app.Implementation {
	version := ses.versions[a.Name]
	impl, exact := a.Implementation(version)
	if !exact && !ses.warned[a.Name] {
		ses.warned[a.Name] = true
		ses.UI.Say(versionWarning(a, version))
	}
	return impl
}

func versionWarning(a *app.App, version string) string {
	written := "These steps were written for " + a.Title + " " + a.Pinned() + " and may not match exactly. Updating " + a.Title + " or Selfnook may help."
	if version == "" {
		return "Selfnook cannot tell which version of " + a.Title + " runs. " + written
	}
	return a.Title + " " + version + " runs, but Selfnook has no steps for that version. " + written
}

// roles is what app a may read: each role it needs, bound to the app that fills it.
func (ses *session) roles(a *app.App) role.Set {
	return *ses.roleSet(a.Name, a.Needs)
}

func (ses *session) roleSet(owner string, needs []role.Role) *role.Set {
	if ses.sets == nil {
		ses.sets = map[string]*role.Set{}
	}
	if set, ok := ses.sets[owner]; ok {
		return set
	}
	filled := map[string]role.Bound{}
	set := role.NewSet(owner, needs, filled)
	ses.sets[owner] = &set
	for _, need := range needs {
		name := need.Info().Name
		provider := ses.app(ses.saved.Roles[name])
		if provider == nil {
			continue
		}
		for _, offer := range ses.impl(provider).Provides {
			if offer.Info().Name == name {
				filled[name] = role.Bound{Offer: offer, Values: app.ValuesFor(ses.saved, provider), Roles: *ses.roleSet(provider.Name, provider.Needs)}
			}
		}
	}
	return &set
}

func (ses *session) app(name string) *app.App {
	for _, a := range ses.Apps {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// coreRoles are the roles the stack itself reads. They form one set, so the
// set cached for the stack holds each of them.
var coreRoles = []role.Role{role.AccessAddress, role.VPN, role.AdminSignIn}

// core is the set of roles the stack itself reads.
func (ses *session) core() role.Set {
	return *ses.roleSet(state.Core, coreRoles)
}

// access is the address that serves every web page.
func (ses *session) access() role.Access {
	return role.Get(ses.core(), role.AccessAddress)
}

// contributions collects every active app's routes, tiles, and shared
// network needs in registry order.
func (ses *session) contributions() app.ComposeData {
	var data app.ComposeData
	for _, a := range ses.active() {
		impl, v, r := ses.impl(a), app.ValuesFor(ses.saved, a), ses.roles(a)
		if impl.Routes != nil {
			data.Routes = append(data.Routes, impl.Routes(v, r)...)
		}
		if impl.Tiles != nil {
			data.Tiles = append(data.Tiles, impl.Tiles(v, r)...)
		}
		if impl.Clients != nil {
			data.Clients = append(data.Clients, impl.Clients(v, r)...)
		}
		if impl.Shared != nil {
			data.Shared = append(data.Shared, *impl.Shared)
		}
	}
	return data
}

// composeEnv is every saved value that has an owner, then what each active
// app's Compose fragment reads from its roles. Unclaimed values stay a
// private record.
func (ses *session) composeEnv() map[string]string {
	env := map[string]string{}
	for _, owner := range slices.Sorted(maps.Keys(ses.saved.Values)) {
		if owner != state.Unclaimed {
			maps.Copy(env, ses.saved.Values[owner])
		}
	}
	data := ses.contributions()
	for _, a := range ses.active() {
		if a.Environment != nil {
			maps.Copy(env, a.Environment(ses.roles(a), data))
		}
	}
	return env
}

// env gives one app's hook its values, roles, and every app's contributions.
func (ses *session) env(a *app.App) *app.Env {
	data := ses.contributions()
	e := &app.Env{
		Values:     app.ValuesFor(ses.saved, a),
		Roles:      ses.roles(a),
		Runtime:    ses.runtime,
		Shell:      ses.Shell,
		UI:         ses.UI,
		Version:    ses.versions[a.Name],
		Routes:     data.Routes,
		Tiles:      data.Tiles,
		Clients:    data.Clients,
		BehindVPN:  ses.behindTitles(),
		SaveValues: ses.save,
		Sleep:      ses.sleep,
		Fetch:      ses.fetch,
	}
	if ses.steps {
		e.Steps = ses
	}
	return e
}

// behindTitles are the titles of the active apps behind the VPN.
func (ses *session) behindTitles() []string {
	var titles []string
	for _, a := range ses.active() {
		if len(a.BehindVPN) > 0 {
			titles = append(titles, a.Title)
		}
	}
	return titles
}

// gatewayLogEvery is how many checks, 2 seconds apart, pass between two
// reports of the gateway's latest message: one minute.
const gatewayLogEvery = 30

// waitForGateway waits without a time limit until the gateway lets traffic
// pass, because a VPN can take long to connect. It reports the wait once,
// then what the gateway says is wrong every minute, so a refused login shows.
// It stops when the gateway's container does not run.
func (ses *session) waitForGateway() error {
	gateway := role.Get(ses.core(), role.VPN)
	for check := 0; ; check++ {
		ready, err := gateway.Ready(ses.runtime)
		if err != nil || ready {
			return err
		}
		if check == 0 {
			ses.UI.Say("Waiting for the VPN connection. " + app.List(ses.behindTitles()) + " will start when it connects.")
		} else if check%gatewayLogEvery == 0 {
			if problem := gateway.Problem(ses.runtime); problem != "" {
				ses.UI.Say(problem)
			}
		}
		ses.sleep(2 * time.Second)
	}
}

// signInWait is how long waitForSignIn waits. Each check can itself take
// seconds, so the wait counts time, not checks.
const signInWait = 5 * time.Minute

// waitForSignIn waits until the admin sign-in page works, so the links the
// setup guides print do not open a page that is still starting.
func (ses *session) waitForSignIn() error {
	if ses.saved.Roles[role.AdminSignIn.Info().Name] == "" {
		return nil
	}
	signIn := role.Get(ses.core(), role.AdminSignIn)
	deadline := time.Now().Add(signInWait)
	for check := 0; time.Now().Before(deadline); check++ {
		ready, err := signIn.Ready(ses.runtime)
		if err != nil || ready {
			return err
		}
		if check == 0 {
			ses.UI.Say("Waiting for " + signIn.Name() + " to finish starting.")
		}
		ses.sleep(2 * time.Second)
	}
	return errors.New(signIn.Name() + " did not finish starting within five minutes.")
}

// detect records the version of every image of apps, from their running
// containers, and uses it to choose their implementations. A service
// without a container runs the pinned image when it starts.
func (ses *session) detect(apps []*app.App) error {
	for _, a := range apps {
		for i, image := range a.Images {
			found, err := ses.runtime.Image(image.Service)
			if err != nil {
				return err
			}
			var version state.Version
			if len(found.Digests) == 0 && found.Label == "" {
				version = app.PinnedVersion(a.Compose, image)
			} else {
				var ask func() (string, error)
				if image.Ask != nil {
					ask = func() (string, error) { return image.Ask(ses.env(a)) }
				}
				version = app.Detect(image, found, ask)
			}
			ses.saved.Versions[image.Service] = version
			if i == 0 {
				ses.versions[a.Name] = version.Version
			}
		}
	}
	// Providers may now run other implementations, which offer their roles.
	ses.sets = nil
	return ses.save()
}

// load reads the saved state and checks that every active app has what it
// needs to start.
func (s *Stack) load() (*session, error) {
	ses, found, err := s.open()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errNoSetup
	}
	if err := ses.assignRoles(false); err != nil {
		return nil, err
	}
	var missing []string
	for _, a := range append([]*app.App{app.Core}, ses.active()...) {
		v := app.ValuesFor(ses.saved, a)
		for _, setting := range a.Settings {
			if setting.Required && !v.Has(setting.Key) {
				missing = append(missing, a.Title+" "+setting.Key)
			}
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("Missing saved settings: %s. Run '%s setup' to add them.", strings.Join(missing, ", "), Command)
	}
	core := app.ValuesFor(ses.saved, app.Core)
	for _, key := range []string{"MEDIA_DIR", "CONFIG_DIR"} {
		if !filepath.IsAbs(filepath.FromSlash(core.Get(key))) {
			return nil, fmt.Errorf("%s must be an absolute path.", key)
		}
	}
	for _, a := range ses.active() {
		if a.Validate != nil {
			if err := a.Validate(app.ValuesFor(ses.saved, a)); err != nil {
				return nil, err
			}
		}
	}
	return ses, nil
}

func (ses *session) derive() {
	for _, a := range ses.active() {
		if a.Derive != nil {
			a.Derive(app.ValuesFor(ses.saved, a))
		}
	}
}

func (ses *session) createDirectories() error {
	core := app.ValuesFor(ses.saved, app.Core)
	for _, a := range ses.active() {
		for _, dir := range a.MediaDirs {
			if err := os.MkdirAll(filepath.Join(core.Get("MEDIA_DIR"), dir), 0o755); err != nil {
				return err
			}
		}
		for _, dir := range a.ConfigDirs {
			if err := os.MkdirAll(filepath.Join(core.Get("CONFIG_DIR"), filepath.FromSlash(dir)), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeRetiredDirs removes what replaced apps left under CONFIG_DIR.
func (ses *session) removeRetiredDirs() error {
	configDir := app.ValuesFor(ses.saved, app.Core).Get("CONFIG_DIR")
	if configDir == "" {
		return nil
	}
	for _, a := range ses.Apps {
		for _, dir := range a.RetiredConfigDirs {
			if err := os.RemoveAll(filepath.Join(configDir, filepath.FromSlash(dir))); err != nil {
				return fmt.Errorf("Cannot remove the unused folder %s: %w", dir, err)
			}
		}
	}
	return nil
}

// StoppedError reports a start that failed after stopping the services
// behind the VPN. They stay stopped until the next successful start.
type StoppedError struct {
	Services []string
	Err      error
}

func (e *StoppedError) Error() string {
	return e.Err.Error() + "\nStopped until the next successful start: " + strings.Join(e.Services, ", ")
}

func (e *StoppedError) Unwrap() error { return e.Err }

// startServices stops the services behind the VPN, runs every Prepare hook,
// starts the other containers, runs every Started hook, waits for the
// gateway, then starts the services behind the VPN. Containers of inactive
// apps are removed. A container that runs another image than the pinned one
// starts by name as it is, so Compose does not replace it.
func (ses *session) startServices() error {
	list, err := ses.keptContainers(ses.active())
	if err != nil {
		return err
	}
	keptNames := keptServices(list)
	var behindVPN, active []string
	var front, behind []*app.App
	for _, a := range ses.active() {
		behindVPN = append(behindVPN, a.BehindVPN...)
		if len(a.BehindVPN) > 0 {
			behind = append(behind, a)
		} else {
			front = append(front, a)
		}
		if a.Services == nil {
			continue
		}
		for _, service := range a.Services(app.ValuesFor(ses.saved, a)) {
			if !slices.Contains(a.BehindVPN, service) {
				active = append(active, service)
			}
		}
	}
	if len(behindVPN) > 0 {
		if err := ses.runtime.Stop(behindVPN...); err != nil {
			return err
		}
	}
	if err := ses.startWhileVPNServicesStopped(active, keptNames, behindVPN, front); err != nil {
		return &StoppedError{Services: behindVPN, Err: err}
	}
	if len(behindVPN) > 0 {
		if err := ses.waitForGateway(); err != nil {
			return &StoppedError{Services: behindVPN, Err: err}
		}
		if err := ses.up(behindVPN, keptNames, false); err != nil {
			return &StoppedError{Services: behindVPN, Err: err}
		}
		if err := ses.detect(behind); err != nil {
			return err
		}
	}
	ses.reportKept(list)
	return nil
}

// up creates or updates services through Compose, except those in kept,
// which start by name as they are.
func (ses *session) up(services, kept []string, removeOrphans bool) error {
	var compose, byName []string
	for _, service := range services {
		if slices.Contains(kept, service) {
			byName = append(byName, service)
		} else {
			compose = append(compose, service)
		}
	}
	// Compose starts every service when none is named.
	if len(compose) > 0 {
		if err := ses.runtime.Up(compose, removeOrphans); err != nil {
			return err
		}
	}
	return ses.runtime.Start(byName...)
}

func (ses *session) startWhileVPNServicesStopped(active, kept, behindVPN []string, started []*app.App) error {
	var restart []string
	for _, a := range ses.active() {
		impl := ses.impl(a)
		if impl.Prepare == nil {
			continue
		}
		services, err := impl.Prepare(ses.env(a))
		if err != nil {
			return err
		}
		restart = append(restart, services...)
	}
	if err := ses.up(active, kept, true); err != nil {
		return err
	}
	for _, service := range restart {
		// A service behind the VPN starts after the gateway is ready, with
		// the changes its Prepare hook made.
		if slices.Contains(behindVPN, service) {
			continue
		}
		if err := ses.runtime.Restart(service); err != nil {
			return err
		}
	}
	if err := ses.detect(started); err != nil {
		return err
	}
	for _, a := range ses.active() {
		if impl := ses.impl(a); impl.Started != nil {
			if err := impl.Started(ses.env(a)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Start starts the stack with the saved values.
func (s *Stack) Start() error {
	ses, err := s.load()
	if err != nil {
		return err
	}
	if err := ses.removeRetiredDirs(); err != nil {
		return err
	}
	ses.derive()
	if err := ses.save(); err != nil {
		return err
	}
	if err := ses.runtime.Ready(); err != nil {
		return err
	}
	if err := ses.writeCompose(); err != nil {
		return err
	}
	if err := ses.runtime.Prepare(); err != nil {
		return err
	}
	if err := ses.createDirectories(); err != nil {
		return err
	}
	if err := ses.startServices(); err != nil {
		return err
	}
	for _, a := range ses.active() {
		if impl := ses.impl(a); impl.AfterStart != nil {
			if err := impl.AfterStart(ses.env(a)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Stop stops every container and keeps it, so the next start finds the
// containers as they were, including one the user changed. It does not wait
// for Docker: when Docker does not answer, no container runs, which is what
// stopping asks for. This keeps a shutdown from waiting.
func (s *Stack) Stop() error {
	ses, found, err := s.open()
	if err != nil {
		return err
	}
	if !found {
		return errNoSetup
	}
	if err := ses.assignRoles(false); err != nil {
		return err
	}
	reachable, err := ses.runtime.Reachable()
	if err != nil {
		return err
	}
	if !reachable {
		s.UI.Say("Docker does not answer, so no container runs.")
		return nil
	}
	behind, front := ses.stopGroups()
	if err := ses.runtime.Stop(behind...); err != nil {
		return err
	}
	return ses.runtime.Stop(front...)
}

// stopGroups splits the active services into those behind the VPN and the
// rest. Stop stops the first group before the second, so no service loses its
// network while it still runs: one docker stop command stops its containers
// at the same time.
func (ses *session) stopGroups() (behind, front []string) {
	for _, a := range ses.active() {
		if a.Services == nil {
			continue
		}
		for _, service := range a.Services(app.ValuesFor(ses.saved, a)) {
			if slices.Contains(a.BehindVPN, service) {
				behind = append(behind, service)
			} else {
				front = append(front, service)
			}
		}
	}
	return behind, front
}

// probeWait is how long a restarted app gets before it is probed again.
const probeWait = 10 * time.Second

// Check restarts the containers of every app whose running containers cannot
// be reached from the computer, as after Docker Desktop restores port
// forwards to outdated container addresses, then probes them again. It
// changes nothing when Docker does not answer or an app's containers are not
// all running, so a stopped stack stays stopped.
func (s *Stack) Check() error {
	ses, found, err := s.open()
	if err != nil {
		return err
	}
	if !found {
		return errNoSetup
	}
	if err := ses.assignRoles(false); err != nil {
		return err
	}
	if reachable, err := ses.runtime.Reachable(); err != nil || !reachable {
		return err
	}
	for _, a := range ses.active() {
		impl := ses.impl(a)
		if impl.Probe == nil || a.Services == nil {
			continue
		}
		services := a.Services(app.ValuesFor(ses.saved, a))
		running, err := ses.allRunning(services)
		if err != nil {
			return err
		}
		if !running {
			continue
		}
		problem, err := impl.Probe(ses.env(a))
		if err != nil {
			return err
		}
		if problem == "" {
			continue
		}
		s.UI.Say(problem + " Restarting " + a.Title + ".")
		if err := ses.runtime.Stop(services...); err != nil {
			return err
		}
		if err := ses.runtime.Start(services...); err != nil {
			return err
		}
		ses.sleep(probeWait)
		if problem, err = impl.Probe(ses.env(a)); err != nil {
			return err
		}
		if problem != "" {
			return errors.New(problem)
		}
		s.UI.Say(a.Title + " answers again.")
	}
	return nil
}

// allRunning reports whether the container of every service runs.
func (ses *session) allRunning(services []string) (bool, error) {
	for _, service := range services {
		state, err := ses.runtime.State(service)
		if err != nil || state != "running" {
			return false, err
		}
	}
	return true, nil
}

// Update lists every container that runs another image than this release
// pins and asks, for each one, whether to replace it. Replaced containers are
// deleted, then the stack starts, which creates them from the pinned images.
// Configuration and media stay in their folders.
func (s *Stack) Update() error {
	ses, err := s.load()
	if err != nil {
		return err
	}
	if err := ses.runtime.Ready(); err != nil {
		return err
	}
	list, err := ses.keptContainers(ses.active())
	if err != nil {
		return err
	}
	if len(list) == 0 {
		s.UI.Say("Every container runs the version this release ships.")
		return nil
	}
	var replace []string
	for _, k := range list {
		answers, err := s.UI.Ask(flow.Screen{Fields: []flow.Field{{
			Key: "replace", Prompt: "Replace " + k.describe() + "?", Kind: flow.YesNo, Default: "no",
		}}})
		if err != nil {
			return err
		}
		if answers["replace"] == "yes" {
			replace = append(replace, k.service)
		}
	}
	if len(replace) == 0 {
		s.UI.Say("No container replaced.")
		return nil
	}
	if err := ses.runtime.Remove(replace...); err != nil {
		return err
	}
	return s.Start()
}

// Status shows every service's state.
func (s *Stack) Status() error {
	ses, err := s.ready()
	if err != nil {
		return err
	}
	return ses.runtime.Status()
}

// ready reads the saved state, waits for the runtime, and writes the Compose files.
func (s *Stack) ready() (*session, error) {
	ses, found, err := s.open()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errNoSetup
	}
	if err := ses.assignRoles(false); err != nil {
		return nil, err
	}
	if err := ses.runtime.Ready(); err != nil {
		return nil, err
	}
	if err := ses.writeCompose(); err != nil {
		return nil, err
	}
	return ses, nil
}

// RoleStatus shows the health report of the app that fills role r.
func (s *Stack) RoleStatus(r role.Role) error {
	ses, err := s.load()
	if err != nil {
		return err
	}
	if err := ses.runtime.Ready(); err != nil {
		return err
	}
	a := ses.app(ses.saved.Roles[r.Info().Name])
	if err := ses.detect([]*app.App{a}); err != nil {
		return err
	}
	impl := ses.impl(a)
	if impl.Status == nil {
		return fmt.Errorf("%s has no status report.", a.Title)
	}
	text, err := impl.Status(ses.env(a))
	if err != nil {
		return err
	}
	s.UI.Say(text)
	return nil
}
