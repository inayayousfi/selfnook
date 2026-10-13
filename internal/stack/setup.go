package stack

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/autostart"
	"github.com/inayayousfi/selfnook/internal/flow"
)

// Setup asks every question, starts the stack, guides through each app's
// first configuration, and installs automatic start. Progress is saved, so
// an interrupted setup resumes where it stopped.
func (s *Stack) Setup() error {
	ses, _, err := s.open()
	if err != nil {
		return err
	}
	if err := ses.runtime.Ready(); err != nil {
		return err
	}
	s.UI.Say("This setup saves progress and can be rerun after an interruption.")
	ses.steps = true
	if err := ses.removeRetiredDirs(); err != nil {
		return err
	}

	core := app.ValuesFor(ses.saved, app.Core)
	var mediaDir string
	if core.Has("MEDIA_DIR") {
		if mediaDir, err = normalizedPath(core.Get("MEDIA_DIR")); err != nil {
			return err
		}
		s.UI.Say("Reusing media directory: " + mediaDir)
	} else {
		answers, err := s.UI.Ask(flow.Screen{Fields: []flow.Field{{Key: "media", Prompt: "Media directory", Check: normalizedPath}}})
		if err != nil {
			return err
		}
		mediaDir = answers["media"]
	}
	uid, gid := userIDs()
	core.Set("MEDIA_DIR", filepath.ToSlash(mediaDir))
	core.Set("CONFIG_DIR", filepath.ToSlash(filepath.Join(s.Root, "config")))
	core.Set("PUID", uid)
	core.Set("PGID", gid)
	core.SetDefault("TZ", "Europe/Paris")
	if err := ses.save(); err != nil {
		return err
	}

	if err := ses.assignRoles(true); err != nil {
		return err
	}
	for _, a := range ses.active() {
		if a.Configure != nil {
			if err := a.Configure(ses.env(a)); err != nil {
				return err
			}
		}
	}
	ses.derive()
	if err := ses.createDirectories(); err != nil {
		return err
	}
	if err := ses.save(); err != nil {
		return err
	}

	if err := ses.writeCompose(); err != nil {
		return err
	}
	if err := ses.runtime.Check(); err != nil {
		return err
	}
	if err := ses.runtime.Prepare(); err != nil {
		return err
	}
	if err := ses.startServices(); err != nil {
		return err
	}
	if err := ses.waitForSignIn(); err != nil {
		return err
	}

	for _, a := range ses.active() {
		if impl := ses.impl(a); impl.Setup != nil {
			if err := impl.Setup(ses.env(a)); err != nil {
				return err
			}
		}
	}

	if err := autostart.Install(s.Shell, s.UI, s.Program, s.Root); err != nil {
		return err
	}
	s.UI.Say("Setup complete. Use '" + Command + " status' to inspect it.\nOpen Homepage:\n" + ses.access().URL())
	return nil
}

// normalizedPath turns an entered folder into an absolute path.
func normalizedPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("Path cannot be empty.")
	}
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("Cannot resolve path: " + raw)
		}
		value = filepath.Join(home, value[1:])
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("Cannot resolve path: " + raw)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path, nil
}

// userIDs are the IDs containers run as, so they can write the shared folders.
func userIDs() (string, string) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid < 0 || gid < 0 {
		return "1000", "1000"
	}
	return strconv.Itoa(uid), strconv.Itoa(gid)
}
