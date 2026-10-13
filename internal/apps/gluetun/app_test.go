package gluetun

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/runtime"
	"github.com/inayayousfi/selfnook/internal/shell"
	"github.com/inayayousfi/selfnook/internal/state"
)

func newValues() app.Values { return app.ValuesFor(state.New(), App) }

func dockerRuntime(sh shell.Shell) runtime.Runtime {
	return runtime.Docker.New(runtime.Options{Shell: sh, Env: func() map[string]string { return nil }})
}

func TestCountriesKeepUniqueOpenVPNCountries(t *testing.T) {
	servers := []map[string]any{
		{"vpn": "openvpn", "country": "Spain"},
		{"vpn": "openvpn", "country": "Austria"},
		{"vpn": "openvpn", "country": "Spain"},
		{"vpn": "wireguard", "country": "Chile"},
		{"vpn": "openvpn"},
	}
	if got := Countries(servers); !slices.Equal(got, []string{"Austria", "Spain"}) {
		t.Errorf("countries = %v", got)
	}
}

func TestSavedCountryIsCopiedIntoValues(t *testing.T) {
	values := newValues()
	configDir := t.TempDir()
	path := filepath.Join(configDir, countryPage, "selection.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"countries": ["Spain", 3]}`), 0o644)
	if !ApplySavedCountry(values, configDir) || values.Get("VPN_SERVER_COUNTRIES") != "Spain" {
		t.Errorf("countries = %q", values.Get("VPN_SERVER_COUNTRIES"))
	}
	if ApplySavedCountry(values, configDir) {
		t.Error("an unchanged country was reported as changed")
	}
}

func TestGluetunAccessFileReportsOnlyRealChanges(t *testing.T) {
	dir := t.TempDir()
	values := newValues()
	values.Set("GLUETUN_API_KEY", "read")
	values.Set("GLUETUN_CONTROL_KEY", "control")
	if changed, _ := WriteGluetunAuth(dir, values); !changed {
		t.Error("first write not reported")
	}
	if changed, _ := WriteGluetunAuth(dir, values); changed {
		t.Error("unchanged file reported as changed")
	}
	content, _ := os.ReadFile(filepath.Join(dir, "gluetun", "auth", "config.toml"))
	want := `[[roles]]
name = "dashboard"
routes = ["GET /v1/publicip/ip"]
auth = "apikey"
apikey = "read"

[[roles]]
name = "vpn-country"
routes = ["GET /v1/publicip/ip", "GET /v1/vpn/settings", "PUT /v1/vpn/settings"]
auth = "apikey"
apikey = "control"
`
	if string(content) != want {
		t.Errorf("content =\n%s", content)
	}
}

// The guided providers use OpenVPN.
func TestValidationNeedsOpenVPN(t *testing.T) {
	values := newValues()
	values.Set("VPN_TYPE", "openvpn")
	values.Set(providerKey, "nordvpn")
	values.Set(userKey, "user")
	values.Set(passwordKey, "secret")
	if err := validate(values); err != nil {
		t.Errorf("complete values rejected: %v", err)
	}
	values.Set("VPN_TYPE", "wireguard")
	if err := validate(values); err == nil || !strings.Contains(err.Error(), "OpenVPN only") {
		t.Errorf("err = %v", err)
	}
}
