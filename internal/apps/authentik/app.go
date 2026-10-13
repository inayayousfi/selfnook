// Package authentik is the sign-in service. It checks the admin sign-in for
// every page that is not public, and it is where people add sign-in methods
// such as Google accounts, an email server, or passkeys.
package authentik

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
)

//go:embed compose.yaml
var compose embed.FS

const (
	userKey     = "ADMIN_USER"
	passwordKey = "ADMIN_PASS"
	// accountKey is the admin username last written into the blueprint;
	// retiredKey lists earlier admin usernames, kept deactivated.
	accountKey = "AUTHENTIK_ADMIN_ACCOUNT"
	retiredKey = "AUTHENTIK_RETIRED_ADMINS"
	// signInPort serves Authentik's own pages, including the sign-in page.
	signInPort = 9091
	// address is where Caddy asks whether a request is signed in.
	address = serverService + ":9000"
	// outpostPath is where Authentik's built-in proxy answers on the main address.
	outpostPath = "/outpost.goauthentik.io"
	// adminGroup is the group Authentik creates for its administrators.
	adminGroup = "authentik Admins"
)

// generated are secrets setup creates once. The bootstrap password locks
// Authentik's default akadmin account, so its public first-run page closes.
var generated = []string{"AUTHENTIK_SECRET_KEY", "AUTHENTIK_DB_PASSWORD", "AUTHENTIK_BOOTSTRAP_PASSWORD"}

var App = &app.App{
	Name:  "authentik",
	Title: "Authentik",
	Settings: []app.Setting{
		{Key: userKey, Required: true},
		{Key: passwordKey, Required: true},
		{Key: generated[0]},
		{Key: generated[1]},
		{Key: generated[2]},
		{Key: accountKey},
		{Key: retiredKey},
	},
	Needs: []role.Role{role.AccessAddress},
	// Tinyauth was the sign-in service before Authentik.
	RetiredSettings:   []string{"TINYAUTH_USERS", "ADMIN_LOGIN_FINGERPRINT", "TINYAUTH_SECURE_COOKIE"},
	RetiredConfigDirs: []string{"tinyauth"},
	Compose:           compose,
	Services:          func(app.Values) []string { return []string{"authentik-db", serverService, "authentik-worker"} },
	ConfigDirs:        []string{"authentik/data", "authentik/blueprints"},
	Derive: func(v app.Values) {
		for _, key := range generated {
			if !v.Has(key) {
				v.Set(key, app.RandomToken(48))
			}
		}
	},
	Configure: configure,
	Images: []app.Image{
		{
			Service: serverService,
			Digests: map[string]string{"sha256:ab9b4e8cc4ab3f8d1198d2db6aeea66bafea1963b3f2843589e0d163f97d9849": "2026.8.3"},
			Labels:  map[string]string{"2026.8.3": "2026.8.3"},
		},
		{
			Service: "authentik-db",
			Digests: map[string]string{"sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea": "16.15"},
		},
	},
	Implementations: []app.Implementation{{
		From: "2026.8.3", Until: "2026.8.3",
		Provides: []role.Offer{role.Provide(role.AdminSignIn, func(v role.Values, r role.Set) role.SignIn {
			return signIn{v, role.Get(r, role.AccessAddress)}
		})},
		Prepare: func(e *app.Env) ([]string, error) {
			if trackAdmin(e.Values) {
				if err := e.SaveValues(); err != nil {
					return nil, err
				}
			}
			_, err := files.WriteIfChanged(e.ConfigPath("authentik", "blueprints", "selfnook.yaml"), []byte(Blueprint(e.Values, role.Get(e.Roles, role.AccessAddress), e.Clients)))
			return nil, err
		},
		Setup: func(e *app.Env) error {
			return e.Step("authentik", false, func() error {
				return flow.Show(e.UI, Guide(e.Values, role.Get(e.Roles, role.AccessAddress)))
			})
		},
		Routes: func(app.Values, role.Set) []app.Route {
			return []app.Route{
				{Upstream: address, Public: true, Port: signInPort},
				{Path: outpostPath, Upstream: address, Public: true},
			}
		},
		Tiles: func(app.Values, role.Set) []app.Tile {
			return []app.Tile{{Group: "Admin", Position: 6, Name: "Authentik", Description: "Manage sign-in methods and users", Link: ":" + strconv.Itoa(signInPort) + "/if/admin/", Service: serverService}}
		},
	}},
	Credentials: &app.CredentialGroup{Name: "admin", Fields: []app.CredentialField{
		{Label: "Username", Key: userKey, Kind: app.PlainText, UsedByStack: true},
		{Label: "Password", Key: passwordKey, Kind: app.Password, UsedByStack: true},
	}},
}

