// Package sonarr manages series.
package sonarr

import (
	"embed"

	"github.com/inayayousfi/selfnook/internal/apps/servarr"
	"github.com/inayayousfi/selfnook/internal/role"
)

//go:embed compose.yaml
var compose embed.FS

var App = servarr.App(servarr.Spec{
	Name:         "sonarr",
	Title:        "Sonarr",
	Kind:         "series",
	Role:         role.Series,
	Service:      "sonarr-app",
	Host:         "sonarr",
	Port:         8989,
	BasePath:     "/sonarr",
	MediaDir:     "Series",
	Events:       "On File Import, On File Upgrade, On Import Complete, On Rename, On Series Delete, On Episode File Delete, and On Episode File Delete For Upgrade",
	TilePosition: 2,
	Compose:      compose,
	Digests: map[string]string{
		"sha256:373159ba768e23a3a1c497d9f2b936addf8fd5b1fdce7dd6a14080ac928bfda0": "4.0.19.2979",
		"sha256:0deb8db6c839ac475e9cd86277712772c40ba7f929a003d7e1de36c4b666bc13": "4.0.20.3014",
	},
	Labels: map[string]string{
		"4.0.19.2979-ls321": "4.0.19.2979",
		"4.0.20.3014-ls328": "4.0.20.3014",
	},
	From:  "4.0.19.2979",
	Until: "4.0.20.3014",
})
