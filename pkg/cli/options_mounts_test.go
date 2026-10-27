// relationships:
//
//	references: [options_mounts, ../workspace/workspace]
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/spf13/pflag"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// gitWorkspace writes repo/.git (the minimum Git discovery accepts) and a
// configuration in repo/app, and returns the app folder.
func gitWorkspace(t *testing.T, config string) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	for _, dir := range []string{".git/objects", ".git/refs", "app"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeTestFile(t, filepath.Join(repo, "app", ".devcontainer.json"), config)
	return repo, filepath.Join(repo, "app")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMountOptionRegistration(t *testing.T) {
	root := (&application{}).command()
	find := func(path ...string) *pflag.FlagSet {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		return cmd.Flags()
	}
	want := map[string]string{"mount-workspace-git-root": "true", "mount-git-worktree-common-dir": "false", "id-label": "[]", "git-prefix": ""}
	for _, name := range []string{"up", "exec", "read-configuration", "run-user-commands"} {
		for flag, def := range want {
			f := find(name).Lookup(flag)
			if f == nil || f.DefValue != def || f.Hidden {
				t.Errorf("%s --%s = %+v, want default %s", name, flag, f, def)
			}
		}
	}
	up := find("up")
	if f := up.Lookup("workspace-mount-consistency"); f == nil || f.DefValue != "cached" {
		t.Errorf("up --workspace-mount-consistency = %+v", f)
	}
	if f := up.Lookup("mount"); f == nil || f.Value.Type() != "stringArray" {
		t.Errorf("up --mount = %+v", f)
	}
	// Upstream build and set-up place the workspace folder alone.
	for _, name := range []string{"build", "set-up"} {
		for _, flag := range []string{"mount-workspace-git-root", "id-label", "mount", "git-prefix"} {
			if find(name).Lookup(flag) != nil {
				t.Errorf("%s has --%s", name, flag)
			}
		}
	}
}

func TestMountOptionValidation(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	_, app := gitWorkspace(t, `{"image":"alpine:3.20"}`)
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"up", "--id-label", "novalue"}, "Unmatched argument format: id-label must match <name>=<value>"},
		{[]string{"exec", "--id-label", "name=", "--", "true"}, "Unmatched argument format: id-label must match <name>=<value>"},
		{[]string{"read-configuration", "--id-label", "=value"}, "Unmatched argument format: id-label must match <name>=<value>"},
		{[]string{"run-user-commands", "--id-label", ""}, "Unmatched argument format: id-label must match <name>=<value>"},
		{[]string{"up", "--mount", "type=tmpfs,source=a,target=/a"}, "Unmatched argument format: mount must match type=<bind|volume>,source=<source>,target=<target>[,external=<true|false>]"},
		{[]string{"up", "--mount", "type=bind,source=/a,target=/b,readonly"}, "Unmatched argument format: mount must match type=<bind|volume>,source=<source>,target=<target>[,external=<true|false>]"},
		{[]string{"up", "--workspace-mount-consistency", "fast"}, "--workspace-mount-consistency must be consistent, cached, or delegated"},
		{[]string{"up", "--git-prefix", ""}, "--git-prefix must not be empty"},
		{[]string{"exec", "--git-prefix=", "--", "true"}, "--git-prefix must not be empty"},
		{[]string{"read-configuration", "--git-prefix", ""}, "--git-prefix must not be empty"},
		{[]string{"run-user-commands", "--git-prefix="}, "--git-prefix must not be empty"},
	} {
		args := append([]string{tc.args[0], "--workspace-folder", app}, tc.args[1:]...)
		result, _ := invoke(t, 2, args...)
		if result["message"] != tc.message {
			t.Errorf("%v: message %v", tc.args, result["message"])
		}
	}
	// Accepted values reach the Docker connection, which is absent here.
	for _, args := range [][]string{
		{"up", "--workspace-folder", app, "--mount", "type=volume,source=cache,target=/cache,external=true", "--id-label", "a=b=c", "--workspace-mount-consistency", "delegated"},
		{"up", "--workspace-folder", app, "--mount", "type=bind,source=/host/data,target=/data"},
	} {
		invoke(t, 1, args...)
	}
}

