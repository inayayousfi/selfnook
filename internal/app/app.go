// Package app defines what one application contributes to the stack.
// Each application lives in its own package under internal/apps and is
// listed once in internal/apps/registry.go.
package app

import (
	"crypto/rand"
	"encoding/base64"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
	"github.com/inayayousfi/selfnook/internal/shell"
	"github.com/inayayousfi/selfnook/internal/state"
)

// App is everything one application adds. It has two parts: what starts
// it, which never changes with its version, and Implementations, which
// hold everything that interacts with the running application and are
// chosen by its version. Every field except Name, Title, and
// Implementations is optional.
type App struct {
	// Name identifies the app, its saved values, and its Compose fragment.
	Name string
	// Title is the app as people know it, such as Radarr.
	Title string

	// Settings are the values this app owns. Only this app changes them.
	Settings []Setting
	// Renamed maps names that an earlier .env used to this app's current
	// setting names. RetiredSettings are .env names an app this one
	// replaced left behind; they are dropped when the .env is moved.
	// RetiredConfigDirs are removed from CONFIG_DIR before every start.
	Renamed           map[string]string
	RetiredSettings   []string
	RetiredConfigDirs []string

	// Needs lists the roles this app reads. It can read no other role.
	Needs []role.Role
	// Options are what this app offers when setup asks which app fills a
	// role it provides. Choose saves the option the user selected.
	Options []flow.Option
	Choose  func(v Values, option string)
	// WasActive tells, from the values of an earlier .env, whether this
	// app filled the roles it provides.
	WasActive func(old map[string]string) bool

	// Compose holds compose.yaml and any build files for this app's
	// containers. The file is a text/template rendered with ComposeData.
	// It reads this app's settings, the core settings, and the values
	// Environment returns.
	Compose fs.FS
	// Environment returns the values this app's Compose fragment reads
	// from the roles it needs, such as another app's API key. Each name
	// starts with the app's name in capitals.
	Environment func(r role.Set, d ComposeData) map[string]string

	// Services lists the Compose services this app runs with the current
	// values. BehindVPN services start only once every Started hook returns
	// and the gateway lets traffic pass.
	Services  func(v Values) []string
	BehindVPN []string

	// ConfigDirs and MediaDirs are created under CONFIG_DIR and MEDIA_DIR
	// before the stack starts.
	ConfigDirs []string
	MediaDirs  []string

	// Validate checks the content of this app's saved settings before a start.
	Validate func(v Values) error

	// Derive fills settings computed from other values or generated at
	// random. It reads nothing but the values. It runs before every start.
	Derive func(v Values)

	// Configure runs only during setup, before the stack starts. It asks the
	// questions this app needs, asking nothing when the saved values are
	// already complete, and performs this app's one-time host preparation
	// as recorded setup steps.
	Configure func(e *Env) error

	// Images are the images this app runs, the first being the one whose
	// version chooses the implementation.
	Images []Image
	// Implementations are the versions of everything that interacts with
	// the application. Exactly one contains the version of the pinned image.
	Implementations []Implementation

	// Credentials is the group of saved values that the credentials command
	// shows and changes.
	Credentials *CredentialGroup
}

// Image tells the versions of one service's image.
type Image struct {
	Service string
	// Digests maps a registry digest to the version it contains.
	Digests map[string]string
	// Labels maps an image's version label to the version it means.
	Labels map[string]string
	// Ask, when set, asks the running application for its version.
	Ask func(e *Env) (string, error)
}

