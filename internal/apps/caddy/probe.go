package caddy

import (
	"os"
	"strconv"
	"strings"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/shell"
)

// servedPorts are the ports Caddy serves and publishes: 80, 443 unless the
// pages use plain HTTP, and the own port of each route. compose.yaml
// publishes the same ports.
func servedPorts(v app.Values, routes []app.Route) []string {
	ports := []string{"80"}
	if v.Get(modeKey) != Local {
		ports = append(ports, "443")
	}
	for _, port := range app.OwnPorts(routes) {
		ports = append(ports, strconv.Itoa(port))
	}
	return ports
}

// probe sends plain HTTP to each served port on this computer. Caddy answers
// every port, its HTTPS ports with 400 Bad Request, without needing a
// certificate. A port that gives no answer at all is forwarded nowhere or to
// another container.
func probe(e *app.Env) (string, error) {
	var silent []string
	for _, port := range servedPorts(e.Values, e.Routes) {
		result, err := e.Shell.Run(shell.Cmd{Args: []string{
			"curl", "--silent", "--output", os.DevNull, "--write-out", "%{http_code}", "--max-time", "10",
			"http://127.0.0.1:" + port + "/",
		}, Capture: true})
		if err != nil {
			return "", err
		}
		if code := strings.TrimSpace(result.Stdout); code == "" || code == "000" {
			silent = append(silent, port)
		}
	}
	if len(silent) == 0 {
		return "", nil
	}
	label := "port "
	if len(silent) > 1 {
		label = "ports "
	}
	return "Caddy does not answer on " + label + app.List(silent) + " of this computer.", nil
}