func TestReadConfigurationWorkspaceGitRoot(t *testing.T) {
	repo, app := gitWorkspace(t, `{"image":"alpine:3.20","containerEnv":{"FOLDER":"${containerWorkspaceFolder}"}}`)
	workspace := func(args ...string) (map[string]any, map[string]any) {
		result, _ := invoke(t, 0, append([]string{"read-configuration", "--workspace-folder", app}, args...)...)
		ws, _ := result["workspace"].(map[string]any)
		cfg, _ := result["configuration"].(map[string]any)
		return ws, cfg
	}

	ws, cfg := workspace()
	if ws["workspaceFolder"] != "/workspaces/repo/app" || ws["workspaceMount"] != "type=bind,source="+repo+",target=/workspaces/repo" {
		t.Fatalf("default workspace=%v", ws)
	}
	if env := cfg["containerEnv"].(map[string]any); env["FOLDER"] != "/workspaces/repo/app" {
		t.Fatalf("containerWorkspaceFolder=%v", env)
	}
	ws, _ = workspace("--mount-workspace-git-root=false")
	if ws["workspaceFolder"] != "/workspaces/app" || ws["workspaceMount"] != "type=bind,source="+app+",target=/workspaces/app" {
		t.Fatalf("workspace-only=%v", ws)
	}

	// A configured workspaceFolder or workspaceMount is kept, as upstream.
	writeTestFile(t, filepath.Join(app, ".devcontainer.json"), `{"image":"alpine:3.20","workspaceFolder":"/src","workspaceMount":"type=bind,source=/host/custom,target=/src"}`)
	if ws, _ = workspace(); ws["workspaceFolder"] != "/src" || ws["workspaceMount"] != "type=bind,source=/host/custom,target=/src" {
		t.Fatalf("configured workspace=%v", ws)
	}
	// A configured folder does not change the generated mount target.
	writeTestFile(t, filepath.Join(app, ".devcontainer.json"), `{"image":"alpine:3.20","workspaceFolder":"/src/${localWorkspaceFolderBasename}"}`)
	for _, tc := range []struct {
		name, source, target string
		args                 []string
	}{
		{"git root default", repo, "/workspaces/repo", nil},
		{"git root explicit", repo, "/workspaces/repo", []string{"--mount-workspace-git-root=true"}},
		{"workspace only", app, "/workspaces/app", []string{"--mount-workspace-git-root=false"}},
		{"common dir enabled", repo, "/workspaces/repo", []string{"--mount-git-worktree-common-dir"}},
		{"common dir without git root", app, "/workspaces/app", []string{"--mount-workspace-git-root=false", "--mount-git-worktree-common-dir"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, _ := workspace(tc.args...)
			if ws["workspaceFolder"] != "/src/app" || ws["workspaceMount"] != "type=bind,source="+tc.source+",target="+tc.target || ws["additionalMountString"] != nil {
				t.Fatalf("configured folder %v=%v", tc.args, ws)
			}
		})
	}
	writeTestFile(t, filepath.Join(app, ".devcontainer.json"), `{"image":"alpine:3.20","workspaceMount":"type=bind,source=/host/custom,target=/src"}`)
	if ws, _ = workspace(); ws["workspaceFolder"] != "/workspaces/repo/app" || ws["workspaceMount"] != "type=bind,source=/host/custom,target=/src" {
		t.Fatalf("configured mount=%v", ws)
	}
	for _, folder := range []string{"", "/tmp"} {
		config := `{"image":"alpine:3.20","workspaceMount":""}`
		if folder != "" {
			config = `{"image":"alpine:3.20","workspaceFolder":"/tmp","workspaceMount":""}`
		}
		writeTestFile(t, filepath.Join(app, ".devcontainer.json"), config)
		ws, _ := workspace()
		if value, ok := ws["workspaceMount"]; !ok || value != "" {
			t.Fatalf("empty workspaceMount = %v", ws)
		}
	}

	// Compose files own the mounts; there is no derived workspace mount.
	writeTestFile(t, filepath.Join(app, "compose.yml"), "services:\n  app:\n    image: alpine:3.20\n")
	for _, folder := range []string{"", "/src/app"} {
		config := `{"dockerComposeFile":"compose.yml","service":"app"}`
		wantFolder := "/"
		if folder != "" {
			config = `{"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/src/app"}`
			wantFolder = folder
		}
		writeTestFile(t, filepath.Join(app, ".devcontainer.json"), config)
		for _, args := range [][]string{nil, {"--mount-workspace-git-root=false"}, {"--mount-git-worktree-common-dir"}} {
			ws, _ := workspace(args...)
			if ws["workspaceFolder"] != wantFolder || ws["workspaceMount"] != nil || ws["additionalMountString"] != nil {
				t.Fatalf("compose folder %q flags %v workspace=%v", folder, args, ws)
			}
		}
	}

	// A linked worktree beside the repository mounts the common Git folder.
	admin := filepath.Join(repo, ".git", "worktrees", "topic")
	writeTestFile(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/topic\n")
	writeTestFile(t, filepath.Join(admin, "commondir"), "../..\n")
	tree := filepath.Join(filepath.Dir(repo), "topic")
	writeTestFile(t, filepath.Join(tree, ".git"), "gitdir: ../repo/.git/worktrees/topic\n")
	writeTestFile(t, filepath.Join(tree, ".devcontainer.json"), `{"image":"alpine:3.20"}`)
	result, _ := invoke(t, 0, "read-configuration", "--workspace-folder", tree, "--mount-git-worktree-common-dir")
	ws = result["workspace"].(map[string]any)
	// The worktree keeps its path below the folder that holds both.
	if ws["workspaceFolder"] != "/workspaces/topic" || ws["additionalMountString"] != "type=bind,source="+filepath.Join(repo, ".git")+",target=/workspaces/repo/.git" {
		t.Fatalf("worktree workspace=%v", ws)
	}
	result, _ = invoke(t, 0, "read-configuration", "--workspace-folder", tree)
	if ws = result["workspace"].(map[string]any); ws["additionalMountString"] != nil || ws["workspaceMount"] != "type=bind,source="+tree+",target=/workspaces/topic" {
		t.Fatalf("worktree without the flag=%v", ws)
	}
	writeTestFile(t, filepath.Join(tree, ".devcontainer.json"), `{"image":"alpine:3.20","workspaceFolder":"/src"}`)
	for _, tc := range []struct {
		args   []string
		common bool
	}{
		{nil, false},
		{[]string{"--mount-git-worktree-common-dir"}, true},
		{[]string{"--mount-workspace-git-root=false", "--mount-git-worktree-common-dir"}, false},
	} {
		args := append([]string{"read-configuration", "--workspace-folder", tree}, tc.args...)
		result, _ := invoke(t, 0, args...)
		ws := result["workspace"].(map[string]any)
		if ws["workspaceFolder"] != "/src" || ws["workspaceMount"] != "type=bind,source="+tree+",target=/workspaces/topic" {
			t.Fatalf("configured worktree folder %v=%v", tc.args, ws)
		}
		wantCommon := any(nil)
		if tc.common {
			wantCommon = "type=bind,source=" + filepath.Join(repo, ".git") + ",target=/workspaces/repo/.git"
		}
		if ws["additionalMountString"] != wantCommon {
			t.Fatalf("configured worktree common mount %v=%v", tc.args, ws)
		}
	}
}

func TestWorkspaceMountConsistency(t *testing.T) {
	repo, app := gitWorkspace(t, `{"image":"alpine:3.20"}`)
	dc, err := spec.Load(filepath.Join(app, ".devcontainer.json"))
	if err != nil {
		t.Fatal(err)
	}
	place := func(platform, consistency string, mounts ...string) (*mount.Mount, []mount.Mount) {
		t.Helper()
		previous := hostOS
		hostOS = platform
		defer func() { hostOS = previous }()
		a := &application{}
		o := &mountOptions{gitRoot: true, consistency: consistency, mounts: mounts}
		if err := o.prepare(a); err != nil {
			t.Fatal(err)
		}
		if err := a.placeWorkspace(dc, app); err != nil {
			t.Fatal(err)
		}
		ws, extra, err := a.dockerMounts()
		if err != nil {
			t.Fatal(err)
		}
		return ws, extra
	}
	ws, extra := place("darwin", "delegated", "type=volume,source=cache,target=/cache,external=true", "type=bind,source=/host/data,target=/data", "type=bind,source=relative/dir,target=/relative")
	if *ws != (mount.Mount{Type: mount.TypeBind, Source: repo, Target: "/workspaces/repo", Consistency: "delegated"}) {
		t.Fatalf("darwin workspace mount=%+v", ws)
	}
	// A relative bind source resolves against the current directory.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := []mount.Mount{{Type: mount.TypeVolume, Source: "cache", Target: "/cache"}, {Type: mount.TypeBind, Source: "/host/data", Target: "/data"}, {Type: mount.TypeBind, Source: filepath.Join(cwd, "relative", "dir"), Target: "/relative"}}
	if len(extra) != 3 || extra[0] != want[0] || extra[1] != want[1] || extra[2] != want[2] {
		t.Fatalf("mounts=%+v", extra)
	}
	if ws, extra = place("linux", "delegated"); ws.Consistency != "" || len(extra) != 0 {
		t.Fatalf("linux mounts=%+v %+v", ws, extra)
	}
}

func TestIDLabelsSubstituteDevcontainerID(t *testing.T) {
	_, app := gitWorkspace(t, `{"image":"alpine:3.20","containerEnv":{"ID":"${devcontainerId}"}}`)
	id := func(args ...string) any {
		result, _ := invoke(t, 0, append([]string{"read-configuration", "--workspace-folder", app}, args...)...)
		cfg, _ := result["configuration"].(map[string]any)
		env, _ := cfg["containerEnv"].(map[string]any)
		return env["ID"]
	}
	want := substitute.Substitute(substitute.Context{IDLabels: map[string]string{"example.test/id": "one", "example.test/role": "a=b"}}, "${devcontainerId}")
	if got := id("--id-label", "example.test/id=one", "--id-label", "example.test/role=a=b"); got != want {
		t.Fatalf("labelled id=%v, want %s", got, want)
	}
	if got := id(); got == want || got == "" {
		t.Fatalf("workspace id=%v", got)
	}
}

// With --id-label and no workspace or configuration flag, upstream reads no
// configuration; the one in the current directory must not be used.
func TestIDLabelsWithoutWorkspaceReadNoConfiguration(t *testing.T) {
	_, app := gitWorkspace(t, `{"image":"alpine:3.20","remoteEnv":{"FROM":"current-directory"}}`)
	t.Chdir(app)
	configuration := func(result map[string]any) map[string]any {
		cfg, _ := result["configuration"].(map[string]any)
		return cfg
	}
	result, _ := invoke(t, 0, "read-configuration", "--id-label", "example.test/id=one")
	if cfg, ok := result["configuration"].(map[string]any); !ok || len(cfg) != 0 || result["workspace"] != nil {
		t.Fatalf("label-only result=%v", result)
	}
	for _, flag := range []string{"--workspace-folder", "--config"} {
		value := "."
		if flag == "--config" {
			value = ".devcontainer.json"
		}
		result, _ = invoke(t, 0, "read-configuration", "--id-label", "example.test/id=one", flag, value)
		if configuration(result)["remoteEnv"] == nil || result["workspace"] == nil {
			t.Fatalf("explicit %s result=%v", flag, result)
		}
	}
	result, _ = invoke(t, 0, "read-configuration")
	if configuration(result)["remoteEnv"] == nil {
		t.Fatalf("default result=%v", result)
	}
}

// gitPrefixWorkspace selects a global config that includes etc/inc.cfg
// through %(prefix)/, and writes a file that Git rejects at
// base/deep/prefix/etc/inc.cfg. It returns base.
func gitPrefixWorkspace(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(base, "global.cfg")
	writeTestFile(t, global, "[include]\n\tpath = %(prefix)/etc/inc.cfg\n")
	writeTestFile(t, filepath.Join(base, "deep", "prefix", "etc", "inc.cfg"), "[broken\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return base
}

// --git-prefix names the prefix of the host Git's build. Without it, an
// include through %(prefix)/ and a relative path is not read; with it, a file
// that Git rejects there gives no Git root, so the workspace folder is
// mounted, as upstream mounts it.
func TestReadConfigurationGitPrefix(t *testing.T) {
	repo, app := gitWorkspace(t, `{"image":"alpine:3.20"}`)
	base := gitPrefixWorkspace(t)
	mount := func(args ...string) any {
		t.Helper()
		result, _ := invoke(t, 0, append([]string{"read-configuration", "--workspace-folder", app}, args...)...)
		ws, _ := result["workspace"].(map[string]any)
		return ws["workspaceMount"]
	}
	repoMount := "type=bind,source=" + repo + ",target=/workspaces/repo"
	appMount := "type=bind,source=" + app + ",target=/workspaces/app"
	prefix := filepath.Join(base, "deep", "prefix")
	if got := mount(); got != repoMount {
		t.Fatalf("without --git-prefix: %v, want %s", got, repoMount)
	}
	if got := mount("--git-prefix", prefix); got != appMount {
		t.Fatalf("--git-prefix %s: %v, want %s", prefix, got, appMount)
	}
	if got := mount("--git-prefix", filepath.Join(base, "deep")); got != repoMount {
		t.Fatalf("other --git-prefix: %v, want %s", got, repoMount)
	}
	if got := mount("--git-prefix", prefix, "--mount-workspace-git-root=false"); got != appMount {
		t.Fatalf("--git-prefix without the Git root: %v", got)
	}
	// A relative value starts at the current folder, and its components are
	// followed: link/../prefix is deep/prefix through the link, where a
	// lexically cleaned value would name cwd/prefix.
	cwd := filepath.Join(base, "cwd")
	writeTestFile(t, filepath.Join(cwd, "prefix", "etc", "inc.cfg"), "[core]\n")
	if err := os.Symlink(filepath.Join(base, "deep", "inner"), filepath.Join(cwd, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "deep", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	for _, value := range []string{"../deep/prefix", "link/../prefix"} {
		if got := mount("--git-prefix", value); got != appMount {
			t.Fatalf("--git-prefix %s: %v, want %s", value, got, appMount)
		}
	}
	if got := mount("--git-prefix", "prefix"); got != repoMount {
		t.Fatalf("--git-prefix prefix: %v, want %s", got, repoMount)
	}
}