// Implementation is everything that interacts with the application, for
// the versions from From to Until, both included, that it was checked with.
type Implementation struct {
	From, Until string

	// Provides lists the roles this app fills with this version.
	Provides []role.Offer

	// Prepare brings this app's files and generated settings up to date
	// before its containers start, saving values it changed. It may stop
	// this app's own containers when a file can change only while they are
	// stopped. It returns the services that must restart because a file
	// they read changed.
	Prepare func(e *Env) (restart []string, err error)

	// Started runs once the containers that do not use the VPN are up. The
	// services behind the VPN start after every Started hook returns and the
	// gateway lets traffic pass.
	Started func(e *Env) error

	// AfterStart runs at the end of the start command.
	AfterStart func(e *Env) error

	// Setup is this app's guided setup, run in registry order once the
	// stack is up. It records finished steps with Env.Step.
	Setup func(e *Env) error

	// Status describes this app's own health.
	Status func(e *Env) (string, error)

	// Probe reports, without changing anything, why this app's running
	// containers cannot be reached from the computer, or "" when they can.
	// The check command restarts the app's containers when it reports one.
	Probe func(e *Env) (string, error)

	// Routes are the web addresses Caddy serves for this app.
	Routes func(v Values, r role.Set) []Route

	// Tiles are this app's entries on the Homepage dashboard.
	Tiles func(v Values, r role.Set) []Tile

	// Clients are the sign-in clients this app registers with the sign-in
	// service, so people sign in to it with their sign-in account.
	Clients func(v Values, r role.Set) []Client

	// Shared, for an app behind the VPN, is how it is reached through the
	// gateway whose network it shares.
	Shared *Shared
}

// Shared is what the gateway opens for an app that shares its network.
type Shared struct {
	// Alias is the name other containers use to reach the app.
	Alias string
	// Ports are the ports the gateway's firewall lets in.
	Ports []string
	// Published are the ports published on the host, such as "6881:6881/tcp".
	Published []string
}

// Covers reports whether version lies within the implementation's range.
func (i *Implementation) Covers(version string) bool {
	return version != "" && CompareVersions(i.From, version) <= 0 && CompareVersions(version, i.Until) <= 0
}

// Implementation returns the implementation for version. When none covers
// it, it returns the one for the pinned version and exact = false.
func (a *App) Implementation(version string) (impl *Implementation, exact bool) {
	for i := range a.Implementations {
		if a.Implementations[i].Covers(version) {
			return &a.Implementations[i], true
		}
	}
	return a.BuiltIn(), false
}

// BuiltIn is the implementation for the version of the pinned image.
func (a *App) BuiltIn() *Implementation {
	pinned := a.Pinned()
	for i := range a.Implementations {
		if a.Implementations[i].Covers(pinned) {
			return &a.Implementations[i]
		}
	}
	panic("app " + a.Name + " has no implementation for its pinned version " + pinned)
}

// Pinned is the version of the image this app's Compose fragment pins for
// its first image.
func (a *App) Pinned() string {
	if len(a.Images) == 0 {
		return ""
	}
	image := a.Images[0]
	return image.Digests[PinnedDigest(a.Compose, image.Service)]
}

// Provides lists the roles this app fills when it runs version, using the
// same implementation Implementation chooses for that version.
func (a *App) Provides(version string) []role.Info {
	impl, _ := a.Implementation(version)
	var infos []role.Info
	for _, offer := range impl.Provides {
		infos = append(infos, offer.Info())
	}
	return infos
}

// Setting is one saved value an app owns.
type Setting struct {
	Key string
	// Required settings must be set before the stack can start.
	Required bool
}

// Owns reports whether key is one of this app's settings.
func (a *App) Owns(key string) bool {
	return slices.ContainsFunc(a.Settings, func(s Setting) bool { return s.Key == key })
}

// Core are the values the stack itself owns, which every app may read.
var Core = &App{
	Name:  state.Core,
	Title: "Selfnook",
	Settings: []Setting{
		{Key: "MEDIA_DIR", Required: true},
		{Key: "CONFIG_DIR", Required: true},
		{Key: "PUID", Required: true},
		{Key: "PGID", Required: true},
		{Key: "TZ", Required: true},
	},
	Credentials: &CredentialGroup{Name: "folders", Fields: []CredentialField{
		{Label: "Media directory", Key: "MEDIA_DIR"},
		{Label: "Configuration directory", Key: "CONFIG_DIR"},
	}},
}

// Values reads the owner's values and the core values, and changes only
// the settings its owner declares.
type Values struct {
	saved *state.State
	owner *App
}

