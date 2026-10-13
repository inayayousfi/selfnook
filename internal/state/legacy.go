package state

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

// ReadEnv reads a .env file that earlier versions wrote. found is false when
// the file does not exist.
func ReadEnv(path string) (values map[string]string, found bool, err error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fail(ErrUnreadable, "Cannot read %s.", path)
	}
	values = map[string]string{}
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		key, rawValue, ok := strings.Cut(line, "=")
		if line == "" || strings.HasPrefix(line, "#") || !ok {
			continue
		}
		var value string
		if json.Unmarshal([]byte(rawValue), &value) != nil {
			value = rawValue
		}
		values[key] = strings.ReplaceAll(value, "$$", "$")
	}
	return values, true, nil
}

// ReadSteps reads the setup progress file that earlier versions wrote: a
// JSON list of finished step names. A missing file yields no steps.
func ReadSteps(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fail(ErrUnreadable, "Cannot read setup progress from %s.", path)
	}
	var steps []string
	if json.Unmarshal(content, &steps) != nil {
		return nil, fail(ErrInvalid, "Invalid setup progress in %s.", path)
	}
	return steps, nil
}
