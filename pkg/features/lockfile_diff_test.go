// relationships:
//
//	references: [lockfile, ../../reference/cli/src/spec-configuration/lockfile]
package features

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Execute the pinned upstream implementation with its filesystem imports bound
// to Node's real filesystem. Node strips types; no copied lockfile algorithm or
// installed upstream dependency tree is involved.
func TestLockfileUpstreamDifferential(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is unavailable")
	}
	capability, err := exec.Command(node, "-p", "typeof require('node:module').stripTypeScriptTypes").CombinedOutput()
	if err != nil {
		t.Fatalf("Node capability probe: %v: %s", err, capability)
	}
	if string(bytes.TrimSpace(capability)) != "function" {
		t.Skip("Node's TypeScript stripping API is unavailable")
	}
	upstream, err := filepath.Abs("../../reference/cli/src/spec-configuration/lockfile.ts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(upstream); os.IsNotExist(err) {
		t.Skip("reference/cli is not initialized")
	}
	resolved := lockedSample()
	for _, key := range []string{"registry.example/collection/B:1", "registry.example/collection/a:1", "registry.example/collection/A:1"} {
		f := resolved[0]
		f.Source = key
		resolved = append(resolved, f)
	}
	resolved = append(resolved, Resolved{Source: "./local", Metadata: Metadata{Version: "2.0.0"}}, Resolved{Source: "https://registry.example/devcontainer-feature-sample.tgz", Tarball: "https://registry.example/devcontainer-feature-sample.tgz", Digest: resolved[0].Digest, Metadata: Metadata{Version: "3.0.0", DependsOn: userFeatures(resolved[0].Source, `{}`)}})
	sets := []any{}
	for _, f := range resolved {
		source := map[string]any{"type": "file-path", "userFeatureId": f.Source}
		if f.Reference != nil {
			source["type"], source["featureRef"] = "oci", map[string]any{"registry": f.Reference.Registry, "path": f.Reference.Repository}
		}
		if f.Tarball != "" {
			source["type"], source["tarballUri"] = "direct-tarball", f.Tarball
		}
		deps := map[string]json.RawMessage{}
		for _, key := range f.Metadata.DependsOn.Keys {
			deps[key] = f.Metadata.DependsOn.Values[key]
		}
		sets = append(sets, map[string]any{"sourceInformation": source, "computedDigest": f.Digest, "features": []any{map[string]any{"version": f.Metadata.Version, "dependsOn": deps}}})
	}
	input, err := json.Marshal(map[string]any{"featureSets": sets})
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs = require('node:fs');
const path = require('node:path');
const {stripTypeScriptTypes} = require('node:module');
let source = fs.readFileSync(process.argv[1], 'utf8').replace(/^import .*;\r?$/gm, '').replace(/^export /gm, '');
source = stripTypeScriptTypes(source);
const load = new Function('path', 'readLocalFile', 'writeLocalFile', source + '; return {generateLockfile, writeLockfile, getLockfilePath};');
const api = load(path, fs.promises.readFile, fs.promises.writeFile);
(async () => {
 const generated = await api.generateLockfile(JSON.parse(fs.readFileSync(0, 'utf8')));
 const config = {configFilePath:{fsPath:path.join(process.argv[2], '.custom.json')}};
 const destination = api.getLockfilePath(config);
 const outcomes = [];
 for (const test of [
  {create:false, frozen:false},
  {create:true, frozen:false},
  {old:'{"features":{}}', create:false, frozen:false},
  {old:'', create:false, frozen:false},
  {create:false, frozen:true},
  {old:'{"features":{}}', create:true, frozen:true},
  {old:JSON.stringify(generated)+'\n\t ', create:false, frozen:true},
  {old:JSON.stringify(generated).replaceAll('registry', '\\u0072egistry'), create:false, frozen:true},
  {old:JSON.stringify({features:Object.fromEntries(Object.entries(generated.features).reverse())}), create:false, frozen:true},
 ]) {
  await fs.promises.rm(destination, {force:true});
  if (test.old !== undefined) await fs.promises.writeFile(destination, test.old);
  let error = '';
  try {await api.writeLockfile({experimentalLockfile:test.create,experimentalFrozenLockfile:test.frozen}, config, generated);} catch (e) {error=e.message;}
  let content = null;
  try {content = await fs.promises.readFile(destination, 'utf8');} catch (e) {if(e.code!=='ENOENT') throw e;}
  outcomes.push({test, error, content});
 }
 process.stdout.write(JSON.stringify({generated,outcomes,path:destination}));
})().catch(e => {console.error(e); process.exitCode=1;});`
	cmd := exec.Command(node, "--disable-warning=ExperimentalWarning", "-e", script, upstream, t.TempDir())
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upstream: %v\n%s", err, output)
	}
	var result struct {
		Generated Lockfile `json:"generated"`
		Path      string   `json:"path"`
		Outcomes  []struct {
			Test struct {
				Old    *string `json:"old"`
				Create bool    `json:"create"`
				Frozen bool    `json:"frozen"`
			} `json:"test"`
			Error   string  `json:"error"`
			Content *string `json:"content"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("output=%s: %v", output, err)
	}
	want, _ := json.Marshal(result.Generated)
	got, _ := json.Marshal(GenerateLockfile(resolved))
	if !bytes.Equal(got, want) {
		t.Fatalf("generated=%s upstream=%s", got, want)
	}
	for i, outcome := range result.Outcomes {
		config := filepath.Join(t.TempDir(), ".custom.json")
		if outcome.Test.Old != nil {
			if err := os.WriteFile(LockfilePath(config), []byte(*outcome.Test.Old), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		state, err := ReadLockfile(config, outcome.Test.Create, outcome.Test.Frozen)
		if err != nil {
			t.Fatal(err)
		}
		err = state.Prepare(resolved)
		if err == nil {
			err = state.Publish(t.Context())
		}
		message := ""
		if err != nil {
			message = err.Error()
		}
		if message != outcome.Error {
			t.Fatalf("case %d: err=%q upstream=%q", i, message, outcome.Error)
		}
		content, readErr := os.ReadFile(LockfilePath(config))
		if outcome.Content == nil {
			if !os.IsNotExist(readErr) {
				t.Fatalf("case %d unexpectedly wrote %s", i, content)
			}
		} else if readErr != nil || string(content) != *outcome.Content {
			t.Fatalf("case %d content=%q upstream=%q err=%v", i, content, *outcome.Content, readErr)
		}
	}
	if filepath.Base(result.Path) != filepath.Base(LockfilePath(".custom.json")) {
		t.Fatal("upstream lockfile name differs")
	}
}
