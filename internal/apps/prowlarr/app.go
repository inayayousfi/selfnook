// Package prowlarr manages indexers. It runs inside the VPN gateway's network.
package prowlarr

import (
	"embed"
	"strconv"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
)

//go:embed compose.yaml
var compose embed.FS

const (
	service = "prowlarr"
	host    = "prowlarr"
	port    = 9696
)

var App = &app.App{
	Name:    "prowlarr",
	Title:   "Prowlarr",
	Needs:   []role.Role{role.AccessAddress, role.Series, role.Movies, role.VPN},
	Compose: compose,
	Environment: func(r role.Set, _ app.ComposeData) map[string]string {
		return map[string]string{"PROWLARR_NETWORK": "service:" + role.Get(r, role.VPN).Service()}
	},
	Services:   func(app.Values) []string { return []string{service} },
	BehindVPN:  []string{service},
	ConfigDirs: []string{"prowlarr"},
	Images: []app.Image{{
		Service: service,
		Digests: map[string]string{"sha256:1295cff29d10b486c0d8324d1559a552140a5932bf8b3d87e398654414f63f92": "2.5.2.5491"},
		Labels:  map[string]string{"2.5.2.5491-ls156": "2.5.2.5491"},
	}},
	Implementations: []app.Implementation{{
		From: "2.5.2.5491", Until: "2.5.2.5491",
		Provides: []role.Offer{role.Provide(role.Indexers, func(role.Values, role.Set) role.IndexerManager { return indexers{} })},
		Setup: func(e *app.Env) error {
			return e.Step("prowlarr", false, func() error {
				screen, err := Guide(role.Get(e.Roles, role.AccessAddress).URL(), role.Get(e.Roles, role.Series), role.Get(e.Roles, role.Movies))
				if err != nil {
					return err
				}
				return flow.Show(e.UI, screen)
			})
		},
		Routes: func(_ app.Values, r role.Set) []app.Route {
			return []app.Route{{
				Name:     "Prowlarr",
				Path:     "/prowlarr",
				Upstream: role.Get(r, role.VPN).Service() + ":" + strconv.Itoa(port),
				Internal: &app.Internal{Name: host, Port: port},
			}}
		},
		Tiles: func(app.Values, role.Set) []app.Tile {
			return []app.Tile{{Group: "Admin", Position: 4, Name: "Prowlarr", Description: "Manage indexers", Link: "/prowlarr", Service: "prowlarr"}}
		},
		Shared: &app.Shared{Ports: []string{strconv.Itoa(port)}},
	}},
}

// indexers is the indexer manager role. Other containers reach it through
// Caddy's internal name.
type indexers struct{}

func (indexers) Name() string { return "Prowlarr" }

func (indexers) Endpoint() role.Endpoint {
	return role.Endpoint{Service: service, Host: host, Port: port}
}

// apps are the libraries Prowlarr can sync indexers to, as its Apps page lists them.
var apps = map[string]string{"Sonarr": "Sonarr", "Radarr": "Radarr"}

// Guide connects Prowlarr to both libraries with their saved API keys.
func Guide(accessURL string, libraries ...role.Library) (flow.Screen, error) {
	body := `Open this link:
` + accessURL + `/prowlarr
`
	step := 1
	add := func(text string) {
		body += "\n" + strconv.Itoa(step) + ". " + text
		step++
	}
	add("Open Settings > Apps.")
	for _, library := range libraries {
		listed, ok := apps[library.Name()]
		if !ok {
			return flow.Screen{}, &role.Unsupported{App: "Prowlarr", Other: library.Name()}
		}
		endpoint := library.Endpoint()
		add("Click Add, then select " + listed + ".")
		add("Set Sync Level to Full Sync.")
		add("Prowlarr Server: http://" + host + ":" + strconv.Itoa(port))
		add(library.Name() + " Server: http://" + endpoint.Host + ":" + strconv.Itoa(endpoint.Port))
		add("API Key: " + library.APIKey())
		add("Click Test, then Save.")
	}
	add("Open Indexers, add your indexers, then test each one.")
	return flow.Screen{Title: "Prowlarr setup", Body: body, Wait: "Prowlarr"}, nil
}
