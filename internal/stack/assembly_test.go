package stack

import (
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/apps"
	"github.com/inayayousfi/selfnook/internal/apps/caddy"
	"github.com/inayayousfi/selfnook/internal/apps/homepage"
	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
	"github.com/inayayousfi/selfnook/internal/shell"
	"github.com/inayayousfi/selfnook/internal/state"
)

// assembled is a session whose routes and tiles come from every active app,
// with the access address and gateway given.
func assembled(t *testing.T, mode, host, gateway string) *session {
	t.Helper()
	s := newTestStack(t, &fake.UI{}, &fake.Shell{})
	writeState(t, s, map[string]string{"ACCESS_MODE": mode, "ADMIN_ACCESS_HOST": host}, map[string]string{"gateway": gateway})
	ses, _, err := s.open()
	if err != nil {
		t.Fatal(err)
	}
	if err := ses.assignRoles(false); err != nil {
		t.Fatal(err)
	}
	ses.derive()
	return ses
}

func caddyfile(t *testing.T, mode, host, gateway string) string {
	t.Helper()
	ses := assembled(t, mode, host, gateway)
	check, err := role.Get(ses.roles(caddy.App), role.AdminSignIn).Check("Caddy")
	if err != nil {
		t.Fatal(err)
	}
	file, err := caddy.Caddyfile(app.ValuesFor(ses.saved, caddy.App), ses.contributions().Routes, check)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// guarded reports whether the block that forwards to upstream checks the sign-in first.
func guarded(t *testing.T, file, upstream string) bool {
	t.Helper()
	end := strings.Index(file, "reverse_proxy "+upstream+"\n")
	if end < 0 {
		t.Fatalf("no route to %s:\n%s", upstream, file)
	}
	block := file[strings.LastIndex(file[:end], "handle"):end]
	return strings.Contains(block, "forward_auth authentik-server:9000")
}

func TestJellyfinAndHomepageStayOutsideTheSignInAndAdminPagesInside(t *testing.T) {
	file := caddyfile(t, "domain", "media.example.com", "gluetun")
	for _, upstream := range []string{"jellyfin-app:8096", "homepage:3000"} {
		if guarded(t, file, upstream) {
			t.Errorf("%s is behind the sign-in", upstream)
		}
	}
	for _, upstream := range []string{"sonarr-app:8989", "radarr-app:7878", "gluetun:9696", "gluetun:8080", "vpn-country:8090"} {
		if !guarded(t, file, upstream) {
			t.Errorf("%s is not behind the sign-in", upstream)
		}
	}
}

func TestSeerrAndSignInHaveTheirOwnPorts(t *testing.T) {
	file := caddyfile(t, "domain", "media.example.com", "gluetun")
	for _, part := range []string{
		"redir @seerr https://media.example.com:5055/",
		"media.example.com:5055 {\n\treverse_proxy seerr:5055\n}",
		"media.example.com:9091 {\n\treverse_proxy authentik-server:9000\n}",
	} {
		if !strings.Contains(file, part) {
			t.Errorf("missing %q", part)
		}
	}
}

// The ports Caddy publishes come from the apps that serve their own port,
// so Seerr and the sign-in pages are reachable from outside.
func TestCaddyPublishesThePortsOfItsRoutes(t *testing.T) {
	ses := assembled(t, "domain", "media.example.com", "gluetun")
	if err := ses.writeCompose(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(ses.Root, "compose", "caddy", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []string{`"80:80/tcp"`, `"443:443/tcp"`, `"5055:5055/tcp"`, `"9091:9091/tcp"`} {
		if !strings.Contains(string(content), port) {
			t.Errorf("Caddy does not publish %s:\n%s", port, content)
		}
	}
}

func TestQBittorrentUsesTheSelectedGatewayAndCountryPageNeedsGluetun(t *testing.T) {
	file := caddyfile(t, "local", "box.local", "tailscale")
	if !strings.Contains(file, "reverse_proxy tailscale-vpn:8080") || strings.Contains(file, "vpn-country") {
		t.Errorf("Caddyfile:\n%s", file)
	}
	if !strings.HasPrefix(strings.SplitN(file, "\n", 4)[2], "http://box.local {") {
		t.Errorf("local mode is not served over plain HTTP:\n%s", file)
	}
}

func TestTailscaleModeUsesTheTailscaleCertificate(t *testing.T) {
	file := caddyfile(t, "tailscale", "box.tail.ts.net", "gluetun")
	if strings.Count(file, "tls /certs/cert.pem /certs/key.pem") != 3 {
		t.Errorf("each site must use the certificate:\n%s", file)
	}
}

func TestInternalNamesAddTheirPathOnlyWhenMissing(t *testing.T) {
	file := caddyfile(t, "domain", "media.example.com", "gluetun")
	want := "http://:8989 {\n\t@missing not path /sonarr /sonarr/*\n\trewrite @missing /sonarr{uri}\n\treverse_proxy sonarr-app:8989\n}"
	if !strings.Contains(file, want) {
		t.Errorf("missing internal Sonarr address:\n%s", file)
	}
}

// Tiles follow their own positions, not the order of the registry.
func TestTilesFollowTheirPositions(t *testing.T) {
	ses := assembled(t, "domain", "media.example.com", "gluetun")
	tiles := ses.contributions().Tiles
	slices.Reverse(tiles)
	var names []string
	for _, line := range strings.Split(homepage.Services(tiles), "\n") {
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "    - ") {
			names = append(names, strings.TrimSpace(strings.TrimSuffix(line, ":")))
		}
	}
	want := "- Media - Jellyfin - Seerr - Admin - qBittorrent - Sonarr - Radarr - Prowlarr - Gluetun - Authentik"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("order = %s", got)
	}
}

// Every role an app needs has an app in the registry that can fill it.
func TestEveryNeededRoleHasAProvider(t *testing.T) {
	ses := &session{Stack: &Stack{Apps: apps.All}}
	for _, a := range apps.All {
		for _, need := range a.Needs {
			if len(ses.providers(need.Info().Name)) == 0 {
				t.Errorf("%s needs %s, which no app provides", a.Name, need.Info().Name)
			}
		}
	}
}

// Choosing the Tailscale exit node in setup makes the Tailscale app fill the
// gateway role: only its Compose fragment is written, and qBittorrent shares
// its network.
func TestTailscaleChoiceRunsOnlyTheTailscaleGateway(t *testing.T) {
	sh := &fake.Shell{Respond: tailscaleHost}
	ui := &fake.UI{}
	s := newTestStack(t, ui, sh)
	ui.Answers = setupAnswers(filepath.Join(t.TempDir(), "Media"))
	ui.Answers["provider"] = "tailscale/tailscale"
	ui.Answers["TAILSCALE_AUTH_KEY"] = "tskey-auth"
	ui.Answers["TAILSCALE_EXIT_NODE"] = "exit-1"
	if err := s.Setup(); err != nil {
		t.Fatalf("setup failed: %v\nevents: %v", err, ui.Events)
	}
	if roles := readState(t, s).Roles; roles["gateway"] != "tailscale" {
		t.Errorf("roles = %v", roles)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "compose", "gluetun")); err == nil {
		t.Error("the Gluetun fragment was written although Tailscale was chosen")
	}
	fragment, _ := os.ReadFile(filepath.Join(s.Root, "compose", "tailscale", "compose.yaml"))
	if !strings.Contains(string(fragment), "        aliases:\n          - qbittorrent\n") {
		t.Errorf("Tailscale fragment does not answer for qBittorrent:\n%s", fragment)
	}
	up := sh.Index("docker compose up -d --no-deps qbittorrent prowlarr")
	if up < 0 || sh.Envs[up]["QBITTORRENT_NETWORK"] != "service:tailscale-vpn" {
		t.Errorf("qBittorrent does not share the Tailscale network:\n%s", strings.Join(sh.Lines(), "\n"))
	}
}

