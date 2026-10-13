// Package caddy is the reverse proxy: the single address that serves every
// web page, with its HTTPS certificate and the admin sign-in check.
package caddy

import (
	"embed"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/files"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
)

//go:embed compose.yaml
var compose embed.FS

const (
	modeKey = "ACCESS_MODE"
	hostKey = "ADMIN_ACCESS_HOST"
	urlKey  = "ACCESS_URL"

	// name is how this proxy asks the sign-in service for its check address.
	name = "Caddy"

	Tailscale = "tailscale"
	Domain    = "domain"
	Local     = "local"
)

var App = &app.App{
	Name:  "caddy",
	Title: "Caddy",
	Settings: []app.Setting{
		{Key: modeKey, Required: true},
		{Key: hostKey, Required: true},
		{Key: urlKey},
	},
	Needs:      []role.Role{role.AdminSignIn},
	Compose:    compose,
	Services:   func(app.Values) []string { return []string{"caddy"} },
	ConfigDirs: []string{"caddy/certs", "caddy/data", "caddy/config"},
	Validate:   validate,
	Derive: func(v app.Values) {
		if v.Has(modeKey) {
			v.Set(urlKey, scheme(v)+"://"+v.Get(hostKey))
		}
	},
	Configure: configure,
	Images: []app.Image{{
		Service: "caddy",
		Digests: map[string]string{"sha256:3422ce6de165df66534f9b9ba50efaf457114ec961763cc52f5dbdaac2972d73": "2.11.6"},
		Labels:  map[string]string{"v2.11.6": "2.11.6"},
	}},
	Implementations: []app.Implementation{{
		From: "2.11.6", Until: "2.11.6",
		Provides: []role.Offer{role.Provide(role.AccessAddress, func(v role.Values, _ role.Set) role.Access { return access{v} })},
		Prepare:  prepare,
		Probe:    probe,
	}},
	Credentials: &app.CredentialGroup{Name: "access", Fields: []app.CredentialField{
		{Label: "Mode", Key: modeKey},
		{Label: "Address", Key: urlKey},
	}},
}

var modes = []flow.Option{
	{
		Value:  Tailscale,
		Label:  "Tailscale HTTPS certificate",
		Detail: "Pros: every device trusts the certificate, nothing to buy, works on Windows and Linux.\nCons: the pages are reachable only from devices signed in to your Tailscale network. Tailscale must be installed on this computer.",
	},
	{
		Value:  Domain,
		Label:  "Own domain with a Let's Encrypt certificate",
		Detail: "Pros: every device trusts the certificate, and the pages are reachable from anywhere.\nCons: you need a domain name and a router that forwards ports to this computer. The pages are reachable from the whole internet; the administration pages are protected by the admin sign-in.",
	},
	{
		Value:  Local,
		Label:  "Local network name without encryption",
		Detail: "Pros: nothing to buy or configure outside this computer.\nCons: passwords and pages cross your network unencrypted, so passkeys and Google sign-in are unavailable. Devices that cannot resolve .local names, including some Android phones, cannot open the pages.",
	},
}

var hostPattern = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)
var numericHost = regexp.MustCompile(`^[0-9.]+$`)

// ValidHost normalizes an address name and rejects bare names and IP addresses.
func ValidHost(host string) (string, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if !hostPattern.MatchString(host) || numericHost.MatchString(host) {
		return "", errors.New("The address must be a name containing a dot, such as media.example.com.")
	}
	return host, nil
}

// access is the address role, read from Caddy's saved values.
type access struct{ v role.Values }

func (access) Name() string   { return name }
func (a access) URL() string  { return a.v.Get(urlKey) }
func (a access) Host() string { return a.v.Get(hostKey) }
func (a access) Secure() bool { return scheme(a.v) == "https" }

func scheme(v role.Values) string {
	if v.Get(modeKey) == Local {
		return "http"
	}
	return "https"
}

func validate(v app.Values) error {
	switch v.Get(modeKey) {
	case Tailscale, Domain, Local:
	default:
		return errors.New("ACCESS_MODE must be tailscale, domain, or local.")
	}
	_, err := ValidHost(v.Get(hostKey))
	return err
}

