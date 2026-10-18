// relationships:
//
//	references: README
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// Adapter owns CLI-specific arguments and JSON extraction. Observation,
// scoring, fixture preparation and teardown are shared.
type Adapter interface {
	Name() string
	Up(context.Context, *Runner, string) ([]byte, error)
}

type DCBAdapter struct{ Binary string }

func (DCBAdapter) Name() string { return "dcb (native BuildKit, JSON result)" }

// Up passes only the options upstream up receives. dcb up builds a derived
// image for --image-name or --label even when the configuration needs none.
func (a DCBAdapter) Up(ctx context.Context, r *Runner, workspace string) ([]byte, error) {
	body, err := r.command(ctx, a.Binary, "up", "--workspace-folder", workspace)
	if err != nil {
		return body, err
	}
	var result struct {
		Outcome string `json:"outcome"`
	}
	err = json.Unmarshal(body, &result)
	if bytes.Count(body, []byte("\n")) != 1 || err != nil {
		return body, fmt.Errorf("dcb up must emit exactly one JSON result line")
	}
	if result.Outcome != "success" {
		return body, fmt.Errorf("dcb up reported outcome %q", result.Outcome)
	}
	return body, nil
}

type UpstreamAdapter struct{ Version string }

func (a UpstreamAdapter) Name() string { return "@devcontainers/cli@" + a.Version }
func (a UpstreamAdapter) Up(ctx context.Context, r *Runner, workspace string) ([]byte, error) {
	return r.command(ctx, "npx", "--yes", "@devcontainers/cli@"+a.Version, "up", "--workspace-folder", workspace, "--mount-workspace-git-root", "false")
}

func buildAdapter(ctx context.Context, r *Runner) (Adapter, error) {
	a := DCBAdapter{Binary: filepath.Join(r.temp, "dcb")}
	if _, err := r.command(ctx, "go", "build", "-o", a.Binary, "./cmd/dcb"); err != nil {
		return nil, err
	}
	return a, nil
}
