// Package qbittorrent is the download client. It runs inside the VPN gateway's network.
package qbittorrent

import (
	"embed"
	"errors"
	"os"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
)

//go:embed compose.yaml
var compose embed.FS

const (
	noticeKey = "QBT_LEGAL_NOTICE"
	host      = "qbittorrent"
	port      = 8080
)

var App = &app.App{
	Name:     "qbittorrent",
	Title:    "qBittorrent",
	Settings: []app.Setting{{Key: noticeKey, Required: true}},
	Needs:    []role.Role{role.AccessAddress, role.AdminSignIn, role.VPN},
	Compose:  compose,
	Environment: func(r role.Set, _ app.ComposeData) map[string]string {
		return map[string]string{"QBITTORRENT_NETWORK": "service:" + role.Get(r, role.VPN).Service()}
	},
	Services:   func(app.Values) []string { return []string{"qbittorrent"} },
	BehindVPN:  []string{"qbittorrent"},
	ConfigDirs: []string{"qbittorrent"},
	MediaDirs:  []string{"Downloads"},
	Validate: func(v app.Values) error {
		if v.Get(noticeKey) != "confirm" {
			return errors.New("qBittorrent's legal notice is not confirmed in the saved settings.")
		}
		return nil
	},
	Configure: configure,
	Images: []app.Image{{
		Service: "qbittorrent",
		Digests: map[string]string{"sha256:9ebb534fe30bab98622cb84a8c3acecfd88319b2d540f52ecdec7b9f866374d7": "5.2.3"},
		Labels:  map[string]string{"5.2.3-1": "5.2.3"},
	}},
	Implementations: []app.Implementation{{
		From: "5.2.3", Until: "5.2.3",
		Provides: []role.Offer{role.Provide(role.Downloads, func(role.Values, role.Set) role.DownloadClient { return client{} })},
		Prepare: func(e *app.Env) ([]string, error) {
			_, err := WriteLoginBypass(e.ConfigPath(configFile...))
			return nil, err
		},
		Setup: setup,
		Routes: func(_ app.Values, r role.Set) []app.Route {
			return []app.Route{{
				Name:        "qBittorrent",
				Path:        "/qbittorrent",
				Upstream:    role.Get(r, role.VPN).Service() + ":8080",
				StripPrefix: true,
			}}
		},
		Tiles: func(app.Values, role.Set) []app.Tile {
			return []app.Tile{{Group: "Admin", Position: 1, Name: "qBittorrent", Description: "Manage downloads", Link: "/qbittorrent/", Service: "qbittorrent"}}
		},
		Shared: &app.Shared{Alias: host, Ports: []string{"8080", "6881"}, Published: []string{"6881:6881/tcp", "6881:6881/udp"}},
	}},
}

// client is the download client role.
type client struct{}

func (client) Name() string { return "qBittorrent" }

// Login is empty: Prepare lets the stack network in without a login.
func (client) Login() (string, string) { return "", "" }

func (client) Endpoint() role.Endpoint {
	return role.Endpoint{Service: "qbittorrent", Host: host, Port: port}
}

func configure(e *app.Env) error {
	if e.Values.Get(noticeKey) == "confirm" {
		return nil
	}
	answers, err := e.UI.Ask(flow.Screen{Fields: []flow.Field{{
		Key:     "accept",
		Prompt:  "Accept qBittorrent's legal notice?",
		Kind:    flow.YesNo,
		Default: "yes",
	}}})
	if err != nil {
		return err
	}
	if answers["accept"] != "yes" {
		return errors.New("qBittorrent's legal notice was not accepted.")
	}
	e.Values.Set(noticeKey, "confirm")
	return nil
}

func setup(e *app.Env) error {
	return e.Step("qbittorrent", false, func() error {
		signIn := role.Get(e.Roles, role.AdminSignIn)
		return flow.Show(e.UI, Guide(role.Get(e.Roles, role.AccessAddress).URL(), signIn.User(), signIn.Password()))
	})
}

// Guide explains the download folders to set in qBittorrent.
func Guide(accessURL, adminUser, adminPassword string) flow.Screen {
	return flow.Screen{
		Title: "qBittorrent setup",
		Body: `Open this link:
` + accessURL + `/qbittorrent/

1. If the Selfnook sign-in page appears, sign in with the admin login.
2. Username: ` + adminUser + `
3. Password: ` + adminPassword + `
4. In qBittorrent, open Tools > Options > Downloads.
5. Set Saving Management > Default Save Path to /media/Downloads
6. Set Keep incomplete torrents in to /media/Downloads/incomplete
7. Click Save.`,
		Wait: "qBittorrent",
	}
}

var configFile = []string{"qbittorrent", "qBittorrent", "config", "qBittorrent.conf"}

// WriteLoginBypass lets requests from the stack network skip qBittorrent's
// login, keeping every other setting. It reports whether the file changed.
func WriteLoginBypass(path string) (bool, error) {
	wanted := [][2]string{
		{`WebUI\AuthSubnetWhitelistEnabled`, "true"},
		{`WebUI\AuthSubnetWhitelist`, runtime.NetworkSubnet},
	}
	originalBytes, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	original := string(originalBytes)
	lines := splitLines(original)
	start := -1
	for i, line := range lines {
		if line == "[Preferences]" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		lines = append(lines, "[Preferences]")
		start = len(lines)
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "[") {
			end = i
			break
		}
	}
	var section []string
	for _, line := range lines[start:end] {
		key, _, _ := strings.Cut(line, "=")
		if key != wanted[0][0] && key != wanted[1][0] {
			section = append(section, line)
		}
	}
	for _, setting := range wanted {
		section = append(section, setting[0]+"="+setting[1])
	}
	result := append(append(append([]string{}, lines[:start]...), section...), lines[end:]...)
	return files.WriteIfChanged(path, []byte(strings.Join(result, "\n")+"\n"))
}

// splitLines splits like Python's str.splitlines for "\n" and "\r\n" endings.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