func modeField(listed bool) flow.Field {
	return flow.Field{Key: "mode", Prompt: "Select an access mode", Kind: flow.Choice, Default: Tailscale, Options: modes, Listed: listed}
}

func hostField(prompt, fallback string) flow.Field {
	return flow.Field{Key: "host", Prompt: prompt, Default: fallback, Check: ValidHost}
}

// configure asks for the access mode and address unless valid ones are
// saved. In Tailscale mode on Linux it lets this user fetch certificates once.
func configure(e *app.Env) error {
	if validate(e.Values) != nil {
		if err := askAccess(e); err != nil {
			return err
		}
	}
	if e.Values.Get(modeKey) != Tailscale {
		return nil
	}
	return e.Step("tailscale-operator", false, func() error {
		return grantTailscaleOperator(e.Shell, e.UI.Say)
	})
}

func askAccess(e *app.Env) error {
	v := e.Values
	screen := flow.Screen{
		Title:  "Secure access",
		Body:   "Every web page is reached through one address. Choose how that address is encrypted:",
		Fields: []flow.Field{modeField(false)},
	}
	var mode, host string
	for {
		answers, err := e.UI.Ask(screen)
		if err != nil {
			return err
		}
		mode = answers["mode"]
		if mode != Tailscale {
			break
		}
		host = tailscaleDNSName(e.Shell)
		if host == "" {
			screen = flow.Screen{Body: "Tailscale is not installed or not signed in on this computer. Choose another mode.", Fields: []flow.Field{modeField(true)}}
			continue
		}
		if tailscaleServeUsesHTTPS(e.Shell) {
			screen = flow.Screen{Body: tailscaleServeConflict, Fields: []flow.Field{modeField(true)}}
			continue
		}
		if err := flow.Show(e.UI, flow.Screen{
			Title: "Tailscale HTTPS certificate",
			Body:  "1. Open the Tailscale admin console DNS page.\nhttps://login.tailscale.com/admin/dns\n2. Under HTTPS Certificates, click Enable HTTPS if it is not already enabled.",
			Wait:  "Tailscale HTTPS",
		}); err != nil {
			return err
		}
		break
	}
	switch mode {
	case Domain:
		answers, err := e.UI.Ask(flow.Screen{
			Title: "Own domain",
			Body: `1. At your domain provider, create an A record that points your chosen name to this network's public IP address.
2. On your router, forward TCP ports ` + app.List(servedPorts(e.Values, e.Routes)) + ` to this computer.
3. The following prompt requests the name, for example media.example.com.

Let's Encrypt connects to this computer through those ports to issue the certificate. Until both steps are done, the pages do not open.`,
			Fields: []flow.Field{hostField("Domain name", "")},
		})
		if err != nil {
			return err
		}
		host = answers["host"]
	case Local:
		name, _ := os.Hostname()
		name, _, _ = strings.Cut(name, ".")
		answers, err := e.UI.Ask(flow.Screen{Fields: []flow.Field{hostField("Local network name", name+".local")}})
		if err != nil {
			return err
		}
		host = answers["host"]
	}
	v.Set(modeKey, mode)
	v.Set(hostKey, host)
	return e.SaveValues()
}

func prepare(e *app.Env) ([]string, error) {
	certChanged := false
	if e.Values.Get(modeKey) == Tailscale {
		var err error
		certChanged, err = tailscaleCert(e.Shell, e.ConfigPath("caddy", "certs", "cert.pem"), e.ConfigPath("caddy", "certs", "key.pem"), e.Values.Get(hostKey))
		if err != nil {
			return nil, err
		}
	}
	check, err := role.Get(e.Roles, role.AdminSignIn).Check(name)
	if err != nil {
		return nil, err
	}
	configChanged, err := files.WriteIfChanged(e.ConfigPath("caddy", "Caddyfile"), []byte(Caddyfile(e.Values, e.Routes, check)))
	if err != nil {
		return nil, err
	}
	if certChanged || configChanged {
		return []string{"caddy"}, nil
	}
	return nil, nil
}
