// Package gluetun is the VPN gateway that carries the traffic of the
// services behind the VPN through a VPN provider, with Gluetun's firewall as
// the kill switch. It also runs the VPN country page.
package gluetun

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
)

//go:embed compose.yaml countrypage/Dockerfile countrypage/main.go
var compose embed.FS

const (
	service      = "gluetun"
	countryPage  = "vpn-country"
	providerKey  = "VPN_SERVICE_PROVIDER"
	countriesKey = "VPN_SERVER_COUNTRIES"
	userKey      = "VPN_OPENVPN_USER"
	passwordKey  = "VPN_OPENVPN_PASSWORD"
	readKey      = "GLUETUN_API_KEY"
	controlKey   = "GLUETUN_CONTROL_KEY"
	// other is the provider option that asks for a Gluetun provider identifier.
	other = "other"
)

var App = &app.App{
	Name:  "gluetun",
	Title: "Gluetun",
	Settings: []app.Setting{
		{Key: providerKey, Required: true},
		{Key: "VPN_TYPE", Required: true},
		{Key: countriesKey},
		{Key: userKey, Required: true},
		{Key: passwordKey, Required: true},
		{Key: readKey},
		{Key: controlKey},
	},
	Options: []flow.Option{
		{Value: "nordvpn", Label: "NordVPN"},
		{Value: "protonvpn", Label: "Proton VPN"},
		{Value: "surfshark", Label: "Surfshark"},
		{Value: "private internet access", Label: "Private Internet Access"},
		{Value: other, Label: "Other Gluetun OpenVPN provider"},
	},
	Choose: func(v app.Values, option string) { v.Set(providerKey, option) },
	// The earlier VPN app saved its gateway choice, which is now the app
	// that fills the gateway role.
	RetiredSettings: []string{"VPN_GATEWAY_SERVICE"},
	WasActive: func(old map[string]string) bool {
		gateway := old["VPN_GATEWAY_SERVICE"]
		return gateway == service || gateway == "" && old[providerKey] != ""
	},
	Compose:    compose,
	Services:   func(app.Values) []string { return []string{service, countryPage} },
	ConfigDirs: []string{"gluetun", countryPage},
	Validate:   validate,
	Configure:  configure,
	Derive: func(v app.Values) {
		for _, key := range []string{readKey, controlKey} {
			if !v.Has(key) {
				v.Set(key, app.RandomToken(32))
			}
		}
	},
	// The pinned image is a build of Gluetun's main line, which has no
	// release number, so its version is the source revision it was built from.
	Images: []app.Image{{
		Service: service,
		Digests: map[string]string{"sha256:12df8b20528d4cd5e9b6e827d40f2886cf78e53e7e9afc750050648c31183793": "e2e4a0658aad"},
	}},
	Implementations: []app.Implementation{{
		From: "e2e4a0658aad", Until: "e2e4a0658aad",
		Provides: []role.Offer{role.Provide(role.VPN, func(role.Values, role.Set) role.Gateway { return gateway{} })},
		Prepare:  prepare,
		Started: func(e *app.Env) error {
			SaveCountryList(e.Runtime, e.Values, e.ConfigDir())
			return nil
		},
		Status: Status,
		Routes: func(app.Values, role.Set) []app.Route {
			return []app.Route{{Name: "VPN country page", Path: "/vpn-country", Upstream: "vpn-country:8090", StripPrefix: true}}
		},
		Tiles: func(v app.Values, _ role.Set) []app.Tile {
			return []app.Tile{{Group: "Admin", Position: 5, Name: "Gluetun", Description: "VPN connection", Link: "/vpn-country/", Service: "gluetun", Status: &app.Status{URL: "http://gluetun:8000", Key: v.Get(readKey)}}}
		},
	}},
	Credentials: &app.CredentialGroup{
		Name: "vpn",
		Fields: []app.CredentialField{
			{Label: "Provider", Key: providerKey},
			{Label: "Server countries", Key: countriesKey},
			{Label: "Service username", Key: userKey, Kind: app.PlainText, UsedByStack: true},
			{Label: "Service password", Key: passwordKey, Kind: app.Password, UsedByStack: true},
		},
	},
}

// gateway is the network gateway role.
type gateway struct{}

func (gateway) Service() string { return service }
func (gateway) Name() string    { return "Gluetun" }

