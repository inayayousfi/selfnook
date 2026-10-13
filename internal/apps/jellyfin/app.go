// Package jellyfin is the media server. It stays outside the admin sign-in
// because its phone and TV applications cannot pass it.
package jellyfin

import (
	"embed"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
)

//go:embed compose.yaml
var compose embed.FS

const (
	userKey     = "JELLYFIN_ADMIN_USER"
	passwordKey = "JELLYFIN_ADMIN_PASS"
	keyName     = "JELLYFIN_API_KEY"
	// secretKey is the secret Jellyfin's sign-in plugin shares with the
	// sign-in service. Selfnook generates it.
	secretKey = "JELLYFIN_OIDC_SECRET"
	// clientID identifies Jellyfin to the sign-in service.
	clientID = "jellyfin"
	baseURL  = "/jellyfin"
	service  = "jellyfin-app"
	host     = "jellyfin"
	port     = 8096
	// moviesLibrary and seriesLibrary are the libraries setup creates.
	moviesLibrary = "Movies"
	seriesLibrary = "Shows"
)

var App = &app.App{
	Name:  "jellyfin",
	Title: "Jellyfin",
	Settings: []app.Setting{
		{Key: userKey},
		{Key: passwordKey},
		{Key: keyName},
		{Key: secretKey},
	},
	Renamed: map[string]string{
		"JELLYFIN_USER": userKey,
		"JELLYFIN_PASS": passwordKey,
	},
	Needs:   []role.Role{role.AccessAddress, role.AdminSignIn, role.Movies, role.Series},
	Compose: compose,
	Environment: func(r role.Set, _ app.ComposeData) map[string]string {
		return map[string]string{"JELLYFIN_PUBLIC_URL": role.Get(r, role.AccessAddress).URL() + baseURL}
	},
	Derive: func(v app.Values) {
		if !v.Has(secretKey) {
			v.Set(secretKey, app.RandomToken(48))
		}
	},
	Services:   func(app.Values) []string { return []string{service} },
	ConfigDirs: []string{"jellyfin", "jellyfin-cache"},
	Images: []app.Image{{
		Service: service,
		Digests: map[string]string{
			"sha256:aefb67e6a7ff1debdd154a78a7bbb780fd0c873d8639210a7f6a2016ad2b35db": "10.11.11",
			"sha256:357724bf0ae27a672c7cbaa899db2d9abeb13dbd8657ccce750258a4c059d037": "12.2.0",
		},
		Labels: map[string]string{"10.11.11": "10.11.11", "12.2": "12.2.0"},
		Ask: func(e *app.Env) (string, error) {
			return app.AskVersion(e, service, "http://localhost:"+strconv.Itoa(port)+baseURL+"/System/Info/Public", "Version")
		},
	}},
	Implementations: []app.Implementation{{
		From: "12.2.0", Until: "12.2.0",
		Provides: []role.Offer{role.Provide(role.Media, func(v role.Values, _ role.Set) role.MediaServer { return server{v} })},
		Prepare:  prepare,
		Setup:    setup,
		AfterStart: func(e *app.Env) error {
			if !e.Values.Has(keyName) {
				return nil
			}
			a, err := signIn(api{e: e})
			if err != nil {
				return err
			}
			return applySignIn(a, role.Get(e.Roles, role.AccessAddress), role.Get(e.Roles, role.AdminSignIn))
		},
		Routes: func(app.Values, role.Set) []app.Route {
			return []app.Route{{
				Name:     "Jellyfin",
				Path:     baseURL,
				Upstream: service + ":" + strconv.Itoa(port),
				Public:   true,
				Internal: &app.Internal{Name: host, Port: port},
			}}
		},
		Tiles: func(app.Values, role.Set) []app.Tile {
			return []app.Tile{{Group: "Media", Position: 1, Name: "Jellyfin", Description: "Watch movies and series", Link: "/jellyfin", Service: "jellyfin-app"}}
		},
		Clients: func(_ app.Values, r role.Set) []app.Client {
			return []app.Client{{
				Name:      "Jellyfin",
				ID:        clientID,
				Return:    returnAddress(role.Get(r, role.AccessAddress), role.Get(r, role.AdminSignIn)),
				SecretKey: secretKey,
			}}
		},
	}},
	Credentials: &app.CredentialGroup{Name: "jellyfin", Fields: []app.CredentialField{
		{Label: "Administrator username", Key: userKey, Kind: app.PlainText},
		{Label: "Administrator password", Key: passwordKey, Kind: app.Password},
		{Label: "API key", Key: keyName, Kind: app.APIKey},
	}},
}

// server is the media server role. Other containers reach it through
// Caddy's internal name.
type server struct{ v role.Values }

func (server) Name() string { return "Jellyfin" }

func (server) Endpoint() role.Endpoint {
	return role.Endpoint{Service: service, Host: host, Port: port}
}

func (s server) AdminUser() string     { return s.v.Get(userKey) }
func (s server) AdminPassword() string { return s.v.Get(passwordKey) }
func (s server) APIKey() string        { return s.v.Get(keyName) }

// Libraries are the libraries setup creates.
func (server) Libraries() (string, string) { return moviesLibrary, seriesLibrary }

