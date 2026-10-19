//go:build docker_integration

// relationships:
//
//	references: [options_mounts, host, ../host/docker/up, ../host/dockercompose/labels]
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
)

const (
	mountsIDKey   = "example.test/mounts-id"
	mountsRoleKey = "example.test/mounts-role"
)

// execOutput runs dcb exec and returns the container command's stdout.
func execOutput(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), append([]string{"exec"}, args...), "test-version", nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exec %v: exit %d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

func findMount(info container.InspectResponse, target string) (container.MountPoint, bool) {
	for _, m := range info.Mounts {
		if m.Destination == target {
			return m, true
		}
	}
	return container.MountPoint{}, false
}

// The real CLI and Engine boundary prove the mount and id-label handlers.
func TestMountOptionsDockerIntegration(t *testing.T) {
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx := context.Background()
	repo, app := gitWorkspace(t, `{"image":"alpine:3.20","updateRemoteUserUID":false}`)
	sum := sha256.Sum256([]byte(repo))
	unique := fmt.Sprintf("%x", sum[:6])
	volumeName := "dcb-mounts-" + unique
	// Remove only what this test created: containers with its unique label or
	// a workspace label below its folder (a defect may drop the id labels), and
	// its named volume. down does not select id-labelled containers.
	t.Cleanup(func() {
		list, _ := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", mountsIDKey))})
		owned, _ := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", hostdocker.LabelLocalFolder))})
		for _, c := range append(list, owned...) {
			if c.Labels[mountsIDKey] == unique || strings.HasPrefix(c.Labels[hostdocker.LabelLocalFolder], filepath.Dir(repo)+string(filepath.Separator)) {
				_ = cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
			}
		}
		_ = cli.VolumeRemove(ctx, volumeName, true)
	})
	inspect := func(t *testing.T, id string) container.InspectResponse {
		t.Helper()
		info, err := cli.ContainerInspect(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	exists := func(id string) bool {
		_, err := cli.ContainerInspect(ctx, id)
		return err == nil
	}
	hostData := t.TempDir()
	writeTestFile(t, filepath.Join(hostData, "marker"), "bound")
	labels := []string{"--id-label", mountsIDKey + "=" + unique, "--id-label", mountsRoleKey + "=one"}
	upArgs := append([]string{"up", "--workspace-folder", app, "--skip-post-create",
		"--mount", "type=volume,source=" + volumeName + ",target=/extra-volume",
		"--mount", "type=bind,source=" + hostData + ",target=/extra-bind"}, labels...)

	result, _ := invoke(t, 0, upArgs...)
	id := result["containerId"].(string)
	t.Run("git root and mounts", func(t *testing.T) {
		info := inspect(t, id)
		if result["remoteWorkspaceFolder"] != "/workspaces/repo/app" {
			t.Fatalf("result=%v", result)
		}
		if m, ok := findMount(info, "/workspaces/repo"); !ok || m.Type != mount.TypeBind || m.Source != repo {
			t.Fatalf("workspace mount=%+v in %+v", m, info.Mounts)
		}
		if m, ok := findMount(info, "/extra-volume"); !ok || m.Type != mount.TypeVolume || m.Name != volumeName {
			t.Fatalf("volume mount=%+v", m)
		}
		if m, ok := findMount(info, "/extra-bind"); !ok || m.Source != hostData {
			t.Fatalf("bind mount=%+v", m)
		}
		if got := execOutput(t, append([]string{"--workspace-folder", app}, append(labels, "--", "sh", "-c", "pwd; cat /extra-bind/marker")...)...); got != "/workspaces/repo/app\nbound" {
			t.Fatalf("exec output=%q", got)
		}
	})
	t.Run("id labels", func(t *testing.T) {
		got := inspect(t, id).Config.Labels
		if got[mountsIDKey] != unique || got[mountsRoleKey] != "one" || got[hostdocker.LabelLocalFolder] != "" || got[hostdocker.LabelConfigFile] != "" {
			t.Fatalf("labels=%v", got)
		}
		// Label-only selection reads no configuration and runs in the home
		// folder, even from the workspace that the container mounts.
		t.Chdir(app)
		if got := execOutput(t, append(labels, "--", "pwd")...); got != "/root" {
			t.Fatalf("label-only exec pwd from the workspace=%q", got)
		}
		t.Chdir(t.TempDir())
		if got := execOutput(t, append(labels, "--", "pwd")...); got != "/root" {
			t.Fatalf("label-only exec pwd=%q", got)
		}
		invoke(t, 0, append([]string{"run-user-commands"}, labels...)...)
		missing, _ := invoke(t, 1, "up", "--workspace-folder", app, "--expect-existing-container", "--id-label", mountsIDKey+"=absent")
		if missing["message"] != "The expected container does not exist." {
			t.Fatalf("missing=%v", missing)
		}
		reused, _ := invoke(t, 0, append([]string{"up", "--workspace-folder", app, "--expect-existing-container", "--skip-post-create"}, labels...)...)
		if reused["containerId"] != id {
			t.Fatalf("labelled container not reused: %v", reused)
		}
	})
	t.Run("workspace-only teardown", func(t *testing.T) {
		plain, _ := invoke(t, 0, "up", "--workspace-folder", app, "--skip-post-create", "--mount-workspace-git-root=false")
		plainID := plain["containerId"].(string)
		if plainID == id || plain["remoteWorkspaceFolder"] != "/workspaces/app" {
			t.Fatalf("plain up=%v", plain)
		}
		if m, ok := findMount(inspect(t, plainID), "/workspaces/app"); !ok || m.Source != app {
			t.Fatalf("workspace-only mount=%+v", m)
		}
		// down selects by workspace: it removes the plain container only.
		invoke(t, 0, "down", "--workspace-folder", app)
		if exists(plainID) || !exists(id) {
			t.Fatalf("down removed plain=%v labelled=%v", !exists(plainID), !exists(id))
		}
	})
	t.Run("remove only the full label set", func(t *testing.T) {
		decoy, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20", Cmd: []string{"sleep", "600"}, Labels: map[string]string{mountsIDKey: unique}}, nil, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		replaced, _ := invoke(t, 0, append(upArgs, "--remove-existing-container")...)
		if replaced["containerId"] == id || exists(id) {
			t.Fatalf("labelled container not replaced: %v", replaced)
		}
		if !exists(decoy.ID) {
			t.Fatal("a container with part of the label set was removed")
		}
	})
	t.Run("worktree common folder", func(t *testing.T) {
		admin := filepath.Join(repo, ".git", "worktrees", "topic")
		writeTestFile(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/topic\n")
		writeTestFile(t, filepath.Join(admin, "commondir"), "../..\n")
		tree := filepath.Join(filepath.Dir(repo), "topic")
		writeTestFile(t, filepath.Join(tree, ".git"), "gitdir: ../repo/.git/worktrees/topic\n")
		writeTestFile(t, filepath.Join(tree, ".devcontainer.json"), `{"image":"alpine:3.20","updateRemoteUserUID":false}`)
		treeLabels := []string{"--id-label", mountsIDKey + "=" + unique, "--id-label", mountsRoleKey + "=tree"}
		got, _ := invoke(t, 0, append([]string{"up", "--workspace-folder", tree, "--skip-post-create", "--mount-git-worktree-common-dir"}, treeLabels...)...)
		if m, ok := findMount(inspect(t, got["containerId"].(string)), "/workspaces/repo/.git"); !ok || m.Source != filepath.Join(repo, ".git") {
			t.Fatalf("common folder mount=%+v", m)
		}
		if out := execOutput(t, append([]string{"--workspace-folder", tree, "--mount-git-worktree-common-dir"}, append(treeLabels, "--", "sh", "-c", "pwd; cat /workspaces/repo/.git/worktrees/topic/HEAD")...)...); out != "/workspaces/topic\nref: refs/heads/topic" {
			t.Fatalf("worktree exec=%q", out)
		}
	})
}

// Compose keeps the ownership labels beside the id labels, so the project
// conflict check and workspace-scoped down still apply.
func TestMountOptionsComposeDockerIntegration(t *testing.T) {
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx := context.Background()
	workspace := t.TempDir()
	sum := sha256.Sum256([]byte(workspace))
	unique := fmt.Sprintf("%x", sum[:6])
	project := "sample-mounts-" + unique
	compose := fmt.Sprintf("name: %s\nservices:\n  app:\n    image: alpine:3.20\n", project)
	config := `{"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/","updateRemoteUserUID":false}`
	writeTestFile(t, filepath.Join(workspace, "compose.yml"), compose)
	writeTestFile(t, filepath.Join(workspace, ".devcontainer.json"), config)
	args := []string{"--workspace-folder", workspace}
	t.Cleanup(func() {
		invoke(t, 0, append([]string{"down"}, args...)...)
		_ = cli.VolumeRemove(ctx, project+"_data", true)
	})
	labels := []string{"--id-label", mountsIDKey + "=" + unique, "--id-label", mountsRoleKey + "=compose"}
	result, _ := invoke(t, 0, append(append([]string{"up", "--skip-post-create", "--mount", "type=volume,source=data,target=/data"}, args...), labels...)...)
	info, err := cli.ContainerInspect(ctx, result["containerId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if l := info.Config.Labels; l[mountsIDKey] != unique || l[mountsRoleKey] != "compose" || l[hostdocker.LabelLocalFolder] != workspace {
		t.Fatalf("labels=%v", l)
	}
	if m, ok := findMount(info, "/data"); !ok || m.Name != project+"_data" {
		t.Fatalf("volume mount=%+v", m)
	}
	// Compose up selects by project and service, as upstream: a changed id
	// label reuses the primary and leaves its labels as created.
	changed, _ := invoke(t, 0, append([]string{"up", "--skip-post-create", "--mount", "type=volume,source=data,target=/data"}, append(args, "--id-label", mountsIDKey+"="+unique, "--id-label", mountsRoleKey+"=changed")...)...)
	if changed["containerId"] != result["containerId"] {
		t.Fatalf("changed id label recreated the primary: %v", changed)
	}
	if again, err := cli.ContainerInspect(ctx, info.ID); err != nil || again.Config.Labels[mountsRoleKey] != "compose" {
		t.Fatalf("primary labels after changed id label=%v err=%v", again.Config.Labels, err)
	}
	// Removal replaces the workspace's project container.
	replaced, _ := invoke(t, 0, append(append([]string{"up", "--skip-post-create", "--remove-existing-container", "--mount", "type=volume,source=data,target=/data"}, args...), labels...)...)
	if replaced["containerId"] == result["containerId"] {
		t.Fatalf("--remove-existing-container kept the primary: %v", replaced)
	}
	t.Chdir(t.TempDir())
	if got := execOutput(t, append(labels, "--", "pwd")...); got != "/root" {
		t.Fatalf("label-only exec pwd=%q", got)
	}

	other := filepath.Join(t.TempDir(), filepath.Base(workspace))
	writeTestFile(t, filepath.Join(other, "compose.yml"), compose)
	writeTestFile(t, filepath.Join(other, ".devcontainer.json"), config)
	conflict, _ := invoke(t, 1, append([]string{"up", "--workspace-folder", other}, labels...)...)
	if conflict["composeProjectName"] != project {
		t.Fatalf("conflict=%v", conflict)
	}
}

// Commands that attach to an existing container use the workspace folder the
// container was created with, whatever flags they get. postAttachCommand runs
// on every attach and records each command's working folder.
func TestAttachedWorkspaceDockerIntegration(t *testing.T) {
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx := context.Background()
	const config = `{"image":"alpine:3.20","updateRemoteUserUID":false,"remoteEnv":{"FOLDER":"${containerWorkspaceFolder}"},"postAttachCommand":"echo $(pwd) ${containerWorkspaceFolder} >> /tmp/attach-pwd"}`
	for _, tc := range []struct {
		name   string
		upArgs []string
		reuse  bool
		want   string
	}{
		{"git root", nil, false, "/workspaces/repo/app"},
		{"workspace folder", []string{"--mount-workspace-git-root=false"}, true, "/workspaces/app"},
		{"worktree common folder", []string{"--mount-git-worktree-common-dir"}, false, "/workspaces/topic/pkg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, local := gitWorkspace(t, config)
			if tc.name == "worktree common folder" {
				admin := filepath.Join(repo, ".git", "worktrees", "topic")
				writeTestFile(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/topic\n")
				writeTestFile(t, filepath.Join(admin, "commondir"), "../..\n")
				tree := filepath.Join(filepath.Dir(repo), "topic")
				writeTestFile(t, filepath.Join(tree, ".git"), "gitdir: ../repo/.git/worktrees/topic\n")
				local = filepath.Join(tree, "pkg")
				writeTestFile(t, filepath.Join(local, ".devcontainer.json"), config)
			}
			t.Cleanup(func() {
				owned, _ := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", hostdocker.LabelLocalFolder))})
				for _, c := range owned {
					if strings.HasPrefix(c.Labels[hostdocker.LabelLocalFolder], filepath.Dir(repo)+string(filepath.Separator)) {
						_ = cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
					}
				}
			})
			ws := []string{"--workspace-folder", local}
			up, _ := invoke(t, 0, append(append([]string{"up"}, ws...), tc.upArgs...)...)
			id := up["containerId"].(string)
			if up["remoteWorkspaceFolder"] != tc.want {
				t.Fatalf("up result=%v, want folder %s", up, tc.want)
			}
			attaches := 1
			setUp, _ := invoke(t, 0, append([]string{"set-up", "--container-id", id}, ws...)...)
			runUser, _ := invoke(t, 0, append([]string{"run-user-commands"}, ws...)...)
			attaches += 2
			for _, result := range []map[string]any{setUp, runUser} {
				if result["remoteWorkspaceFolder"] != tc.want {
					t.Fatalf("attach result=%v, want folder %s", result, tc.want)
				}
			}
			if tc.reuse {
				// A reused container keeps its placement under other flags.
				again, _ := invoke(t, 0, append([]string{"up"}, ws...)...)
				if again["containerId"] != id || again["remoteWorkspaceFolder"] != tc.want {
					t.Fatalf("reused up=%v", again)
				}
				attaches++
			}
			if got := execOutput(t, append(ws, "--", "sh", "-c", "echo $(pwd) $FOLDER")...); got != tc.want+" "+tc.want {
				t.Fatalf("exec folder and substitution=%q, want %s", got, tc.want)
			}
			want := strings.TrimSuffix(strings.Repeat(tc.want+" "+tc.want+"\n", attaches), "\n")
			if got := execOutput(t, append(ws, "--", "cat", "/tmp/attach-pwd")...); got != want {
				t.Fatalf("lifecycle folders=%q, want %q", got, want)
			}
		})
	}
}
