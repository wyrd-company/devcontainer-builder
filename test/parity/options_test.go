// relationships:
//
//	references: [options, options_source, ../../docs/audits/upstream-options]
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/cli"
)

const sampleEntry = `.version(version)
 y.command('up', 'Run', provisionOptions, handler);
 y.command('features', 'Features', (y: Argv) => {
 y.command('package <target>', 'Package', featuresPackageOptions, handler);
 });
`

func sampleBuilders() map[string]optionBuilder {
	return map[string]optionBuilder{
		"provisionOptions":       {body: "return y.options({\n 'config': { type: 'string' },\n 'hidden-option': { type: 'boolean', hidden: true },\n prebuild: { type: 'boolean' },\n});", source: "sample.ts", line: 1},
		"featuresPackageOptions": {body: "return PackageOptions(y, 'feature');"},
		"PackageOptions":         {body: "return y.options({\n 'output-folder': { type: 'string', alias: 'o' },\n});", source: "package.ts", line: 1},
	}
}

func writeSampleSource(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "reference/cli/src/spec-node")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	body := sampleEntry
	for name, builder := range sampleBuilders() {
		body += "\nfunction " + name + "(y: Argv) {\n" + builder.body + "\n}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "devContainersSpecCLI.ts"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamOptionsReadsHiddenBareSharedAndNested(t *testing.T) {
	options, err := upstreamCommandOptions(sampleEntry, sampleBuilders())
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 4 || len(options["up"]) != 5 || !options["up"][1].Hidden || options["up"][2].Name != "prebuild" || options["features package"][0].Aliases[0] != "o" {
		t.Fatalf("incomplete inventory: %+v", options)
	}
	builders := sampleBuilders()
	b := builders["provisionOptions"]
	b.body = strings.Replace(b.body, "});", " 'added-option': { type: 'boolean' },\n});", 1)
	builders["provisionOptions"] = b
	added, err := upstreamCommandOptions(sampleEntry, builders)
	if err != nil {
		t.Fatal(err)
	}
	if len(added["up"]) != len(options["up"])+1 {
		t.Fatal("new source option did not expand denominator")
	}
	newCommand, err := upstreamCommandOptions(sampleEntry+" y.command('extra', 'Extra', provisionOptions, handler);\n", builders)
	if err != nil {
		t.Fatal(err)
	}
	if len(newCommand) != len(added)+1 || len(newCommand["extra"]) != len(added["up"]) {
		t.Fatal("new command was not discovered")
	}
}