// prepare sets Jellyfin's base URL while Jellyfin is stopped, because Jellyfin
// rewrites its network file when it exits, and installs the sign-in plugin,
// which Jellyfin loads when it starts.
func prepare(e *app.Env) ([]string, error) {
	var restart []string
	if path := networkFile(e.ConfigDir()); !BaseURLIsSet(path) {
		if err := e.Runtime.Stop(service); err != nil {
			return nil, err
		}
		if err := WriteBaseURL(path); err != nil {
			return nil, err
		}
	}
	changed, err := installPlugin(pluginsDir(e.ConfigDir()), e.Fetch)
	if err != nil {
		return nil, err
	}
	if changed {
		restart = append(restart, service)
	}
	return restart, nil
}

// setup completes Jellyfin's first run, names the server, creates the
// libraries and the API key the other apps use, and connects the sign-in plugin. It asks nothing.
func setup(e *app.Env) error {
	access, signInRole := role.Get(e.Roles, role.AccessAddress), role.Get(e.Roles, role.AdminSignIn)
	movies, series := role.Get(e.Roles, role.Movies), role.Get(e.Roles, role.Series)
	err := e.Step("jellyfin", !e.Values.Has(keyName), func() error {
		a := api{e: e}
		complete, err := waitReady(a)
		if err != nil {
			return err
		}
		if !complete {
			if err := firstRun(a); err != nil {
				return err
			}
		}
		if a, err = signIn(a); err != nil {
			return err
		}
		if err := ensureServerName(a); err != nil {
			return err
		}
		if err := ensureLibraries(a, movies, series); err != nil {
			return err
		}
		if err := ensureKey(a); err != nil {
			return err
		}
		return applySignIn(a, access, signInRole)
	})
	if err != nil {
		return err
	}

	return e.Step("jellyfin-notifications", false, func() error {
		screen, err := NotificationsGuide(access.URL(), server{e.Values}, movies, series)
		if err != nil {
			return err
		}
		return flow.Show(e.UI, screen)
	})
}

// NotificationsGuide makes the libraries refresh Jellyfin after each import.
func NotificationsGuide(accessURL string, media role.MediaServer, libraries ...role.Library) (flow.Screen, error) {
	var links []string
	var steps []string
	for _, library := range libraries {
		links = append(links, library.Name()+" link:\n"+accessURL+library.WebPath())
		librarySteps, err := library.MediaServerSteps(media)
		if err != nil {
			return flow.Screen{}, err
		}
		steps = append(steps, librarySteps...)
	}
	for i := range steps {
		steps[i] = strconv.Itoa(i+1) + ". " + steps[i]
	}
	return flow.Screen{
		Title: "Jellyfin notifications",
		Body:  strings.Join(links, "\n\n") + "\n\n" + strings.Join(steps, "\n"),
		Wait:  "Jellyfin notification",
	}, nil
}

func networkFile(configDir string) string {
	return filepath.Join(configDir, "jellyfin", "config", "network.xml")
}

// BaseURLIsSet reports whether Jellyfin's network file already serves /jellyfin.
func BaseURLIsSet(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var network struct {
		BaseURL *string `xml:"BaseUrl"`
	}
	if xml.Unmarshal(content, &network) != nil || network.BaseURL == nil {
		return false
	}
	return *network.BaseURL == baseURL
}

// WriteBaseURL sets BaseUrl in Jellyfin's network file, keeping every other setting.
func WriteBaseURL(path string) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		content = []byte(`<?xml version='1.0' encoding='utf-8'?>` + "\n<NetworkConfiguration />")
	} else if err != nil {
		return err
	}
	updated, err := setBaseURL(string(content))
	if err != nil {
		return err
	}
	return files.WriteAtomic(path, []byte(updated), 0o644)
}

// setBaseURL edits the BaseUrl element of the root element as text, so the
// rest of the file keeps its exact form.
func setBaseURL(document string) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(document))
	depth := 0
	rootEnd, start, end := -1, -1, -1
	rootSelfClosing := false
	for {
		offset := decoder.InputOffset()
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				rootEnd = int(decoder.InputOffset())
				rootSelfClosing = strings.HasSuffix(strings.TrimSpace(document[offset:rootEnd]), "/>")
			}
			if depth == 2 && t.Name.Local == "BaseUrl" && start < 0 {
				start = int(offset)
			}
		case xml.EndElement:
			if depth == 2 && t.Name.Local == "BaseUrl" && start >= 0 && end < 0 {
				end = int(decoder.InputOffset())
			}
			depth--
		}
	}
	if rootEnd < 0 {
		return "", errors.New("Jellyfin's network.xml has no root element.")
	}
	element := "<BaseUrl>" + baseURL + "</BaseUrl>"
	switch {
	case start >= 0 && end >= 0:
		return document[:start] + element + document[end:], nil
	case start >= 0:
		// A self-closing <BaseUrl />.
		closeAt := strings.Index(document[start:], "/>") + start + 2
		return document[:start] + element + document[closeAt:], nil
	case rootSelfClosing:
		openTag := strings.TrimSuffix(strings.TrimSpace(document[:rootEnd]), "/>")
		name := strings.Fields(openTag[strings.LastIndex(openTag, "<")+1:])[0]
		return strings.TrimRight(openTag, " ") + ">" + element + "</" + name + ">" + document[rootEnd:], nil
	default:
		return document[:rootEnd] + element + document[rootEnd:], nil
	}
}
