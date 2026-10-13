package stack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/state"
)

// ErrLeftBehind reports an earlier file that was moved into the state file
// but could not be deleted. The state file is complete; only the named
// file remains and holds a copy of the moved values.
var ErrLeftBehind = errors.New("earlier file left behind")

// importEnv moves the .env and setup progress that earlier versions kept in
// the stack folder into the state file, then deletes them. found is false
// when there was no .env. The state file is written before anything is
// deleted, so a failure leaves at least one complete copy.
func (s *Stack) importEnv() (*state.State, bool, error) {
	envPath := filepath.Join(s.Root, ".env")
	old, found, err := state.ReadEnv(envPath)
	if err != nil || !found {
		return state.New(), false, err
	}
	saved := state.New()
	for key, value := range old {
		if owner := s.ownerOf(key); owner != "" {
			saved.Set(owner, key, value)
		}
	}
	for _, a := range s.Apps {
		for oldKey, current := range a.Renamed {
			if value, ok := old[oldKey]; ok && !saved.Has(a.Name, current) {
				saved.Set(a.Name, current, value)
			}
		}
	}
	for _, a := range s.Apps {
		if a.WasActive == nil || !a.WasActive(old) {
			continue
		}
		for _, info := range a.Provides(a.Pinned()) {
			saved.Roles[info.Name] = a.Name
		}
	}
	progressPath := ""
	if configDir := saved.Get(state.Core, "CONFIG_DIR"); configDir != "" {
		progressPath = filepath.Join(filepath.FromSlash(configDir), "setup-state.json")
		if saved.Progress, err = state.ReadSteps(progressPath); err != nil {
			return nil, false, err
		}
	}
	if err := state.Write(s.StatePath, saved); err != nil {
		return nil, false, err
	}
	for _, path := range []string{envPath, progressPath} {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, false, &state.Error{Kind: ErrLeftBehind, Message: fmt.Sprintf("The saved settings moved to %s, but %s could not be deleted. Delete it yourself.", s.StatePath, path)}
		}
	}
	return saved, true, nil
}

// ownerOf names who keeps an earlier .env value: the core or the app that
// owns it, Unclaimed when nobody does, or "" for a value that is no longer
// used.
func (s *Stack) ownerOf(key string) string {
	if app.Core.Owns(key) {
		return state.Core
	}
	for _, a := range s.Apps {
		if a.Owns(key) {
			return a.Name
		}
	}
	for _, a := range s.Apps {
		if _, renamed := a.Renamed[key]; renamed || slices.Contains(a.RetiredSettings, key) {
			return ""
		}
	}
	return state.Unclaimed
}
