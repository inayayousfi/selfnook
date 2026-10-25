package authentik

import (
	"errors"
	"strings"
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/state"
)

// A new named route appears in the sign-in text without editing Authentik.
func TestCoverageNamesProtectedAndPublicApps(t *testing.T) {
	routes := []app.Route{
		{Name: "Sonarr", Path: "/sonarr"},
		{Name: "Bazarr", Path: "/bazarr"},
		{Name: "Tool", Port: 8000},
		{Path: pagesPath, Upstream: address, Public: true},
		{Upstream: "homepage:3000", Fallback: true},
		{Name: "Jellyfin", Path: "/jellyfin", Public: true},
	}
	want := "They protect Sonarr, Bazarr, and Tool, which no longer ask for their own logins. Jellyfin keeps its own login."
	if got := coverage(routes); got != want {
		t.Errorf("text = %q", got)
	}
}

// access is a fixed address in place of the app that fills the role.
type access struct {
	url    string
	secure bool
}

func (access) Name() string   { return "Caddy" }
func (a access) URL() string  { return a.url }
func (a access) Host() string { return "" }
func (a access) Secure() bool { return a.secure }

var encrypted = access{"https://media.example.com", true}

func blueprintFor(address access) string {
	saved := state.New()
	saved.Set("authentik", passwordKey, "secret-admin-password")
	return Blueprint(app.ValuesFor(saved, App), address, nil)
}

// The blueprint file reads the admin password from the environment, so the file never holds it.
func TestBlueprintKeepsThePasswordOutOfTheFile(t *testing.T) {
	file := blueprintFor(encrypted)
	if strings.Contains(file, "secret-admin-password") || !strings.Contains(file, "password: !Env ADMIN_PASS") {
		t.Errorf("blueprint:\n%s", file)
	}
	for _, part := range []string{`external_host: "https://media.example.com"`, `authentik_host: "https://media.example.com/authentik"`} {
		if !strings.Contains(file, part) {
			t.Errorf("missing %s", part)
		}
	}
}

// On an unencrypted address, the sign-in page itself says which methods cannot work.
func TestLocalModeSignInPageNamesTheUnavailableMethods(t *testing.T) {
	if !strings.Contains(blueprintFor(access{"http://box.local", false}), "passkeys and Google sign-in are unavailable") {
		t.Error("local sign-in page does not explain the missing methods")
	}
	if strings.Contains(blueprintFor(access{"https://box.tail.ts.net", true}), "unavailable") {
		t.Error("an encrypted address claims methods are unavailable")
	}
}

// A changed admin password changes the file, so Authentik applies it at once.
func TestChangedLoginChangesTheBlueprint(t *testing.T) {
	v := app.ValuesFor(state.New(), App)
	v.Set("AUTHENTIK_SECRET_KEY", "key")
	v.Set(userKey, "chief")
	v.Set(passwordKey, "first")
	before := Blueprint(v, encrypted, nil)
	v.Set(passwordKey, "second")
	if Blueprint(v, encrypted, nil) == before {
		t.Error("a new password left the blueprint unchanged")
	}
}

// Renaming the admin deactivates the earlier account; renaming back reactivates it.
func TestRenamedAdminAccountIsDeactivated(t *testing.T) {
	v := app.ValuesFor(state.New(), App)
	v.Set(userKey, "chief")
	if !trackAdmin(v) || strings.Contains(Blueprint(v, encrypted, nil), "is_active: false") {
		t.Fatal("the first admin was retired")
	}
	if trackAdmin(v) {
		t.Error("an unchanged admin reported a change")
	}
	v.Set(userKey, "boss")
	trackAdmin(v)
	if !strings.Contains(Blueprint(v, encrypted, nil), "      username: \"chief\"\n    attrs:\n      is_active: false\n") {
		t.Errorf("old account not deactivated:\n%s", Blueprint(v, encrypted, nil))
	}
	v.Set(userKey, "chief")
	trackAdmin(v)
	file := Blueprint(v, encrypted, nil)
	if strings.Contains(file, "username: \"chief\"\n    attrs:\n      is_active: false") || !strings.Contains(file, "username: \"boss\"\n    attrs:\n      is_active: false") {
		t.Errorf("rename back:\n%s", file)
	}
}

// Authentik answers sign-in checks only for the proxies it supports.
func TestCheckRefusesAnUnsupportedProxy(t *testing.T) {
	s := signIn{app.ValuesFor(state.New(), App), encrypted}
	if check, err := s.Check("Caddy"); err != nil || check != (role.Check{Upstream: address, Path: "/outpost.goauthentik.io/auth/caddy"}) {
		t.Errorf("Caddy check = %+v, err = %v", check, err)
	}
	var unsupported *role.Unsupported
	if _, err := s.Check("nginx"); !errors.As(err, &unsupported) || err.Error() != "Authentik cannot work with nginx." {
		t.Errorf("err = %v", err)
	}
}

// A sign-in client becomes an Authentik application that reads its secret
// from the environment and returns people only to the client's address.
func TestClientsBecomeApplicationsWithoutTheirSecrets(t *testing.T) {
	saved := state.New()
	file := Blueprint(app.ValuesFor(saved, App), encrypted, []app.Client{{
		Name: "Jellyfin", ID: "jellyfin", Return: "https://media.example.com/jellyfin/sso/OID/redirect/authentik", SecretKey: "JELLYFIN_OIDC_SECRET",
	}})
	for _, part := range []string{
		"      client_id: \"jellyfin\"\n      client_secret: !Env JELLYFIN_OIDC_SECRET\n      grant_types: [authorization_code]\n",
		"        - matching_mode: strict\n          url: \"https://media.example.com/jellyfin/sso/OID/redirect/authentik\"\n",
		"      slug: \"jellyfin\"\n    attrs:\n      name: \"Jellyfin\"\n      provider: !KeyOf client-jellyfin\n",
	} {
		if !strings.Contains(file, part) {
			t.Errorf("missing %q in:\n%s", part, file)
		}
	}
}

// The sign-in page title lives in Selfnook's own flow, which the default
// brand uses, because Authentik re-applies its default flow over any edit.
func TestSignInTitleLivesInSelfnooksOwnFlow(t *testing.T) {
	file := blueprintFor(access{"http://box.local", false})
	for _, part := range []string{
		"      slug: selfnook-authentication\n    attrs:\n      name: Selfnook sign-in\n      title: \"Sign in. " + unencrypted + "\"\n",
		"      domain: authentik-default\n    attrs:\n      flow_authentication: !KeyOf sign-in\n",
	} {
		if !strings.Contains(file, part) {
			t.Errorf("missing %q in:\n%s", part, file)
		}
	}
	if strings.Contains(file, "slug: default-authentication-flow") {
		t.Error("the blueprint still edits Authentik's default sign-in flow")
	}
}
