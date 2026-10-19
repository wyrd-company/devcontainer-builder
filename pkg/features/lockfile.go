// relationships:
//
//	references: [features, ../../reference/cli/src/spec-configuration/lockfile]
package features

import (
	"bytes"
	"context"
	"crypto/rand"
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
	s.path, err = lockfileTarget(path)
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	s.old, err = os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	if err == nil {
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

// Follow the final link even when its target does not exist yet. Staging beside
// that target retains ordinary write behavior and same-filesystem publication.
func lockfileTarget(path string) (string, error) {
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		directory := strings.HasSuffix(target, string(filepath.Separator))
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if directory {
			return target + string(filepath.Separator), nil
		}
		path = filepath.Clean(target)
	}
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
	f, err := createLockfileSibling(filepath.Dir(s.path), rand.Text)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if s.old != nil {
		if err := f.Chmod(s.mode); err != nil {
			return err
		}
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

func createLockfileSibling(dir string, randomName func() string) (*os.File, error) {
	for {
		// O_EXCL owns a unique sibling; 0666 lets the caller's umask govern
		// new files, as an ordinary write does. Existing modes are restored.
		name := filepath.Join(dir, ".devcontainer-lock-"+randomName())
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if os.IsExist(err) {
			continue
		}
		return f, err
	}
}
