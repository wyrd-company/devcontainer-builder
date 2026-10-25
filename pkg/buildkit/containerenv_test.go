package buildkit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/buildkit/util/system"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

const basePath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// localFeatures resolves local features from their devcontainer-feature.json
// documents, so the metadata keeps its containerEnv declaration order.
func localFeatures(t *testing.T, docs ...string) []features.Resolved {
	t.Helper()
	base := t.TempDir()
	entries := map[string]json.RawMessage{}
	var keys []string
	for _, doc := range docs {
		var head struct{ ID string }
		if err := json.Unmarshal([]byte(doc), &head); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(base, head.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string]string{"devcontainer-feature.json": doc, "install.sh": "#!/bin/sh\n"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		keys = append(keys, "./"+head.ID)
		entries["./"+head.ID] = json.RawMessage(`{}`)
	}
	resolved, err := features.New().Resolve(spec.OrderedMap{Keys: keys, Values: entries}, nil, t.TempDir(), base)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// chainedFeatures are two features in install order. The second reads the
// first's value, its own earlier keys (declared before keys that sort
// earlier), an unset name, a default, quotes and a backslash.
func chainedFeatures(t *testing.T) []features.Resolved {
	return localFeatures(t,
		`{"id":"env-base","version":"1.0.0","containerEnv":{"SAMPLE_ROOT":"/opt/sample","PATH":"/opt/sample/bin:${PATH}"}}`,
		`{"id":"env-chain","version":"1.0.0","installsAfter":["./env-base"],"containerEnv":{
			"ZZ_HOME":"${SAMPLE_ROOT}/zz",
			"AA_BIN":"${ZZ_HOME}/bin",
			"MM_UNSET":"[${SAMPLE_UNSET}]",
			"QQ_DEFAULT":"${SAMPLE_UNSET:-fallback}",
			"QUOTED":"say \"hi\" \\ back",
			"BACKSLASH":"\\$SAMPLE_ROOT"}}`)
}

// wantChained is the environment the reference CLI's Dockerfile gives the
// second feature's install and the final image.
var wantChained = []string{
	"PATH=/opt/sample/bin:" + basePath,
	"SAMPLE_ROOT=/opt/sample",
	"ZZ_HOME=/opt/sample/zz",
	"AA_BIN=/opt/sample/zz/bin",
	"MM_UNSET=[]",
	"QQ_DEFAULT=fallback",
	`QUOTED=say "hi" \ back`,
	`BACKSLASH=\/opt/sample`,
}

func installEnv(t *testing.T, g graphOps, f features.Resolved) []string {
	t.Helper()
	return g.execWithArg(t, features.InstallCommand(features.InstallDir+"/"+f.SafeID)).GetExec().Meta.Env
}

func configEnv(t *testing.T, cfg []byte) []string {
	t.Helper()
	var img struct{ Config struct{ Env []string } }
	if err := json.Unmarshal(cfg, &img); err != nil {
		t.Fatal(err)
	}
	return img.Config.Env
}

func assertEnv(t *testing.T, where string, got, want []string) {
	t.Helper()
	for _, kv := range want {
		if !slices.Contains(got, kv) {
			t.Errorf("%s: missing %q in %q", where, kv, got)
		}
	}
}

// A feature's containerEnv reads the environment as built, as a Dockerfile
// ENV does: the base image's ENV, earlier features and the feature's own
// earlier keys. The install step and the final image config agree. The
// spec's containerEnv stays literal, as the reference CLI escapes `$` there.
func TestBuildOnePlatform_FeatureContainerEnvExpandsLikeDockerfileEnv(t *testing.T) {
	for _, upstream := range []bool{false, true} {
		resolved := chainedFeatures(t)
		gc := &fakeGateway{baseConfig: []byte(`{"config":{"Env":["PATH=` + basePath + `","BASE_ONLY=base"]}}`)}
		in := featureGraphInputs(resolved[0])
		in.Features = resolved
		in.Spec.ContainerEnv = map[string]any{"SPEC_REF": "${SAMPLE_ROOT}/spec"}
		in.ResolveImageConfig = true
		bs := BuildSpec{Graph: in}
		if upstream {
			bs.Upstream = &DockerfileBuild{ContextDir: "."}
		}
		_, cfg, err := buildOnePlatform(context.Background(), gc, bs, ocispecs.Platform{OS: "linux", Architecture: "amd64"}, buildSettings{})
		if err != nil {
			t.Fatal(err)
		}
		g := solvedGraph(t, gc)
		assertEnv(t, "first install", installEnv(t, g, resolved[0]), []string{"PATH=/opt/sample/bin:" + basePath, "BASE_ONLY=base"})
		chain := installEnv(t, g, resolved[1])
		assertEnv(t, "second install", chain, wantChained)
		if slices.ContainsFunc(chain, func(kv string) bool { return strings.HasPrefix(kv, "SPEC_REF=") }) {
			t.Errorf("spec containerEnv visible to a feature install: %q", chain)
		}
		final := configEnv(t, cfg)
		assertEnv(t, "image config", final, append([]string{"BASE_ONLY=base", "SPEC_REF=${SAMPLE_ROOT}/spec"}, wantChained...))
	}
}

// Without a PATH in the base image the Dockerfile frontend reads the
// platform default, so a feature that extends PATH keeps the system folders.
func TestBuildOnePlatform_FeatureContainerEnvDefaultsPath(t *testing.T) {
	resolved := localFeatures(t, `{"id":"env-path","version":"1.0.0","containerEnv":{"PATH":"/opt/tool/bin:${PATH}"}}`)
	gc := &fakeGateway{baseConfig: []byte(`{"config":{"Env":["OTHER=1"]}}`)}
	in := featureGraphInputs(resolved[0])
	in.ResolveImageConfig = true
	_, cfg, err := buildOnePlatform(context.Background(), gc, BuildSpec{Graph: in}, ocispecs.Platform{OS: "linux", Architecture: "amd64"}, buildSettings{})
	if err != nil {
		t.Fatal(err)
	}
	want := "PATH=/opt/tool/bin:" + system.DefaultPathEnv("linux")
	assertEnv(t, "install", installEnv(t, solvedGraph(t, gc), resolved[0]), []string{want})
	assertEnv(t, "image config", configEnv(t, cfg), []string{want})
}

// A substitution that a Dockerfile rejects fails the build with the feature
// and key named, on both surfaces. The devcontainer variable syntax is one:
// the reference CLI does not substitute feature values before its Dockerfile.
func TestFeatureContainerEnvRejectedSubstitutionFails(t *testing.T) {
	for _, value := range []string{"${SAMPLE_UNSET:?is required}", "${localEnv:HOME}"} {
		resolved := localFeatures(t, `{"id":"env-bad","version":"1.0.0","containerEnv":{"BAD":"`+value+`"}}`)
		in := featureGraphInputs(resolved[0])
		state, err := Build(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.Marshal(context.Background()); err == nil || !strings.Contains(err.Error(), "feature env-bad: containerEnv BAD") {
			t.Errorf("%s: llb error = %v", value, err)
		}
		if _, err := mergeImageConfig(nil, in, nil, ocispecs.Platform{OS: "linux"}); err == nil || !strings.Contains(err.Error(), "feature env-bad: containerEnv BAD") {
			t.Errorf("%s: image config error = %v", value, err)
		}
	}
}

// Array-index keys apply before the others, as the reference CLI's
// Object.keys order emits them, on the install step and in the image config.
func TestBuildOnePlatform_FeatureContainerEnvArrayIndexKeysFirst(t *testing.T) {
	resolved := localFeatures(t, `{"id":"env-index","version":"1.0.0","containerEnv":{
		"RESULT":"${1:-u}-${10:-u}-${4294967294:-u}-${01:-u}-${4294967295:-u}",
		"01":"a", "10":"${2:-u}b", "4294967295":"c", "1":"d", "2":"two", "4294967294":"e"}}`)
	gc := &fakeGateway{baseConfig: []byte(`{"config":{"Env":["PATH=` + basePath + `"]}}`)}
	in := featureGraphInputs(resolved[0])
	in.ResolveImageConfig = true
	_, cfg, err := buildOnePlatform(context.Background(), gc, BuildSpec{Graph: in}, ocispecs.Platform{OS: "linux", Architecture: "amd64"}, buildSettings{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"RESULT=d-twob-e-u-u", "10=twob", "01=a", "4294967295=c"}
	assertEnv(t, "install", installEnv(t, solvedGraph(t, gc), resolved[0]), want)
	assertEnv(t, "image config", configEnv(t, cfg), want)
}
