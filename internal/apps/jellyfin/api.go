package jellyfin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
)

// api sends requests to Jellyfin from inside its container, so Jellyfin
// needs no published port. It uses the Authorization header, the only
// sign-in method Jellyfin 12 accepts by default.
type api struct {
	e     *app.Env
	token string
}

// StatusError is a request Jellyfin answered with an unexpected status.
type StatusError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("Jellyfin answered %s %s with status %d: %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

func (a api) header() string {
	value := `MediaBrowser Client="Selfnook", Device="Selfnook", DeviceId="selfnook", Version="1"`
	if a.token != "" {
		value += `, Token="` + a.token + `"`
	}
	return "Authorization: " + value
}

// do sends body, when not nil, as JSON and returns the status and answer.
// A status of 0 means Jellyfin did not answer.
func (a api) do(method, path string, body any) (int, string, error) {
	args := []string{"curl", "-sS", "-X", method, "-H", a.header(), "-w", "\n%{http_code}"}
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		args = append(args, "-H", "Content-Type: application/json", "--data-binary", string(encoded))
	}
	args = append(args, "http://localhost:"+strconv.Itoa(port)+baseURL+path)
	result, err := a.e.Runtime.Exec(service, args...)
	if err != nil {
		return 0, "", err
	}
	answer, code, _ := strings.Cut(strings.TrimRight(result.Stdout, "\n"), "\n")
	if code == "" {
		answer, code = "", answer
	}
	status, _ := strconv.Atoi(strings.TrimSpace(code))
	return status, answer, nil
}

// call sends the request and decodes the answer into out, when not nil.
// Any status other than 2xx is a StatusError.
func (a api) call(method, path string, body, out any) error {
	status, answer, err := a.do(method, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return &StatusError{Method: method, Path: path, Status: status, Body: answer}
	}
	if out == nil || answer == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(answer), out); err != nil {
		return fmt.Errorf("Jellyfin answered %s %s with unreadable data: %w", method, path, err)
	}
	return nil
}
