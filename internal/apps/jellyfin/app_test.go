package jellyfin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
	"github.com/inayayousfi/selfnook/internal/shell"
	"github.com/inayayousfi/selfnook/internal/state"
)

func TestBaseURLKeepsOtherNetworkSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.xml")
	original := `<?xml version="1.0" encoding="utf-8"?>
<NetworkConfiguration xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <BaseUrl />
  <EnableHttps>false</EnableHttps>
</NetworkConfiguration>`
	os.WriteFile(path, []byte(original), 0o644)
	if BaseURLIsSet(path) {
		t.Fatal("an empty base URL counts as set")
	}
	if err := WriteBaseURL(path); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if want := strings.Replace(original, "<BaseUrl />", "<BaseUrl>/jellyfin</BaseUrl>", 1); string(content) != want {
		t.Errorf("content =\n%s", content)
	}
	if !BaseURLIsSet(path) {
		t.Error("base URL not detected after writing")
	}
}

func TestBaseURLIsAddedWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.xml")
	os.WriteFile(path, []byte("<NetworkConfiguration>\n  <EnableHttps>false</EnableHttps>\n</NetworkConfiguration>"), 0o644)
	if err := WriteBaseURL(path); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "<EnableHttps>false</EnableHttps>") || !BaseURLIsSet(path) {
		t.Errorf("content =\n%s", content)
	}
}

func TestBaseURLCreatesAMissingNetworkFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "network.xml")
	if err := WriteBaseURL(path); err != nil {
		t.Fatal(err)
	}
	if !BaseURLIsSet(path) {
		content, _ := os.ReadFile(path)
		t.Errorf("content =\n%s", content)
	}
}

// Jellyfin rewrites its network file on exit, so it must be stopped before the edit.
func TestMissingBaseURLIsWrittenWhileJellyfinIsStopped(t *testing.T) {
	saved := state.New()
	saved.Set(state.Core, "CONFIG_DIR", t.TempDir())
	sh := &fake.Shell{Respond: func(args []string) shell.Result {
		if strings.Join(args, " ") == "docker inspect selfnook-jellyfin-app --format {{.State.Status}}" {
			return shell.Result{Stdout: "running\n"}
		}
		return shell.Result{}
	}}
	r := runtime.Docker.New(runtime.Options{Shell: sh, Env: func() map[string]string { return nil }})
	e := &app.Env{Values: app.ValuesFor(saved, App), Runtime: r}
	path := networkFile(e.ConfigDir())
	installed := filepath.Join(pluginsDir(e.ConfigDir()), pluginPrefix+pluginVersion, "meta.json")
	os.MkdirAll(filepath.Dir(installed), 0o755)
	os.WriteFile(installed, []byte(`{"version": "`+pluginVersion+`"}`), 0o644)
	if _, err := prepare(e); err != nil {
		t.Fatal(err)
	}
	setWhenStopped := sh.Index("docker stop selfnook-jellyfin-app") >= 0
	if !setWhenStopped || !BaseURLIsSet(path) {
		t.Errorf("commands = %v", sh.Lines())
	}
	sh.Commands = nil
	prepare(e)
	if len(sh.Commands) != 0 {
		t.Errorf("Jellyfin stopped although its base URL was set: %v", sh.Lines())
	}
}

// pluginHost answers the plugin's settings request with saved, records the
// settings sent through sh, and answers every other request with an empty
// object.
func pluginHost(sh *fake.Shell, saved string, sent *[]string) func([]string) shell.Result {
	return func(args []string) shell.Result {
		address := args[len(args)-1]
		switch {
		case strings.HasSuffix(address, "/sso/OID/Get"):
			return shell.Result{Stdout: saved + "\n200\n"}
		case strings.Contains(address, "/sso/OID/Add/"):
			*sent = append(*sent, fake.CurlBody(sh.LastInput()))
			return shell.Result{Stdout: "\n204\n"}
		}
		return shell.Result{Stdout: "{}\n200\n"}
	}
}

type signInService struct{}

func (signInService) Name() string                        { return "Authentik" }
func (signInService) User() string                        { return "" }
func (signInService) Password() string                    { return "" }
func (signInService) URL() string                         { return "" }
func (signInService) Check(string) (role.Check, error)    { return role.Check{}, nil }
func (signInService) AdminGroup() string                  { return "authentik Admins" }
func (signInService) Ready(runtime.Runtime) (bool, error) { return true, nil }
func (signInService) Issuer(client string) string {
	return "https://media.example.com:9091/application/o/" + client + "/"
}

type address struct{}

func (address) Name() string { return "Caddy" }
func (address) URL() string  { return "https://media.example.com" }
func (address) Host() string { return "media.example.com" }
func (address) Secure() bool { return true }

func signInEnv(sh *fake.Shell) *app.Env {
	saved := state.New()
	saved.Set("jellyfin", secretKey, "client-secret")
	r := runtime.Docker.New(runtime.Options{Shell: sh, Env: func() map[string]string { return nil }})
	return &app.Env{Values: app.ValuesFor(saved, App), Runtime: r, UI: &fake.UI{}}
}

