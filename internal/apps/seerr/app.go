// Package seerr takes movie and series requests and sends them to the
// libraries. It keeps its own login and is served on its own port.
package seerr

import (
	"embed"
	"strconv"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
)

//go:embed compose.yaml
var compose embed.FS

// Port is where Seerr is served, because it does not support a path prefix.
const Port = 5055

var App = &app.App{
	Name:    "seerr",
	Title:   "Seerr",
	Needs:   []role.Role{role.AccessAddress, role.Media, role.Movies, role.Series, role.Profiles},
	Compose: compose,
	Environment: func(r role.Set, _ app.ComposeData) map[string]string {
		return map[string]string{
			"SEERR_MEDIA_SERVER": role.Get(r, role.Media).Endpoint().Service,
			"SEERR_MOVIES":       role.Get(r, role.Movies).Endpoint().Service,
			"SEERR_SERIES":       role.Get(r, role.Series).Endpoint().Service,
		}
	},
	Services:   func(app.Values) []string { return []string{"seerr"} },
	ConfigDirs: []string{"seerr"},
	Images: []app.Image{{
		Service: "seerr",
		Digests: map[string]string{"sha256:27602401178d54f1964442287b9f23f67a3fa2252645ee8065839ea1c3f69e45": "3.5.0"},
		Labels:  map[string]string{"v3.5.0": "3.5.0"},
	}},
	Implementations: []app.Implementation{{
		From: "3.5.0", Until: "3.5.0",
		Setup: func(e *app.Env) error {
			return e.Step("seerr", false, func() error {
				screen, err := Guide(role.Get(e.Roles, role.AccessAddress).URL(), role.Get(e.Roles, role.Media), role.Get(e.Roles, role.Movies), role.Get(e.Roles, role.Series), role.Get(e.Roles, role.Profiles))
				if err != nil {
					return err
				}
				return flow.Show(e.UI, screen)
			})
		},
		Routes: func(app.Values, role.Set) []app.Route {
			return []app.Route{{Name: "Seerr", Path: "/seerr", Upstream: "seerr:" + strconv.Itoa(Port), Public: true, Port: Port}}
		},
		Tiles: func(app.Values, role.Set) []app.Tile {
			return []app.Tile{{Group: "Media", Position: 2, Name: "Seerr", Description: "Request movies and series", Link: "/seerr", Service: "seerr"}}
		},
	}},
}

// servers are the media servers Seerr connects to, as its server type
// choice lists them.
var servers = map[string]string{"Jellyfin": "Jellyfin"}

// libraries are the software Seerr sends each kind of request to.
var libraries = map[string]string{"movies": "Radarr", "series": "Sonarr"}

// Guide connects Seerr to the media server and both libraries with the saved logins and keys.
func Guide(accessURL string, media role.MediaServer, movies, series role.Library, profiles role.QualityProfiles) (flow.Screen, error) {
	serverType, ok := servers[media.Name()]
	if !ok {
		return flow.Screen{}, &role.Unsupported{App: "Seerr", Other: media.Name()}
	}
	for kind, library := range map[string]role.Library{"movies": movies, "series": series} {
		if library.Name() != libraries[kind] {
			return flow.Screen{}, &role.Unsupported{App: "Seerr", Other: library.Name()}
		}
	}
	moviesProfile, err := profiles.Profile(movies)
	if err != nil {
		return flow.Screen{}, err
	}
	seriesProfile, err := profiles.Profile(series)
	if err != nil {
		return flow.Screen{}, err
	}
	moviesLibrary, seriesLibrary := media.Libraries()
	server, movieHost, seriesHost := media.Endpoint(), movies.Endpoint(), series.Endpoint()
	return flow.Screen{
		Title: "Seerr setup",
		Body: `Open this link:
` + accessURL + ":" + strconv.Itoa(Port) + `

1. Choose ` + serverType + ` as the server type.
2. ` + media.Name() + ` URL: ` + server.Host + `
3. Port: ` + strconv.Itoa(server.Port) + `
4. Email Address: enter an email address of your choice. Seerr uses it for notifications and its own sign-in.
5. Username: ` + media.AdminUser() + `
6. Password: ` + media.AdminPassword() + `
7. Click Sign In.
8. Click Sync Libraries, enable the ` + moviesLibrary + ` and ` + seriesLibrary + ` libraries, then click Continue.
9. Click Add ` + movies.Name() + ` Server and check Default Server.
10. Server Name: ` + movies.Name() + `
11. Hostname or IP Address: ` + movieHost.Host + `
12. Port: ` + strconv.Itoa(movieHost.Port) + `
13. API Key: ` + movies.APIKey() + `
14. Click Test.
15. Quality Profile: ` + moviesProfile + `
16. Root Folder: ` + movies.RootFolder() + `
17. Select a Minimum Availability, then click Add Server.
18. Click Add ` + series.Name() + ` Server and check Default Server.
19. Server Name: ` + series.Name() + `
20. Hostname or IP Address: ` + seriesHost.Host + `
21. Port: ` + strconv.Itoa(seriesHost.Port) + `
22. API Key: ` + series.APIKey() + `
23. Click Test.
24. Quality Profile: ` + seriesProfile + `
25. Root Folder: ` + series.RootFolder() + `
26. Check Season Folders, then click Add Server.
27. Click Finish Setup.`,
		Wait: "Seerr",
	}, nil
}
