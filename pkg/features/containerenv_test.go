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

// The reference CLI reads the keys with ECMAScript Object.keys: keys that are
// array indices (canonical decimal integers from 0 to 2^32-2) come first in
// ascending numeric order, then the other keys in declaration order.
func TestContainerEnvKeysFollowObjectKeysOrder(t *testing.T) {
	doc := `{"id":"sample","containerEnv":{
		"RESULT":"r", "01":"a", "10":"b", "4294967295":"c", "2":"d",
		"4294967294":"e", "-1":"f", "+1":"g", "1.0":"h", " 1":"i", "0":"j", "":"k", "1e3":"l"}}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := readMetadata(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0", "2", "10", "4294967294", "RESULT", "01", "4294967295", "-1", "+1", "1.0", " 1", "", "1e3"}
	if got := meta.ContainerEnvKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ContainerEnvKeys() = %q, want %q", got, want)
	}
}

func TestIsArrayIndex(t *testing.T) {
	for key, want := range map[string]bool{
		"0": true, "1": true, "10": true, "4294967294": true,
		"4294967295": false, "4294967296": false, "01": false, "00": false, "-0": false,
		"-1": false, "+1": false, "1.0": false, "1e3": false, " 1": false, "1 ": false, "": false, "0x1": false,
	} {
		if got := isArrayIndex(key); got != want {
			t.Errorf("isArrayIndex(%q) = %v, want %v", key, got, want)
		}
	}
}
