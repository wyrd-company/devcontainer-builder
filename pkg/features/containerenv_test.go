package features

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The reference CLI applies a feature's containerEnv keys in declaration
// order, so the order must survive reading devcontainer-feature.json,
// including the legacy migration that re-encodes the document.
func TestReadMetadataKeepsContainerEnvDeclarationOrder(t *testing.T) {
	want := []string{"ZZ_HOME", "AA_BIN", "MM_VALUE"}
	for _, tc := range []struct{ name, input string }{
		{"current", `{"id":"sample","containerEnv":{"ZZ_HOME":"/zz","AA_BIN":"${ZZ_HOME}/bin","MM_VALUE":"m"}}`},
		{"legacy migration", `{"id":"sample","extensions":["one"],"containerEnv":{"ZZ_HOME":"/zz","AA_BIN":"${ZZ_HOME}/bin","MM_VALUE":"m"}}`},
		{"jsonc and repeated key", `{
			// comment
			"id": "sample",
			"containerEnv": {"ZZ_HOME": "/old", "AA_BIN": "${ZZ_HOME}/bin", "ZZ_HOME": "/zz", "MM_VALUE": "m",},
		}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(tc.input), 0o644); err != nil {
				t.Fatal(err)
			}
			meta, err := readMetadata(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := meta.ContainerEnvKeys(); !reflect.DeepEqual(got, want) {
				t.Fatalf("ContainerEnvKeys() = %q, want %q", got, want)
			}
			if got := meta.ContainerEnv["ZZ_HOME"]; got != "/zz" {
				t.Fatalf("ZZ_HOME = %q, want the last value", got)
			}
		})
	}
}

func TestContainerEnvKeysWithoutRecordedOrderAreSorted(t *testing.T) {
	m := Metadata{ContainerEnv: map[string]string{"B": "2", "A": "1"}}
	if got := m.ContainerEnvKeys(); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("ContainerEnvKeys() = %q", got)
	}
}

// QuoteContainerEnv writes the value as the reference CLI's
// generateContainerEnvs does: `"` and `\` escaped, `$` untouched.
func TestQuoteContainerEnv(t *testing.T) {
	for in, want := range map[string]string{
		`/usr/local/go/bin:${PATH}`: `"/usr/local/go/bin:${PATH}"`,
		`say "hi" \ back`:           `"say \"hi\" \\ back"`,
		``:                          `""`,
	} {
		if got := QuoteContainerEnv(in); got != want {
			t.Errorf("QuoteContainerEnv(%q) = %s, want %s", in, got, want)
		}
	}
}
