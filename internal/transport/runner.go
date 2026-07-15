package transport

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes a command with an optional working directory.
// Tests inject fakes; production uses DefaultRunner.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

// FuncRunner adapts a function to Runner.
type FuncRunner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// Run implements Runner.
func (f FuncRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	return f(ctx, dir, name, args...)
}

// DefaultRunner runs commands with exec.CommandContext.
var DefaultRunner Runner = FuncRunner(func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return out, nil
})
