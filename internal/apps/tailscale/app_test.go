package tailscale

import (
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/runtime"
	"github.com/inayayousfi/selfnook/internal/shell"
	"github.com/inayayousfi/selfnook/internal/state"
)

func TestStatusReportsAnOfflineExitNode(t *testing.T) {
	values := app.ValuesFor(state.New(), App)
	values.Set(exitNode, "exit-1")
	sh := &fake.Shell{Respond: func(args []string) shell.Result {
		if args[len(args)-1] == "{{.State.Status}}" {
			return shell.Result{Stdout: "running\n"}
		}
		return shell.Result{Stdout: `{"ExitNodeStatus": {"Online": false}}`}
	}}
	r := runtime.Docker.New(runtime.Options{Shell: sh, Env: func() map[string]string { return nil }})
	text, err := Status(&app.Env{Values: values, Runtime: r})
	if err != nil || text != "Tailscale exit node: not online\nSelected exit node:\nexit-1" {
		t.Errorf("text = %q, err = %v", text, err)
	}
}
