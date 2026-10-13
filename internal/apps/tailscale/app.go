// Package tailscale is the VPN gateway that carries the traffic of the
// services behind the VPN through one fixed Tailscale exit node.
package tailscale

import (
	"embed"
	"encoding/json"
	"errors"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
)

//go:embed compose.yaml
var compose embed.FS

const (
	service  = "tailscale-vpn"
	authKey  = "TAILSCALE_AUTH_KEY"
	exitNode = "TAILSCALE_EXIT_NODE"
)

var App = &app.App{
	Name:  "tailscale",
	Title: "Tailscale",
	Settings: []app.Setting{
		{Key: authKey, Required: true},
		{Key: exitNode, Required: true},
	},
	Options: []flow.Option{{Value: "tailscale", Label: "Tailscale exit node"}},
	WasActive: func(old map[string]string) bool {
		return old["VPN_GATEWAY_SERVICE"] == service
	},
	Compose:    compose,
	Services:   func(app.Values) []string { return []string{service} },
	ConfigDirs: []string{"tailscale"},
	Configure:  configure,
	Images: []app.Image{{
		Service: service,
		Digests: map[string]string{"sha256:2667499ed87ae29218f292556ba062918402dd5e92e93637af14867e4df12dd3": "1.102.4"},
		Ask: func(e *app.Env) (string, error) {
			result, err := e.Runtime.Exec(service, "tailscale", "version")
			if err != nil || result.Code != 0 {
				return "", err
			}
			version, _, _ := strings.Cut(result.Stdout, "\n")
			return strings.TrimSpace(version), nil
		},
	}},
	Implementations: []app.Implementation{{
		From: "1.102.4", Until: "1.102.4",
		Provides: []role.Offer{role.Provide(role.VPN, func(role.Values, role.Set) role.Gateway { return gateway{} })},
		Status:   Status,
		Tiles: func(app.Values, role.Set) []app.Tile {
			return []app.Tile{{Group: "Admin", Position: 5, Name: "Tailscale", Description: "VPN connection", Service: "tailscale-vpn"}}
		},
	}},
	Credentials: &app.CredentialGroup{
		Name: "vpn",
		Fields: []app.CredentialField{
			{Label: "Exit node", Key: exitNode, Kind: app.PlainText, UsedByStack: true},
			{Label: "Auth key", Key: authKey, Kind: app.Password, UsedByStack: true},
		},
	},
}

// gateway is the network gateway role.
type gateway struct{}

func (gateway) Service() string { return service }
func (gateway) Name() string    { return "Tailscale" }

// Ready reports whether the exit node is online.
func (g gateway) Ready(r runtime.Runtime) (bool, error) {
	if err := role.Running(r, g.Name(), service); err != nil {
		return false, err
	}
	result, err := r.Exec(service, "tailscale", "status", "--json")
	if err != nil || result.Code != 0 {
		return false, err
	}
	var status struct {
		ExitNodeStatus struct{ Online bool }
	}
	return json.Unmarshal([]byte(result.Stdout), &status) == nil && status.ExitNodeStatus.Online, nil
}

// Problem is Tailscale's latest message.
func (gateway) Problem(r runtime.Runtime) string {
	lines, err := r.Logs(service, 1)
	if err != nil || len(lines) == 0 {
		return ""
	}
	return "Tailscale's latest message: " + lines[0]
}

func configure(e *app.Env) error {
	v := e.Values
	if v.Has(authKey) && v.Has(exitNode) {
		return nil
	}
	var fields []flow.Field
	if !v.Has(authKey) {
		fields = append(fields, flow.Field{Key: authKey, Prompt: "Tailscale auth key", Kind: flow.Secret, Check: flow.Required("Auth key cannot be empty.")})
	}
	if !v.Has(exitNode) {
		fields = append(fields, flow.Field{Key: exitNode, Prompt: "Tailscale exit-node name or IP", Check: flow.Required("Exit-node identifier cannot be empty.")})
	}
	return e.AskAndSave(guide(app.List(e.BehindVPN), fields))
}

func guide(behind string, fields []flow.Field) flow.Screen {
	return flow.Screen{
		Title: "Tailscale exit-node setup",
		Body: behind + ` will share a separate Tailscale container and use one fixed exit node. Setup starts them only after that exit node reports online.

1. Open the Tailscale admin console Keys page.
https://login.tailscale.com/admin/settings/keys
2. Generate a one-off, non-ephemeral auth key. Enable Pre-approved if device approval is enabled.
3. Open the Machines page and identify the exact machine name or Tailscale IP of an available exit node.
https://login.tailscale.com/admin/machines
4. The following prompts will request the auth key and exit-node identifier.

If the Tailscale gateway later restarts, the traffic of ` + behind + ` stays blocked, and a stack restart may be needed to reconnect it.`,
		Fields: fields,
	}
}

// Status describes the exit node's connection.
func Status(e *app.Env) (string, error) {
	ready, err := gateway{}.Ready(e.Runtime)
	var down *role.GatewayDown
	state := "not online"
	switch {
	case errors.As(err, &down):
		state = "not running"
	case err != nil:
		return "", err
	case ready:
		state = "online"
	}
	return "Tailscale exit node: " + state + "\nSelected exit node:\n" + e.Values.Get(exitNode), nil
}