func TestUpstreamOptionsRejectsIncompleteSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		entry  string
		change func(map[string]optionBuilder)
	}{
		{"empty", "", nil},
		{"missing-global-version", strings.Replace(sampleEntry, ".version(version)", "", 1), nil},
		{"multiline-registration", sampleEntry + " y.command(\n dynamic);", nil},
		{"no-commands", ".version(version)", nil},
		{"unrecognized-registration", sampleEntry + " y.command(dynamic);", nil},
		{"missing-builder", strings.Replace(sampleEntry, "provisionOptions", "missingOptions", 1), nil},
		{"unsupported-property", sampleEntry, func(b map[string]optionBuilder) {
			v := b["provisionOptions"]
			v.body = strings.Replace(v.body, " prebuild:", " [dynamic]:", 1)
			b["provisionOptions"] = v
		}},
		{"multiple-properties", sampleEntry, func(b map[string]optionBuilder) {
			v := b["provisionOptions"]
			v.body = strings.Replace(v.body, "prebuild: { type: 'boolean' },", "prebuild: { type: 'boolean' }, other: { type: 'string' },", 1)
			b["provisionOptions"] = v
		}},
		{"individual-option", sampleEntry, func(b map[string]optionBuilder) {
			v := b["provisionOptions"]
			v.body += ".option('extra',{});"
			b["provisionOptions"] = v
		}},
		{"duplicate-option", sampleEntry, func(b map[string]optionBuilder) {
			v := b["provisionOptions"]
			v.body = strings.Replace(v.body, "});", " prebuild: { type: 'boolean' },\n});", 1)
			b["provisionOptions"] = v
		}},
		{"empty-table", sampleEntry, func(b map[string]optionBuilder) {
			b["provisionOptions"] = optionBuilder{body: "return y.options({\n});"}
		}},
		{"unterminated-table", sampleEntry, func(b map[string]optionBuilder) {
			v := b["provisionOptions"]
			v.body = strings.Replace(v.body, "});", "", 1)
			b["provisionOptions"] = v
		}},
		{"cyclic-builder", sampleEntry, func(b map[string]optionBuilder) {
			b["PackageOptions"] = optionBuilder{body: "return featuresPackageOptions(y, 'feature');"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := sampleBuilders()
			if tc.change != nil {
				tc.change(b)
			}
			if _, err := upstreamCommandOptions(tc.entry, b); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestOptionCoverageDropsWhenRegisteredFlagRemoved(t *testing.T) {
	upstream := map[string][]sourceOption{"up": {{Name: "workspace-folder"}, {Name: "help"}}, "missing command": {{Name: "help"}}}
	local := cli.RegisteredOptions()
	before := measureOptions(upstream, local)
	flags := []string{}
	for _, name := range local["up"] {
		if name != "workspace-folder" {
			flags = append(flags, name)
		}
	}
	local["up"] = flags
	after := measureOptions(upstream, local)
	if !before.Complete || before.Covered != 2 || before.Total != 3 || after.Covered != 1 || after.Total != before.Total || after.Score >= before.Score {
		t.Fatalf("flag removal did not lower coverage: before=%+v after=%+v", before, after)
	}
}

func TestOptionCoverageRejectsEmptyInventory(t *testing.T) {
	coverage := measureOptions(nil, cli.RegisteredOptions())
	if coverage.Complete || coverage.Error == "" || coverage.Score != 0 {
		t.Fatalf("empty inventory passed: %+v", coverage)
	}
}

func TestOptionOnlyReportWithoutRuntimeTools(t *testing.T) {
	root := t.TempDir()
	writeSampleSource(t, root)
	t.Setenv("PATH", t.TempDir())
	output := filepath.Join(t.TempDir(), "report")
	if err := runParity(context.Background(), root, output, true); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(output, "score.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Options.Complete || report.Options.Total != 12 || report.Options.Covered == 0 || report.Total != 0 || report.Skipped == "" {
		t.Fatalf("report lost option dimension: %+v", report)
	}
	md, err := os.ReadFile(filepath.Join(output, "report.md"))
	if err != nil || !strings.Contains(string(md), "Option coverage") || !strings.Contains(string(md), "features package") {
		t.Fatalf("markdown lost option dimension: %s %v", md, err)
	}
}

func TestMissingSourceWritesIncompleteReport(t *testing.T) {
	output := filepath.Join(t.TempDir(), "report")
	if err := runParity(context.Background(), t.TempDir(), output, true); err == nil {
		t.Fatal("missing source passed")
	}
	body, err := os.ReadFile(filepath.Join(output, "score.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Options.Complete || report.Options.Error == "" || report.Options.Score != 0 {
		t.Fatalf("missing source passed: %+v", report.Options)
	}
}

func TestPinnedOptionInventoryAndRegisteredBoundary(t *testing.T) {
	root := "../.."
	if _, err := os.Stat(filepath.Join(root, "reference/cli/src/spec-node/devContainersSpecCLI.ts")); os.IsNotExist(err) {
		t.Skip("reference/cli not initialized")
	}
	options, err := readUpstreamOptions(root)
	if err != nil {
		t.Fatal(err)
	}
	// Independent inventory from the pinned source audit. These assertions bind
	// the reader to hidden options, command groups, shared builders and exec.
	if len(options) != 21 || len(options["up"]) != 46 || len(options["features test"]) != 15 || len(options["templates publish"]) != 5 || len(options["exec"]) != 21 {
		t.Fatalf("pinned inventory incomplete: %+v", options)
	}
	coverage := optionCoverage(root)
	if !coverage.Complete || coverage.Total != 253 {
		t.Fatalf("pinned denominator: %+v", coverage)
	}
	for _, option := range coverage.Options {
		if option.Command == "up" && option.Name == "workspace-folder" {
			if !option.Covered {
				t.Fatal("real up --workspace-folder registration missing from coverage")
			}
			return
		}
	}
	t.Fatal("up workspace-folder missing from source inventory")
}

func TestOptionAuditCoversEverySourceOption(t *testing.T) {
	if _, err := os.Stat("../../reference/cli/src/spec-node/devContainersSpecCLI.ts"); os.IsNotExist(err) {
		t.Skip("reference/cli not initialized")
	}
	upstream, err := readUpstreamOptions("../..")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("../../docs/audits/upstream-options.md")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	command := ""
	registered := cli.RegisteredOptions()
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "### ") {
			command = strings.TrimPrefix(line, "### ")
			if command == "(root)" {
				command = ""
			}
		}
		if !strings.HasPrefix(line, "| --") || strings.HasPrefix(line, "| ---") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) != 7 {
			t.Fatalf("invalid audit row: %s", line)
		}
		flag := strings.TrimPrefix(strings.Fields(cells[1])[0], "--")
		key := command + "/" + flag
		if seen[key] {
			t.Errorf("duplicate audit row %s", key)
		}
		seen[key] = true
		found := false
		for _, option := range upstream[command] {
			if option.Name == flag {
				found = true
			}
		}
		if !found {
			t.Errorf("audit option absent from source: %s", key)
		}
		switch strings.TrimSpace(cells[3]) {
		case "implemented":
			accepted := false
			for _, name := range registered[command] {
				if name == flag {
					accepted = true
				}
			}
			if !accepted {
				t.Errorf("audit claims missing flag implemented: %s", key)
			}
		case "implement":
		case "not applicable by design":
			if flag != "docker-path" && flag != "docker-compose-path" && flag != "buildkit" {
				t.Errorf("unapproved exclusion: %s", key)
			}
			if !strings.Contains(cells[4], "approved epic requirement") || strings.TrimSpace(cells[5]) == "" {
				t.Errorf("exclusion without authority/reason: %s", key)
			}
		case "needs Bob's decision":
		default:
			t.Errorf("invalid disposition: %s", line)
		}
		if flag == "additional-features" || strings.HasPrefix(flag, "dotfiles-") || flag == "default-user-env-probe" || flag == "version" {
			if !strings.Contains(cells[4], "1371") {
				t.Errorf("missing sibling ownership: %s", key)
			}
		}
	}
	for command, options := range upstream {
		for _, option := range options {
			if !seen[command+"/"+option.Name] {
				t.Errorf("missing audit row: %s/%s", command, option.Name)
			}
		}
	}
}
