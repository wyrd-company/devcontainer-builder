package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DevContainer is a partial model of the devcontainer.json schema. It covers
// the fields needed to drive a buildx-backed image build: base image, build
// context, features, lifecycle hooks, user, and customizations relevant to
// the VS Code remote experience.
//
// The full spec is much larger; unknown fields are preserved through Raw so
// callers can pass data through to downstream tooling untouched.
type DevContainer struct {
	Name            string         `json:"name,omitempty"`
	Image           string         `json:"image,omitempty"`
	Dockerfile      string         `json:"dockerFile,omitempty"`
	Build           *Build         `json:"build,omitempty"`
	Features        OrderedMap     `json:"features,omitempty"`
	OverrideFeature []string       `json:"overrideFeatureInstallOrder,omitempty"`
	RemoteUser      string         `json:"remoteUser,omitempty"`
	ContainerUser   string         `json:"containerUser,omitempty"`
	UpdateRemoteEnv map[string]any `json:"remoteEnv,omitempty"`
	ContainerEnv    map[string]any `json:"containerEnv,omitempty"`
	WorkspaceFolder string         `json:"workspaceFolder,omitempty"`
	WorkspaceMount  string         `json:"workspaceMount,omitempty"`
	Mounts          []any          `json:"mounts,omitempty"`
	ForwardPorts    []any          `json:"forwardPorts,omitempty"`
	OnCreate        Command        `json:"onCreateCommand,omitempty"`
	UpdateContent   Command        `json:"updateContentCommand,omitempty"`
	PostCreate      Command        `json:"postCreateCommand,omitempty"`
	PostStart       Command        `json:"postStartCommand,omitempty"`
	PostAttach      Command        `json:"postAttachCommand,omitempty"`
	Init            *bool          `json:"init,omitempty"`
	Privileged      *bool          `json:"privileged,omitempty"`
	CapAdd          []string       `json:"capAdd,omitempty"`
	SecurityOpt     []string       `json:"securityOpt,omitempty"`
	RunArgs         []string       `json:"runArgs,omitempty"`
	Customizations  map[string]any `json:"customizations,omitempty"`

	// Path is the on-disk location the spec was loaded from. Not part of
	// the JSON document itself.
	Path string `json:"-"`
	// Raw retains the original (jsonc-stripped) bytes for round-trip needs.
	Raw json.RawMessage `json:"-"`
}

// Build models the inline build object inside devcontainer.json.
type Build struct {
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Target     string            `json:"target,omitempty"`
	CacheFrom  StringOrSlice     `json:"cacheFrom,omitempty"`
	Options    []string          `json:"options,omitempty"`
}

// Command models the three forms a devcontainer lifecycle command can
// take in devcontainer.json:
//
//   - String form ("npm install") — runs in a shell.
//   - Array form (["npm", "install"]) — exec'd directly without a shell.
//   - Object form ({"server": "npm start", "watch": ["npm", "watch"]})
//     — multiple named commands run in parallel, each interpreted as
//     string or array per the same rules.
//
// Exactly zero or one of Shell, Args, Parallel will be non-empty for a
// well-formed value.
type Command struct {
	Shell    string             // string form
	Args     []string           // array form
	Parallel map[string]Command // object form; each value is itself a Command
}

// IsZero reports whether c carries no command at all.
func (c Command) IsZero() bool {
	return c.Shell == "" && len(c.Args) == 0 && len(c.Parallel) == 0
}

// UnmarshalJSON accepts string / []string / object-of-(string|[]string).
func (c *Command) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch data[0] {
	case '"':
		return json.Unmarshal(data, &c.Shell)
	case '[':
		return json.Unmarshal(data, &c.Args)
	case '{':
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		c.Parallel = make(map[string]Command, len(m))
		for k, raw := range m {
			var sub Command
			if err := sub.UnmarshalJSON(raw); err != nil {
				return fmt.Errorf("parallel command %q: %w", k, err)
			}
			c.Parallel[k] = sub
		}
		return nil
	}
	return fmt.Errorf("unsupported Command value: %s", string(data))
}

// StringOrSlice accepts either a single string or a list of strings, which
// the devcontainer spec allows for command fields. Retained for fields
// that don't need the parallel-object form.
type StringOrSlice []string

func (s *StringOrSlice) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*s = []string{v}
		return nil
	}
	if data[0] == '[' {
		var v []string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*s = v
		return nil
	}
	// Map form: {"name": "command"} — pick values in iteration order. We
	// preserve the values only since names are advisory.
	if data[0] == '{' {
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		for _, v := range m {
			switch t := v.(type) {
			case string:
				*s = append(*s, t)
			case []any:
				for _, e := range t {
					if str, ok := e.(string); ok {
						*s = append(*s, str)
					}
				}
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported StringOrSlice value: %s", string(data))
}

// OrderedMap preserves insertion order while still decoding JSON object
// payloads. It's used for `features`, where install order can matter.
type OrderedMap struct {
	Keys   []string
	Values map[string]json.RawMessage
}

func (m *OrderedMap) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytesReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("expected object, got %v", tok)
	}
	m.Values = map[string]json.RawMessage{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("non-string object key %v", keyTok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		m.Keys = append(m.Keys, key)
		m.Values[key] = raw
	}
	return nil
}

func (m OrderedMap) Len() int { return len(m.Keys) }

// Load reads and parses a devcontainer.json file (with JSONC support).
func Load(path string) (*DevContainer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	stripped := StripJSONC(data)
	var dc DevContainer
	if err := json.Unmarshal(stripped, &dc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	dc.Path = path
	dc.Raw = stripped
	return &dc, nil
}

// LoadFromWorkspace finds and loads the conventional devcontainer.json from
// a workspace directory. It checks .devcontainer/devcontainer.json first,
// then .devcontainer.json.
func LoadFromWorkspace(workspace string) (*DevContainer, error) {
	candidates := []string{
		filepath.Join(workspace, ".devcontainer", "devcontainer.json"),
		filepath.Join(workspace, ".devcontainer.json"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return Load(p)
		}
	}
	return nil, fmt.Errorf("no devcontainer.json found in %s (looked for %v)", workspace, candidates)
}

// BuildContextDir resolves the build context directory relative to the
// devcontainer.json location. If the spec uses an `image`, the empty string
// is returned with no error.
func (d *DevContainer) BuildContextDir() (string, error) {
	specDir := filepath.Dir(d.Path)
	switch {
	case d.Build != nil && d.Build.Context != "":
		if filepath.IsAbs(d.Build.Context) {
			return filepath.Clean(d.Build.Context), nil
		}
		return filepath.Clean(filepath.Join(specDir, d.Build.Context)), nil
	case d.Build != nil && d.Build.Dockerfile != "":
		return specDir, nil
	case d.Dockerfile != "":
		return specDir, nil
	case d.Image != "":
		return "", nil
	}
	return "", fmt.Errorf("spec has no image, build, or dockerFile field")
}

// DockerfilePath returns the Dockerfile path indicated by the spec, if any.
func (d *DevContainer) DockerfilePath() string {
	if d.Build != nil && d.Build.Dockerfile != "" {
		return d.Build.Dockerfile
	}
	return d.Dockerfile
}

// BaseImage returns the directly-specified image, if any.
func (d *DevContainer) BaseImage() string { return d.Image }
