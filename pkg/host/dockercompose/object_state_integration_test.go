//go:build docker_integration

// relationships:
//
// references: [object_state, objects, manage, up]
package dockercompose

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestComposeObjectRemoveIntegration(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	defer cli.Close()
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "selected"}[selected], func(t *testing.T) {
			parent := t.TempDir()
			writeNameFile(t, filepath.Join(parent, "keep"), "sentinel")
			opts := Options{}
			if selected {
				opts.UserDataFolder = parent + "/./"
			}
			h := New(cli, opts)
			var roots []string
			var workspaces []string
			var sources []string
			for i := 0; i < 2; i++ {
				workspace := prepareComposeWorkspace(t)
				workspaces = append(workspaces, workspace)
				config := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
				compose := filepath.Join(filepath.Dir(config), "compose.yml")
				source := filepath.Join(filepath.Dir(config), "source")
				sources = append(sources, source)
				writeNameFile(t, source, "source-sentinel")
				writeNameFile(t, config, `{"dockerComposeFile":"compose.yml","service":"app"}`)
				writeNameFile(t, compose, "services:\n  app:\n    image: alpine:3.20\n    configs: [sample, original]\n    secrets: [token]\nconfigs:\n  sample:\n    content: synthetic-config\n  original:\n    file: ./source\nsecrets:\n  token:\n    environment: SAMPLE_TOKEN\n")
				dc, err := spec.Load(config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = h.Remove(ctx, workspace) })
				up, err := h.Up(ctx, UpRequest{Spec: dc, SpecPath: config, LocalWorkspace: workspace, LocalEnv: map[string]string{"SAMPLE_TOKEN": "synthetic-token"}, SkipLifecycle: true})
				if err != nil {
					t.Fatal(err)
				}
				info, err := cli.ContainerInspect(ctx, up.PrimaryContainerID)
				if err != nil {
					t.Fatal(err)
				}
				root := ""
				for _, m := range info.Mounts {
					if m.Destination == "/sample" || m.Destination == "/run/secrets/token" {
						got := filepath.Dir(filepath.Dir(m.Source))
						if root != "" && got != root {
							t.Fatal("objects disagree on ownership")
						}
						root = got
						wantParent := filepath.Join(os.TempDir(), "dcb-compose")
						if selected {
							wantParent = parent
						}
						if filepath.Dir(root) != wantParent {
							t.Fatalf("materialized outside selected parent: %s", root)
						}
					}
				}
				if root == "" {
					t.Fatal("no materialized mounts")
				}
				if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0700 {
					t.Fatal("private root permissions changed")
				}
				records, err := h.objectRecords(ctx, workspace)
				if err != nil || len(records) != 1 || records[0].Labels[labelObjectRoot] != root {
					t.Fatal("mount and ownership paths disagree")
				}
				for _, value := range records[0].Labels {
					if strings.Contains(value, "synthetic-token") || strings.Contains(value, "synthetic-config") {
						t.Fatal("payload in ownership labels")
					}
				}

				roots = append(roots, root)
				out, err := h.Exec(ctx, ExecRequest{ContainerID: up.PrimaryContainerID, Cmd: []string{"sh", "-c", "cat /sample; echo; cat /run/secrets/token; echo; cat /original"}})
				if err != nil || out.ExitCode != 0 || string(out.Stdout) != "synthetic-config\nsynthetic-token\nsource-sentinel" {
					t.Fatalf("container values mismatch: %v", err)
				}
				// Lose every container before cleanup, and make the source config unusable.
				if err := cli.ContainerRemove(ctx, up.PrimaryContainerID, container.RemoveOptions{Force: true}); err != nil {
					t.Fatal(err)
				}
				writeNameFile(t, config, "{")
			}
			t.Setenv("TMPDIR", t.TempDir())
			fresh := New(cli, Options{})
			for i, workspace := range workspaces {
				for repeat := 0; repeat < 2; repeat++ {
					if err := fresh.Remove(ctx, workspace); err != nil && !errors.Is(err, ErrProjectNotFound) {
						t.Fatal(err)
					}
				}
				assertObjectAbsent(t, roots[i])
				if i == 0 {
					if _, err := os.Stat(filepath.Join(roots[1], "secrets", "token")); err != nil {
						t.Fatal("other workspace secret removed")
					}
				}
				if b, err := os.ReadFile(sources[i]); err != nil || string(b) != "source-sentinel" {
					t.Fatal("source file changed")
				}
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
				t.Fatalf("selected root residue: %v %v", entries, err)
			}
		})
	}
}

func assertObjectAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("materialized residue at %s: %v", path, err)
	}
}

