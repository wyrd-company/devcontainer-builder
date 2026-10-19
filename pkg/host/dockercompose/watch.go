package dockercompose

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"golang.org/x/sync/errgroup"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// WatchRequest drives Host.Watch.
type WatchRequest struct {
	// Spec is the parsed devcontainer.json (must be in compose mode).
	Spec *spec.DevContainer
	// SpecPath is the host path the spec was loaded from. Used only
	// for diagnostics.
	SpecPath string
	// LocalWorkspace is the absolute host workspace path. Used as
	// the project name source so the active project lookup matches
	// what Up brought up.
	LocalWorkspace string
	// LocalEnv is forwarded to compose's `${VAR}` interpolation.
	LocalEnv map[string]string
	// PollInterval governs the host-side mtime sweep cadence.
	// Defaults to 1 second; smaller values are CPU-hungry on deep
	// trees.
	PollInterval time.Duration
	// OnEvent receives one callback per fired trigger. Optional;
	// useful for progress UIs and tests. The slice of changed
	// relative paths is supplied for `sync` and `sync+*` actions;
	// `restart` and `rebuild` pass nil.
	OnEvent func(service string, action types.WatchAction, changed []string)
}

// Watch loops over every service in the project that declares
// `develop.watch`, polls each trigger's Path for filesystem changes,
// and fires the configured action against the running container.
//
// Action coverage:
//
//   - sync: changed files are tarred and pushed via the docker
//     CopyToContainer API. Deleted files trigger an `rm -rf` exec
//     inside the target container.
//   - restart: the container is restarted (graceful stop, then start).
//   - sync+restart: sync, then restart.
//   - sync+exec: sync, then run the trigger's `exec:` command.
//   - rebuild: not honored — would need the build pipeline, which is
//     out of scope for the watch loop. Returns an error if any active
//     trigger uses it.
//
// Include/ignore are matched with path.Match against the path
// relative to Trigger.Path, with `**` interpreted as match-any-suffix.
// Watch blocks until ctx is cancelled or any session errors out.
func (h *Host) Watch(ctx context.Context, req WatchRequest) error {
	if req.Spec == nil {
		return errorf("WatchRequest.Spec is required")
	}
	if req.Spec.Mode() != spec.ModeCompose {
		return errorf("WatchRequest.Spec is not in compose mode")
	}
	if req.LocalWorkspace == "" {
		return errorf("WatchRequest.LocalWorkspace is required")
	}

	proj, err := LoadProject(ctx, LoadInputs{
		Spec:      req.Spec,
		Workspace: req.LocalWorkspace,
		LocalEnv:  req.LocalEnv,
	})
	if err != nil {
		return err
	}

	interval := req.PollInterval
	if interval <= 0 {
		interval = time.Second
	}

	var sessions []*watchSession
	for _, name := range proj.ServiceNames() {
		svc, _ := proj.GetService(name)
		if svc.Develop == nil || len(svc.Develop.Watch) == 0 {
			continue
		}
		for _, tr := range svc.Develop.Watch {
			if tr.Action == types.WatchActionRebuild {
				return errorf("service %q: develop.watch action %q is not honored by this host", name, tr.Action)
			}
		}
		cid, err := FindExisting(ctx, h.client, proj.Name, name)
		if err != nil {
			return err
		}
		if cid == "" {
			h.logf("watch: skipping service %q — no running container", name)
			continue
		}
		sessions = append(sessions, &watchSession{
			host:        h,
			containerID: cid,
			service:     svc,
			onEvent:     req.OnEvent,
		})
	}

	if len(sessions) == 0 {
		return errorf("no services with develop.watch found (or none have a running container)")
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, s := range sessions {
		s := s
		g.Go(func() error { return s.run(gctx, interval) })
	}
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// watchSession owns one service's worth of develop.watch loop. The
// public Host.Watch fans out one goroutine per session.
type watchSession struct {
	host        *Host
	containerID string
	service     types.ServiceConfig
	onEvent     func(service string, action types.WatchAction, changed []string)

	mu       sync.Mutex
	snapshot map[int]map[string]time.Time // trigger index → relpath → mtime
}

func (s *watchSession) run(ctx context.Context, interval time.Duration) error {
	s.snapshot = make(map[int]map[string]time.Time, len(s.service.Develop.Watch))
	// Seed each trigger's mtime map so the first tick doesn't
	// classify everything as a change.
	for i, tr := range s.service.Develop.Watch {
		state, err := scanTriggerPath(tr)
		if err != nil {
			return fmt.Errorf("service %q trigger %d: %w", s.service.Name, i, err)
		}
		s.snapshot[i] = state
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := s.tick(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *watchSession) tick(ctx context.Context) error {
	for i, tr := range s.service.Develop.Watch {
		current, err := scanTriggerPath(tr)
		if err != nil {
			return fmt.Errorf("service %q trigger %d: %w", s.service.Name, i, err)
		}
		s.mu.Lock()
		prev := s.snapshot[i]
		changed, deleted := diffSnapshots(prev, current)
		s.snapshot[i] = current
		s.mu.Unlock()
		if len(changed) == 0 && len(deleted) == 0 {
			continue
		}
		if err := s.fire(ctx, tr, changed, deleted); err != nil {
			return fmt.Errorf("service %q trigger %d (%s): %w", s.service.Name, i, tr.Action, err)
		}
	}
	return nil
}

// fire runs the configured action for one trigger. changed are
// relative paths that need to be (re)synced; deleted are relative
// paths that need to be removed in the container.
func (s *watchSession) fire(ctx context.Context, tr types.Trigger, changed, deleted []string) error {
	doSync := tr.Action == types.WatchActionSync ||
		tr.Action == types.WatchActionSyncRestart ||
		tr.Action == types.WatchActionSyncExec
	doRestart := tr.Action == types.WatchActionRestart || tr.Action == types.WatchActionSyncRestart
	doExec := tr.Action == types.WatchActionSyncExec

	if doSync {
		if err := s.syncChanges(ctx, tr, changed, deleted); err != nil {
			return err
		}
		if s.onEvent != nil {
			s.onEvent(s.service.Name, tr.Action, append(append([]string(nil), changed...), deleted...))
		}
	} else if s.onEvent != nil {
		s.onEvent(s.service.Name, tr.Action, nil)
	}

	if doRestart {
		timeout := int(s.host.opts.StopTimeout.Seconds())
		if err := s.host.client.ContainerRestart(ctx, s.containerID, container.StopOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("restart container: %w", err)
		}
	}
	if doExec {
		if len(tr.Exec.Command) == 0 {
			return fmt.Errorf("sync+exec trigger has no exec.command")
		}
		envMap := map[string]string{}
		for k, v := range tr.Exec.Environment {
			if v == nil {
				continue
			}
			envMap[k] = *v
		}
		if _, err := s.host.Exec(ctx, ExecRequest{
			ContainerID: s.containerID,
			Cmd:         []string(tr.Exec.Command),
			User:        tr.Exec.User,
			WorkDir:     tr.Exec.WorkingDir,
			Env:         envMap,
			Stdout:      s.host.opts.Stdout,
			Stderr:      s.host.opts.Stderr,
		}); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}
	return nil
}

// syncChanges pushes changed and deleted relative paths into the
// container under tr.Target.
func (s *watchSession) syncChanges(ctx context.Context, tr types.Trigger, changed, deleted []string) error {
	target := tr.Target
	if target == "" {
		return fmt.Errorf("trigger has no target; sync needs an explicit container path")
	}
	if len(changed) > 0 {
		buf, err := buildTar(tr.Path, changed)
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if err := s.host.client.CopyToContainer(ctx, s.containerID, target, buf, container.CopyToContainerOptions{
			AllowOverwriteDirWithFile: true,
		}); err != nil {
			return fmt.Errorf("copy to container: %w", err)
		}
	}
	if len(deleted) > 0 {
		argv := []string{"rm", "-rf"}
		for _, rel := range deleted {
			argv = append(argv, path.Join(target, filepath.ToSlash(rel)))
		}
		if _, err := s.host.Exec(ctx, ExecRequest{
			ContainerID: s.containerID,
			Cmd:         argv,
		}); err != nil {
			return fmt.Errorf("rm in container: %w", err)
		}
	}
	return nil
}

// scanTriggerPath walks tr.Path and returns one mtime per file that
// matches the include/ignore filters. Symlinks are not followed —
// docker compose's watch loop uses inotify under the hood and
// inherits whatever the kernel reports; we don't.
func scanTriggerPath(tr types.Trigger) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	root := tr.Path
	if root == "" {
		return nil, fmt.Errorf("trigger has no path")
	}
	info, err := os.Stat(root)
	if err != nil {
		// Missing path: empty snapshot. Picks the file up if it
		// later appears.
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		if matchesTrigger(tr, filepath.Base(root)) {
			out[filepath.Base(root)] = info.ModTime()
		}
		return out, nil
	}
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if !matchesTrigger(tr, rel) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out[rel] = fi.ModTime()
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// matchesTrigger applies Trigger.Include / Trigger.Ignore against a
// path relative to Trigger.Path. Empty include == match everything.
// The matcher accepts plain glob (`*`, `?`) and `**` for recursive
// directory matches.
func matchesTrigger(tr types.Trigger, rel string) bool {
	rel = filepath.ToSlash(rel)
	if len(tr.Include) > 0 {
		any := false
		for _, pat := range tr.Include {
			if matchPath(pat, rel) {
				any = true
				break
			}
		}
		if !any {
			return false
		}
	}
	for _, pat := range tr.Ignore {
		if matchPath(pat, rel) {
			return false
		}
	}
	return true
}

// matchPath supports the docker watch glob superset: `*` and `?` per
// path.Match, plus `**` for any-suffix and a leading `**/` for
// any-prefix.
func matchPath(pattern, rel string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "**") {
		matched, _ := path.Match(pattern, rel)
		return matched
	}
	// **/foo.txt — anywhere in tree.
	if strings.HasPrefix(pattern, "**/") {
		suffix := strings.TrimPrefix(pattern, "**/")
		segs := strings.Split(rel, "/")
		for i := 0; i < len(segs); i++ {
			if matched, _ := path.Match(suffix, strings.Join(segs[i:], "/")); matched {
				return true
			}
		}
		return false
	}
	// foo/** — match prefix.
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return rel == prefix || strings.HasPrefix(rel, prefix+"/")
	}
	// Anywhere-in-the-middle ** is not modeled; fall back to a
	// stricter interpretation that requires literal **.
	literal := strings.ReplaceAll(pattern, "**", "*")
	matched, _ := path.Match(literal, rel)
	return matched
}

// diffSnapshots returns the relpaths that need a (re)sync (newly
// present or with a newer mtime) and the relpaths that have been
// deleted since the previous tick.
func diffSnapshots(prev, current map[string]time.Time) (changed, deleted []string) {
	for rel, t := range current {
		old, ok := prev[rel]
		if !ok || !t.Equal(old) {
			changed = append(changed, rel)
		}
	}
	for rel := range prev {
		if _, ok := current[rel]; !ok {
			deleted = append(deleted, rel)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted
}

// buildTar produces a tar archive containing the supplied relative
// paths under root. Each entry's name is the relpath in posix form so
// it lands at <target>/<relpath> in the container.
func buildTar(root string, rels []string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, rel := range rels {
		full := filepath.Join(root, rel)
		fi, err := os.Stat(full)
		if err != nil {
			return nil, err
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return nil, err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(full)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}
