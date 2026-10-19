// relationships:
//
//	references: [features, ../../reference/cli/src/spec-configuration/lockfile]
package features

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Lockfile records remote features by their original user-supplied identifiers.
// Local features are omitted, as in upstream devcontainer-lock.json.
type Lockfile struct {
	Features map[string]LockedFeature `json:"features"`
}

type LockedFeature struct {
	Version   string   `json:"version"`
	Resolved  string   `json:"resolved"`
	Integrity string   `json:"integrity"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

func LockfilePath(configPath string) string {
	name := "devcontainer-lock.json"
	if strings.HasPrefix(filepath.Base(configPath), ".") {
		name = "." + name
	}
	return filepath.Join(filepath.Dir(filepath.Clean(configPath)), name)
}

// LockfileState holds one input snapshot and its completed resolution. Keep it
// for the entire command, then Publish only after all command work succeeds.
// A state belongs to one invocation; concurrent invocations use separate states.
type LockfileState struct {
	Lockfile *Lockfile
	path     string
	old      []byte
	write    bool
	frozen   bool
	prepared []byte
	mode     fs.FileMode
}

func ReadLockfile(configPath string, create, frozen bool) (*LockfileState, error) {
	path, err := filepath.Abs(LockfilePath(configPath))
	if err != nil {
		return nil, err
	}
	s := &LockfileState{path: path, frozen: frozen, write: create, mode: 0o644}
	s.old, err = os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	if err == nil {
		// Follow an existing link as a normal write would, and stage beside its
		// target so publication remains atomic across filesystem boundaries.
		s.path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(s.path)
		if err != nil {
			return nil, err
		}
		s.mode = info.Mode().Perm()
		s.write = true // Existing files, including empty initialization markers.
		if len(bytes.TrimSpace(s.old)) != 0 {
			if err := json.Unmarshal(s.old, &s.Lockfile); err != nil {
				return nil, fmt.Errorf("parse lockfile: %w", err)
			}
		}
	}
	return s, nil
}

func GenerateLockfile(resolved []Resolved) *Lockfile {
	lock := &Lockfile{Features: map[string]LockedFeature{}}
	for _, f := range resolved {
		location := f.Tarball
		if f.Reference != nil {
			location = f.Reference.Registry + "/" + f.Reference.Repository + "@" + f.Digest
		}
		if location == "" {
			continue
		}
		lock.Features[f.Source] = LockedFeature{Version: f.Metadata.Version, Resolved: location, Integrity: f.Digest, DependsOn: f.Metadata.DependsOn.Keys}
	}
	return lock
}

// Prepare validates frozen mode before building. The candidate is held in memory
// so an interrupted, failed or cancelled command cannot publish it.
func (s *LockfileState) Prepare(resolved []Resolved) error {
	if s == nil {
		return nil
	}
	s.prepared = nil
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(GenerateLockfile(resolved)); err != nil {
		return err
	}
	data := encoded.Bytes()
	old, next := normalizeLockfileJSON(s.old), normalizeLockfileJSON(data)
	if s.frozen {
		if s.old == nil {
			return fmt.Errorf("Lockfile does not exist.")
		}
		if !bytes.Equal(old, next) {
			return fmt.Errorf("Lockfile does not match.")
		}
	}
	if !bytes.Equal(old, next) {
		s.prepared = data
	}
	return nil
}

// Publish replaces the destination with a complete sibling file. Each writer
// owns its temporary file; readers see either complete generation. A frozen
// state never writes, even when creation was requested as well.
func (s *LockfileState) Publish(ctx context.Context) error {
	if s == nil || s.frozen || !s.write || s.prepared == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".devcontainer-lock-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(s.mode); err != nil {
		return err
	}
	if _, err := f.Write(s.prepared); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
