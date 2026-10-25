package stack

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/apps"
	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/shell"
	"github.com/inayayousfi/selfnook/internal/state"
)

const (
	sonarrKey   = "0123456789abcdef0123456789abcdef"
	radarrKey   = "fedcba9876543210fedcba9876543210"
	jellyfinKey = "00112233445566778899aabbccddeeff"
)

// dockerHost answers like a working Docker host whose VPN connects at once.
func dockerHost(subnet string) func([]string) shell.Result {
	return func(args []string) shell.Result {
		line := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(line, "docker network inspect selfnook "):
			if subnet == "" {
				return shell.Result{Code: 1}
			}
			return shell.Result{Stdout: subnet + " \n"}
		case strings.HasPrefix(line, "docker network inspect media-stack"):
			return shell.Result{Code: 1}
		case strings.HasSuffix(line, "{{.State.Status}}"):
			return shell.Result{Stdout: "running\n"}
		case strings.HasPrefix(line, "docker inspect selfnook-gluetun"):
			return shell.Result{Stdout: "healthy\n"}
		case strings.HasPrefix(line, "docker exec -i selfnook-jellyfin-app curl -sS "):
			return jellyfinAnswer(args)
		case strings.HasPrefix(line, "docker exec selfnook-gluetun"):
			return shell.Result{Stdout: `[{"vpn":"openvpn","country":"Spain"},{"vpn":"wireguard","country":"Chile"}]`}
		}
		return shell.Result{}
	}
}

// jellyfinAnswer answers like a new Jellyfin 12 with the sign-in plugin: its
// first run is not done, it has no library, and the API key it creates is
// jellyfinKey.
func jellyfinAnswer(args []string) shell.Result {
	method := args[slices.Index(args, "-X")+1]
	address := args[len(args)-1]
	path := address[strings.Index(address, "/jellyfin")+len("/jellyfin"):]
	answers := map[string]string{
		"GET /System/Info/Public":            `{"StartupWizardCompleted": false}`,
		"POST /Users/AuthenticateByName":     `{"AccessToken": "session"}`,
		"GET /Library/VirtualFolders":        `[]`,
		"GET /Auth/Keys":                     `{"Items": [{"AppName": "Selfnook", "AccessToken": "` + jellyfinKey + `"}]}`,
		"GET /sso/OID/Get":                   `{}`,
		"GET /System/Configuration/branding": `{}`,
		"GET /System/Configuration":          `{"ServerName": ""}`,
	}
	if answer, ok := answers[method+" "+path]; ok {
		return shell.Result{Stdout: answer + "\n200\n"}
	}
	return shell.Result{Stdout: "\n204\n"}
}

