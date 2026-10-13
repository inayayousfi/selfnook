// Package autostart makes the stack start with the computer.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/flow"
	"github.com/inayayousfi/selfnook/internal/shell"
)

const (
	unitName     = "selfnook.service"
	launcherName = "selfnook.vbs"
	// The check runs every checkInterval, from a systemd timer on Linux and
	// from a scheduled task on Windows.
	checkUnitName     = "selfnook-check.service"
	checkTimerName    = "selfnook-check.timer"
	checkLauncherName = "selfnook-check.vbs"
	checkTaskName     = "Selfnook check"
	checkMinutes      = 15
	// The names earlier versions installed, removed when the new ones are installed.
	retiredUnitName     = "media-stack.service"
	retiredLauncherName = "media-stack.vbs"
)

// SystemdUnit returns a unit that starts the stack with program. A non-empty
// user makes it a system unit that runs as that user after Docker.
func SystemdUnit(program, root, user string) string {
	var unit strings.Builder
	unit.WriteString("[Unit]\nDescription=Selfnook\n")
	if user != "" {
		unit.WriteString("Requires=docker.service\nWants=network-online.target\nAfter=docker.service network-online.target\n")
	}
	unit.WriteString("\n[Service]\nType=oneshot\nRemainAfterExit=yes\n")
	if user != "" {
		fmt.Fprintf(&unit, "User=%s\n", user)
	}
	logFile := filepath.Join(root, "config", "startup.log")
	fmt.Fprintf(&unit, "WorkingDirectory=%s\n", systemdQuote(root))
	fmt.Fprintf(&unit, "ExecStart=%s start --log-file %s\n", systemdQuote(program), systemdQuote(logFile))
	fmt.Fprintf(&unit, "ExecStop=%s stop\n", systemdQuote(program))
	unit.WriteString("TimeoutStartSec=infinity\n\n[Install]\n")
	if user != "" {
		unit.WriteString("WantedBy=multi-user.target\n")
	} else {
		unit.WriteString("WantedBy=default.target\n")
	}
	return unit.String()
}

// CheckUnit returns a unit that runs the check once with program. A non-empty
// user makes it a system unit that runs as that user after Docker.
func CheckUnit(program, root, user string) string {
	var unit strings.Builder
	unit.WriteString("[Unit]\nDescription=Selfnook check\n")
	if user != "" {
		unit.WriteString("After=docker.service\n")
	}
	unit.WriteString("\n[Service]\nType=oneshot\n")
	if user != "" {
		fmt.Fprintf(&unit, "User=%s\n", user)
	}
	fmt.Fprintf(&unit, "WorkingDirectory=%s\n", systemdQuote(root))
	fmt.Fprintf(&unit, "ExecStart=%s check --log-file %s\n", systemdQuote(program), systemdQuote(filepath.Join(root, "config", "check.log")))
	return unit.String()
}

// CheckTimer runs the check unit 5 minutes after boot, then every checkMinutes.
func CheckTimer() string {
	return fmt.Sprintf("[Unit]\nDescription=Selfnook check every %d minutes\n\n[Timer]\nOnBootSec=5min\nOnUnitActiveSec=%dmin\n\n[Install]\nWantedBy=timers.target\n", checkMinutes, checkMinutes)
}