// Ready reports whether Gluetun's health check passes.
func (g gateway) Ready(r runtime.Runtime) (bool, error) {
	if err := role.Running(r, g.Name(), service); err != nil {
		return false, err
	}
	health, err := r.Health(service)
	return health == "healthy", err
}

// Problem is Gluetun's latest error. OpenVPN restarts right after an error
// and logs that restart last, so the error is searched among recent lines.
func (gateway) Problem(r runtime.Runtime) string {
	lines, err := r.Logs(service, 50)
	if err != nil {
		return ""
	}
	for i := len(lines) - 1; i >= 0; i-- {
		_, message, found := strings.Cut(lines[i], " ERROR ")
		switch {
		case !found:
		case strings.Contains(message, "AUTH_FAILED"):
			return "The VPN provider refuses the saved VPN login."
		default:
			return "Gluetun's latest error: " + message
		}
	}
	return ""
}

func validate(v app.Values) error {
	if v.Get("VPN_TYPE") != "openvpn" {
		return errors.New("The guided Gluetun providers currently use OpenVPN only.")
	}
	var missing []string
	for _, key := range []string{providerKey, userKey, passwordKey} {
		if !v.Has(key) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return errors.New("Missing required VPN values: " + strings.Join(missing, ", "))
	}
	return nil
}

// configure asks for the provider identifier when the user chose another
// provider, then for the service login.
func configure(e *app.Env) error {
	v := e.Values
	if v.Get(providerKey) == other {
		answers, err := e.UI.Ask(flow.Screen{Fields: []flow.Field{{
			Key: "provider", Prompt: "Gluetun provider identifier", Check: flow.Required("Provider identifier cannot be empty."),
		}}})
		if err != nil {
			return err
		}
		v.Set(providerKey, strings.ToLower(answers["provider"]))
		if err := e.SaveValues(); err != nil {
			return err
		}
	}
	v.SetDefault(providerKey, "nordvpn")
	v.Set("VPN_TYPE", "openvpn")
	v.SetDefault(countriesKey, "")
	if v.Has(userKey) && v.Has(passwordKey) {
		return nil
	}
	var fields []flow.Field
	if !v.Has(userKey) {
		fields = append(fields, flow.Field{Key: userKey, Prompt: "VPN service username", Check: flow.Required("Username cannot be empty.")})
	}
	if !v.Has(passwordKey) {
		fields = append(fields, flow.Field{Key: passwordKey, Prompt: "VPN service password", Kind: flow.Secret, Check: flow.Required("Password cannot be empty.")})
	}
	return e.AskAndSave(providerGuide(v.Get(providerKey), app.List(e.BehindVPN), fields))
}

type providerHelp struct{ source, location, warning string }

var providerGuides = map[string]providerHelp{
	"nordvpn": {
		"NordVPN generates the OpenVPN service username and password automatically.",
		"Nord Account > NordVPN > Advanced Settings > Set up NordVPN manually > Service credentials",
		"Do not use the email address and password used to sign in to the NordVPN application.",
	},
	"protonvpn": {
		"Proton VPN generates a separate OpenVPN username and password automatically.",
		"Proton Account > VPN > OpenVPN / IKEv2 username",
		"Do not use the email address and password used to sign in to Proton.",
	},
	"surfshark": {
		"Surfshark generates the OpenVPN service username and password automatically.",
		"Surfshark Account > VPN > Manual setup > Desktop or mobile > OpenVPN > Credentials",
		"Do not use the email address and password used to sign in to Surfshark.",
	},
	"private internet access": {
		"Private Internet Access assigns a service username beginning with p and a service password.",
		"The purchase email or Private Internet Access Client Control Panel",
		"Use the VPN service credentials, not unrelated device or operating-system credentials.",
	},
}

var otherProviderGuide = providerHelp{
	"Your provider supplies the OpenVPN service username and password.",
	"The provider's manual OpenVPN setup documentation",
	"Use service credentials intended for manual OpenVPN connections.",
}

func providerGuide(provider, behind string, fields []flow.Field) flow.Screen {
	help, ok := providerGuides[provider]
	if !ok {
		help = otherProviderGuide
	}
	return flow.Screen{
		Title: "VPN kill-switch setup",
		Body: behind + ` will use Gluetun's VPN connection. If the VPN disconnects, Gluetun's firewall blocks their network traffic.

` + help.source + `

1. Open ` + help.location + `
2. Keep the generated service username and password available.
3. The following prompts will request those 2 values.

` + help.warning,
		Fields: fields,
	}
}

