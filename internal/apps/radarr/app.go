// Package radarr manages movies.
package radarr

import (
	"embed"

	"github.com/inayayousfi/selfnook/internal/apps/servarr"
	"github.com/inayayousfi/selfnook/internal/role"
)

//go:embed compose.yaml
var compose embed.FS

var App = servarr.App(servarr.Spec{
	Name:         "radarr",
	Title:        "Radarr",
	Kind:         "movies",
	Role:         role.Movies,
	Service:      "radarr-app",
	Host:         "radarr",
	Port:         7878,
	BasePath:     "/radarr",
	MediaDir:     "Movies",
	Events:       "On File Import, On File Upgrade, On Rename, On Movie Delete, On Movie File Delete, and On Movie File Delete For Upgrade",
	TilePosition: 3,
	Compose:      compose,
	Digests: map[string]string{
		"sha256:a45b5ab0f850f39edb4cc9c95bbd967b52ddc3d4574a4dfb45561177db6c88f4": "6.3.0.10514",
		"sha256:cfbc4fa09dc9d78f7cedc898f4ec48c6277cacedc523c4e159db6ff1c741959c": "6.4.4.10685",
	},
	Labels: map[string]string{
		"6.3.0.10514-ls313": "6.3.0.10514",
		"6.4.4.10685-ls320": "6.4.4.10685",
	},
	From:  "6.3.0.10514",
	Until: "6.4.4.10685",
})
