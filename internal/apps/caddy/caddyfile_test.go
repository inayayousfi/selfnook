package caddy_test

import (
	"strings"
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/apps/caddy"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/state"
)

func values(mode, host string) app.Values {
	saved := state.New()
	saved.Set("caddy", "ACCESS_MODE", mode)
	saved.Set("caddy", "ADMIN_ACCESS_HOST", host)
	v := app.ValuesFor(saved, caddy.App)
	caddy.App.Derive(v)
	return v
}

func TestAccessAddressMustBeAName(t *testing.T) {
	for _, bad := range []string{"localhost", "192.168.1.10", "media example.com"} {
		if _, err := caddy.ValidHost(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if host, err := caddy.ValidHost(" Media.Example.COM. "); err != nil || host != "media.example.com" {
		t.Errorf("host = %q, err = %v", host, err)
	}
}

// An app served on its own port is still behind the sign-in unless it is public.
func TestOwnPortRoutesKeepTheSignIn(t *testing.T) {
	check := role.Check{Upstream: "authentik-server:9000", Path: "/outpost.goauthentik.io/auth/caddy"}
	file, err := caddy.Caddyfile(values("domain", "media.example.com"), []app.Route{
		{Upstream: "authentik-server:9000", Public: true, Port: 9091},
		{Path: "/tool", Upstream: "tool:8000", Port: 8000},
	}, check)
	if err != nil {
		t.Fatal(err)
	}
	want := "media.example.com:8000 {\n\thandle {\n\t\tforward_auth authentik-server:9000 {\n\t\t\turi /outpost.goauthentik.io/auth/caddy\n\t\t\ttrusted_proxies private_ranges\n\t\t}\n\t\treverse_proxy tool:8000\n\t}\n}"
	if !strings.Contains(file, want) {
		t.Errorf("Caddyfile:\n%s", file)
	}
}
