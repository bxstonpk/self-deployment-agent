package runtimeengine

import (
	"context"
	"fmt"
	"io"
)

// SaveImages writes the images as one `docker save` archive, which
// `docker load` reads back with their tags, layers and configuration
// (command, environment, ports) — unlike `docker export` of a container,
// which keeps only its filesystem. For IT handoff packages (DEC-033).
func (r *DockerRuntime) SaveImages(ctx context.Context, refs []string, w io.Writer) error {
	rc, err := r.cli.ImageSave(ctx, refs)
	if err != nil {
		return fmt.Errorf("save images: %w", err)
	}
	defer rc.Close()
	if _, err := io.Copy(w, rc); err != nil {
		return fmt.Errorf("save images: %w", err)
	}
	return nil
}