// The links between Authentik and Jellyfin accounts, which the plugin
// stores in its settings, survive when Selfnook writes the settings; the
// rest points the plugin at Authentik with its administrator group.
func TestSignInSettingsKeepAccountLinks(t *testing.T) {
	var sent []string
	sh := &fake.Shell{}
	sh.Respond = pluginHost(sh, `{"authentik": {"CanonicalLinks": {"ana": "0f8fad5b-d9cb-469f-a165-70867728950e"}, "OidSecret": "old"}}`, &sent)
	if err := applySignIn(api{e: signInEnv(sh)}, address{}, signInService{}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d settings", len(sent))
	}
	var settings map[string]any
	json.Unmarshal([]byte(sent[0]), &settings)
	links, _ := settings["CanonicalLinks"].(map[string]any)
	if links["ana"] != "0f8fad5b-d9cb-469f-a165-70867728950e" {
		t.Errorf("links lost: %s", sent[0])
	}
	want := map[string]any{
		"OidEndpoint": "https://media.example.com:9091/application/o/jellyfin/", "OidClientId": "jellyfin", "OidSecret": "client-secret",
		"SchemeOverride": "https", "RoleClaim": "groups", "Enabled": true, "DisableHttps": false,
	}
	for key, value := range want {
		if settings[key] != value {
			t.Errorf("%s = %v", key, settings[key])
		}
	}
	if admins, _ := settings["AdminRoles"].([]any); len(admins) != 1 || admins[0] != "authentik Admins" {
		t.Errorf("AdminRoles = %v", settings["AdminRoles"])
	}
}

// Settings that already match are not sent again, so each start leaves
// Jellyfin's files unchanged.
func TestMatchingSignInSettingsAreNotSent(t *testing.T) {
	var sent []string
	sh := &fake.Shell{}
	sh.Respond = pluginHost(sh, `{}`, &sent)
	e := signInEnv(sh)
	if err := applySignIn(api{e: e}, address{}, signInService{}); err != nil {
		t.Fatal(err)
	}
	sh.Respond = pluginHost(sh, `{"authentik": `+sent[0]+`}`, &sent)
	if err := applySignIn(api{e: e}, address{}, signInService{}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Errorf("sent the same settings %d times", len(sent))
	}
}

// A download whose checksum differs from the pinned one is not installed,
// and no plugin folder is left behind.
func TestPluginWithAnotherChecksumIsRefused(t *testing.T) {
	dir := t.TempDir()
	changed, err := installPlugin(dir, func(string) ([]byte, error) { return []byte("not the release"), nil })
	if changed || err == nil || err.Error() != "The downloaded Jellyfin sign-in plugin does not match its pinned checksum, so it was not installed." {
		t.Errorf("changed = %v, err = %v", changed, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

// configHost answers Jellyfin's server settings request with saved and
// records the settings sent back through sh.
func configHost(sh *fake.Shell, saved string, sent *[]string) func([]string) shell.Result {
	return func(args []string) shell.Result {
		address := args[len(args)-1]
		if strings.HasSuffix(address, "/System/Configuration") {
			if body := fake.CurlBody(sh.LastInput()); body != "" {
				*sent = append(*sent, body)
				return shell.Result{Stdout: "\n204\n"}
			}
			return shell.Result{Stdout: saved + "\n200\n"}
		}
		return shell.Result{Stdout: "{}\n200\n"}
	}
}

// An unnamed server gets the name Selfnook and keeps its other settings; a
// name chosen in Jellyfin stays.
func TestServerIsNamedOnlyWhenUnnamed(t *testing.T) {
	var sent []string
	sh := &fake.Shell{}
	sh.Respond = configHost(sh, `{"ServerName": "", "EnableMetrics": true}`, &sent)
	if err := ensureServerName(api{e: signInEnv(sh)}); err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if len(sent) != 1 || json.Unmarshal([]byte(sent[0]), &config) != nil || config["ServerName"] != "Selfnook" || config["EnableMetrics"] != true {
		t.Fatalf("sent %v", sent)
	}
	sent = nil
	sh.Respond = configHost(sh, `{"ServerName": "Living room"}`, &sent)
	if err := ensureServerName(api{e: signInEnv(sh)}); err != nil || len(sent) != 0 {
		t.Errorf("err = %v, sent %v", err, sent)
	}
}

// An answer that spans several lines keeps all of them, and the status after
// the last line still counts.
func TestAnswerOnSeveralLinesKeepsItsStatus(t *testing.T) {
	sh := &fake.Shell{Respond: func([]string) shell.Result {
		return shell.Result{Stdout: "{\n  \"ServerName\": \"Selfnook\"\n}\n200\n"}
	}}
	var config map[string]any
	if err := (api{e: signInEnv(sh)}).call("GET", "/System/Configuration", nil, &config); err != nil || config["ServerName"] != "Selfnook" {
		t.Errorf("config = %v, err = %v", config, err)
	}
}
