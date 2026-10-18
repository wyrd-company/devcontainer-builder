package dockercompose

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestOverrideYAML_KeepaliveRunsFeatureEntrypoints(t *testing.T) {
	out, err := OverrideYAML(OverrideInputs{
		Spec:            &spec.DevContainer{Service: "app"},
		OverrideCommand: true,
		Entrypoints:     []string{"/usr/local/share/alpha-init.sh", "/usr/local/share/beta-init.sh"},
	})
	if err != nil {
		t.Fatalf("OverrideYAML: %v", err)
	}
	var doc struct {
		Services map[string]struct {
			Command []string `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	cmd := doc.Services["app"].Command
	want := "trap \"exit 0\" 15\n/usr/local/share/alpha-init.sh\n/usr/local/share/beta-init.sh\nexec \"$@\""
	if len(cmd) != 2 || !strings.Contains(cmd[0], want) {
		t.Fatalf("command = %q, want script containing %q", cmd, want)
	}
}