// ValuesFor gives owner its view of saved.
func ValuesFor(saved *state.State, owner *App) Values {
	return Values{saved: saved, owner: owner}
}

func (v Values) group(key string) string {
	if v.owner != Core && Core.Owns(key) {
		return state.Core
	}
	return v.owner.Name
}

func (v Values) Get(key string) string { return v.saved.Get(v.group(key), key) }

func (v Values) Has(key string) bool { return v.saved.Has(v.group(key), key) }

// Set changes one of the owner's settings. Setting another app's value is a
// programming error and stops the program.
func (v Values) Set(key, value string) {
	v.mustOwn(key)
	v.saved.Set(v.owner.Name, key, value)
}

// SetDefault sets one of the owner's settings only when it has no entry yet.
func (v Values) SetDefault(key, value string) {
	v.mustOwn(key)
	v.saved.SetDefault(v.owner.Name, key, value)
}

func (v Values) mustOwn(key string) {
	if !v.owner.Owns(key) {
		panic("app " + v.owner.Name + " cannot set " + key + ", which it does not own")
	}
}

// Route is one address that Caddy forwards to a container.
type Route struct {
	// Name is the application's name as people know it, such as Sonarr.
	// Named routes appear in the admin sign-in's setup text.
	Name string
	// Path is the address path, such as /sonarr.
	Path string
	// Upstream is the container address, such as sonarr-app:8989.
	Upstream string
	// Public routes skip the admin sign-in.
	Public bool
	// StripPrefix removes Path before forwarding and redirects Path to Path/.
	StripPrefix bool
	// Port, when set, serves Upstream on its own port; Path redirects there.
	Port int
	// Fallback receives every request that no other route matches.
	Fallback bool
	// Internal, when set, also answers inside the Docker network at
	// http://Internal.Name:Internal.Port, adding Path when it is missing.
	Internal *Internal
}

type Internal struct {
	Name string
	Port int
}

// Client is an application that signs people in through the sign-in
// service with OpenID Connect.
type Client struct {
	Name string
	// ID identifies the client to the sign-in service, such as jellyfin.
	ID string
	// Return is the only address the sign-in service sends people back to.
	Return string
	// SecretKey is the saved setting that holds the client's secret. The
	// sign-in service reads it from its environment, never from a file.
	SecretKey string
}

// SecretRef is the Compose reference to the client's secret.
func (c Client) SecretRef() string { return "${" + c.SecretKey + "}" }

// Tile is one dashboard entry, as data any dashboard can show.
type Tile struct {
	Group string
	// Position orders the tiles within their group, lowest first.
	Position int
	// Name is the app as people know it; dashboards also choose its icon by it.
	Name        string
	Description string
	// Link is added to the access address, such as /radarr or :9091/if/admin/.
	Link string
	// Service is the Compose service whose running state the entry shows.
	Service string
	// Status, when set, is where the app reports its live status.
	Status *Status
}

// Status is an app's live status address for a dashboard panel.
type Status struct {
	URL string
	// Key is the secret the panel sends. It must not be written to disk.
	Key string
}

// KeyName names the tile's status key, such as GLUETUN_KEY.
func (t Tile) KeyName() string {
	var name strings.Builder
	for _, r := range strings.ToUpper(t.Name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			name.WriteRune(r)
		}
	}
	return name.String() + "_KEY"
}

// CredentialGroup lists saved values shown together by the credentials command.
type CredentialGroup struct {
	Name   string
	Fields []CredentialField
}

type CredentialKind int

const (
	// Fixed values are shown but not changed through the credentials command.
	Fixed CredentialKind = iota
	PlainText
	Password
	APIKey
)

type CredentialField struct {
	Label string
	Key   string
	Kind  CredentialKind
	// UsedByStack means the containers read this value at start.
	UsedByStack bool
}

// ComposeData is passed to every Compose fragment template.
type ComposeData struct {
	// Routes, Tiles, Clients, and Shared are every active app's contributions.
	Routes  []Route
	Tiles   []Tile
	Clients []Client
	Shared  []Shared
}

