package jellyfin

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/role"
)

const (
	// adminName is the local administrator Selfnook creates. Seerr signs
	// in to Jellyfin with it; people sign in with Authentik.
	adminName = "selfnook"
	// keyApp names the API key Selfnook creates for the other apps.
	keyApp = "Selfnook"
	// readyChecks, 2 seconds apart, are how long Jellyfin gets to answer.
	readyChecks = 150
)

// errNoLogin is a Jellyfin set up before Selfnook managed it, whose
// administrator login Selfnook does not know.
var errNoLogin = errors.New("Jellyfin was set up without Selfnook, so its administrator login is unknown. Run 'selfnook credentials jellyfin' to record that login, then run setup again.")

// waitReady waits until Jellyfin answers and returns whether its first-run
// setup is complete.
func waitReady(a api) (bool, error) {
	var info struct{ StartupWizardCompleted bool }
	for range readyChecks {
		status, answer, err := a.do("GET", "/System/Info/Public", nil)
		if err != nil {
			return false, err
		}
		if status == 200 {
			if err := json.Unmarshal([]byte(answer), &info); err != nil {
				return false, err
			}
			return info.StartupWizardCompleted, nil
		}
		a.e.Sleep(2 * time.Second)
	}
	return false, errors.New("Jellyfin did not answer within 5 minutes.")
}

// firstRun completes Jellyfin's first-run setup with the administrator
// Selfnook creates. The login is saved before Jellyfin receives it.
func firstRun(a api) error {
	v := a.e.Values
	if !v.Has(userKey) {
		v.Set(userKey, adminName)
	}
	if !v.Has(passwordKey) {
		v.Set(passwordKey, app.RandomToken(24))
	}
	if err := a.e.SaveValues(); err != nil {
		return err
	}
	steps := []struct {
		method, path string
		body         any
	}{
		{"POST", "/Startup/Configuration", map[string]string{"UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": "en"}},
		{"GET", "/Startup/User", nil},
		{"POST", "/Startup/User", map[string]string{"Name": v.Get(userKey), "Password": v.Get(passwordKey)}},
		{"POST", "/Startup/RemoteAccess", map[string]bool{"EnableRemoteAccess": true, "EnableAutomaticPortMapping": false}},
		{"POST", "/Startup/Complete", nil},
	}
	for _, step := range steps {
		if err := a.call(step.method, step.path, step.body, nil); err != nil {
			return err
		}
	}
	return nil
}

// signIn returns a session as the saved administrator.
func signIn(a api) (api, error) {
	v := a.e.Values
	if !v.Has(userKey) || !v.Has(passwordKey) {
		return a, errNoLogin
	}
	var session struct{ AccessToken string }
	err := a.call("POST", "/Users/AuthenticateByName", map[string]string{"Username": v.Get(userKey), "Pw": v.Get(passwordKey)}, &session)
	var status *StatusError
	if errors.As(err, &status) && status.Status == 401 {
		return a, errors.New("Jellyfin refused the saved administrator login. Run 'selfnook credentials jellyfin' to record the current one.")
	}
	a.token = session.AccessToken
	return a, err
}

// serverName is the name Jellyfin shows in its pages and apps. Without one,
// Jellyfin uses the container's random host name, which changes whenever the
// container is created again.
const serverName = "Selfnook"

// ensureServerName names the server, unless it already has a name.
func ensureServerName(a api) error {
	var config map[string]any
	if err := a.call("GET", "/System/Configuration", nil, &config); err != nil {
		return err
	}
	// Jellyfin replaces every server setting with the ones sent, so the name
	// is sent only together with the settings it returned.
	if config == nil {
		return errors.New("Jellyfin returned no server settings, so its name was not set.")
	}
	if name, _ := config["ServerName"].(string); name != "" {
		return nil
	}
	config["ServerName"] = serverName
	return a.call("POST", "/System/Configuration", config, nil)
}

// ensureLibraries adds a library for each library app whose folder no
// library uses yet, then scans them.
func ensureLibraries(a api, movies, series role.Library) error {
	var existing []struct{ Locations []string }
	if err := a.call("GET", "/Library/VirtualFolders", nil, &existing); err != nil {
		return err
	}
	used := map[string]bool{}
	for _, library := range existing {
		for _, location := range library.Locations {
			used[location] = true
		}
	}
	for _, wanted := range []struct{ name, kind, folder string }{
		{moviesLibrary, "movies", movies.RootFolder()},
		{seriesLibrary, "tvshows", series.RootFolder()},
	} {
		if used[wanted.folder] {
			continue
		}
		body := map[string]any{"LibraryOptions": map[string]any{"PathInfos": []map[string]string{{"Path": wanted.folder}}}}
		if err := a.call("POST", "/Library/VirtualFolders?name="+wanted.name+"&collectionType="+wanted.kind+"&refreshLibrary=false", body, nil); err != nil {
			return err
		}
	}
	return a.call("POST", "/Library/Refresh", nil, nil)
}

