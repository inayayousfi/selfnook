// Package servarr builds the apps made from the Servarr code base, Sonarr
// and Radarr, which share their pages, settings, and API. Each app keeps its
// own package and Compose file and describes only what differs.
package servarr

import (
	"io/fs"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
)

// Spec is what differs between the Servarr apps.
type Spec struct {
	// Name and Title identify the app, such as sonarr and Sonarr.
	Name, Title string
	// Kind is what the app manages, such as series.
	Kind string
	// Role is the library role the app fills.
	Role role.Key[role.Library]
	// Service runs the app; other containers reach it at Host:Port.
	Service, Host string
	Port          int
	// BasePath is the app's path at the access address.
	BasePath string
	// MediaDir is the folder under MEDIA_DIR that holds the media.
	MediaDir string
	// Events are the Connect page checkboxes that refresh the media server.
	Events string
	// TilePosition orders the app's tile among the admin tiles.
	TilePosition int
	Compose      fs.FS
	// Digests and Labels map the known images to their versions.
	Digests, Labels map[string]string
	// From and Until are the versions these steps were checked with.
	From, Until string
}

// mediaServers are the servers a Servarr app can refresh, as its Connect page lists them.
var mediaServers = map[string]string{"Jellyfin": "Emby / Jellyfin"}

// downloadClients are the clients a Servarr app can use, as its Download Clients page lists them.
var downloadClients = map[string]string{"qBittorrent": "qBittorrent"}

func (s Spec) keyName() string         { return strings.ToUpper(s.Name) + "_API_KEY" }
func (s Spec) rootFolder() string      { return "/media/" + s.MediaDir }
func (s Spec) statusPath() string      { return s.BasePath + "/api/v3/system/status" }
func (s Spec) upstream() string        { return s.Service + ":" + strconv.Itoa(s.Port) }
func (s Spec) envName(n string) string { return strings.ToUpper(s.Name) + "_" + n }

// App builds the app that spec describes.
func App(spec Spec) *app.App {
	key := spec.keyName()
	return &app.App{
		Name:     spec.Name,
		Title:    spec.Title,
		Settings: []app.Setting{{Key: key, Required: true}},
		Needs:    []role.Role{role.AccessAddress, role.Downloads, role.Indexers},
		Compose:  spec.Compose,
		Environment: func(r role.Set, _ app.ComposeData) map[string]string {
			return map[string]string{
				spec.envName("DOWNLOADS"): role.Get(r, role.Downloads).Endpoint().Service,
				spec.envName("INDEXERS"):  role.Get(r, role.Indexers).Endpoint().Service,
			}
		},
		Services:   func(app.Values) []string { return []string{spec.Service} },
		ConfigDirs: []string{spec.Name},
		MediaDirs:  []string{spec.MediaDir},
		Validate: func(v app.Values) error {
			_, err := flow.ValidAPIKey(v.Get(key))
			return err
		},
		Images: []app.Image{{
			Service: spec.Service,
			Digests: spec.Digests,
			Labels:  spec.Labels,
			Ask: func(e *app.Env) (string, error) {
				return app.AskVersion(e, spec.Service, "http://localhost:"+strconv.Itoa(spec.Port)+spec.statusPath(), "version", "X-Api-Key: "+e.Values.Get(key))
			},
		}},
		Implementations: []app.Implementation{{
			From: spec.From, Until: spec.Until,
			Provides: []role.Offer{role.Provide(spec.Role, func(v role.Values, _ role.Set) role.Library { return library{spec, v} })},
			Setup: func(e *app.Env) error {
				return e.Step(spec.Name, !e.Values.Has(key), func() error {
					screen, err := spec.Guide(role.Get(e.Roles, role.AccessAddress).URL(), role.Get(e.Roles, role.Downloads))
					if err != nil {
						return err
					}
					return e.AskAndSave(screen)
				})
			},
			Routes: func(app.Values, role.Set) []app.Route {
				return []app.Route{{
					Name:     spec.Title,
					Path:     spec.BasePath,
					Upstream: spec.upstream(),
					Internal: &app.Internal{Name: spec.Host, Port: spec.Port},
				}}
			},
			Tiles: func(app.Values, role.Set) []app.Tile {
				return []app.Tile{{Group: "Admin", Position: spec.TilePosition, Name: spec.Title, Description: "Manage " + spec.Kind, Link: spec.BasePath, Service: spec.Service}}
			},
		}},
		Credentials: &app.CredentialGroup{Name: spec.Name, Fields: []app.CredentialField{
			{Label: "API key", Key: key, Kind: app.APIKey, UsedByStack: true},
		}},
	}
}

// library is the library role. Other containers reach the app through
// Caddy's internal name, which adds the base path.
type library struct {
	spec Spec
	v    role.Values
}

func (l library) Name() string { return l.spec.Title }

func (l library) Endpoint() role.Endpoint {
	return role.Endpoint{Service: l.spec.Service, Host: l.spec.Host, Port: l.spec.Port}
}

func (l library) StatusPath() string { return l.spec.statusPath() }

func (l library) WebPath() string { return l.spec.BasePath }

func (l library) RootFolder() string { return l.spec.rootFolder() }

func (l library) APIKey() string { return l.v.Get(l.spec.keyName()) }

func (l library) MediaServerSteps(server role.MediaServer) ([]string, error) {
	listed, ok := mediaServers[server.Name()]
	if !ok {
		return nil, &role.Unsupported{App: l.spec.Title, Other: server.Name()}
	}
	endpoint := server.Endpoint()
	return []string{
		"In " + l.spec.Title + ", open Settings > Connect, click +, then select " + listed + ".",
		"Name: " + server.Name(),
		"Check " + l.spec.Events + ".",
		"Host: " + endpoint.Host,
		"Port: " + strconv.Itoa(endpoint.Port),
		"API Key: " + server.APIKey(),
		"Keep Update Library checked, then click Test and Save.",
	}, nil
}

// Guide connects the app to the media folder and the download client, then asks for its API key.
func (s Spec) Guide(accessURL string, downloads role.DownloadClient) (flow.Screen, error) {
	listed, ok := downloadClients[downloads.Name()]
	if !ok {
		return flow.Screen{}, &role.Unsupported{App: s.Title, Other: downloads.Name()}
	}
	client := downloads.Endpoint()
	login := "Leave API Key, Username, and Password empty."
	if user, password := downloads.Login(); user != "" || password != "" {
		login = "Enter this login:\nUsername: " + user + "\nPassword: " + password
	}
	return flow.Screen{
		Title: s.Title + " setup",
		Body: `Open this link:
` + accessURL + s.BasePath + `

1. Open Settings > Media Management.
2. Under Root Folders, click Add Root Folder.
3. Click Ok after entering ` + s.rootFolder() + `
4. Open Settings > Download Clients.
5. Click Add, then select ` + listed + `.
6. Set Host to ` + client.Host + ` and Port to ` + strconv.Itoa(client.Port) + `. ` + login + `
7. Set Category to ` + s.Name + `.
8. Click Test, then Save.
9. Open Settings > General > Security.
10. Copy the API Key that ` + s.Title + ` generated automatically. The next terminal prompt will ask for it.`,
		Wait:   s.Title,
		Fields: []flow.Field{flow.APIKeyField(s.keyName(), s.Title)},
	}, nil
}
