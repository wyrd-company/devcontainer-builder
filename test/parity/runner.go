// relationships:
//
//	references: README
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
const discoveryLabel = "devcontainer.parity.fixture-side"

type Fixture struct {
	Name, Path, Config string
	Markers, Effects   []string
	// UpArgs are passed to both sides' up after the workspace.
	UpArgs []string
	// Source is the repository directory that holds Path. Empty means
	// reference/cli.
	Source string
}

// contextMarkers read what a Dockerfile copied from its build context.
var contextMarkers = []string{"/context-files.txt", "/context/kept.txt"}

var fixtures = []Fixture{
	{"image", "src/test/configs/image", ".devcontainer.json", []string{"/postCreateCommand.txt"}, nil, nil, ""},
	{"dockerfile", "src/test/configs/dockerfile-without-features", ".devcontainer.json", []string{"/postCreateCommand.txt"}, nil, nil, ""},
	{"features", "src/test/container-features/configs/dockerfile-with-v2-local-features-config-inside-dev-container-folder", ".devcontainer/devcontainer.json", nil, []string{"/usr/local/bin/hello", "/usr/local/bin/color"}, nil, ""},
	{"compose", "src/test/configs/compose-image-without-features-minimal", ".devcontainer/devcontainer.json", []string{"/postCreateCommand.txt"}, nil, nil, ""},
	{"remote-user", "src/test/configs/image-with-git-feature", ".devcontainer.json", nil, nil, nil, ""},
	{"additional-features", "src/test/configs/image", ".devcontainer.json", []string{"/postCreateCommand.txt"}, nil, []string{"--additional-features", `{"ghcr.io/devcontainers/features/common-utils:2":{}}`}, ""},
	{"dockerfile-context-config-dir", "dockerfile-context/config-dir", ".devcontainer/devcontainer.json", contextMarkers, nil, nil, "test/parity/fixtures"},
	{"dockerfile-context-parent-dir", "dockerfile-context/parent-dir", ".devcontainer/devcontainer.json", contextMarkers, nil, nil, "test/parity/fixtures"},
	{"dockerfile-context-subdir", "dockerfile-context/subdir", ".devcontainer/devcontainer.json", contextMarkers, nil, nil, "test/parity/fixtures"},
	// Local features whose containerEnv reads the base PATH, another
	// feature, earlier keys of its own, an unset name and a default, and one
	// whose array-index keys apply first. The markers record what their
	// install steps saw.
	{"feature-containerenv", "feature-containerenv", ".devcontainer/devcontainer.json", []string{"/sample-install-env.txt", "/sample-index-env.txt"}, nil, nil, "test/parity/fixtures"},
}

// composeLabelsFile is appended to a Compose configuration's files, so that
// the primary service carries the harness labels; Compose ignores runArgs.
const composeLabelsFile = "parity-labels.yml"

type Runner struct {
	root, temp, runID string
	env               []string
	log               *os.File
	derived           map[string]bool // image names derived from this run's workspaces
}

// dcbImage is the image name dcb up gives a derived image without
// --image-name (defaultImage in pkg/cli). The workspace path is unique to the run.
func dcbImage(workspace string) string {
	id := sha256.Sum256([]byte(workspace))
	return fmt.Sprintf("dcb-%x:latest", id[:8])
}

func (r *Runner) label() string { return ownershipLabel + "=" + r.runID }

// sourceDir is the fixture directory relative to the repository root.
func (f Fixture) sourceDir() string {
	if f.Source == "" {
		return filepath.Join("reference/cli", f.Path)
	}
	return filepath.Join(f.Source, f.Path)
}

func fixtureSide(f Fixture, side string) string { return f.Name + "/" + side }

// workspaceName is unique to the run, fixture and side, so that names derived
// from the folder (Compose projects, upstream image names) never collide.
func (r *Runner) workspaceName(f Fixture, side string) string {
	return r.runID + "-" + f.Name + "-" + side
}

func discovery(f Fixture, side string) string {
	return discoveryLabel + "=" + fixtureSide(f, side)
}

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
	workspace := filepath.Join(r.temp, f.Name, side, r.workspaceName(f, side))
	source := filepath.Join(r.root, f.sourceDir())
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
	if files, ok := config["dockerComposeFile"]; ok {
		if err := r.labelCompose(config, files, filepath.Dir(path), f, side); err != nil {
			return "", err
		}
	} else {
		args, _ := config["runArgs"].([]any)
		config["runArgs"] = append(args, "--label", r.label(), "--label", discovery(f, side))
	}
	if build, ok := config["build"].(map[string]any); ok && side == "upstream" {
		options, _ := build["options"].([]any)
		build["options"] = append(options, "--label", r.label())
	}
	body, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	if r.derived == nil {
		r.derived = map[string]bool{}
	}
	r.derived[dcbImage(workspace)] = true
	return workspace, os.WriteFile(path, body, 0644)
}

