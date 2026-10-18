// relationships:
//   references: [spec, features]

// Package metadata reads and writes the devcontainer.metadata image label and
// merges its ordered entries using the upstream devcontainers CLI rules.
package metadata

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Label is the interoperable image and container label name.
const Label = "devcontainer.metadata"

// Entry is one base-image, feature, or devcontainer.json contribution.
// Feature IDs identify the original user feature reference, not its short ID.
type Entry struct {
	ID                   string         `json:"id,omitempty"`
	Init                 *bool          `json:"init,omitempty"`
	Privileged           *bool          `json:"privileged,omitempty"`
	CapAdd               []string       `json:"capAdd,omitempty"`
	SecurityOpt          []string       `json:"securityOpt,omitempty"`
	Entrypoint           string         `json:"entrypoint,omitempty"`
	Mounts               []any          `json:"mounts,omitempty"`
	Customizations       map[string]any `json:"customizations,omitempty"`
	OnCreate             spec.Command   `json:"onCreateCommand,omitzero"`
	UpdateContent        spec.Command   `json:"updateContentCommand,omitzero"`
	PostCreate           spec.Command   `json:"postCreateCommand,omitzero"`
	PostStart            spec.Command   `json:"postStartCommand,omitzero"`
	PostAttach           spec.Command   `json:"postAttachCommand,omitzero"`
	WaitFor              string         `json:"waitFor,omitempty"`
	RemoteUser           string         `json:"remoteUser,omitempty"`
	ContainerUser        string         `json:"containerUser,omitempty"`
	UserEnvProbe         string         `json:"userEnvProbe,omitempty"`
	RemoteEnv            map[string]any `json:"remoteEnv,omitempty"`
	ContainerEnv         map[string]any `json:"containerEnv,omitempty"`
	OverrideCommand      *bool          `json:"overrideCommand,omitempty"`
	PortsAttributes      map[string]any `json:"portsAttributes,omitempty"`
	OtherPortsAttributes map[string]any `json:"otherPortsAttributes,omitempty"`
	ForwardPorts         []any          `json:"forwardPorts,omitempty"`
	ShutdownAction       string         `json:"shutdownAction,omitempty"`
	UpdateRemoteUserUID  *bool          `json:"updateRemoteUserUID,omitempty"`
	HostRequirements     map[string]any `json:"hostRequirements,omitempty"`
}

// Parse accepts the array and legacy single-object forms. Invalid labels return
// an error; callers can log it and continue without metadata, as upstream does.
func Parse(value string) ([]Entry, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var entries []Entry
	switch value[0] {
	case '[':
		if err := json.Unmarshal([]byte(value), &entries); err != nil {
			return nil, fmt.Errorf("image metadata: %w", err)
		}
	case '{':
		var entry Entry
		if err := json.Unmarshal([]byte(value), &entry); err != nil {
			return nil, fmt.Errorf("image metadata: %w", err)
		}
		entries = []Entry{entry}
	default:
		return nil, fmt.Errorf("image metadata must be an object or array")
	}
	return entries, nil
}

// Read reads labels and falls back to no metadata on invalid input. The optional
// logger receives the parse error, matching upstream's nonfatal label handling.
func Read(labels map[string]string, logf func(string, ...any)) []Entry {
	entries, err := Parse(labels[Label])
	if err != nil && logf != nil {
		logf("warning: %v", err)
	}
	return entries
}

// Encode always emits a JSON array, including for a single entry.
func Encode(entries []Entry) (string, error) {
	if entries == nil {
		entries = []Entry{}
	}
	data, err := json.Marshal(entries)
	return string(data), err
}

// DockerfileLabel escapes a label value for a double-quoted Dockerfile LABEL.
func DockerfileLabel(entries []Entry) (string, error) {
	value, err := Encode(entries)
	if err != nil {
		return "", err
	}
	value = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(value)
	return `LABEL ` + Label + `="` + value + `"`, nil
}

// FromConfig picks only the properties upstream persists in image metadata.
func FromConfig(dc *spec.DevContainer) Entry {
	if dc == nil {
		return Entry{}
	}
	return Entry{
		Init: dc.Init, Privileged: dc.Privileged, CapAdd: dc.CapAdd, SecurityOpt: dc.SecurityOpt,
		Mounts: dc.Mounts, Customizations: dc.Customizations, OnCreate: dc.OnCreate,
		UpdateContent: dc.UpdateContent, PostCreate: dc.PostCreate, PostStart: dc.PostStart,
		PostAttach: dc.PostAttach, WaitFor: dc.WaitFor, RemoteUser: dc.RemoteUser,
		ContainerUser: dc.ContainerUser, UserEnvProbe: dc.UserEnvProbe, RemoteEnv: dc.UpdateRemoteEnv,
		ContainerEnv: dc.ContainerEnv, OverrideCommand: dc.OverrideCommand, PortsAttributes: dc.PortsAttributes,
		OtherPortsAttributes: dc.OtherPortsAttributes, ForwardPorts: dc.ForwardPorts,
		ShutdownAction: dc.ShutdownAction, UpdateRemoteUserUID: dc.UpdateRemoteUserUID,
		HostRequirements: dc.HostRequirements,
	}
}

// Collect appends feature entries in install order, then the config entry.
// Feature containerEnv is image ENV, rather than label metadata, in upstream.
func Collect(base []Entry, resolved []features.Resolved, dc *spec.DevContainer) []Entry {
	entries := append([]Entry(nil), base...)
	for _, f := range resolved {
		m := f.Metadata
		entries = append(entries, Entry{ID: f.Source, Init: m.Init, Privileged: m.Privileged,
			CapAdd: m.CapAdd, SecurityOpt: m.SecurityOpt, Entrypoint: m.Entrypoint,
			Mounts: m.Mounts, Customizations: m.Customizations, OnCreate: m.OnCreateCommand,
			UpdateContent: m.UpdateContent, PostCreate: m.PostCreateCommand,
			PostStart: m.PostStartCommand, PostAttach: m.PostAttachCommand})
	}
	entry := FromConfig(dc)
	// Empty config entries are omitted by upstream.
	data, _ := json.Marshal(entry)
	if string(data) != "{}" {
		entries = append(entries, entry)
	}
	return entries
}

// Update appends only the properties upstream permits to change on an existing
// labelled container. Its persisted lifecycle and container settings stay intact.
func Update(entries []Entry, dc *spec.DevContainer) []Entry {
	return append(append([]Entry(nil), entries...), Entry{RemoteUser: dc.RemoteUser,
		UserEnvProbe: dc.UserEnvProbe, RemoteEnv: dc.UpdateRemoteEnv})
}