// OwnPorts are the ports that routes are served on besides the main address,
// sorted and listed once.
func OwnPorts(routes []Route) []int {
	var ports []int
	for _, route := range routes {
		if route.Port != 0 && !slices.Contains(ports, route.Port) {
			ports = append(ports, route.Port)
		}
	}
	slices.Sort(ports)
	return ports
}

// OwnPorts are the ports that routes are served on besides the main address.
func (d ComposeData) OwnPorts() []int { return OwnPorts(d.Routes) }

// SharedPorts are the ports every app behind the gateway needs opened.
func (d ComposeData) SharedPorts() []string {
	var ports []string
	for _, shared := range d.Shared {
		ports = append(ports, shared.Ports...)
	}
	return ports
}

// Published are the host ports of every app behind the gateway.
func (d ComposeData) Published() []string {
	var ports []string
	for _, shared := range d.Shared {
		ports = append(ports, shared.Published...)
	}
	return ports
}

// Aliases are the names of every app behind the gateway.
func (d ComposeData) Aliases() []string {
	var names []string
	for _, shared := range d.Shared {
		if shared.Alias != "" {
			names = append(names, shared.Alias)
		}
	}
	return names
}

// SharedTemplate is available to every Compose fragment as {{template
// "shared" .}}: a gateway service's published ports and network aliases for
// the apps that share its network.
const SharedTemplate = `{{define "shared"}}{{with .Published}}
    ports:
{{- range .}}
      - "{{.}}"
{{- end}}{{end}}{{with .Aliases}}
    networks:
      default:
        aliases:
{{- range .}}
          - {{.}}
{{- end}}{{end}}{{end}}`

// Steps records finished guided setup steps.
type Steps interface {
	Done(step string) bool
	Complete(step string) error
}

// Env gives a hook its app's values and roles, the runtime, and the user interface.
type Env struct {
	Values Values
	Roles  role.Set
	// Runtime runs and inspects containers.
	Runtime runtime.Runtime
	// Shell runs programs on the host, such as tailscale or systemctl.
	Shell shell.Shell
	UI    flow.UI
	// Version is the version of the app's first image that chose the
	// running implementation. It is empty when unknown.
	Version string
	// Routes and Tiles are every active app's contributions, for the apps
	// that assemble them.
	Routes  []Route
	Tiles   []Tile
	Clients []Client
	// BehindVPN are the titles of the active apps behind the VPN.
	BehindVPN []string
	// Steps is set during setup only.
	Steps Steps
	// SaveValues writes the saved state.
	SaveValues func() error
	// Sleep waits between checks while a hook polls a container.
	Sleep func(time.Duration)
	// Fetch downloads a file, such as a pinned plugin release.
	Fetch func(url string) ([]byte, error)
}

func (e *Env) ConfigDir() string { return e.Values.Get("CONFIG_DIR") }

func (e *Env) MediaDir() string { return e.Values.Get("MEDIA_DIR") }

// ConfigPath joins parts under CONFIG_DIR.
func (e *Env) ConfigPath(parts ...string) string {
	return filepath.Join(append([]string{e.ConfigDir()}, parts...)...)
}

// AskAndSave asks the screen, whose field keys are this app's settings, and
// saves each value as soon as it is entered, so a later prompt that is
// cancelled keeps the earlier ones.
func (e *Env) AskAndSave(screen flow.Screen) error {
	screen.Answered = func(key, value string) error {
		e.Values.Set(key, value)
		return e.SaveValues()
	}
	_, err := e.UI.Ask(screen)
	return err
}

// Step runs a guided step once, recording it in the setup progress. It is
// available during setup only. Steps already recorded are skipped unless
// again reports that they must repeat.
func (e *Env) Step(name string, again bool, run func() error) error {
	if e.Steps.Done(name) && !again {
		return nil
	}
	if err := run(); err != nil {
		return err
	}
	return e.Steps.Complete(name)
}

// RandomToken returns a random secret of n bytes, encoded without characters
// that environment values or URLs would need to escape.
func RandomToken(n int) string {
	bytes := make([]byte, n)
	rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// List joins names as "A", "A and B", or "A, B, and C".
func List(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
}
