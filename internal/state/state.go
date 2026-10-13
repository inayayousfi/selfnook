// Package state reads and writes the installation's saved state: every
// saved value grouped by the app that owns it, the finished setup steps,
// which app fills each role, and the detected version of each service.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/inayayousfi/selfnook/internal/files"
)

// Core is the owner of the values the stack itself needs, such as MEDIA_DIR.
const Core = "selfnook"

// Unclaimed holds values moved from an earlier .env that no app owns.
const Unclaimed = "unclaimed"

// State is the content of the state file.
type State struct {
	// Values maps an owner to its values.
	Values map[string]map[string]string `json:"values"`
	// Progress lists the finished guided setup steps.
	Progress []string `json:"progress"`
	// Roles maps a role name to the app or runtime that fills it.
	Roles map[string]string `json:"roles"`
	// Versions maps a Compose service to the version found for its image.
	Versions map[string]Version `json:"versions"`
	// Addresses maps a Compose service to its fixed network address.
	Addresses map[string]string `json:"addresses"`
}

// Version is what the program found about the image of one service.
type Version struct {
	// Version is empty when no source could tell it.
	Version string `json:"version"`
	// Source is how the version was found: "digest", "label", or "app".
	Source string `json:"source,omitempty"`
	Digest string `json:"digest,omitempty"`
	Label  string `json:"label,omitempty"`
}

// Version sources.
const (
	FromDigest = "digest"
	FromLabel  = "label"
	FromApp    = "app"
)

func New() *State {
	return &State{
		Values:    map[string]map[string]string{},
		Roles:     map[string]string{},
		Versions:  map[string]Version{},
		Addresses: map[string]string{},
	}
}

// Get returns owner's value for key.
func (s *State) Get(owner, key string) string { return s.Values[owner][key] }

// Has reports whether owner has a non-empty value for key.
func (s *State) Has(owner, key string) bool { return s.Values[owner][key] != "" }

// Set stores owner's value for key.
func (s *State) Set(owner, key, value string) {
	if s.Values[owner] == nil {
		s.Values[owner] = map[string]string{}
	}
	s.Values[owner][key] = value
}

// SetDefault stores the value only when owner has no entry for key yet.
func (s *State) SetDefault(owner, key, value string) {
	if _, ok := s.Values[owner][key]; !ok {
		s.Set(owner, key, value)
	}
}

// Done reports whether a guided setup step is finished.
func (s *State) Done(step string) bool { return slices.Contains(s.Progress, step) }

// Complete records a finished guided setup step.
func (s *State) Complete(step string) {
	if !s.Done(step) {
		s.Progress = append(s.Progress, step)
		slices.Sort(s.Progress)
	}
}

// ErrUnreadable and ErrInvalid tell a file that cannot be read from one
// whose content is not a state file. Both leave the file unchanged.
var (
	ErrUnreadable = errors.New("unreadable state file")
	ErrInvalid    = errors.New("invalid state file")
)

// Error is a failure shown to the user as Message alone, identified by Kind.
type Error struct {
	Kind    error
	Message string
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Kind }

func fail(kind error, format string, args ...any) error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Read loads path. A missing file yields an empty state and found = false.
func Read(path string) (s *State, found bool, err error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(), false, nil
	}
	if err != nil {
		return nil, false, fail(ErrUnreadable, "Cannot read the saved settings from %s.", path)
	}
	s = New()
	if err := json.Unmarshal(content, s); err != nil {
		return nil, false, fail(ErrInvalid, "The saved settings in %s are damaged.", path)
	}
	if s.Values == nil {
		s.Values = map[string]map[string]string{}
	}
	if s.Roles == nil {
		s.Roles = map[string]string{}
	}
	if s.Versions == nil {
		s.Versions = map[string]Version{}
	}
	if s.Addresses == nil {
		s.Addresses = map[string]string{}
	}
	return s, true, nil
}

// Write replaces path atomically. On Unix the file is readable only by its owner.
func Write(path string, s *State) error {
	content, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return files.WriteAtomic(path, append(content, '\n'), 0o600)
}

// Path is the state file in this user's state folder: $XDG_STATE_HOME or
// ~/.local/state on Linux, %LOCALAPPDATA% on Windows, and
// ~/Library/Application Support on macOS.
func Path() (string, error) {
	var dir string
	switch runtime.GOOS {
	case "windows":
		dir = os.Getenv("LOCALAPPDATA")
		if dir == "" {
			return "", errors.New("Cannot find the folder for saved settings: LOCALAPPDATA is not set.")
		}
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("Cannot find the folder for saved settings: the home folder is unknown.")
		}
		dir = filepath.Join(home, "Library", "Application Support")
	default:
		dir = os.Getenv("XDG_STATE_HOME")
		if !filepath.IsAbs(dir) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", errors.New("Cannot find the folder for saved settings: the home folder is unknown.")
			}
			dir = filepath.Join(home, ".local", "state")
		}
	}
	return filepath.Join(dir, "selfnook", "state.json"), nil
}