// ensureKey creates the API key the other apps use, unless one is saved.
func ensureKey(a api) error {
	if a.e.Values.Has(keyName) {
		return nil
	}
	if err := a.call("POST", "/Auth/Keys?app="+keyApp, nil, nil); err != nil {
		return err
	}
	var keys struct {
		Items []struct{ AccessToken, AppName string }
	}
	if err := a.call("GET", "/Auth/Keys", nil, &keys); err != nil {
		return err
	}
	for i := len(keys.Items) - 1; i >= 0; i-- {
		if keys.Items[i].AppName == keyApp {
			a.e.Values.Set(keyName, keys.Items[i].AccessToken)
			return a.e.SaveValues()
		}
	}
	return errors.New("Jellyfin created no API key for " + keyApp + ".")
}

// provider names the sign-in service in the plugin's addresses.
func provider(signIn role.SignIn) string { return strings.ToLower(signIn.Name()) }

// returnAddress is where the sign-in service sends people back to Jellyfin.
func returnAddress(access role.Access, signIn role.SignIn) string {
	return access.URL() + baseURL + "/sso/OID/redirect/" + provider(signIn)
}

// applySignIn points the sign-in plugin at the sign-in service and adds the
// sign-in button to Jellyfin's login page. Settings the plugin keeps, such
// as the links between accounts, are preserved. Nothing is sent when the
// settings already match. A plugin that Jellyfin did not load is reported,
// and the password login keeps working.
func applySignIn(a api, access role.Access, signIn role.SignIn) error {
	var configs map[string]map[string]any
	status, answer, err := a.do("GET", "/sso/OID/Get", nil)
	if err != nil {
		return err
	}
	if status == 404 {
		a.e.UI.Say("Jellyfin did not load its sign-in plugin, so its login page has no " + signIn.Name() + " button. The Jellyfin password login still works.")
		return nil
	}
	if status != 200 {
		return &StatusError{Method: "GET", Path: "/sso/OID/Get", Status: status, Body: answer}
	}
	if err := json.Unmarshal([]byte(answer), &configs); err != nil {
		return err
	}
	name := provider(signIn)
	current := configs[name]
	wanted := maps.Clone(current)
	if wanted == nil {
		wanted = map[string]any{}
	}
	scheme := "http"
	if access.Secure() {
		scheme = "https"
	}
	for key, value := range map[string]any{
		"OidEndpoint":              signIn.Issuer(clientID),
		"OidClientId":              clientID,
		"OidSecret":                a.e.Values.Get(secretKey),
		"Enabled":                  true,
		"EnableAuthorization":      true,
		"EnableAllFolders":         true,
		"Roles":                    []any{},
		"AdminRoles":               []any{signIn.AdminGroup()},
		"RoleClaim":                "groups",
		"SchemeOverride":           scheme,
		"DisableHttps":             !access.Secure(),
		"PreserveAdminPermissions": false,
	} {
		wanted[key] = value
	}
	if !equalJSON(current, wanted) {
		if err := a.call("POST", "/sso/OID/Add/"+name, wanted, nil); err != nil {
			return err
		}
	}

	var branding map[string]any
	if err := a.call("GET", "/System/Configuration/branding", nil, &branding); err != nil {
		return err
	}
	wantedBranding := maps.Clone(branding)
	if wantedBranding == nil {
		wantedBranding = map[string]any{}
	}
	wantedBranding["LoginDisclaimer"] = `<form action="` + access.URL() + baseURL + `/sso/OID/start/` + name + `"><button class="raised block emby-button button-submit">Sign in with ` + signIn.Name() + `</button></form>`
	wantedBranding["CustomCss"] = loginCSS
	if equalJSON(branding, wantedBranding) {
		return nil
	}
	return a.call("POST", "/System/Configuration/branding", wantedBranding, nil)
}

// loginCSS shows the sign-in button as a full-width button.
const loginCSS = `a.raised.emby-button {
  padding: 0.9em 1em;
  color: inherit !important;
}

.disclaimerContainer {
  display: block;
}`

// equalJSON compares two decoded JSON objects by their encoding.
func equalJSON(a, b map[string]any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