// tailscaleHost answers like a Docker host whose Tailscale exit node is online.
func tailscaleHost(args []string) shell.Result {
	if strings.HasPrefix(strings.Join(args, " "), "docker exec selfnook-tailscale-vpn tailscale status") {
		return shell.Result{Stdout: `{"ExitNodeStatus": {"Online": true}}`}
	}
	return dockerHost("")(args)
}

// Secrets reach the containers through the environment of the Compose
// commands, never through a file the program generates.
func TestSecretsStayOutOfGeneratedFiles(t *testing.T) {
	s, sh, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	saved, _, err := state.Read(s.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	clientSecret := saved.Get("jellyfin", "JELLYFIN_OIDC_SECRET")
	if clientSecret == "" {
		t.Fatal("no Jellyfin sign-in secret was generated")
	}
	secrets := []string{sonarrKey, radarrKey, "admin-secret", "vpn-secret", clientSecret}
	err = filepath.WalkDir(s.Root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		for _, secret := range secrets {
			if strings.Contains(string(content), secret) {
				t.Errorf("%s holds a secret", path)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	up := sh.Index("docker compose up -d --no-deps --remove-orphans")
	env := sh.Envs[up]
	if env["RECYCLARR_SERIES_API_KEY"] != sonarrKey || env["ADMIN_PASS"] != "admin-secret" || env["HOMEPAGE_VAR_GLUETUN_KEY"] == "" || env["JELLYFIN_OIDC_SECRET"] != clientSecret {
		t.Errorf("Compose did not receive the secrets: %v", env)
	}
}

var reference = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)(:-[^}]*)?\}`)

// Each Compose fragment reads only its own settings, the core settings, and
// the values its app declares from roles, named after the app; and every
// value a running fragment reads is given.
func TestFragmentsReadOnlyTheirOwnValues(t *testing.T) {
	s, _, _ := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	ses, _, err := s.open()
	if err != nil {
		t.Fatal(err)
	}
	if err := ses.assignRoles(false); err != nil {
		t.Fatal(err)
	}
	env := ses.composeEnv()
	active := ses.active()
	for _, a := range apps.All {
		content, err := os.ReadFile(filepath.Join("..", "apps", a.Name, "compose.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		prefix := strings.ToUpper(a.Name) + "_"
		for _, match := range reference.FindAllStringSubmatch(string(content), -1) {
			name := match[1]
			if !a.Owns(name) && !app.Core.Owns(name) && !strings.HasPrefix(name, prefix) {
				t.Errorf("%s reads %s, which belongs to another app", a.Name, name)
			}
			if _, ok := env[name]; slices.Contains(active, a) && !ok && !strings.Contains(name, "{{") {
				t.Errorf("%s reads %s, which no value gives", a.Name, name)
			}
		}
	}
}

// When a running app's image is unknown, the program asks the app for its
// version, records it, and, having no steps for that version, uses the
// built-in ones with a warning.
func TestUnknownRunningVersionWarnsAndUsesTheBuiltInSteps(t *testing.T) {
	s, sh, ui := startedStack(t, "172.31.250.0/24 172.31.250.128/25")
	host := sh.Respond
	sh.Respond = func(args []string) shell.Result {
		line := strings.Join(args, " ")
		switch {
		case line == "docker inspect selfnook-radarr-app --format {{.Image}}":
			return shell.Result{Stdout: "sha256:local\n"}
		case strings.HasPrefix(line, "docker image inspect sha256:local "):
			return shell.Result{Stdout: "lscr.io/linuxserver/radarr@sha256:0000 |6.5.0.1-ls400\n"}
		case strings.HasPrefix(line, "docker exec selfnook-radarr-app curl -fsS -H X-Api-Key: "):
			return shell.Result{Stdout: `{"version": "6.5.0.1"}`}
		}
		return host(args)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	want := "say: Radarr 6.5.0.1 runs, but Selfnook has no steps for that version. These steps were written for Radarr 6.4.4.10685 and may not match exactly. Updating Radarr or Selfnook may help."
	if ui.Position(want) < 0 {
		t.Errorf("events = %v", ui.Events)
	}
	recorded := readState(t, s).Versions["radarr-app"]
	if recorded != (state.Version{Version: "6.5.0.1", Source: state.FromApp, Digest: "sha256:0000", Label: "6.5.0.1-ls400"}) {
		t.Errorf("recorded = %+v", recorded)
	}
	if sonarr := readState(t, s).Versions["sonarr-app"]; sonarr.Version != "4.0.20.3014" {
		t.Errorf("Sonarr = %+v", sonarr)
	}
}

// Given one saved address, writing the Compose files keeps it, gives every
// other container on the network its own fixed address outside Docker's
// automatic range, saves them, and writes them for Compose. Containers
// behind the VPN share the gateway's address and get none.
func TestEachContainerKeepsAFixedAddress(t *testing.T) {
	ses := assembled(t, "domain", "media.example.com", "gluetun")
	ses.saved.Addresses["jellyfin-app"] = "172.31.250.2"
	if err := ses.writeCompose(); err != nil {
		t.Fatal(err)
	}
	saved := readState(t, ses.Stack).Addresses
	if saved["jellyfin-app"] != "172.31.250.2" {
		t.Errorf("saved address changed: %v", saved)
	}
	if _, ok := saved["qbittorrent"]; ok {
		t.Errorf("a container behind the VPN got an address: %v", saved)
	}
	automatic := netip.MustParsePrefix(runtime.AutomaticRange)
	seen := map[string]string{}
	for _, service := range []string{"sonarr-app", "radarr-app", "gluetun", "caddy", "authentik-server", "seerr"} {
		address, err := netip.ParseAddr(saved[service])
		if err != nil || automatic.Contains(address) || seen[saved[service]] != "" {
			t.Errorf("%s address = %q, addresses = %v", service, saved[service], saved)
		}
		seen[saved[service]] = service
	}
	content, err := os.ReadFile(filepath.Join(ses.Root, "compose", "jellyfin", addressFile))
	if err != nil || !strings.Contains(string(content), "  jellyfin-app:\n    networks:\n      default:\n        ipv4_address: 172.31.250.2\n") {
		t.Errorf("address file = %q, %v", content, err)
	}
}