// serverService runs Authentik's pages and answers the sign-in checks.
const serverService = "authentik-server"

// signIn is the admin sign-in role, read from Authentik's saved values.
type signIn struct {
	v      role.Values
	access role.Access
}

func (signIn) Name() string       { return "Authentik" }
func (s signIn) User() string     { return s.v.Get(userKey) }
func (s signIn) Password() string { return s.v.Get(passwordKey) }
func (s signIn) URL() string      { return pagesURL(s.access) }

// AdminGroup is Authentik's own administrator group, which the admin joins.
func (signIn) AdminGroup() string { return adminGroup }

// Issuer follows Authentik's address for each application's provider.
func (s signIn) Issuer(client string) string {
	return pagesURL(s.access) + "/application/o/" + client + "/"
}

// checks are the proxies Authentik answers sign-in checks for, with the
// path its built-in proxy answers on for each.
var checks = map[string]string{"Caddy": outpostPath + "/auth/caddy"}

// readyScript asks the sign-in check at the address given as its first
// argument about a page of the address given as its second, with its scheme
// as the third. It exits 0 only for a redirect to the sign-in address given
// as the fourth argument. Authentik answers "Server is starting up" during
// its first minutes, then 404 until it has applied the blueprint's proxy
// provider for the address.
const readyScript = `import sys, urllib.request as request
class Keep(request.HTTPRedirectHandler):
    def redirect_request(self, *args): return None
check = request.Request(sys.argv[1],
    headers={"X-Forwarded-Host": sys.argv[2], "X-Forwarded-Proto": sys.argv[3], "X-Forwarded-Uri": "/"})
try:
    request.build_opener(Keep).open(check, timeout=10)
except request.HTTPError as answer:
    sys.exit(0 if answer.code == 302 and answer.headers.get("Location", "").startswith(sys.argv[4]) else 1)
sys.exit(1)
`

// Ready reports whether Authentik sends a visitor of the address to its
// sign-in page, which the image's own Python asks from inside the server
// through the same check the address's proxy uses.
func (s signIn) Ready(r runtime.Runtime) (bool, error) {
	check, err := s.Check(s.access.Name())
	if err != nil {
		return false, err
	}
	scheme, _, _ := strings.Cut(s.access.URL(), "://")
	result, err := r.Exec(serverService, "python3", "-c", readyScript, "http://"+check.Upstream+check.Path, s.access.Host(), scheme, s.URL()+"/application/o/authorize/")
	return err == nil && result.Code == 0, err
}

func (s signIn) Check(proxy string) (role.Check, error) {
	path, ok := checks[proxy]
	if !ok {
		return role.Check{}, &role.Unsupported{App: s.Name(), Other: proxy}
	}
	return role.Check{Upstream: address, Path: path}, nil
}

// pagesURL is the address of Authentik's own pages.
func pagesURL(access role.Access) string { return access.URL() + ":" + strconv.Itoa(signInPort) }

// trackAdmin retires the previous admin username when it changed, so the
// blueprint keeps that account deactivated. It reports whether values changed.
func trackAdmin(v app.Values) bool {
	current, previous := v.Get(userKey), v.Get(accountKey)
	retired := retiredAdmins(v)
	if previous != "" && previous != current && !slices.Contains(retired, previous) {
		retired = append(retired, previous)
	}
	retired = slices.DeleteFunc(retired, func(name string) bool { return name == current })
	encoded, _ := json.Marshal(retired)
	changed := previous != current || v.Get(retiredKey) != string(encoded)
	v.Set(accountKey, current)
	v.Set(retiredKey, string(encoded))
	return changed
}

func retiredAdmins(v app.Values) []string {
	retired := []string{}
	json.Unmarshal([]byte(v.Get(retiredKey)), &retired)
	return retired
}

func configure(e *app.Env) error {
	if e.Values.Has(userKey) && e.Values.Has(passwordKey) {
		return nil
	}
	var fields []flow.Field
	if !e.Values.Has(userKey) {
		fields = append(fields, flow.UsernameField(userKey, "Admin"))
	}
	if !e.Values.Has(passwordKey) {
		fields = append(fields, flow.PasswordField(passwordKey, "Admin"))
	}
	return e.AskAndSave(flow.Screen{
		Title:  "Admin sign-in",
		Body:   "Create one username and password. " + coverage(e.Routes) + "\n\nThe following prompts request the username and password.",
		Fields: fields,
	})
}

