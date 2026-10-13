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
	headers := []string{a.header()}
	data := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		headers, data = append(headers, "Content-Type: application/json"), string(encoded)
	}
	url := "http://localhost:" + strconv.Itoa(port) + baseURL + path
	result, err := a.e.Runtime.ExecInput(service, app.CurlInput(headers, data), "curl", "-sS", "-X", method, "-w", "\n%{http_code}", "--config", "-", url)
	if err != nil {
		return 0, "", err
	}
	answer, code := "", strings.TrimRight(result.Stdout, "\n")
	if last := strings.LastIndex(code, "\n"); last >= 0 {
		answer, code = code[:last], code[last+1:]
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