func (r *Runner) labelCompose(config map[string]any, files any, dir string, f Fixture, side string) error {
	list, ok := files.([]any)
	if !ok {
		list = []any{files}
	}
	config["dockerComposeFile"] = append(list, composeLabelsFile)
	service, _ := config["service"].(string)
	body, err := json.Marshal(map[string]any{"services": map[string]any{service: map[string]any{
		"labels": []string{r.label(), discovery(f, side)},
	}}})
	if err != nil {
		return err
	}
	// JSON is YAML, so Compose reads the file without a YAML encoder.
	return os.WriteFile(filepath.Join(dir, composeLabelsFile), body, 0644)
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
	// Keep copied fixtures isolated from the enclosing checkout on both sides.
	args := append([]string{"--mount-workspace-git-root=false"}, f.UpArgs...)
	body, upErr := a.Up(ctx, r, workspace, args)
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
	ids, err := r.command(ctx, "docker", "ps", "-aq", "--no-trunc", "--filter", "label="+r.label(), "--filter", "label="+discovery(f, side))
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
	if c.ID != id || c.Config.Labels[ownershipLabel] != r.runID || c.Config.Labels[discoveryLabel] != fixtureSide(f, side) {
		return out
	}
	n := Normalizer{map[string]string{workspace: "<workspace>", filepath.Base(workspace): "<workspace-name>", r.temp: "<temp>", id: "<container>", id[:12]: "<container>", c.Image: "<image>", c.Config.Hostname: "<hostname>"}}
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
		if k != ownershipLabel && k != discoveryLabel {
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
	user := Observation{Info: map[string]string{"rawUser": c.Config.User}}
	uid, uidErr := r.command(ctx, "docker", "exec", id, "id", "-u")
	gid, gidErr := r.command(ctx, "docker", "exec", id, "id", "-g")
	switch {
	case uidErr != nil:
		user.Error = uidErr.Error()
	case gidErr != nil:
		user.Error = gidErr.Error()
	default:
		user.Value = map[string]string{"uid": strings.TrimSpace(string(uid)), "gid": strings.TrimSpace(string(gid))}
	}
	out["user"] = user
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
	return append(errors, r.cleanupNamed(ctx)...)
}

const composeProjectLabel = "com.docker.compose.project"

// composeKinds names each Compose resource's identity field in list and
// inspect templates; networks call it ID in one and Id in the other.
var composeKinds = []struct{ kind, list, inspect string }{
	{"network", "{{.ID}}", "{{.Id}}"},
	{"volume", "{{.Name}}", "{{.Name}}"},
}

// ownsName reports whether a name embeds this run's workspace name prefix.
// Upstream derived image names and Compose project names carry the folder
// name; neither CLI can add the run label to them.
func (r *Runner) ownsName(name string) bool {
	i := strings.Index(name, r.runID+"-")
	if i < 0 {
		return false
	}
	if i == 0 {
		return true
	}
	c := name[i-1]
	return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9')
}

// cleanupNamed removes derived image tags, then Compose networks and volumes
// whose project carries this run's workspace name. Images are removed by tag
// without force, so an image shared with another tag stays.
func (r *Runner) cleanupNamed(ctx context.Context) []string {
	var errors []string
	body, err := r.command(ctx, "docker", "image", "ls", "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		errors = append(errors, err.Error())
	}
	for _, ref := range strings.Fields(string(body)) {
		if r.ownsName(ref) || r.derived[ref] {
			if _, err := r.command(ctx, "docker", "image", "rm", ref); err != nil {
				errors = append(errors, err.Error())
			}
		}
	}
	project := " {{.Label \"" + composeProjectLabel + "\"}}"
	for _, k := range composeKinds {
		body, err := r.command(ctx, "docker", k.kind, "ls", "--filter", "label="+composeProjectLabel, "--format", k.list+project)
		if err != nil {
			errors = append(errors, err.Error())
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || !r.ownsName(fields[1]) {
				continue
			}
			data, err := r.command(ctx, "docker", k.kind, "inspect", fields[0], "--format", k.inspect+" {{index .Labels \""+composeProjectLabel+"\"}}")
			if err != nil {
				errors = append(errors, err.Error())
				continue
			}
			identity := strings.Fields(string(data))
			if len(identity) != 2 || !strings.HasPrefix(identity[0], fields[0]) || !r.ownsName(identity[1]) {
				errors = append(errors, "ownership verification failed for "+k.kind+" "+fields[0])
				continue
			}
			if _, err := r.command(ctx, "docker", k.kind, "rm", identity[0]); err != nil {
				errors = append(errors, err.Error())
			}
		}
	}
	return errors
}
