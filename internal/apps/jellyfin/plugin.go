package jellyfin

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The sign-in plugin lets people sign in to Jellyfin with their Authentik
// account. Its release is pinned by checksum, like the container images.
const (
	pluginVersion = "5.1.1"
	pluginURL     = "https://github.com/K0lin/jellyfin-plugin-sso/releases/download/v" + pluginVersion + "/sso-authentication_" + pluginVersion + ".zip"
	pluginSHA256  = "db126b4763bda3a3754fa2103232fc03ef1bf929cfeda35f2e9af39c875233b1"
	// pluginPrefix names the plugin's folders, one per installed version.
	pluginPrefix = "SSO-Auth_"
)

func pluginsDir(configDir string) string {
	return filepath.Join(configDir, "jellyfin", "plugins")
}

// pluginInstalled reports whether the pinned version is in its folder.
func pluginInstalled(dir string) bool {
	content, err := os.ReadFile(filepath.Join(dir, pluginPrefix+pluginVersion, "meta.json"))
	if err != nil {
		return false
	}
	var meta struct{ Version string }
	return json.Unmarshal(content, &meta) == nil && meta.Version == pluginVersion
}

// installPlugin puts the pinned plugin in its folder and removes the folders
// of other versions. It reports whether anything changed. The archive is
// unpacked beside its folder first, so a failure leaves no partial plugin.
func installPlugin(dir string, download func(string) ([]byte, error)) (bool, error) {
	if pluginInstalled(dir) {
		return removeOtherVersions(dir)
	}
	archive, err := download(pluginURL)
	if err != nil {
		return false, fmt.Errorf("Cannot download Jellyfin's sign-in plugin: %w", err)
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != pluginSHA256 {
		return false, errors.New("The downloaded Jellyfin sign-in plugin does not match its pinned checksum, so it was not installed.")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	staging, err := os.MkdirTemp(dir, ".selfnook-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(staging)
	if err := unzip(archive, staging); err != nil {
		return false, fmt.Errorf("Cannot unpack Jellyfin's sign-in plugin: %w", err)
	}
	// MkdirTemp creates the folder readable only by this user.
	if err := os.Chmod(staging, 0o755); err != nil {
		return false, err
	}
	target := filepath.Join(dir, pluginPrefix+pluginVersion)
	if err := os.RemoveAll(target); err != nil {
		return false, err
	}
	if err := os.Rename(staging, target); err != nil {
		return false, err
	}
	if _, err := removeOtherVersions(dir); err != nil {
		return true, err
	}
	return true, nil
}

func removeOtherVersions(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	changed := false
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), pluginPrefix) && entry.Name() != pluginPrefix+pluginVersion {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}

// unzip writes the archive's files, which sit at its top level, into dir.
func unzip(archive []byte, dir string) error {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		name := filepath.Base(file.Name)
		if file.FileInfo().IsDir() || name != file.Name {
			continue
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		content, err := io.ReadAll(source)
		source.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			return err
		}
	}
	return nil
}