// Hold the first producer after durable ownership publication, then complete a
// second producer. Neither reader adopts another producer's partial folder.
func TestComposeObjectPublicationIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	defer cli.Close()
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "held-success", true: "held-failure"}[fails], func(t *testing.T) {
			parent := t.TempDir()
			workspace := t.TempDir()
			h := New(cli, Options{UserDataFolder: parent + "/../" + filepath.Base(parent) + "/./"})
			project := objectTestProject(workspace)
			t.Cleanup(func() { _ = h.Remove(ctx, workspace) })
			held, release := make(chan string, 1), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				m := newObjectMaterializer(func() (string, error) {
					root, err := h.allocateObjects(ctx, project, workspace)
					if err != nil {
						return "", err
					}
					held <- root
					<-release
					if fails {
						return "", errors.New("interrupted producer")
					}
					return root, nil
				}, nil)
				_, err := m.resolveSecret("token", types.SecretConfig{Content: "first-synthetic"})
				done <- err
			}()
			released := false
			t.Cleanup(func() {
				if !released {
					close(release)
				}
			})
			var root string
			select {
			case root = <-held:
			case err := <-done:
				t.Fatalf("held allocation failed: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("payload written before ownership publication")
			}
			second := newObjectMaterializer(func() (string, error) { return h.allocateObjects(ctx, project, workspace) }, nil)
			path, err := second.resolveSecret("token", types.SecretConfig{Content: "second-synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(path, root+"/") {
				t.Fatal("second producer adopted partial output")
			}
			close(release)
			released = true
			err = <-done
			if (err != nil) != fails {
				t.Fatalf("held outcome: %v", err)
			}
			if b, err := os.ReadFile(path); err != nil || string(b) != "second-synthetic" {
				t.Fatal("held producer overwrote published output")
			}
			// A non-directory parent makes RemoveAll fail even for a privileged user.
			saved := parent + "-saved"
			if err := os.Rename(parent, saved); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := os.Stat(saved); err == nil {
					_ = os.Remove(parent)
					_ = os.Rename(saved, parent)
				}
			})
			if err := os.WriteFile(parent, []byte("blocked"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := New(cli, Options{}).Remove(ctx, workspace); err == nil {
				t.Fatal("cleanup failure reported success")
			}
			records, err := h.objectRecords(ctx, workspace)
			if err != nil || len(records) != 2 {
				t.Fatalf("retry ownership lost: %v %v", records, err)
			}
			if err := os.Remove(parent); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(saved, parent); err != nil {
				t.Fatal(err)
			}
			if err := New(cli, Options{}).Remove(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			assertObjectAbsent(t, root)
			assertObjectAbsent(t, second.root)
		})
	}
}

func TestComposeObjectCreationFailureIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	defer cli.Close()
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	workspace := prepareComposeWorkspace(t)
	parent := t.TempDir()
	h := New(cli, Options{UserDataFolder: parent})
	config := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	writeNameFile(t, config, `{"dockerComposeFile":"compose.yml","service":"app"}`)
	writeNameFile(t, filepath.Join(filepath.Dir(config), "compose.yml"), "services:\n  app:\n    image: alpine:3.20\n    configs:\n      - source: sample\n        target: /duplicate\n    secrets:\n      - source: token\n        target: /duplicate\nconfigs:\n  sample:\n    content: synthetic-config\nsecrets:\n  token:\n    environment: SAMPLE_TOKEN\n")
	dc, err := spec.Load(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Remove(ctx, workspace) })
	_, err = h.Up(ctx, UpRequest{Spec: dc, SpecPath: config, LocalWorkspace: workspace, LocalEnv: map[string]string{"SAMPLE_TOKEN": "synthetic-token"}, SkipLifecycle: true})
	if err == nil {
		t.Fatal("duplicate mount creation succeeded")
	}
	records, err := h.objectRecords(ctx, workspace)
	if err != nil || len(records) != 1 {
		t.Fatalf("failed creation lost ownership: %v %v", records, err)
	}
	root := records[0].Labels[labelObjectRoot]
	if _, err := os.Stat(filepath.Join(root, "secrets", "token")); err != nil {
		t.Fatal("creation did not reach secret materialization")
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if err := New(cli, Options{}).Remove(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	assertObjectAbsent(t, root)
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed creation left secret residue")
	}
}

