// Package engine builds agent images with Docker or Podman on the developer's
// machine. We publish Dockerfiles, not agent binaries.
package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

type Builder struct {
	Bin string
	Out io.Writer
}

// Detect picks docker, then podman. Which one the gateway's compute driver
// uses is not matched yet; the first one on PATH wins.
func Detect(out io.Writer) (Builder, error) {
	for _, bin := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(bin); err == nil {
			return Builder{Bin: bin, Out: out}, nil
		}
	}
	return Builder{}, errors.New("neither docker nor podman is installed; one is needed to build agent images")
}

func (b Builder) Exists(ctx context.Context, tag string) bool {
	return exec.CommandContext(ctx, b.Bin, "image", "inspect", tag).Run() == nil
}

// Build builds from a Dockerfile on stdin, with no build context. Our
// Dockerfiles copy nothing from the host.
func (b Builder) Build(ctx context.Context, tag string, dockerfile []byte) error {
	cmd := exec.CommandContext(ctx, b.Bin, "build", "-t", tag, "-")
	cmd.Stdin = bytes.NewReader(dockerfile)
	cmd.Stdout = b.Out
	cmd.Stderr = b.Out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build %s: %w", b.Bin, tag, err)
	}
	return nil
}
