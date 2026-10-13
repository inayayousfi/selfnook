// Package homepage is the dashboard, with the container status reader and
// the system monitor it displays.
package homepage

import (
	"cmp"
	"embed"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
)

//go:embed compose.yaml
var compose embed.FS

//go:embed files
var embedded embed.FS

// groupOrder matches the layout in files/settings.yaml.
var groupOrder = []string{"Media", "Admin"}

var App = &app.App{
	Name:    "homepage",
	Title:   "Homepage",
	Needs:   []role.Role{role.AccessAddress},
	Compose: compose,
	Environment: func(r role.Set, d app.ComposeData) map[string]string {
		access := role.Get(r, role.AccessAddress)
		env := map[string]string{"HOMEPAGE_HOST": access.Host(), "HOMEPAGE_URL": access.URL()}
		for _, tile := range d.Tiles {
			if tile.Status != nil {
				env["HOMEPAGE_VAR_"+tile.KeyName()] = tile.Status.Key
			}
		}
		return env
	},
	Services:   func(app.Values) []string { return []string{"dockerproxy", "homepage", "glances"} },
	ConfigDirs: []string{"homepage"},
	Images: []app.Image{
		{
			Service: "homepage",
			Digests: map[string]string{"sha256:643bd0be730d40f69d58028a55d1a896739333e8815786df42bc97f109ecbe61": "2.4.0"},
			Labels:  map[string]string{"v2.4.0": "2.4.0"},
		},
		{
			Service: "dockerproxy",
			Digests: map[string]string{"sha256:1f5038b54f06c3e18422902cf00ba21803d1c97805aae032e5e6673d532d3459": "0.5.0"},
			Labels:  map[string]string{"v0.5.0": "0.5.0"},
		},
		{
			Service: "glances",
			Digests: map[string]string{"sha256:c47322ae60260f6039302dde576c8250f28e67cea77f5b93a270e6cd90995665": "4.5.6"},
			Labels:  map[string]string{"v4.5.6": "4.5.6"},
		},
	},
	Implementations: []app.Implementation{{
		From: "2.4.0", Until: "2.4.0",
		Prepare: func(e *app.Env) ([]string, error) {
			return nil, WriteConfig(e.ConfigPath("homepage"), e.Tiles)
		},
		Routes: func(app.Values, role.Set) []app.Route {
			return []app.Route{{Upstream: "homepage:3000", Fallback: true}}
		},
	}},
}

// WriteConfig writes Homepage's settings and a services list built from every app's tiles.
func WriteConfig(dir string, tiles []app.Tile) error {
	entries, err := embedded.ReadDir("files")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		content, err := embedded.ReadFile("files/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err := files.WriteIfChanged(filepath.Join(dir, entry.Name()), content); err != nil {
			return err
		}
	}
	_, err = files.WriteIfChanged(filepath.Join(dir, "services.yaml"), []byte(Services(tiles)))
	return err
}

// widgets maps an app's name to the Homepage widget that shows its status.
// Apps without a widget here appear without a status panel.
var widgets = map[string]string{"Gluetun": "gluetun"}

// Services lists the tiles under their groups, each group ordered by position.
func Services(tiles []app.Tile) string {
	order := slices.Clone(groupOrder)
	groups := map[string][]app.Tile{}
	for _, tile := range tiles {
		if !slices.Contains(order, tile.Group) {
			order = append(order, tile.Group)
		}
		groups[tile.Group] = append(groups[tile.Group], tile)
	}
	var out strings.Builder
	out.WriteString("---\n")
	for _, group := range order {
		if len(groups[group]) == 0 {
			continue
		}
		slices.SortStableFunc(groups[group], func(a, b app.Tile) int { return cmp.Compare(a.Position, b.Position) })
		out.WriteString("- " + group + ":\n")
		for _, tile := range groups[group] {
			out.WriteString(entry(tile))
		}
	}
	return out.String()
}

// entry is one tile in Homepage's services format.
func entry(tile app.Tile) string {
	var out strings.Builder
	fmt.Fprintf(&out, "    - %s:\n        icon: %s.png\n        description: %s\n", tile.Name, strings.ToLower(tile.Name), tile.Description)
	if tile.Service != "" {
		fmt.Fprintf(&out, "        server: %s\n        container: %s\n", runtime.Project, runtime.Container(tile.Service))
	}
	if tile.Link != "" {
		fmt.Fprintf(&out, "        href: \"{{HOMEPAGE_VAR_URL}}%s\"\n", tile.Link)
	}
	if widget := widgets[tile.Name]; widget != "" && tile.Status != nil {
		fmt.Fprintf(&out, "        widget:\n          type: %s\n          url: %s\n          key: \"{{HOMEPAGE_VAR_%s}}\"\n", widget, tile.Status.URL, tile.KeyName())
	}
	return out.String()
}