// coverage names the applications the sign-in protects and those that keep their own login.
func coverage(routes []app.Route) string {
	var protected, own []string
	for _, route := range routes {
		switch {
		case route.Name == "":
		case route.Public:
			own = append(own, route.Name)
		default:
			protected = append(protected, route.Name)
		}
	}
	if len(protected) == 0 {
		protected = []string{"no application"}
	}
	text := "They protect " + app.List(protected) + ", which no longer ask for their own logins."
	switch len(own) {
	case 0:
	case 1:
		text += " " + own[0] + " keeps its own login."
	default:
		text += " " + app.List(own) + " keep their own logins."
	}
	return text
}

// unencrypted explains, on the sign-in page and in setup, what local mode cannot offer.
const unencrypted = "This address is not encrypted, so passkeys and Google sign-in are unavailable."

// Guide shows where the admin signs in and where sign-in methods are added.
func Guide(v app.Values, access role.Access) flow.Screen {
	body := `Open this link:
` + pagesURL(access) + `/if/admin/

1. Sign in with the admin login.
2. Username: ` + v.Get(userKey) + `
3. Password: ` + v.Get(passwordKey)
	if access.Secure() {
		body += "\n4. To let people sign in with Google or another account, open Directory > Federation and Social login, click Create, then paste the keys from that provider."
	} else {
		body += "\n\n" + unencrypted
	}
	return flow.Screen{Title: "Authentik setup", Body: body}
}

// Blueprint is the Authentik configuration the program owns: the admin
// user, the sign-in check for the administration pages, an application for
// each sign-in client, and the sign-in page's title. Authentik applies it whenever the file changes. Secrets are
// read from the worker's environment, never written into the file; the login
// fingerprint changes the file when the admin login changes.
func Blueprint(v app.Values, access role.Access, clients []app.Client) string {
	title := "Welcome to authentik!"
	if !access.Secure() {
		title = "Sign in. " + unencrypted
	}
	return `# Generated by selfnook. Changes here are overwritten.
# Admin login fingerprint: ` + loginFingerprint(v) + `
version: 1
metadata:
  name: selfnook
entries:
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: authentik Bootstrap
      required: true
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: Default - Authentication flow
      required: true
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: Default - Brand
      required: true
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: Default - Provider authorization flow (implicit consent)
      required: true
  - model: authentik_blueprints.metaapplyblueprint
    attrs:
      identifiers:
        name: Default - Provider invalidation flow
      required: true
  - model: authentik_core.user
    state: present
    identifiers:
      username: !Env ADMIN_USER
    attrs:
      name: !Env ADMIN_USER
      password: !Env ADMIN_PASS
      groups:
        - !Find [authentik_core.group, [name, ` + adminGroup + `]]
` + retiredEntries(v) + `  - model: authentik_providers_proxy.proxyprovider
    id: provider
    state: present
    identifiers:
      name: selfnook administration pages
    attrs:
      mode: forward_single
      external_host: ` + quote(access.URL()) + `
      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]
      invalidation_flow: !Find [authentik_flows.flow, [slug, default-provider-invalidation-flow]]
  - model: authentik_core.application
    state: present
    identifiers:
      slug: selfnook-administration
    attrs:
      name: Administration pages
      provider: !KeyOf provider
  - model: authentik_outposts.outpost
    state: present
    identifiers:
      managed: goauthentik.io/outposts/embedded
    attrs:
      providers:
        - !KeyOf provider
      config:
        authentik_host: ` + quote(pagesURL(access)) + `
` + clientEntries(clients) + signInFlow(title)
}

