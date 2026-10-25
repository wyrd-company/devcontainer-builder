package dockerfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// resolveLocal resolves local features from their devcontainer-feature.json
// documents, so the metadata keeps its containerEnv declaration order.
func resolveLocal(t *testing.T, docs ...string) []features.Resolved {
	t.Helper()
	base := t.TempDir()
	m := spec.OrderedMap{Values: map[string]json.RawMessage{}}
	for _, doc := range docs {
		var head struct{ ID string }
		if err := json.Unmarshal([]byte(doc), &head); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(base, head.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		m.Keys = append(m.Keys, "./"+head.ID)
		m.Values["./"+head.ID] = json.RawMessage(`{}`)
	}
	resolved, err := features.New().Resolve(m, nil, t.TempDir(), base)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// The generated Dockerfile writes a feature's containerEnv as the reference
// CLI's generateContainerEnvs does: one ENV per key, in declaration order,
// double-quoted with `$` left for the Dockerfile. The spec's containerEnv
// stays literal. The Dockerfile parser and lexer then give the values the
// reference build gives.
func TestGenerate_FeatureContainerEnvIsReferenceEnvLines(t *testing.T) {
	resolved := resolveLocal(t,
		`{"id":"env-base","version":"1.0.0","containerEnv":{"SAMPLE_ROOT":"/opt/sample","PATH":"/opt/sample/bin:${PATH}"}}`,
		`{"id":"env-chain","version":"1.0.0","installsAfter":["./env-base"],"containerEnv":{"ZZ_HOME":"${SAMPLE_ROOT}/zz","AA_BIN":"${ZZ_HOME}/bin","QUOTED":"say \"hi\" \\ back"}}`)
	out, err := Generate(Inputs{
		Spec:               &spec.DevContainer{Image: "alpine", ContainerEnv: map[string]any{"SPEC_REF": "${SAMPLE_ROOT}/spec"}},
		BaseImage:          "alpine",
		ImageUser:          "root",
		Features:           resolved,
		FeaturesContextDir: "_features",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []string{
		`ENV SAMPLE_ROOT="/opt/sample"`,
		`ENV PATH="/opt/sample/bin:${PATH}"`,
		`ENV ZZ_HOME="${SAMPLE_ROOT}/zz"`,
		`ENV AA_BIN="${ZZ_HOME}/bin"`,
		`ENV QUOTED="say \"hi\" \\ back"`,
		`ENV SPEC_REF='${SAMPLE_ROOT}/spec'`,
	}
	var got []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "ENV ") && !strings.HasPrefix(line, "ENV _") {
			got = append(got, line)
		}
	}
	if !slices.Equal(got, wantLines) {
		t.Fatalf("ENV lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantLines, "\n"))
	}

	parsed, err := parser.Parse(strings.NewReader(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/usr/bin:/bin"}
	lex := shell.NewLex(parser.DefaultEscapeToken)
	for _, node := range parsed.AST.Children {
		if !strings.EqualFold(node.Value, "env") {
			continue
		}
		instruction, err := instructions.ParseInstruction(node)
		if err != nil {
			t.Fatal(err)
		}
		for _, kv := range instruction.(*instructions.EnvCommand).Env {
			value, _, err := lex.ProcessWord(kv.Value, shell.EnvsFromSlice(env))
			if err != nil {
				t.Fatal(err)
			}
			env = slices.DeleteFunc(env, func(e string) bool { return strings.HasPrefix(e, kv.Key+"=") })
			env = append(env, kv.Key+"="+value)
		}
	}
	for _, want := range []string{
		"PATH=/opt/sample/bin:/usr/bin:/bin",
		"ZZ_HOME=/opt/sample/zz",
		"AA_BIN=/opt/sample/zz/bin",
		`QUOTED=say "hi" \ back`,
		"SPEC_REF=${SAMPLE_ROOT}/spec",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("Dockerfile env missing %q: %q", want, env)
		}
	}
}

// The generated Dockerfile emits array-index keys first, in numeric order,
// as the reference CLI's Object.keys order does.
func TestGenerate_FeatureContainerEnvArrayIndexKeysFirst(t *testing.T) {
	resolved := resolveLocal(t, `{"id":"env-index","version":"1.0.0","containerEnv":{"RESULT":"${1:-u}","01":"a","10":"b","1":"c","4294967295":"d"}}`)
	out, err := Generate(Inputs{Spec: &spec.DevContainer{Image: "alpine"}, BaseImage: "alpine", ImageUser: "root", Features: resolved, FeaturesContextDir: "_features"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "ENV ") && !strings.HasPrefix(line, "ENV _") {
			got = append(got, line)
		}
	}
	want := []string{`ENV 1="c"`, `ENV 10="b"`, `ENV RESULT="${1:-u}"`, `ENV 01="a"`, `ENV 4294967295="d"`}
	if !slices.Equal(got, want) {
		t.Fatalf("ENV lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
