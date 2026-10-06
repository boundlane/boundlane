// Package openshell is the only package that executes the openshell and
// openshell-prover binaries. It builds argument lists and returns output; it
// does not interpret OpenShell's JSON beyond what the upstream docs describe.
package openshell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner runs one binary. Tests replace it with a fake that records calls.
type Runner interface {
	// Output runs the command and returns its stdout. A non-zero exit returns
	// stdout together with an *Error.
	Output(ctx context.Context, args ...string) ([]byte, error)
	// Attach runs the command with the terminal attached.
	Attach(ctx context.Context, args ...string) error
}

// EnvRunner can add environment variables for one command.
type EnvRunner interface {
	OutputEnv(ctx context.Context, env []string, args ...string) ([]byte, error)
}

// Error is a command that exited non-zero.
type Error struct {
	Bin      string
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.ExitCode)
	}
	return fmt.Sprintf("%s %s: %s", e.Bin, strings.Join(e.Args, " "), msg)
}

// ExitCode returns the exit code of a failed command, or -1.
func ExitCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.ExitCode
	}
	return -1
}

// Exec runs a binary from PATH.
type Exec struct {
	Bin    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (x Exec) Output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, x.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), x.wrap(args, err, stderr.String())
}

// OutputEnv is Output with extra KEY=VALUE pairs in the child's environment
// only. Secrets go this way so they never appear in a command line.
func (x Exec) OutputEnv(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, x.Bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), x.wrap(args, err, stderr.String())
}

func (x Exec) Attach(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, x.Bin, args...)
	cmd.Stdin = or[io.Reader](x.Stdin, os.Stdin)
	cmd.Stdout = or[io.Writer](x.Stdout, os.Stdout)
	cmd.Stderr = or[io.Writer](x.Stderr, os.Stderr)
	return x.wrap(args, cmd.Run(), "")
}

func (x Exec) wrap(args []string, err error, stderr string) error {
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &Error{Bin: x.Bin, Args: args, ExitCode: ee.ExitCode(), Stderr: stderr}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s is not installed or not on PATH: %w", x.Bin, err)
	}
	return fmt.Errorf("%s: %w", x.Bin, err)
}

func or[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}

// Installed reports whether bin is on PATH.
func Installed(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