// prepare copies the country chosen on the VPN country page into the values
// Gluetun starts with, and writes Gluetun's API access file.
func prepare(e *app.Env) ([]string, error) {
	if ApplySavedCountry(e.Values, e.ConfigDir()) {
		if err := e.SaveValues(); err != nil {
			return nil, err
		}
	}
	changed, err := WriteGluetunAuth(e.ConfigDir(), e.Values)
	if err != nil || !changed {
		return nil, err
	}
	return []string{service}, nil
}

// ApplySavedCountry copies the VPN country page's saved selection into
// VPN_SERVER_COUNTRIES. It reports whether the value changed.
func ApplySavedCountry(v app.Values, configDir string) bool {
	content, err := os.ReadFile(filepath.Join(configDir, countryPage, "selection.json"))
	if err != nil {
		return false
	}
	var selection struct {
		Countries []any `json:"countries"`
	}
	if json.Unmarshal(content, &selection) != nil || selection.Countries == nil {
		return false
	}
	var countries []string
	for _, country := range selection.Countries {
		if name, ok := country.(string); ok {
			countries = append(countries, name)
		}
	}
	selected := strings.Join(countries, ",")
	if v.Get(countriesKey) == selected {
		return false
	}
	v.Set(countriesKey, selected)
	return true
}

// WriteGluetunAuth gives the dashboard read access and the country page control
// access to Gluetun's API. It reports whether the file changed.
func WriteGluetunAuth(configDir string, v app.Values) (bool, error) {
	roles := []struct {
		name   string
		routes []string
		key    string
	}{
		{"dashboard", []string{"GET /v1/publicip/ip"}, v.Get(readKey)},
		{countryPage, []string{"GET /v1/publicip/ip", "GET /v1/vpn/settings", "PUT /v1/vpn/settings"}, v.Get(controlKey)},
	}
	var blocks []string
	for _, role := range roles {
		name, _ := json.Marshal(role.name)
		routes, _ := json.Marshal(role.routes)
		key, _ := json.Marshal(role.key)
		blocks = append(blocks, fmt.Sprintf("[[roles]]\nname = %s\nroutes = %s\nauth = \"apikey\"\napikey = %s\n", name, strings.ReplaceAll(string(routes), `","`, `", "`), key))
	}
	return files.WriteIfChanged(filepath.Join(configDir, "gluetun", "auth", "config.toml"), []byte(strings.Join(blocks, "\n")))
}

// Countries returns the sorted, unique countries that offer OpenVPN servers.
func Countries(servers []map[string]any) []string {
	seen := map[string]bool{}
	var countries []string
	for _, server := range servers {
		country, _ := server["country"].(string)
		if server["vpn"] == "openvpn" && country != "" && !seen[country] {
			seen[country] = true
			countries = append(countries, country)
		}
	}
	slices.Sort(countries)
	return countries
}

// SaveCountryList asks Gluetun for its server list and saves the countries
// the VPN country page offers. Failures leave the previous list in place.
func SaveCountryList(r runtime.Runtime, v app.Values, configDir string) {
	flag := "-" + strings.ReplaceAll(v.Get(providerKey), " ", "-")
	result, err := r.Exec(service, "sh", "-c",
		"/gluetun-entrypoint format-servers "+shellQuote(flag)+" -format json -output /tmp/servers.json >/dev/null && cat /tmp/servers.json")
	if err != nil || result.Code != 0 {
		return
	}
	var servers []map[string]any
	if json.Unmarshal([]byte(result.Stdout), &servers) != nil {
		return
	}
	content, _ := json.Marshal(Countries(servers))
	files.WriteIfChanged(filepath.Join(configDir, countryPage, "countries.json"), content)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// Status describes the gateway's connection.
func Status(e *app.Env) (string, error) {
	ready, err := gateway{}.Ready(e.Runtime)
	var down *role.GatewayDown
	state := "not healthy"
	switch {
	case errors.As(err, &down):
		state = "not running"
	case err != nil:
		return "", err
	case ready:
		state = "healthy"
	}
	text := "Gluetun VPN: " + state
	if e.Values.Has(countriesKey) {
		text += "\nSelected countries:\n" + e.Values.Get(countriesKey)
	}
	return text, nil
}
