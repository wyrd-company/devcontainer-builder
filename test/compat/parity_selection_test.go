// relationships:
//
//	references: [../parity/README, ci_integration_test]
package compat_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"testing"
)

// Ask Go to select the files so this checks build constraints, not filenames.
func TestParityTestSelection(t *testing.T) {
	live := []string{
		"TestObserveComparedLocalFolderDivergence", "TestObserveFixtureSideIsolation",
		"TestObserveRejectsAmbiguousIdentity", "TestCleanupRemovesOnlyRunNamedResources",
		"TestObserveEffectiveUser", "TestObserveEffectiveUserUnavailable",
		"TestObserveEffectiveUserProbeFailure",
	}
	for _, tagged := range []bool{false, true} {
		args := []string{"list", "-json"}
		if tagged {
			args = append(args, "-tags=docker_integration")
		}
		args = append(args, "../parity")
		body, err := exec.Command("go", args...).Output()
		if err != nil {
			t.Fatal(err)
		}
		var pkg struct {
			Dir         string
			TestGoFiles []string
		}
		if err := json.Unmarshal(body, &pkg); err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, name := range pkg.TestGoFiles {
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
					names[fn.Name.Name] = true
				}
			}
		}
		for _, name := range live {
			if names[name] != tagged {
				t.Errorf("docker_integration=%t: %s selected=%t", tagged, name, names[name])
			}
		}
		for _, name := range []string{"TestObserveVerifiesInspectedIdentity", "TestRealDockerConfigIsolation"} {
			if !names[name] {
				t.Errorf("docker_integration=%t: missing unit control %s", tagged, name)
			}
		}
	}
}
