// Package commands lists what the program does, for both the command line
// and the full-screen interface.
package commands

import (
	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/stack"
)

// Command is one action of the program.
type Command struct {
	// Name is the command-line name; Label is the menu entry.
	Name  string
	Label string
	// Help is the one-line summary; Detail is shown by -h.
	Help   string
	Detail string
	// Group means the command takes a credential group name as its only
	// argument, which the full-screen interface asks for in a list.
	Group bool
	// Logged means the command runs from automatic start or a schedule, so
	// it accepts --log-file <path> to write its output to that file.
	Logged bool
	Run    func(s *stack.Stack, args []string) error
}

var All = []Command{
	{
		Name: "setup", Label: "Setup",
		Help:   "Configure credentials, services, Recyclarr, and automatic startup.",
		Detail: "Configure credentials, start the services, apply Recyclarr profiles, and install automatic startup.",
		Run:    func(s *stack.Stack, _ []string) error { return s.Setup() },
	},
	{
		Name: "start", Label: "Start",
		Help:   "Start the services and synchronize Recyclarr.",
		Detail: "Start the services, wait for Sonarr and Radarr, then synchronize Recyclarr.",
		Logged: true,
		Run:    func(s *stack.Stack, _ []string) error { return s.Start() },
	},
	{
		Name: "stop", Label: "Stop",
		Help:   "Stop the stack containers.",
		Detail: "Stop the stack containers and keep them, with their configuration and media files, for the next start.",
		Run:    func(s *stack.Stack, _ []string) error { return s.Stop() },
	},
	{
		Name: "update", Label: "Update",
		Help:   "Choose which containers move to the versions this release ships.",
		Detail: "List each container that runs another version than this release ships, ask whether to replace it, then start the stack.",
		Run:    func(s *stack.Stack, _ []string) error { return s.Update() },
	},
	{
		Name: "check", Label: "Check",
		Help:   "Restart Caddy when its ports stop answering.",
		Detail: "Restart Caddy when a port it publishes does not answer on this computer, as after Docker Desktop restores port forwards to outdated container addresses. Setup schedules this check every 15 minutes.",
		Logged: true,
		Run:    func(s *stack.Stack, _ []string) error { return s.Check() },
	},
	{
		Name: "status", Label: "Status",
		Help:   "Show the current service status.",
		Detail: "Show the current Docker Compose status for every service.",
		Run:    func(s *stack.Stack, _ []string) error { return s.Status() },
	},
	{
		Name: "vpn-status", Label: "VPN status",
		Help:   "Show the selected VPN gateway and its connection status.",
		Detail: "Check the selected VPN gateway health or Tailscale exit-node availability.",
		Run:    func(s *stack.Stack, _ []string) error { return s.RoleStatus(role.VPN) },
	},
	{
		Name: "credentials", Label: "Credentials",
		Help:   "List or change the saved values of one group.",
		Detail: "Show the saved values of one group. Answer y to type new values, pressing Enter to keep a value.",
		Group:  true,
		Run: func(s *stack.Stack, args []string) error {
			group := ""
			if len(args) > 0 {
				group = args[0]
			}
			return s.Credentials(group)
		},
	},
}
