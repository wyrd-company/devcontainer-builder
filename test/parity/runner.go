// relationships:
//
//	references: README
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

const ownershipLabel = "devcontainer.parity.run"

type Fixture struct {
	Name, Path, Config string
	Markers, Effects   []string
}

var fixtures = []Fixture{
	{"image", "src/test/configs/image", ".devcontainer.json", []string{"/postCreateCommand.txt"}, nil},
	{"dockerfile", "src/test/configs/dockerfile-without-features", ".devcontainer.json", []string{"/postCreateCommand.txt"}, nil},
	{"features", "src/test/container-features/configs/dockerfile-with-v2-local-features-config-inside-dev-container-folder", ".devcontainer/devcontainer.json", nil, []string{"/usr/local/bin/hello", "/usr/local/bin/color"}},
}

type Runner struct {
	root, temp, runID string
	env               []string
	log               *os.File
}

func (r *Runner) label() string { return ownershipLabel + "=" + r.runID }

func isolatedEnv(config string) []string {
	var out []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		// DOCKER_AUTH_CONFIG is consumed by some Docker/build integrations.
		if key != "DOCKER_CONFIG" && key != "DOCKER_AUTH_CONFIG" {
			out = append(out, value)
		}
	}
	return append(out, "DOCKER_CONFIG="+config)
}
func newRunner(root string, log *os.File) (*Runner, error) {
	temp, err := os.MkdirTemp(filepath.Join(root, ".tmp"), "parity-")
	if err != nil {
		return nil, err
	}
	config := filepath.Join(temp, "docker")
	if err := os.Mkdir(config, 0700); err != nil {
		os.RemoveAll(temp)
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(config, "config.json"), []byte("{}\n"), 0600); err != nil {
		os.RemoveAll(temp)
		return nil, err
	}
	return &Runner{root: root, temp: temp, runID: filepath.Base(temp), env: isolatedEnv(config), log: log}, nil
}
func (r *Runner) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	fmt.Fprintf(r.log, "\n$ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = r.root
	cmd.Env = r.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	r.log.Write(stdout.Bytes())
	r.log.Write(stderr.Bytes())
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w (see run.log)", name, err)
	}
	return stdout.Bytes(), nil
}

func (r *Runner) prepare(f Fixture, side string) (string, error) {
	workspace := filepath.Join(r.temp, f.Name, side, "workspace")
	source := filepath.Join(r.root, "reference/cli", f.Path)
	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(workspace, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture symlink unsupported: %s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, body, info.Mode().Perm())
	})
	if err != nil {
		return "", err
	}
	path := filepath.Join(workspace, f.Config)
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var config map[string]any
	if err := json.Unmarshal(spec.StripJSONC(body), &config); err != nil {
		return "", err
	}
	args, _ := config["runArgs"].([]any)
	config["runArgs"] = append(args, "--label", r.label())
	if build, ok := config["build"].(map[string]any); ok {
		options, _ := build["options"].([]any)
		build["options"] = append(options, "--label", r.label())
	}
	body, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return workspace, os.WriteFile(path, body, 0644)
}

func failedObservations(err error) map[string]Observation {
	out := map[string]Observation{}
	for _, d := range dimensions {
		out[d] = Observation{Error: err.Error()}
	}
	return out
}

func resultJSON(body []byte) (any, error) {
	lines := bytes.Split(bytes.TrimSpace(body), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		v, err := jsonValue(lines[i])
		if err == nil {
			if _, ok := v.(map[string]any); ok {
				return v, nil
			}
		}
	}
	return nil, fmt.Errorf("CLI emitted no JSON object")
}