// newTestStack isolates PATH, HOME, and the stack folder, with a docker
// command present so the stack finds Docker installed.
func newTestStack(t *testing.T, ui flow.UI, sh *fake.Shell) *Stack {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER", "tester")
	root := t.TempDir()
	plugin := filepath.Join(root, "config", "jellyfin", "plugins", "SSO-Auth_5.1.1", "meta.json")
	if err := os.MkdirAll(filepath.Dir(plugin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte(`{"version": "5.1.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Stack{
		Root:      root,
		StatePath: filepath.Join(t.TempDir(), "state", "selfnook", "state.json"),
		Apps:      apps.All,
		Runtimes:  apps.Runtimes,
		Shell:     sh,
		UI:        ui,
		Program:   "/opt/selfnook/selfnook",
		Sleep:     func(time.Duration) {},
		Fetch:     func(url string) ([]byte, error) { return nil, errors.New("tests do not download " + url) },
	}
}

func setupAnswers(media string) map[string]string {
	return map[string]string{
		"media":                media,
		"accept":               "yes",
		"provider":             "gluetun/nordvpn",
		"VPN_OPENVPN_USER":     "vpn-user",
		"VPN_OPENVPN_PASSWORD": "vpn-secret",
		"mode":                 "domain",
		"host":                 "Media.Example.com.",
		"ADMIN_USER":           "admin",
		"ADMIN_PASS":           "admin-secret",
		"SONARR_API_KEY":       sonarrKey,
		"RADARR_API_KEY":       radarrKey,
		"kind":                 "user",
	}
}

// readValues returns every saved value, whatever its owner.
func readValues(t *testing.T, s *Stack) map[string]string {
	t.Helper()
	saved := readState(t, s)
	values := map[string]string{}
	for _, group := range saved.Values {
		for key, value := range group {
			values[key] = value
		}
	}
	return values
}

func readState(t *testing.T, s *Stack) *state.State {
	t.Helper()
	saved, found, err := state.Read(s.StatePath)
	if err != nil || !found {
		t.Fatalf("state file: found = %v, err = %v", found, err)
	}
	return saved
}

// writeState saves values under their owners, with the given role assignments.
func writeState(t *testing.T, s *Stack, values, roles map[string]string) {
	t.Helper()
	saved := state.New()
	for key, value := range values {
		saved.Set(s.ownerOf(key), key, value)
	}
	for name, provider := range roles {
		saved.Roles[name] = provider
	}
	if err := state.Write(s.StatePath, saved); err != nil {
		t.Fatal(err)
	}
}

// changeValue changes one saved value in the state file.
func changeValue(t *testing.T, s *Stack, key, value string) {
	t.Helper()
	saved := readState(t, s)
	saved.Set(s.ownerOf(key), key, value)
	if err := state.Write(s.StatePath, saved); err != nil {
		t.Fatal(err)
	}
}

// Given a fresh folder, through the whole guided setup with every app in the
// registry, expect saved values, each guide in order, and a resumable record.
func TestSetupGuidesEveryAppInOrderAndSavesValues(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	media := filepath.Join(t.TempDir(), "Media")
	ui.Answers = setupAnswers(media)
	var savedAtNotifications map[string]string
	commandsAtSeerr := -1
	ui.OnAsk = func(screen flow.Screen) {
		switch {
		case screen.Title == "Jellyfin notifications":
			savedAtNotifications = readValues(t, s)
		case screen.Title == "Seerr setup":
			commandsAtSeerr = len(sh.Commands)
		}
	}

	if err := s.Setup(); err != nil {
		t.Fatalf("setup failed: %v\nevents: %v", err, ui.Events)
	}

	values := readValues(t, s)
	want := map[string]string{
		"MEDIA_DIR":            filepath.ToSlash(media),
		"CONFIG_DIR":           filepath.ToSlash(filepath.Join(s.Root, "config")),
		"QBT_LEGAL_NOTICE":     "confirm",
		"VPN_SERVICE_PROVIDER": "nordvpn",
		"VPN_TYPE":             "openvpn",
		"ACCESS_MODE":          "domain",
		"ADMIN_ACCESS_HOST":    "media.example.com",
		"ACCESS_URL":           "https://media.example.com",
		"ADMIN_USER":           "admin",
		"SONARR_API_KEY":       sonarrKey,
		"RADARR_API_KEY":       radarrKey,
		"JELLYFIN_API_KEY":     jellyfinKey,
		"JELLYFIN_ADMIN_USER":  "selfnook",
	}
	for key, value := range want {
		if values[key] != value {
			t.Errorf("%s = %q, want %q", key, values[key], value)
		}
	}
	if roles := readState(t, s).Roles; roles["gateway"] != "gluetun" || roles["runtime"] != "docker" {
		t.Errorf("roles = %v", roles)
	}
	for _, dir := range []string{"Movies", "Series", "Downloads"} {
		if _, err := os.Stat(filepath.Join(media, dir)); err != nil {
			t.Errorf("media folder %s missing", dir)
		}
	}

	var order []string
	for _, event := range ui.Events {
		if strings.HasPrefix(event, "screen: ") && strings.HasSuffix(event, " setup") {
			order = append(order, strings.TrimPrefix(event, "screen: "))
		}
	}
	wantOrder := []string{"VPN kill-switch setup", "qBittorrent setup", "Sonarr setup", "Radarr setup", "Prowlarr setup", "Authentik setup", "Seerr setup"}
	if !slices.Equal(order, wantOrder) {
		t.Errorf("guide order = %v, want %v", order, wantOrder)
	}

	if savedAtNotifications["JELLYFIN_ADMIN_USER"] != "selfnook" || savedAtNotifications["JELLYFIN_API_KEY"] != jellyfinKey {
		t.Error("Jellyfin administrator login and API key were not saved before the notifications guide")
	}
	if !strings.Contains(ui.Text(), "API Key: "+jellyfinKey) {
		t.Error("notifications guide does not print the saved Jellyfin API key")
	}
	syncAt := sh.Index("docker compose run --rm recyclarr sync")
	if syncAt < 0 || commandsAtSeerr < 0 || syncAt >= commandsAtSeerr {
		t.Errorf("Recyclarr sync (command %d) must run before the Seerr guide (after command %d)", syncAt, commandsAtSeerr)
	}

	unit, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "selfnook.service"))
	if err != nil || !strings.Contains(string(unit), "ExecStart=/opt/selfnook/selfnook start --log-file ") {
		t.Errorf("user service not installed for the program: %v\n%s", err, unit)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "selfnook-check.timer")); err != nil || sh.Index("systemctl --user enable --now selfnook-check.timer") < 0 {
		t.Errorf("check timer not installed and started: %v", err)
	}
	if last := ui.Events[len(ui.Events)-1]; !strings.Contains(last, "Setup complete.") || !strings.Contains(last, "https://media.example.com") {
		t.Errorf("last message = %q", last)
	}
}

// Authentik needs minutes on its first start, so the guides, whose links lead
// to its sign-in page, wait until it is ready, and the wait shows once.
func TestSetupGuidesWaitForTheSignInPage(t *testing.T) {
	checks := 0
	sh := &fake.Shell{Respond: func(args []string) shell.Result {
		if len(args) > 4 && slices.Equal(args[:5], []string{"docker", "exec", "selfnook-authentik-server", "python3", "-c"}) {
			if checks++; checks <= 3 {
				return shell.Result{Code: 1}
			}
		}
		return dockerHost("")(args)
	}}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	wait := slices.Index(ui.Events, "say: Waiting for Authentik to finish starting.")
	guide := slices.Index(ui.Events, "screen: qBittorrent setup")
	waits := 0
	for _, event := range ui.Events {
		if strings.HasPrefix(event, "say: Waiting for Authentik") {
			waits++
		}
	}
	if checks != 4 || waits != 1 || wait < 0 || wait > guide {
		t.Errorf("checks = %d, events = %q", checks, ui.Events)
	}
}

// Without systemd, setup ends with a sentence that says so instead of the
// error of a missing systemctl.
func TestSetupWithoutSystemdSaysAutomaticStartNeedsIt(t *testing.T) {
	sh := &fake.Shell{Respond: func(args []string) shell.Result {
		if strings.Join(args, " ") == "systemctl is-system-running" {
			return shell.Result{Stdout: "offline\n", Code: 1}
		}
		return dockerHost("")(args)
	}}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	err := s.Setup()
	if err == nil || err.Error() != "Automatic start needs systemd, which does not run on this computer." || slices.Contains(ui.Events, "screen: Linux automatic start") {
		t.Errorf("err = %v, events = %q", err, ui.Events)
	}
}

// Given a finished setup, running setup again asks only for the automatic
// start choice, because every answer and guided step is saved.
func TestSetupResumesWithoutRepeatingSavedSteps(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}

	again := &fake.UI{Answers: map[string]string{"kind": "user"}}
	s.UI = again
	if err := s.Setup(); err != nil {
		t.Fatalf("second setup failed: %v\nevents: %v", err, again.Events)
	}
	var asked []string
	for _, event := range again.Events {
		if strings.HasPrefix(event, "ask: ") || strings.HasPrefix(event, "screen: ") {
			asked = append(asked, event)
		}
	}
	if !slices.Equal(asked, []string{"screen: Linux automatic start", "ask: kind"}) {
		t.Errorf("second setup asked %v", asked)
	}
}

// Guides describe actions in ordinary words; saved setting names are internal bookkeeping.
func TestGuidesDoNotExposeEnvironmentNames(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	if names := regexp.MustCompile(`\b[A-Z]+_[A-Z_]+\b`).FindAllString(ui.Text(), -1); len(names) > 0 {
		t.Errorf("guides expose %v", names)
	}
}

func startedStack(t *testing.T, subnet string) (*Stack, *fake.Shell, *fake.UI) {
	t.Helper()
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	sh.Commands, sh.Envs, sh.Inputs = nil, nil, nil
	sh.Respond = dockerHost(subnet)
	ui.Events = nil
	return s, sh, ui
}

// Services outside the VPN start first; qBittorrent and Prowlarr start only
// after the gateway reports ready, and Recyclarr syncs last.
func TestStartWaitsForTheGatewayBeforeVPNServices(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	stop := sh.Index("docker stop selfnook-qbittorrent selfnook-prowlarr")
	up := sh.Index("docker compose up -d --no-deps --remove-orphans")
	ready := sh.Index("docker inspect selfnook-gluetun --format {{.State.Health.Status}}")
	vpnUp := sh.Index("docker compose up -d --no-deps qbittorrent prowlarr")
	sync := sh.Index("docker compose run --rm recyclarr sync")
	if !(stop >= 0 && stop < up && up < ready && ready < vpnUp && vpnUp < sync) {
		t.Errorf("order stop=%d up=%d ready=%d vpnUp=%d sync=%d\n%s", stop, up, ready, vpnUp, sync, strings.Join(sh.Lines(), "\n"))
	}
	upLine := sh.Lines()[up]
	for _, service := range []string{"gluetun", "vpn-country", "caddy", "authentik-db", "authentik-server", "authentik-worker", "homepage", "jellyfin-app", "seerr", "sonarr-app", "radarr-app"} {
		if !strings.Contains(upLine, " "+service) {
			t.Errorf("%q does not start %s", upLine, service)
		}
	}
	if strings.Contains(upLine, "qbittorrent") || strings.Contains(upLine, "tailscale-vpn") {
		t.Errorf("%q starts a VPN user or the inactive gateway", upLine)
	}
	if sh.Index("docker compose down") >= 0 {
		t.Error("a network with the expected address range was recreated")
	}
}

// A VPN can take long to connect, so the wait has no time limit. It is
// reported once, then what the gateway says is wrong shows every minute, so
// a refused VPN login becomes visible although OpenVPN logs its restart last.
func TestStartReportsTheGatewayWhileWaiting(t *testing.T) {
	s, sh, ui := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	checks := 0
	sh.Respond = func(args []string) shell.Result {
		line := strings.Join(args, " ")
		switch {
		case line == "docker inspect selfnook-gluetun --format {{.State.Health.Status}}":
			checks++
			if checks <= 2*gatewayLogEvery+1 {
				return shell.Result{Stdout: "starting\n"}
			}
			return shell.Result{Stdout: "healthy\n"}
		case line == "docker logs --tail 50 selfnook-gluetun":
			return shell.Result{Stdout: "2026-10-12T09:00:00Z ERROR [openvpn] AUTH: Received control message: AUTH_FAILED\n" +
				"2026-10-12T09:00:00Z INFO [openvpn] SIGUSR1[soft,auth-failure] received, process restarting\n"}
		}
		return dockerHost("172.31.250.0/24 172.31.250.128/25")(args)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	var waits, messages int
	for _, event := range ui.Events {
		if strings.HasPrefix(event, "say: Waiting for the VPN connection.") {
			waits++
		}
		if event == "say: The VPN provider refuses the saved VPN login." {
			messages++
		}
	}
	if waits != 1 || messages != 2 {
		t.Errorf("waits = %d, messages = %d, events = %q", waits, messages, ui.Events)
	}
	if sh.Index("docker compose up -d --no-deps qbittorrent prowlarr") < 0 {
		t.Error("the services behind the VPN did not start")
	}
}

// A gateway whose container stopped cannot connect by waiting, so the start
// ends with that failure and names the services it left stopped.
func TestStartStopsWhenTheGatewayDoesNotRun(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	sh.Respond = func(args []string) shell.Result {
		if strings.Join(args, " ") == "docker inspect selfnook-gluetun --format {{.State.Status}}" {
			return shell.Result{Stdout: "exited\n"}
		}
		return dockerHost("172.31.250.0/24 172.31.250.128/25")(args)
	}
	err := s.Start()
	var down *role.GatewayDown
	var stopped *StoppedError
	if !errors.As(err, &down) || !errors.As(err, &stopped) || !slices.Equal(stopped.Services, []string{"qbittorrent", "prowlarr"}) {
		t.Fatalf("err = %v", err)
	}
	if down.State != "exited" || sh.Index("docker compose up -d --no-deps qbittorrent prowlarr") >= 0 {
		t.Errorf("state = %q, commands:\n%s", down.State, strings.Join(sh.Lines(), "\n"))
	}
}

// Stopping at shutdown must not wait for Docker: when Docker does not
// answer, no container runs, and stop succeeds after one check.
func TestStopDoesNotWaitForDocker(t *testing.T) {
	s, sh, ui := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	sh.Respond = func([]string) shell.Result { return shell.Result{Code: 1} }
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if len(sh.Commands) != 1 || sh.Index("docker info") != 0 {
		t.Errorf("commands:\n%s", strings.Join(sh.Lines(), "\n"))
	}
	if !slices.Contains(ui.Events, "say: Docker does not answer, so no container runs.") {
		t.Errorf("events = %q", ui.Events)
	}
}

// A network left with another address range is removed so Compose recreates it.
func TestStartRecreatesANetworkWithAnotherAddressRange(t *testing.T) {
	s, sh, _ := startedStack(t, "10.0.0.0/24")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if sh.Index("docker compose down --remove-orphans") < 0 {
		t.Errorf("network not recreated:\n%s", strings.Join(sh.Lines(), "\n"))
	}
}

// An install made under the earlier project name is removed before the
// stack starts, because its containers hold the same ports and address range.
func TestStartRemovesTheEarlierProject(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	host := sh.Respond
	sh.Respond = func(args []string) shell.Result {
		if strings.Join(args, " ") == "docker ps --all --quiet --filter label=com.docker.compose.project=media-stack" {
			return shell.Result{Stdout: "0123abcd\n"}
		}
		return host(args)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	down := sh.Index("docker compose --project-name media-stack down --remove-orphans")
	image := sh.Index("docker image rm media-stack/vpn-country")
	up := sh.Index("docker compose up -d --no-deps --remove-orphans")
	if !(down >= 0 && down < image && image < up) {
		t.Errorf("order down=%d image=%d up=%d\n%s", down, image, up, strings.Join(sh.Lines(), "\n"))
	}
}

// Setup replaces the service that earlier versions installed, so the earlier
// program no longer runs at login.
func TestSetupReplacesTheEarlierUserService(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	units := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	if err := os.MkdirAll(units, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(units, "media-stack.service"), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(units, "media-stack.service")); err == nil {
		t.Error("media-stack.service kept")
	}
	if _, err := os.Stat(filepath.Join(units, "selfnook.service")); err != nil {
		t.Error("selfnook.service not installed")
	}
	if sh.Index("systemctl --user disable media-stack.service") < 0 {
		t.Errorf("earlier service not disabled:\n%s", strings.Join(sh.Lines(), "\n"))
	}
}

// When the Gluetun access file changes, Gluetun restarts before the VPN users start.
func TestChangedGatewayAccessRestartsGluetunBeforeVPNServices(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	changeValue(t, s, "GLUETUN_API_KEY", "changed")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	restart := sh.Index("docker compose restart gluetun")
	if restart < 0 || restart > sh.Index("docker compose up -d --no-deps qbittorrent prowlarr") {
		t.Errorf("gluetun restart missing or late:\n%s", strings.Join(sh.Lines(), "\n"))
	}
	if sh.Index("docker compose restart caddy") >= 0 {
		t.Error("Caddy restarted although its configuration did not change")
	}
}

func TestStartWithoutSetupAsksForSetup(t *testing.T) {
	s := newTestStack(t, &fake.UI{}, &fake.Shell{})
	if err := s.Start(); err == nil || err.Error() != "Run 'selfnook setup' first." {
		t.Errorf("err = %v", err)
	}
}

func TestStartReportsMissingValues(t *testing.T) {
	s := newTestStack(t, &fake.UI{}, &fake.Shell{})
	writeState(t, s, map[string]string{"MEDIA_DIR": "/media"}, map[string]string{"gateway": "gluetun"})
	err := s.Start()
	if err == nil || !strings.HasPrefix(err.Error(), "Missing saved settings: Authentik ADMIN_PASS, Authentik ADMIN_USER, Caddy ACCESS_MODE,") {
		t.Errorf("err = %v", err)
	}
}

func TestVPNStatusReportsHealthAndCountries(t *testing.T) {
	s, _, ui := startedStack(t, "")
	changeValue(t, s, "VPN_SERVER_COUNTRIES", "Spain")
	if err := s.RoleStatus(role.VPN); err != nil {
		t.Fatal(err)
	}
	if got := ui.Events[len(ui.Events)-1]; got != "say: Gluetun VPN: healthy\nSelected countries:\nSpain" {
		t.Errorf("status = %q", got)
	}
}

// A start that fails after stopping qBittorrent and Prowlarr says they stay stopped.
func TestFailedStartNamesTheServicesItLeftStopped(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	ready := sh.Respond
	sh.Respond = func(args []string) shell.Result {
		if strings.HasPrefix(strings.Join(args, " "), "docker compose up -d --no-deps --remove-orphans") {
			return shell.Result{Code: 1}
		}
		return ready(args)
	}
	err := s.Start()
	var stopped *StoppedError
	if !errors.As(err, &stopped) || !slices.Equal(stopped.Services, []string{"qbittorrent", "prowlarr"}) {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasSuffix(err.Error(), "\nStopped until the next successful start: qbittorrent, prowlarr") {
		t.Errorf("message = %q", err.Error())
	}
}

// Containers reach the access address, Sonarr, Radarr, Prowlarr, and
// Jellyfin by name through Caddy.
func TestCaddyAnswersForTheInternalNames(t *testing.T) {
	s, _, _ := startedStack(t, "")
	content, err := os.ReadFile(filepath.Join(s.Root, "compose", "caddy", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "        aliases:\n          - \"${ADMIN_ACCESS_HOST}\"\n          - sonarr\n          - radarr\n          - prowlarr\n          - jellyfin\n"
	if !strings.Contains(string(content), want) {
		t.Errorf("caddy fragment:\n%s", content)
	}
}

// An earlier .env and setup progress move into the state file: each value
// goes to its owner, renamed values take their current names, values no
// longer used are dropped, the gateway choice becomes the role, and both
// earlier files are deleted.
func TestEarlierEnvMovesIntoTheStateFile(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	s := newTestStack(t, &fake.UI{}, sh)
	configDir := filepath.Join(s.Root, "config")
	env := strings.Join([]string{
		`MEDIA_DIR="/media"`,
		`CONFIG_DIR="` + filepath.ToSlash(configDir) + `"`,
		`JELLYFIN_USER="jelly"`,
		`JELLYFIN_PASS=secret`,
		`VPN_GATEWAY_SERVICE="tailscale-vpn"`,
		`TAILSCALE_AUTH_KEY="tskey"`,
		`TINYAUTH_USERS="admin:hash"`,
		`MY_NOTE="kept"`,
	}, "\n")
	envPath := filepath.Join(s.Root, ".env")
	progressPath := filepath.Join(configDir, "setup-state.json")
	if err := os.WriteFile(envPath, []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(configDir, 0o755)
	if err := os.WriteFile(progressPath, []byte(`["sonarr"]`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.Status(); err != nil {
		t.Fatal(err)
	}

	saved := readState(t, s)
	checks := map[[2]string]string{
		{state.Core, "MEDIA_DIR"}:           "/media",
		{"jellyfin", "JELLYFIN_ADMIN_USER"}: "jelly",
		{"jellyfin", "JELLYFIN_ADMIN_PASS"}: "secret",
		{"tailscale", "TAILSCALE_AUTH_KEY"}: "tskey",
		{state.Unclaimed, "MY_NOTE"}:        "kept",
	}
	for place, want := range checks {
		if got := saved.Get(place[0], place[1]); got != want {
			t.Errorf("%s %s = %q, want %q", place[0], place[1], got, want)
		}
	}
	values := readValues(t, s)
	for _, gone := range []string{"JELLYFIN_USER", "TINYAUTH_USERS", "VPN_GATEWAY_SERVICE"} {
		if _, ok := values[gone]; ok {
			t.Errorf("%s kept", gone)
		}
	}
	if saved.Roles["gateway"] != "tailscale" || !slices.Equal(saved.Progress, []string{"sonarr"}) {
		t.Errorf("roles = %v, progress = %v", saved.Roles, saved.Progress)
	}
	for _, path := range []string{envPath, progressPath} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s kept", path)
		}
	}
	// Unclaimed values are a private record that never reaches Compose.
	for i, command := range sh.Commands {
		if _, leaked := sh.Envs[i]["MY_NOTE"]; leaked {
			t.Errorf("%q received MY_NOTE", command)
		}
	}
}

// Each setting has one owner, so only that app can change it.
func TestEverySettingHasOneOwner(t *testing.T) {
	owners := map[string]string{}
	for _, setting := range app.Core.Settings {
		owners[setting.Key] = "stack"
	}
	for _, a := range apps.All {
		for _, setting := range a.Settings {
			if owner, ok := owners[setting.Key]; ok {
				t.Errorf("%s is owned by both %s and %s", setting.Key, owner, a.Name)
			}
			owners[setting.Key] = a.Name
		}
		for _, current := range a.Renamed {
			if !a.Owns(current) {
				t.Errorf("%s renames into %s, which it does not own", a.Name, current)
			}
		}
	}
	for _, a := range apps.All {
		for _, key := range a.RetiredSettings {
			if owner, ok := owners[key]; ok {
				t.Errorf("%s retires %s, which %s still owns", a.Name, key, owner)
			}
		}
	}
}

// An install upgraded from Tinyauth loses its folder at the next start.
func TestStartRemovesWhatTinyauthLeftBehind(t *testing.T) {
	s, _, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	old := filepath.Join(readValues(t, s)["CONFIG_DIR"], "tinyauth")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("config/tinyauth kept")
	}
}

func TestSignInTextListsTheRegistryApps(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	want := "They protect qBittorrent, Sonarr, Radarr, Prowlarr, and the VPN country page, which no longer ask for their own logins. Jellyfin and Seerr keep their own logins."
	if !strings.Contains(ui.Text(), want) {
		t.Errorf("sign-in text missing; guides:\n%s", ui.Text())
	}
}

// Given setup stopped at the admin password prompt, the username entered
// just before is already saved, so it can be read back with credentials.
func TestEachAnswerIsSavedAsSoonAsItIsEntered(t *testing.T) {
	ui := &fake.UI{}
	s := newTestStack(t, ui, &fake.Shell{Respond: dockerHost("")})
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	delete(ui.Answers, "ADMIN_PASS")
	if err := s.Setup(); err == nil {
		t.Fatal("setup finished without an admin password")
	}
	values := readValues(t, s)
	if values["ADMIN_USER"] != "admin" || values["VPN_OPENVPN_PASSWORD"] != "vpn-secret" {
		t.Errorf("saved values = %v", values)
	}
	if _, saved := values["ADMIN_PASS"]; saved {
		t.Error("an unanswered password was saved")
	}
}

// changedHost answers like dockerHost, except that the containers of
// changed run an image that this release does not pin, until they are
// removed.
func changedHost(changed ...string) (func([]string) shell.Result, *[]string) {
	removed := &[]string{}
	host := dockerHost("172.31.250.0/24 172.31.250.128/25")
	return func(args []string) shell.Result {
		line := strings.Join(args, " ")
		if strings.HasPrefix(line, "docker rm --force ") {
			*removed = append(*removed, args[3:]...)
		}
		for _, service := range changed {
			container := "selfnook-" + service
			if slices.Contains(*removed, container) {
				continue
			}
			if line == "docker inspect "+container+" --format {{.Image}}" {
				return shell.Result{Stdout: "sha256:image-" + service + "\n"}
			}
			if strings.HasPrefix(line, "docker image inspect sha256:image-"+service+" ") {
				return shell.Result{Stdout: "example/" + service + "@sha256:chosen |chosen-1.0\n"}
			}
		}
		return host(args)
	}, removed
}

// Given a container that runs another image than the pinned one, start
// runs it by name instead of letting Compose replace it, and says so.
func TestStartKeepsAContainerThatRunsAnotherVersion(t *testing.T) {
	s, sh, ui := startedStack(t, "")
	sh.Respond, _ = changedHost("sonarr-app")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	up := sh.Lines()[sh.Index("docker compose up -d --no-deps --remove-orphans")]
	if strings.Contains(up, " sonarr-app") || !strings.Contains(up, " radarr-app") {
		t.Errorf("compose start = %q", up)
	}
	if sh.Index("docker start selfnook-sonarr-app") < 0 {
		t.Errorf("Sonarr not started by name:\n%s", strings.Join(sh.Lines(), "\n"))
	}
	want := "say: Started as they are, because they run another version than this release: Sonarr chosen-1.0 (this release ships 4.0.20.3014). Run 'selfnook update' to choose whether to replace them."
	if !slices.Contains(ui.Events, want) {
		t.Errorf("events = %q", ui.Events)
	}
}

// Stop keeps every container, so a changed one is still there at the next
// start. The containers behind the VPN stop first.
func TestStopKeepsEveryContainer(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	behind := slices.Index(sh.Lines(), "docker stop selfnook-qbittorrent selfnook-prowlarr")
	front := sh.Index("docker stop selfnook-sonarr-app ")
	if behind < 0 || front < behind || sh.Index("docker compose down") >= 0 || sh.Index("docker rm") >= 0 {
		t.Errorf("commands:\n%s", strings.Join(sh.Lines(), "\n"))
	}
}

// Update asks about each changed container and replaces only those the user
// approves: they are removed, then created from the pinned images.
func TestUpdateReplacesOnlyApprovedContainers(t *testing.T) {
	s, sh, ui := startedStack(t, "")
	var removed *[]string
	sh.Respond, removed = changedHost("sonarr-app", "radarr-app")
	ui.OnAsk = func(screen flow.Screen) {
		ui.Answers["replace"] = "no"
		if strings.HasPrefix(screen.Fields[0].Prompt, "Replace Sonarr chosen-1.0 (this release ships 4.0.20.3014)?") {
			ui.Answers["replace"] = "yes"
		}
	}
	if err := s.Update(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*removed, []string{"selfnook-sonarr-app"}) {
		t.Errorf("removed %v", *removed)
	}
	up := sh.Lines()[sh.Index("docker compose up -d --no-deps --remove-orphans")]
	if !strings.Contains(up, " sonarr-app") || strings.Contains(up, " radarr-app") {
		t.Errorf("compose start = %q", up)
	}
}

// probedHost answers like dockerHost, with the containers in the given
// state, and with port 443 silent until Caddy restarts.
func probedHost(state string) func([]string) shell.Result {
	host := dockerHost("172.31.250.0/24 172.31.250.128/25")
	restarted := false
	return func(args []string) shell.Result {
		line := strings.Join(args, " ")
		switch {
		case strings.HasSuffix(line, "{{.State.Status}}"):
			return shell.Result{Stdout: state + "\n"}
		case line == "docker start selfnook-caddy":
			restarted = true
		case args[0] == "curl":
			if strings.HasSuffix(line, "http://127.0.0.1:443/") && !restarted {
				return shell.Result{Code: 7, Stdout: "000"}
			}
			return shell.Result{Stdout: "400"}
		}
		return host(args)
	}
}

// Given a running stack whose port 443 does not answer, as after Docker
// Desktop restores a port forward to an outdated address, check restarts
// Caddy by name and reports that it answers again.
func TestCheckRestartsCaddyWhenAPortIsSilent(t *testing.T) {
	s, sh, ui := startedStack(t, "")
	sh.Respond = probedHost("running")
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	stop, start := sh.Index("docker stop selfnook-caddy"), sh.Index("docker start selfnook-caddy")
	if stop < 0 || start < stop {
		t.Errorf("commands:\n%s", strings.Join(sh.Lines(), "\n"))
	}
	want := []string{"say: Caddy does not answer on port 443 of this computer. Restarting Caddy.", "say: Caddy answers again."}
	if !slices.Equal(ui.Events, want) {
		t.Errorf("events = %q", ui.Events)
	}
}

// A stopped stack stays stopped: check probes nothing and starts nothing.
func TestCheckLeavesAStoppedStackAlone(t *testing.T) {
	s, sh, ui := startedStack(t, "")
	sh.Respond = probedHost("exited")
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if sh.Index("curl") >= 0 || sh.Index("docker start") >= 0 || len(ui.Events) != 0 {
		t.Errorf("commands:\n%s\nevents = %q", strings.Join(sh.Lines(), "\n"), ui.Events)
	}
}

// Setup completes Jellyfin's first run with a generated administrator, adds
// both libraries, and connects Jellyfin to Authentik, asking nothing.
func TestSetupConfiguresJellyfinWithoutQuestions(t *testing.T) {
	sh := &fake.Shell{Respond: dockerHost("")}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	if err := s.Setup(); err != nil {
		t.Fatal(err)
	}
	values := readValues(t, s)
	var requests []string
	for i, command := range sh.Commands {
		if len(command) > 4 && command[3] == "selfnook-jellyfin-app" && command[4] == "curl" {
			body := ""
			if data := fake.CurlBody(sh.Inputs[i]); data != "" {
				body = " " + data
			}
			address := command[len(command)-1]
			requests = append(requests, command[slices.Index(command, "-X")+1]+" "+address[strings.Index(address, "/jellyfin")+len("/jellyfin"):]+body)
		}
	}
	login := `POST /Startup/User {"Name":"selfnook","Password":"` + values["JELLYFIN_ADMIN_PASS"] + `"}`
	want := []string{
		login,
		"POST /Startup/Complete",
		`POST /Library/VirtualFolders?name=Movies&collectionType=movies&refreshLibrary=false {"LibraryOptions":{"PathInfos":[{"Path":"/media/Movies"}]}}`,
		`POST /Library/VirtualFolders?name=Shows&collectionType=tvshows&refreshLibrary=false {"LibraryOptions":{"PathInfos":[{"Path":"/media/Series"}]}}`,
		"POST /sso/OID/Add/authentik",
		`POST /System/Configuration/branding {"CustomCss"`,
	}
	at := 0
	for _, request := range requests {
		if at < len(want) && strings.HasPrefix(request, want[at]) {
			at++
		}
	}
	if len(values["JELLYFIN_ADMIN_PASS"]) < 24 || at != len(want) {
		t.Errorf("missing %q among requests:\n%s", want[min(at, len(want)-1)], strings.Join(requests, "\n"))
	}
	for _, request := range requests {
		if strings.HasPrefix(request, "POST /System/Configuration/branding") && !strings.Contains(request, `action=\"https://media.example.com/jellyfin/sso/OID/start/authentik\"`) {
			t.Errorf("login button = %s", request)
		}
	}
}

// seerrHost answers like a running stack whose Seerr gives status to a new user.
func seerrHost(status string) func([]string) shell.Result {
	host := dockerHost("")
	return func(args []string) shell.Result {
		if strings.HasPrefix(strings.Join(args, " "), "docker exec -i selfnook-seerr node -e ") {
			return shell.Result{Stdout: status + "\n{}\n"}
		}
		return host(args)
	}
}

// Given a running stack, add-user asks for the person once, sends them to
// Seerr on the command's input with a generated password, and shows that
// password with the email and Seerr's address, because Seerr cannot email it.
func TestAddUserCreatesASeerrLoginAndShowsItsPassword(t *testing.T) {
	s, sh, ui := startedStack(t, "")
	sh.Respond = seerrHost("201")
	ui.Answers = map[string]string{"email": " ana@example.org ", "name": "Ana"}
	if err := s.AddUser(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ui.Events[:3], []string{"screen: New user", "ask: email", "ask: name"}) {
		t.Fatalf("events = %q", ui.Events)
	}
	i := sh.Index("docker exec -i selfnook-seerr node -e ")
	if i < 0 {
		t.Fatalf("commands:\n%s", strings.Join(sh.Lines(), "\n"))
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(sh.Inputs[i]), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["email"] != "ana@example.org" || sent["username"] != "Ana" || len(sent["password"]) < 16 {
		t.Errorf("sent = %q", sent)
	}
	if strings.Contains(strings.Join(sh.Commands[i], " "), sent["password"]) {
		t.Error("the password appears in the command line")
	}
	said := ui.Events[len(ui.Events)-1]
	for _, want := range []string{":5055\n", "Email: ana@example.org\n", "Password: " + sent["password"] + "\n"} {
		if !strings.Contains(said, want) {
			t.Errorf("said %q, want %q", said, want)
		}
	}
}

// Seerr refuses a second user with the same email; add-user names it.
func TestAddUserNamesAnEmailSeerrAlreadyHas(t *testing.T) {
	s, sh, ui := startedStack(t, "")
	sh.Respond = seerrHost("409")
	ui.Answers = map[string]string{"email": "ana@example.org", "name": "Ana"}
	err := s.AddUser()
	if err == nil || err.Error() != "Seerr already has a user with the email address ana@example.org" {
		t.Errorf("err = %v", err)
	}
}