func TestComposeObjectInterruptedProcessIntegration(t *testing.T) {
	if os.Getenv("DCB_OBJECT_CHILD") == "1" {
		ctx := context.Background()
		cli := requireComposeDocker(t, ctx)
		defer cli.Close()
		h := New(cli, Options{UserDataFolder: os.Getenv("DCB_OBJECT_PARENT")})
		m := newObjectMaterializer(func() (string, error) {
			return h.allocateObjects(ctx, objectTestProject(os.Getenv("DCB_OBJECT_WORKSPACE")), os.Getenv("DCB_OBJECT_WORKSPACE"))
		}, nil)
		if _, err := m.resolveSecret("token", types.SecretConfig{Content: "synthetic-interrupted"}); err != nil {
			t.Fatal(err)
		}
		fmt.Println("OBJECT_READY")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	defer cli.Close()
	parent, workspace := t.TempDir(), t.TempDir()
	h := New(cli, Options{})
	t.Cleanup(func() { _ = h.Remove(ctx, workspace) })
	cmd := exec.Command(os.Args[0], "-test.run=^TestComposeObjectInterruptedProcessIntegration$", "-test.v")
	cmd.Env = append(os.Environ(), "DCB_OBJECT_CHILD=1", "DCB_OBJECT_PARENT="+parent, "DCB_OBJECT_WORKSPACE="+workspace)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	scanner := bufio.NewScanner(output)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "OBJECT_READY" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("child never reached materialization boundary")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed producer reported success")
	}
	records, err := h.objectRecords(ctx, workspace)
	if err != nil || len(records) != 1 {
		t.Fatal("killed producer lost durable ownership")
	}
	root := records[0].Labels[labelObjectRoot]
	if _, err := os.Stat(filepath.Join(root, "secrets", "token")); err != nil {
		t.Fatal("kill did not leave expected secret for teardown")
	}
	if err := h.Remove(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	assertObjectAbsent(t, root)
}

func TestComposeObjectOwnershipValidationIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	defer cli.Close()
	for _, shape := range []string{"relative", "unclean", "not-private", "name-mismatch"} {
		t.Run(shape, func(t *testing.T) {
			parent, workspace := t.TempDir(), t.TempDir()
			root, err := os.MkdirTemp(parent, "objects-")
			if err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(root, "keep")
			writeNameFile(t, sentinel, "sentinel")
			labelRoot, name := root, "dcb-"+filepath.Base(root)
			switch shape {
			case "relative":
				cwd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				labelRoot, err = filepath.Rel(cwd, root)
				if err != nil {
					t.Fatal(err)
				}
			case "unclean":
				labelRoot = root + "/../" + filepath.Base(root)
			case "not-private":
				labelRoot = parent
				name = "dcb-" + filepath.Base(parent)
			case "name-mismatch":
				name += "-other"
			}
			record, err := cli.VolumeCreate(ctx, volume.CreateOptions{Name: name, Labels: map[string]string{labelObjectRoot: labelRoot, LabelLocalFolder: workspace, LabelProject: objectTestProject(workspace)}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cli.VolumeRemove(ctx, record.Name, true) })
			if err := New(cli, Options{}).Remove(ctx, workspace); err == nil {
				t.Fatal("invalid ownership accepted")
			}
			if b, err := os.ReadFile(sentinel); err != nil || string(b) != "sentinel" {
				t.Fatal("unowned contents removed")
			}
		})
	}
}

func objectTestProject(workspace string) string {
	hash := sha256.Sum256([]byte(workspace))
	return fmt.Sprintf("sample-%x", hash[:8])
}

func TestComposeObjectLedgerRemovalRetryIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	defer cli.Close()
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	parent, workspace := t.TempDir(), t.TempDir()
	h := New(cli, Options{UserDataFolder: parent})
	t.Cleanup(func() { _ = h.Remove(ctx, workspace) })
	m := newObjectMaterializer(func() (string, error) { return h.allocateObjects(ctx, objectTestProject(workspace), workspace) }, nil)
	if _, err := m.resolveSecret("token", types.SecretConfig{Content: "synthetic-token"}); err != nil {
		t.Fatal(err)
	}
	records, err := h.objectRecords(ctx, workspace)
	if err != nil || len(records) != 1 {
		t.Fatal("missing ownership")
	}
	holder, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20"}, &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: records[0].Name, Target: "/hold"}}}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.ContainerRemove(ctx, holder.ID, container.RemoveOptions{Force: true}) })
	if err := h.Remove(ctx, workspace); err == nil {
		t.Fatal("busy ownership deletion reported success")
	}
	assertObjectAbsent(t, m.root)
	records, err = h.objectRecords(ctx, workspace)
	if err != nil || len(records) != 1 {
		t.Fatal("failed ledger deletion lost retry record")
	}
	if err := cli.ContainerRemove(ctx, holder.ID, container.RemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err := New(cli, Options{}).Remove(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	records, err = h.objectRecords(ctx, workspace)
	if err != nil || len(records) != 0 {
		t.Fatal("ledger retry failed")
	}
}
