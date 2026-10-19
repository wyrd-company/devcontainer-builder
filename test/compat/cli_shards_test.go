// relationships:
//
//	references: [../../scripts/cli-integration-shards, ci_integration_test]
package compat_test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type cliShard struct {
	name    string
	pattern *regexp.Regexp
}

func readCLIShards(t *testing.T) []cliShard {
	t.Helper()
	body, err := os.ReadFile("../../scripts/cli-integration-shards.tsv")
	if err != nil {
		t.Fatal(err)
	}
	var shards []cliShard
	names := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		name, pattern, ok := strings.Cut(line, "\t")
		if !ok || names[name] || name == "all" || !regexp.MustCompile(`^[a-z]+$`).MatchString(name) {
			t.Fatalf("invalid or duplicate shard: %q", line)
		}
		if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") || strings.Contains(pattern, "/") {
			t.Fatalf("shard must select whole top-level names: %q", line)
		}
		names[name] = true
		shards = append(shards, cliShard{name, regexp.MustCompile(pattern)})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(shards) == 0 {
		t.Fatal("empty CLI shard manifest")
	}
	return shards
}

// Discover tagged files using Go's build constraints, rather than a test-name
// prefix or filename convention. New integration test names must opt into a shard.
func cliDockerTests(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	plain := build.Default
	tagged := build.Default
	tagged.BuildTags = append(append([]string{}, tagged.BuildTags...), "docker_integration")
	var names []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		included, err := tagged.MatchFile(dir, entry.Name())
		if err != nil {
			return nil, err
		}
		unit, err := plain.MatchFile(dir, entry.Name())
		if err != nil {
			return nil, err
		}
		if !included || unit {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				names = append(names, fn.Name.Name)
			}
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no CLI Docker tests discovered in %s", dir)
	}
	return names, nil
}

func cliShardCoverage(names []string, shards []cliShard) error {
	counts := make([]int, len(shards))
	for _, name := range names {
		var owners []string
		for i, shard := range shards {
			if shard.pattern.MatchString(name) {
				owners = append(owners, shard.name)
				counts[i]++
			}
		}
		if len(owners) != 1 {
			return fmt.Errorf("Docker test %s belongs to %d shards: %v", name, len(owners), owners)
		}
	}
	for i, count := range counts {
		if count == 0 {
			return fmt.Errorf("CLI shard %s selects no Docker tests", shards[i].name)
		}
	}
	return nil
}

func TestCLIDockerShardCoverage(t *testing.T) {
	names, err := cliDockerTests("../../cmd/dcb")
	if err != nil {
		t.Fatal(err)
	}
	shards := readCLIShards(t)
	if err := cliShardCoverage(names, shards); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d Docker tests covered exactly once by %d CLI shards", len(names), len(shards))
}

func TestCLIDockerShardCoverageRejectsGapsAndOverlap(t *testing.T) {
	dir := t.TempDir()
	body := "//go:build docker_integration\n\npackage sample\nimport \"testing\"\nfunc TestUnassignedSample(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := cliDockerTests(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cliShardCoverage(names, readCLIShards(t)); err == nil || !strings.Contains(err.Error(), "TestUnassignedSample belongs to 0 shards") {
		t.Fatalf("new test outside manifest was not rejected: %v", err)
	}
	shards := []cliShard{{"first", regexp.MustCompile("^TestUnassignedSample$")}, {"second", regexp.MustCompile("^TestUnassignedSample$")}}
	if err := cliShardCoverage(names, shards); err == nil || !strings.Contains(err.Error(), "belongs to 2 shards") {
		t.Fatalf("overlap was not rejected: %v", err)
	}
	if _, err := cliDockerTests(t.TempDir()); err == nil {
		t.Fatal("empty discovery passed")
	}
	if err := cliShardCoverage(names, []cliShard{{"empty", regexp.MustCompile("^TestOtherSample$")}, shards[0]}); err == nil || !strings.Contains(err.Error(), "selects no Docker tests") {
		t.Fatalf("empty shard was not rejected: %v", err)
	}
}

func TestCLIDockerLegacyRoutesRunEveryShard(t *testing.T) {
	for _, route := range []string{"build", "host"} {
		t.Run(route, func(t *testing.T) {
			for _, name := range []string{"docker-" + route + "-integration.sh", "docker-cli-integration.sh", "integration-timeout.sh"} {
				if _, err := os.ReadFile(filepath.Join("../../scripts", name)); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			stub := `#!/bin/sh
if [ "$1" = run ]; then
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --work-dir ]; then mkdir -p "$2"; break; fi
    shift
  done
  exit 0
fi
printf '%s\n' "$*" >> "$RECORD_DIR/calls"
`
			if err := os.WriteFile(filepath.Join(dir, "go"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("RECORD_DIR", dir)
			t.Setenv("DCB_ARTIFACT_DIR", filepath.Join(dir, "artifacts"))
			cmd := exec.Command("bash", "../../scripts/docker-"+route+"-integration.sh")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s route: %v\n%s", route, err, out)
			}
			body, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if route == "build" {
				want = append(want, "test -timeout 20m -v -tags docker_integration ./pkg/builder ./pkg/buildkit -count=1")
			}
			for _, shard := range readCLIShards(t) {
				want = append(want, "test -timeout 20m -v -tags docker_integration ./cmd/dcb -run "+shard.pattern.String()+" -count=1")
			}
			if got := strings.TrimSpace(string(body)); got != strings.Join(want, "\n") {
				t.Fatalf("%s route calls = %s, want %s", route, got, strings.Join(want, "\n"))
			}
		})
	}
}
