package features

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeEnvID(t *testing.T) {
	cases := map[string]string{
		"version":       "VERSION",
		"my-option.1":   "MY_OPTION_1",
		"installZsh":    "INSTALLZSH",
		"1st-option":    "_ST_OPTION",
		"__private":     "_PRIVATE",
		"_9lives":       "_LIVES",
		"with space":    "WITH_SPACE",
		"already_SAFE2": "ALREADY_SAFE2",
	}
	for in, want := range cases {
		if got := SafeEnvID(in); got != want {
			t.Errorf("SafeEnvID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContainerUsers(t *testing.T) {
	cases := []struct {
		container, remote, image  string
		wantContainer, wantRemote string
	}{
		{"", "", "", "root", "root"},
		{"", "", "imageuser", "imageuser", "imageuser"},
		{"cuser", "", "imageuser", "cuser", "cuser"},
		{"", "ruser", "imageuser", "imageuser", "ruser"},
		{"cuser", "ruser", "", "cuser", "ruser"},
	}
	for _, c := range cases {
		gc, gr := ContainerUsers(c.container, c.remote, c.image)
		if gc != c.wantContainer || gr != c.wantRemote {
			t.Errorf("ContainerUsers(%q,%q,%q) = %q,%q want %q,%q",
				c.container, c.remote, c.image, gc, gr, c.wantContainer, c.wantRemote)
		}
	}
}

func TestOptionValues_DefaultsOverlaidByConfigured(t *testing.T) {
	r := Resolved{
		Options: map[string]any{"color": "blue"},
		Metadata: Metadata{Options: map[string]Option{
			"color": {Type: "string", Default: "red"},
			"size":  {Type: "string", Default: "small"},
			"plain": {Type: "string"},
		}},
	}
	got := r.OptionValues()
	if got["color"] != "blue" || got["size"] != "small" {
		t.Fatalf("values = %v", got)
	}
	if _, ok := got["plain"]; ok {
		t.Fatalf("option without default must be absent: %v", got)
	}
}

func TestEnvFileContent(t *testing.T) {
	r := Resolved{
		Options: map[string]any{
			"my-option.1": "two words",
			"enabled":     true,
			"count":       float64(3),
			"list":        []any{"a", "b"},
			"tricky":      `say "hi" $HOME ` + "`x` \\",
			"nothing":     nil,
			"nested":      map[string]any{"a": "b"},
		},
	}
	want := strings.Join([]string{
		`COUNT="3"`,
		`ENABLED="true"`,
		`LIST="a,b"`,
		`MY_OPTION_1="two words"`,
		`NESTED="[object Object]"`,
		`NOTHING="null"`,
		`TRICKY="say \"hi\" \$HOME \` + "`x\\`" + ` \\"`,
	}, "\n") + "\n"
	if got := r.EnvFileContent(); got != want {
		t.Fatalf("env file:\n%s\nwant:\n%s", got, want)
	}
}

func TestSourceWithoutVersion(t *testing.T) {
	cases := map[string]string{
		"registry.example.test:5000/collection/alpha:1":   "registry.example.test:5000/collection/alpha",
		"registry.example.test/collection/alpha@sha256:0": "registry.example.test/collection/alpha",
		"registry.example.test/collection/alpha":          "registry.example.test/collection/alpha",
	}
	for src, want := range cases {
		r := ociNode(t, "x:1").res
		r.Source = src
		if got := r.SourceWithoutVersion(); got != want {
			t.Errorf("SourceWithoutVersion(%q) = %q, want %q", src, got, want)
		}
	}
	local := Resolved{Source: "./local:feature"}
	if got := local.SourceWithoutVersion(); got != "./local:feature" {
		t.Errorf("local source changed: %q", got)
	}
}

// stageFeature lays out a feature the way both build paths do and returns
// the feature directory.
func stageFeature(t *testing.T, r Resolved, containerUser, remoteUser string) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "feature")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "install-env.txt")
	install := "#!/bin/sh\nenv > " + singleQuote(out) + "\n"
	files := map[string]string{
		"install.sh":       install,
		"entrypoint.sh":    "#!/bin/sh\ntouch " + singleQuote(filepath.Join(root, "entrypoint-ran")) + "\n",
		EnvFile:            r.EnvFileContent(),
		InstallWrapperFile: r.InstallWrapperContent(),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	builtin := filepath.Join(root, BuiltinEnvFile)
	if err := os.WriteFile(builtin, []byte(BuiltinEnvContent(containerUser, remoteUser)), 0o644); err != nil {
		t.Fatal(err)
	}
	runShell(t, BuiltinHomesCommand(builtin, containerUser, remoteUser))
	return dir, out
}

func runShell(t *testing.T, script string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sh -c %q: %v\n%s", script, err, out)
	}
	return string(out)
}

func TestInstallWrapper_ExportsOptionsAndBuiltinsToInstallScript(t *testing.T) {
	r := Resolved{
		Source:  "./alpha",
		Options: map[string]any{"my-option.1": "two words $HOME", "enabled": false, "quoted": "a \"b\" `c` \\d"},
		Metadata: Metadata{
			ID:         "alpha",
			Name:       "Alpha's feature",
			Entrypoint: "./entrypoint.sh",
			Options:    map[string]Option{"level": {Type: "string", Default: "high"}},
		},
	}
	dir, out := stageFeature(t, r, "root", "nobody-such-user")
	log := runShell(t, InstallCommand(dir))
	if !strings.Contains(log, "Feature       : Alpha's feature") {
		t.Fatalf("wrapper banner missing:\n%s", log)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("install.sh did not run: %v", err)
	}
	env := string(data)
	for _, want := range []string{
		"MY_OPTION_1=two words $HOME",
		"ENABLED=false",
		"QUOTED=a \"b\" `c` \\d\n",
		"LEVEL=high",
		"_CONTAINER_USER=root",
		"_REMOTE_USER=nobody-such-user",
		"_CONTAINER_USER_HOME=/root",
		"_REMOTE_USER_HOME=\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("install.sh env missing %q:\n%s", want, env)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "entrypoint-ran")); err == nil {
		t.Fatal("feature entrypoint ran at install time")
	}
}

func TestInstallWrapper_FailingInstallReportsFeature(t *testing.T) {
	r := Resolved{Source: "./alpha", Metadata: Metadata{ID: "alpha", Name: "Alpha"}}
	dir, _ := stageFeature(t, r, "root", "root")
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", InstallCommand(dir))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(string(out), `ERROR: Feature "Alpha" (./alpha) failed to install!`) {
		t.Fatalf("missing failure message:\n%s", out)
	}
}