// signInFlow is Selfnook's own sign-in flow, which the default brand uses.
// It reuses the steps and optional-step rules of Authentik's default sign-in
// flow and only sets its own title. Authentik applies its built-in blueprints
// in no guaranteed order and again when they change, so a title written into
// its default flow would be overwritten; the default brand is only created by
// Authentik, never updated, so its sign-in flow stays Selfnook's.
func signInFlow(title string) string {
	return `  - model: authentik_flows.flow
    id: sign-in
    state: present
    identifiers:
      slug: ` + signInSlug + `
    attrs:
      name: Selfnook sign-in
      title: ` + quote(title) + `
      designation: authentication
      authentication: none
  - model: authentik_flows.flowstagebinding
    identifiers:
      order: 10
      target: !KeyOf sign-in
      stage: !Find [authentik_stages_identification.identificationstage, [name, default-authentication-identification]]
  - model: authentik_flows.flowstagebinding
    id: sign-in-password
    identifiers:
      order: 20
      target: !KeyOf sign-in
      stage: !Find [authentik_stages_password.passwordstage, [name, default-authentication-password]]
    attrs:
      re_evaluate_policies: true
  - model: authentik_flows.flowstagebinding
    id: sign-in-mfa
    identifiers:
      order: 30
      target: !KeyOf sign-in
      stage: !Find [authentik_stages_authenticator_validate.authenticatorvalidatestage, [name, default-authentication-mfa-validation]]
  - model: authentik_flows.flowstagebinding
    identifiers:
      order: 100
      target: !KeyOf sign-in
      stage: !Find [authentik_stages_user_login.userloginstage, [name, default-authentication-login]]
  - model: authentik_policies.policybinding
    identifiers:
      order: 10
      target: !KeyOf sign-in-password
      policy: !Find [authentik_policies_expression.expressionpolicy, [name, default-authentication-flow-password-stage]]
    attrs:
      failure_result: true
  - model: authentik_policies.policybinding
    identifiers:
      order: 10
      target: !KeyOf sign-in-mfa
      policy: !Find [authentik_policies_expression.expressionpolicy, [name, default-authentication-flow-authenticator-validate-stage]]
    attrs:
      failure_result: true
  - model: authentik_brands.brand
    state: present
    identifiers:
      domain: authentik-default
    attrs:
      flow_authentication: !KeyOf sign-in
`
}

// signInSlug identifies Selfnook's sign-in flow.
const signInSlug = "selfnook-authentication"

// clientEntries registers each client as an OpenID Connect application. Its
// secret comes from the worker's environment. Tokens are signed with
// Authentik's own certificate, so clients check them with its public keys,
// and they carry the openid, email, and profile scopes; profile includes the
// user's groups.
func clientEntries(clients []app.Client) string {
	var out strings.Builder
	for _, c := range clients {
		id := "client-" + c.ID
		out.WriteString(`  - model: authentik_providers_oauth2.oauth2provider
    id: ` + id + `
    state: present
    identifiers:
      name: ` + quote("selfnook "+c.Name) + `
    attrs:
      client_type: confidential
      client_id: ` + quote(c.ID) + `
      client_secret: !Env ` + c.SecretKey + `
      grant_types: [authorization_code]
      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]
      invalidation_flow: !Find [authentik_flows.flow, [slug, default-provider-invalidation-flow]]
      signing_key: !Find [authentik_crypto.certificatekeypair, [name, authentik Self-signed Certificate]]
      redirect_uris:
        - matching_mode: strict
          url: ` + quote(c.Return) + `
      property_mappings:
        - !Find [authentik_providers_oauth2.scopemapping, [managed, goauthentik.io/providers/oauth2/scope-openid]]
        - !Find [authentik_providers_oauth2.scopemapping, [managed, goauthentik.io/providers/oauth2/scope-email]]
        - !Find [authentik_providers_oauth2.scopemapping, [managed, goauthentik.io/providers/oauth2/scope-profile]]
  - model: authentik_core.application
    state: present
    identifiers:
      slug: ` + quote(c.ID) + `
    attrs:
      name: ` + quote(c.Name) + `
      provider: !KeyOf ` + id + `
`)
	}
	return out.String()
}

// retiredEntries keeps every earlier admin account deactivated.
func retiredEntries(v app.Values) string {
	var out strings.Builder
	for _, name := range retiredAdmins(v) {
		out.WriteString("  - model: authentik_core.user\n    state: present\n    identifiers:\n      username: " + quote(name) + "\n    attrs:\n      is_active: false\n")
	}
	return out.String()
}

// loginFingerprint changes whenever the admin login changes. It is keyed
// with the stack's secret, so it does not reveal the password.
func loginFingerprint(v app.Values) string {
	mac := hmac.New(sha256.New, []byte(v.Get("AUTHENTIK_SECRET_KEY")))
	mac.Write([]byte(v.Get(userKey) + "\x00" + v.Get(passwordKey)))
	return hex.EncodeToString(mac.Sum(nil))
}

// quote writes value as a YAML string.
func quote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