// systemdQuote quotes a path only when systemd would otherwise split it.
func systemdQuote(value string) string {
	if !strings.ContainsAny(value, " \t\"'\\") {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// WindowsLauncher returns a script that runs command, such as start, without
// a visible window.
func WindowsLauncher(program, command, logFile string) string {
	command = fmt.Sprintf(`"%s" %s --log-file "%s"`, program, command, logFile)
	escaped := strings.ReplaceAll(command, `"`, `""`)
	return "Set shell = CreateObject(\"WScript.Shell\")\n" +
		fmt.Sprintf("shell.Run \"%s\", 0, False\n", escaped)
}

const (
	userService   = "user"
	systemService = "system"
)

// Install makes the stack start with the computer.
func Install(s shell.Shell, ui flow.UI, program, root string) error {
	switch runtime.GOOS {
	case "windows":
		return installWindowsAutostart(s, ui, program, root)
	case "linux":
		return installLinuxAutostart(s, ui, program, root)
	}
	return errors.New("Automatic start is supported only on Windows and Linux.")
}

func installLinuxAutostart(s shell.Shell, ui flow.UI, program, root string) error {
	runs, err := systemdRuns(s)
	if err != nil {
		return err
	}
	if !runs {
		return errors.New("Automatic start needs systemd, which does not run on this computer.")
	}
	answers, err := ui.Ask(flow.Screen{
		Title: "Linux automatic start",
		Fields: []flow.Field{{
			Key:    "kind",
			Prompt: "Choose 1 or 2",
			Kind:   flow.Choice,
			Options: []flow.Option{
				{Value: userService, Label: "User service (starts with this user's systemd session)"},
				{Value: systemService, Label: "System service (starts at boot and requires sudo)"},
			},
		}},
	})
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := removeRetiredUnits(s, home); err != nil {
		return err
	}
	if answers["kind"] == userService {
		dir := filepath.Join(home, ".config", "systemd", "user")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for name, content := range units(program, root, "") {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				return err
			}
		}
		for _, args := range [][]string{
			{"systemctl", "--user", "daemon-reload"},
			{"systemctl", "--user", "enable", unitName},
			{"systemctl", "--user", "enable", "--now", checkTimerName},
		} {
			if _, err := s.Run(shell.Cmd{Args: args, Check: true}); err != nil {
				return err
			}
		}
		ui.Say("Installed " + filepath.Join(dir, unitName) + "\nInstalled " + filepath.Join(dir, checkTimerName))
		return nil
	}

	user := os.Getenv("USER")
	if user == "" {
		return errors.New("Cannot determine the current Linux user.")
	}
	temporary, err := os.MkdirTemp("", "selfnook-units-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	var commands [][]string
	for name, content := range units(program, root, user) {
		path := filepath.Join(temporary, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
		commands = append(commands, []string{"sudo", "install", "-m", "644", path, "/etc/systemd/system/" + name})
	}
	slices.SortFunc(commands, func(a, b []string) int { return strings.Compare(a[5], b[5]) })
	commands = append(commands,
		[]string{"sudo", "systemctl", "daemon-reload"},
		[]string{"sudo", "systemctl", "enable", unitName},
		[]string{"sudo", "systemctl", "enable", "--now", checkTimerName},
	)
	for _, args := range commands {
		if _, err := s.Run(shell.Cmd{Args: args, Terminal: true, Check: true}); err != nil {
			return err
		}
	}
	ui.Say("Installed /etc/systemd/system/" + unitName + "\nInstalled /etc/systemd/system/" + checkTimerName)
	return nil
}

// systemdRuns reports whether systemd runs this computer. systemctl answers
// "offline" when it is installed but systemd does not run.
func systemdRuns(s shell.Shell) (bool, error) {
	result, err := s.Run(shell.Cmd{Args: []string{"systemctl", "is-system-running"}, Capture: true})
	if errors.Is(err, exec.ErrNotFound) {
		return false, nil
	}
	return err == nil && strings.TrimSpace(result.Stdout) != "offline", err
}

// units are the systemd files that start the stack and schedule the check.
func units(program, root, user string) map[string]string {
	return map[string]string{
		unitName:       SystemdUnit(program, root, user),
		checkUnitName:  CheckUnit(program, root, user),
		checkTimerName: CheckTimer(),
	}
}

func installWindowsAutostart(s shell.Shell, ui flow.UI, program, root string) error {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return errors.New("APPDATA is unavailable; cannot find the Startup folder.")
	}
	startup := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	if err := os.MkdirAll(startup, 0o755); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(startup, retiredLauncherName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	launcher := filepath.Join(startup, launcherName)
	content := WindowsLauncher(program, "start", filepath.Join(root, "config", "startup.log"))
	if err := os.WriteFile(launcher, []byte(content), 0o644); err != nil {
		return err
	}
	checkLauncher := filepath.Join(root, checkLauncherName)
	content = WindowsLauncher(program, "check", filepath.Join(root, "config", "check.log"))
	if err := os.WriteFile(checkLauncher, []byte(content), 0o644); err != nil {
		return err
	}
	if _, err := s.Run(shell.Cmd{Args: CheckTask(checkLauncher), Check: true}); err != nil {
		return err
	}
	ui.Say("Installed " + launcher + "\nScheduled the task " + checkTaskName)
	return nil
}

// CheckTask is the command that schedules the check launcher every
// checkMinutes while this user is signed in, replacing an earlier task.
func CheckTask(launcher string) []string {
	return []string{"schtasks", "/Create", "/F", "/TN", checkTaskName, "/SC", "MINUTE", "/MO", strconv.Itoa(checkMinutes),
		"/TR", `wscript.exe "` + launcher + `"`}
}

// removeRetiredUnits disables and deletes the services earlier versions
// installed. It does not stop them: their stop command runs the earlier
// program, which would rewrite this stack's Compose files.
func removeRetiredUnits(s shell.Shell, home string) error {
	userUnit := filepath.Join(home, ".config", "systemd", "user", retiredUnitName)
	if _, err := os.Stat(userUnit); err == nil {
		if _, err := s.Run(shell.Cmd{Args: []string{"systemctl", "--user", "disable", retiredUnitName}, Check: true}); err != nil {
			return err
		}
		if err := os.Remove(userUnit); err != nil {
			return err
		}
		if _, err := s.Run(shell.Cmd{Args: []string{"systemctl", "--user", "daemon-reload"}, Check: true}); err != nil {
			return err
		}
	}
	systemUnit := "/etc/systemd/system/" + retiredUnitName
	if _, err := os.Stat(systemUnit); err == nil {
		for _, args := range [][]string{
			{"sudo", "systemctl", "disable", retiredUnitName},
			{"sudo", "rm", systemUnit},
			{"sudo", "systemctl", "daemon-reload"},
		} {
			if _, err := s.Run(shell.Cmd{Args: args, Terminal: true, Check: true}); err != nil {
				return err
			}
		}
	}
	return nil
}
