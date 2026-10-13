// Package recyclarr applies quality profiles to the series and movie
// libraries. It runs once per start instead of staying up.
package recyclarr

import (
	"bytes"
	"embed"
	"errors"
	"strconv"
	"text/template"
	"time"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/role"
)

//go:embed compose.yaml
var compose embed.FS

//go:embed recyclarr.yml
var configTemplate string

var App = &app.App{
	Name:    "recyclarr",
	Title:   "Recyclarr",
	Needs:   []role.Role{role.Series, role.Movies},
	Compose: compose,
	Environment: func(r role.Set, _ app.ComposeData) map[string]string {
		series, movies := role.Get(r, role.Series), role.Get(r, role.Movies)
		return map[string]string{
			"RECYCLARR_SERIES":         series.Endpoint().Service,
			"RECYCLARR_MOVIES":         movies.Endpoint().Service,
			"RECYCLARR_SERIES_API_KEY": series.APIKey(),
			"RECYCLARR_MOVIES_API_KEY": movies.APIKey(),
		}
	},
	ConfigDirs: []string{"recyclarr", "recyclarr-data"},
	Images: []app.Image{{
		Service: "recyclarr",
		Digests: map[string]string{"sha256:73303ba5ee647b6492fee917d3a781c6e59b6cab08f9dfce9482a950846f564f": "8.7.1"},
		Labels:  map[string]string{"8.7.1": "8.7.1"},
	}},
	Implementations: []app.Implementation{{
		From: "8.7.1", Until: "8.7.1",
		Provides:   []role.Offer{role.Provide(role.Profiles, func(role.Values, role.Set) role.QualityProfiles { return profiles{} })},
		Setup:      Sync,
		AfterStart: Sync,
	}},
}

// managed are the libraries recyclarr.yml configures, with the quality
// profile it creates in each.
var managed = map[string]string{"Sonarr": "4K Progressive", "Radarr": "4K Progressive"}

// profiles is the quality profiles role.
type profiles struct{}

func (profiles) Name() string { return "Recyclarr" }

func (p profiles) Profile(library role.Library) (string, error) {
	profile, ok := managed[library.Name()]
	if !ok {
		return "", &role.Unsupported{App: p.Name(), Other: library.Name()}
	}
	return profile, nil
}

// Config is the profile configuration, pointed at the libraries' addresses.
// Its series section is written for Sonarr and its movies section for Radarr.
func Config(series, movies role.Library) (string, error) {
	for _, pair := range []struct {
		library  role.Library
		expected string
	}{{series, "Sonarr"}, {movies, "Radarr"}} {
		if pair.library.Name() != pair.expected {
			return "", &role.Unsupported{App: "Recyclarr", Other: pair.library.Name()}
		}
	}
	tmpl, err := template.New("recyclarr.yml").Option("missingkey=error").Parse(configTemplate)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	err = tmpl.Execute(&out, map[string]string{
		"Series":        address(series),
		"Movies":        address(movies),
		"SeriesProfile": managed[series.Name()],
		"MoviesProfile": managed[movies.Name()],
	})
	return out.String(), err
}

func address(library role.Library) string {
	endpoint := library.Endpoint()
	return "http://" + endpoint.Host + ":" + strconv.Itoa(endpoint.Port)
}

// Sync writes the profile configuration, waits for both libraries, and applies it.
func Sync(e *app.Env) error {
	series, movies := role.Get(e.Roles, role.Series), role.Get(e.Roles, role.Movies)
	config, err := Config(series, movies)
	if err != nil {
		return err
	}
	if _, err := files.WriteIfChanged(e.ConfigPath("recyclarr", "recyclarr.yml"), []byte(config)); err != nil {
		return err
	}
	for _, library := range []role.Library{series, movies} {
		if err := waitForAPI(e, library); err != nil {
			return err
		}
	}
	return e.Runtime.RunOnce("recyclarr", "sync")
}

// apiChecks is how many times waitForAPI checks, 2 seconds apart: three minutes.
const apiChecks = 90

func waitForAPI(e *app.Env, library role.Library) error {
	endpoint := library.Endpoint()
	url := "http://localhost:" + strconv.Itoa(endpoint.Port) + library.StatusPath()
	for range apiChecks {
		result, err := e.Runtime.Exec(endpoint.Service, "curl", "-fsS", "-o", "/dev/null", "-H", "X-Api-Key: "+library.APIKey(), url)
		if err != nil {
			return err
		}
		if result.Code == 0 {
			return nil
		}
		e.Sleep(2 * time.Second)
	}
	return errors.New(library.Name() + " did not become ready within three minutes.")
}