func (r *Runner) observe(ctx context.Context, a Adapter, f Fixture, side string) map[string]Observation {
	workspace, err := r.prepare(f, side)
	if err != nil {
		return failedObservations(err)
	}
	tag := "parity-" + r.runID + "-" + f.Name + "-" + side + ":latest"
	body, upErr := a.Up(ctx, r, workspace, tag)
	out := failedObservations(fmt.Errorf("container observation unavailable"))
	v, jsonErr := resultJSON(body)
	if upErr != nil {
		out["result"] = Observation{Error: upErr.Error()}
	} else if jsonErr != nil {
		out["result"] = Observation{Error: jsonErr.Error()}
	} else {
		out["result"] = Observation{Value: v}
	}
	// Discovery also works after a lifecycle failure, when the CLI exits after
	// creating the container but before returning its JSON success result.
	ids, err := r.command(ctx, "docker", "ps", "-aq", "--no-trunc", "--filter", "label="+r.label(), "--filter", "label=devcontainer.local_folder="+workspace)
	if err != nil {
		return out
	}
	fields := strings.Fields(string(ids))
	if len(fields) != 1 {
		return out
	}
	id := fields[0]
	inspect, err := r.command(ctx, "docker", "inspect", id)
	if err != nil {
		return out
	}
	var containers []struct {
		ID     string `json:"Id"`
		Image  string
		Config struct {
			Env                        []string
			Labels                     map[string]string
			User, WorkingDir, Hostname string
		}
		Mounts []struct {
			Type, Source, Destination, Mode string
			RW                              bool
		}
	}
	if err := json.Unmarshal(inspect, &containers); err != nil || len(containers) != 1 {
		return out
	}
	c := containers[0]
	n := Normalizer{map[string]string{workspace: "<workspace>", r.temp: "<temp>", id: "<container>", id[:12]: "<container>", c.Image: "<image>", c.Config.Hostname: "<hostname>", tag: "<image-tag>"}}
	if out["result"].Error == "" {
		out["result"] = Observation{Value: n.Result(out["result"].Value)}
	}
	env := map[string]any{}
	for _, entry := range c.Config.Env {
		k, v, _ := strings.Cut(entry, "=")
		env[k] = v
	}
	out["env"] = Observation{Value: n.Value(env)}
	labels := map[string]any{}
	for k, v := range c.Config.Labels {
		if k != ownershipLabel {
			if k == "devcontainer.metadata" {
				if parsed, e := jsonValue([]byte(v)); e == nil {
					labels[k] = parsed
					continue
				}
			}
			labels[k] = v
		}
	}
	out["labels"] = Observation{Value: n.Value(labels)}
	mounts := []any{}
	for _, m := range c.Mounts {
		mounts = append(mounts, n.Value(map[string]any{"type": m.Type, "source": m.Source, "destination": m.Destination, "mode": m.Mode, "rw": m.RW}))
	}
	out["mounts"] = Observation{Value: unordered(mounts)}
	out["user"] = Observation{Value: c.Config.User}
	out["workdir"] = Observation{Value: n.Value(c.Config.WorkingDir)}
	image, err := r.command(ctx, "docker", "image", "inspect", c.Image, "--format", "{{json .Config.Labels}}")
	if err == nil {
		var labels map[string]string
		err = json.Unmarshal(image, &labels)
		if err == nil {
			var metadata any
			if raw := labels["devcontainer.metadata"]; raw != "" {
				metadata, err = jsonValue([]byte(raw))
			}
			if err == nil {
				out["metadata"] = Observation{Value: n.Value(metadata)}
			}
		}
	}
	for dim, paths := range map[string][]string{"lifecycle": f.Markers, "features": f.Effects} {
		values := map[string]any{}
		var probeErr error
		for _, path := range paths {
			data, e := r.command(ctx, "docker", "exec", id, "cat", path)
			if e != nil {
				probeErr = e
				break
			}
			values[path] = n.Value(string(data))
			if dim == "features" {
				data, e = r.command(ctx, "docker", "exec", id, "sh", "-c", "test -x \"$1\" && \"$1\"", "sh", path)
				if e != nil {
					probeErr = e
					break
				}
				values[path+":output"] = n.Value(string(data))
			}
		}
		if probeErr != nil {
			out[dim] = Observation{Error: probeErr.Error()}
		} else {
			out[dim] = Observation{Value: values}
		}
	}
	return out
}

// Cleanup discovers by label and verifies the full identity and ownership again
// immediately before deletion. Shared pulled bases and build cache are retained.
func (r *Runner) cleanup(ctx context.Context) []string {
	var errors []string
	for _, kind := range []string{"container", "image"} {
		args := []string{"ps", "-aq", "--no-trunc", "--filter", "label=" + r.label()}
		if kind == "image" {
			args = []string{"image", "ls", "-q", "--no-trunc", "--filter", "label=" + r.label()}
		}
		body, err := r.command(ctx, "docker", args...)
		if err != nil {
			errors = append(errors, err.Error())
			continue
		}
		seen := map[string]bool{}
		for _, id := range strings.Fields(string(body)) {
			if seen[id] {
				continue
			}
			seen[id] = true
			data, err := r.command(ctx, "docker", kind, "inspect", id, "--format", "{{.Id}} {{index .Config.Labels \""+ownershipLabel+"\"}}")
			if err != nil {
				errors = append(errors, err.Error())
				continue
			}
			identity := strings.Fields(string(data))
			if len(identity) != 2 || identity[0] != id || identity[1] != r.runID {
				errors = append(errors, "ownership verification failed for "+id)
				continue
			}
			args := []string{"rm", "-f", id}
			if kind == "image" {
				args = []string{"image", "rm", "-f", id}
			}
			if _, err := r.command(ctx, "docker", args...); err != nil {
				errors = append(errors, err.Error())
			}
		}
	}
	return errors
}
