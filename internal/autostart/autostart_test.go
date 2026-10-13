package autostart

import (
	"strings"
	"testing"
)

func TestUserUnitRunsAsItsOwnerAfterLogin(t *testing.T) {
	unit := SystemdUnit("/opt/stack/selfnook", "/opt/stack", "")
	if strings.Contains(unit, "User=") || strings.Contains(unit, "docker.service") || !strings.Contains(unit, "WantedBy=default.target") {
		t.Errorf("unit:\n%s", unit)
	}
	if !strings.Contains(unit, "ExecStart=/opt/stack/selfnook start --log-file /opt/stack/config/startup.log\n") {
		t.Errorf("unit:\n%s", unit)
	}
}

func TestSystemUnitRunsAsTheSelectedUserAfterDocker(t *testing.T) {
	unit := SystemdUnit("/opt/stack/selfnook", "/opt/stack", "media")
	for _, line := range []string{"User=media\n", "Requires=docker.service\n", "Wants=network-online.target\n", "After=docker.service network-online.target\n", "WantedBy=multi-user.target\n"} {
		if !strings.Contains(unit, line) {
			t.Errorf("unit lacks %q", line)
		}
	}
}

func TestUnitQuotesPathsWithSpaces(t *testing.T) {
	unit := SystemdUnit("/home/me/my stack/selfnook", "/home/me/my stack", "")
	if !strings.Contains(unit, `ExecStart="/home/me/my stack/selfnook" start --log-file "/home/me/my stack/config/startup.log"`) {
		t.Errorf("unit:\n%s", unit)
	}
}

func TestWindowsLauncherStartsWithoutAWindow(t *testing.T) {
	launcher := WindowsLauncher(`C:\Media Stack\selfnook.exe`, "start", `C:\Media Stack\config\startup.log`)
	want := "Set shell = CreateObject(\"WScript.Shell\")\nshell.Run \"\"\"C:\\Media Stack\\selfnook.exe\"\" start --log-file \"\"C:\\Media Stack\\config\\startup.log\"\"\", 0, False\n"
	if launcher != want {
		t.Errorf("launcher = %q", launcher)
	}
}

// The check runs 5 minutes after boot, then every 15 minutes, and writes to
// its own log file.
func TestCheckRunsEveryFifteenMinutes(t *testing.T) {
	unit := CheckUnit("/opt/stack/selfnook", "/opt/stack", "")
	if !strings.Contains(unit, "ExecStart=/opt/stack/selfnook check --log-file /opt/stack/config/check.log\n") {
		t.Errorf("unit:\n%s", unit)
	}
	timer := CheckTimer()
	for _, line := range []string{"OnBootSec=5min\n", "OnUnitActiveSec=15min\n", "WantedBy=timers.target\n"} {
		if !strings.Contains(timer, line) {
			t.Errorf("timer lacks %q:\n%s", line, timer)
		}
	}
}

// Windows runs the hidden launcher every 15 minutes; the launcher path is
// quoted inside the task command so a folder with spaces works.
func TestWindowsTaskRunsTheHiddenCheckLauncher(t *testing.T) {
	got := strings.Join(CheckTask(`C:\Media Stack\selfnook-check.vbs`), "|")
	want := `schtasks|/Create|/F|/TN|Selfnook check|/SC|MINUTE|/MO|15|/TR|wscript.exe "C:\Media Stack\selfnook-check.vbs"`
	if got != want {
		t.Errorf("task = %s", got)
	}
}
