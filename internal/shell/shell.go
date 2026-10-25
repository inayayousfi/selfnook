// Package shell runs external programs such as docker, tailscale, and systemctl.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Cmd describes one program run.
type Cmd struct {
	Args []string
	// Capture collects output into the Result instead of showing it.
	Capture bool
	// Terminal gives the program the user's terminal, for commands such as
	// sudo that ask for a password.
	Terminal bool
	// Check turns a non-zero exit code into an error.
	Check bool
	// Env adds environment variables to the program's own environment,
	// replacing variables of the same name.
	Env map[string]string
	// Stdin is the program's input, which keeps secrets out of its
	// arguments, where any account on the computer can read them.
	Stdin string
}

type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// Shell runs commands. Tests replace it to record calls.
type Shell interface {
	Run(Cmd) (Result, error)
}

// ExitError reports a checked command that failed.
type ExitError struct {
	Args []string
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("Command '%s' returned non-zero exit status %d.", strings.Join(e.Args, " "), e.Code)
}

// OS runs commands as real processes in Dir.
type OS struct {
	Context context.Context
	Dir     string
	// Output receives the output of commands that neither capture it nor use the terminal.
	Output io.Writer
	// TakeTerminal wraps a Terminal command, so a full-screen interface can
	// hand over the terminal and take it back. Nil runs the command directly.
	TakeTerminal func(run func() error) error
}

func (s *OS) Run(c Cmd) (Result, error) {
	ctx := s.Context
	if ctx == nil {
		ctx = context.Background()
	}
	program := c.Args[0]
	if path, ok := Found(program); ok {
		program = path
	}
	command := exec.CommandContext(ctx, program, c.Args[1:]...)
	command.Dir = s.Dir
	if len(c.Env) > 0 {
		command.Env = os.Environ()
		for key, value := range c.Env {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	if c.Stdin != "" {
		command.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr bytes.Buffer
	run := command.Run
	switch {
	case c.Capture:
		command.Stdout, command.Stderr = &stdout, &stderr
	case c.Terminal:
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		if s.TakeTerminal != nil {
			run = func() error { return s.TakeTerminal(command.Run) }
		}
	default:
		output := s.Output
		if output == nil {
			output = os.Stdout
		}
		command.Stdout, command.Stderr = output, output
	}
	err := run()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.Code = exit.ExitCode()
		if c.Check {
			return result, &ExitError{Args: c.Args, Code: result.Code}
		}
		return result, nil
	}
	return result, err
}

// Found reports whether name is on the search path, returning its full path.
// When Go's own search fails, it also splits the search path at every
// separator and drops double quotes, as PowerShell does. Go reads a Windows
// search path's quotes as grouping, so one stray quote would otherwise hide
// every folder after it.
func Found(name string) (string, bool) {
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}
	if filepath.Base(name) != name {
		return "", false
	}
	for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if dir = strings.ReplaceAll(dir, `"`, ""); dir == "" {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return path, true
		}
	}
	return "", false
}

// Capture runs args, collecting output without checking the exit code.
func Capture(s Shell, args ...string) (Result, error) {
	return s.Run(Cmd{Args: args, Capture: true})
}
