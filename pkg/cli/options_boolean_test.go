// relationships:
//
//	references: [options_boolean, inventory]
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestEveryBooleanOptionValueForm(t *testing.T) {
	for path, names := range RegisteredOptions() {
		for _, name := range names {
			root := (&application{}).command()
			commandArgs := strings.Fields(path)
			cmd, _, err := root.Find(commandArgs)
			if err != nil {
				t.Fatal(err)
			}
			cmd.InitDefaultHelpFlag()
			flag := cmd.Flags().Lookup(name)
			if flag == nil {
				flag = cmd.InheritedFlags().Lookup(name)
			}
			if flag.Value.Type() != "bool" {
				continue
			}
			for _, form := range []struct {
				args []string
				want bool
			}{
				{[]string{"--" + name}, true},
				{[]string{"--" + name + "=true"}, true},
				{[]string{"--" + name + "=false"}, false},
				{[]string{"--" + name, "true"}, true},
				{[]string{"--" + name, "false"}, false},
			} {
				t.Run(path+"/"+strings.Join(form.args, "_"), func(t *testing.T) {
					// Start opposite the expected result, including for default-true flags.
					if err := flag.Value.Set(strconv.FormatBool(!form.want)); err != nil {
						t.Fatal(err)
					}
					args := append(append([]string{}, commandArgs...), form.args...)
					normalized := booleanOptionArgs(root, args)
					selected, remaining, err := root.Find(normalized)
					if err != nil || selected != cmd {
						t.Fatalf("Find(%q)=%v, %v", normalized, selected, err)
					}
					if err := selected.ParseFlags(remaining); err != nil {
						t.Fatal(err)
					}
					got, err := selected.Flags().GetBool(name)
					if err != nil || got != form.want || len(selected.Flags().Args()) != 0 {
						t.Fatalf("value=%v err=%v remaining=%q want=%v", got, err, selected.Flags().Args(), form.want)
					}
				})
			}
		}
	}
}

func TestBooleanOptionNonLiteralsAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args, want []string
	}{
		{"uppercase", []string{"up", "--prebuild", "FALSE"}, []string{"up", "--prebuild", "FALSE"}},
		{"mixed-case", []string{"up", "--prebuild", "True"}, []string{"up", "--prebuild", "True"}},
		{"one", []string{"up", "--prebuild", "1"}, []string{"up", "--prebuild", "1"}},
		{"zero", []string{"up", "--prebuild", "0"}, []string{"up", "--prebuild", "0"}},
		{"empty", []string{"up", "--prebuild", ""}, []string{"up", "--prebuild", ""}},
		{"word", []string{"up", "--prebuild", "example"}, []string{"up", "--prebuild", "example"}},
		{"flag", []string{"up", "--prebuild", "--skip-post-create", "false"}, []string{"up", "--prebuild", "--skip-post-create=false"}},
		{"exec-boundary", []string{"exec", "--tty", "false", "--", "--tty", "true", "--help", "false"}, []string{"exec", "--tty=false", "--", "--tty", "true", "--help", "false"}},
		{"nonboolean-literal", []string{"up", "--config", "true"}, []string{"up", "--config", "true"}},
		{"positional-command", []string{"skills", "dcb", "install", "--list", "false"}, []string{"skills", "dcb", "install", "--list=false"}},
		{"short-help", []string{"up", "-h", "false"}, []string{"up", "-h=false"}},
		{"nonboolean-value", []string{"read-configuration", "--config", "--include-configuration", "false"}, []string{"read-configuration", "--config", "--include-configuration", "false"}},
		{"nonboolean-command", []string{"--log-level", "up", "version", "--help", "false"}, []string{"--log-level", "up", "version", "--help=false"}},
		{"nested", []string{"skills", "install", "--help", "false"}, []string{"skills", "install", "--help=false"}},
		{"inherited", []string{"skills", "install", "--version", "false"}, []string{"skills", "install", "--version=false"}},
		{"negation", []string{"up", "--no-prebuild"}, []string{"up", "--no-prebuild"}},
		{"unknown", []string{"up", "--unknown", "false"}, []string{"up", "--unknown", "false"}},
		{"equals", []string{"up", "--prebuild=false", "true"}, []string{"up", "--prebuild=false", "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string{}, tc.args...)
			got := booleanOptionArgs((&application{}).command(), tc.args)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
			if !reflect.DeepEqual(tc.args, original) {
				t.Fatal("input mutated")
			}
		})
	}
}

func TestRunSeparatedBooleanValues(t *testing.T) {
	workspace := t.TempDir()
	config := workspace + "/devcontainer.json"
	if err := os.WriteFile(config, []byte(`{"image":"alpine"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"true", "false"} {
		invoke(t, 0, "read-configuration", "--config", config, "--mount-workspace-git-root", value)
	}

	result, _ := invoke(t, 0, "--help", "false", "--version", "true")
	if result["version"] != "test-version" {
		t.Fatalf("result=%v", result)
	}
	for _, token := range []string{"FALSE", "True", "0", "1", "example", ""} {
		invoke(t, 2, "read-configuration", "--include-configuration", token)
	}
	invoke(t, 2, "read-configuration", "--no-include-configuration")
}

func TestBooleanOptionHelpUnchanged(t *testing.T) {
	for path := range RegisteredOptions() {
		var before, after bytes.Buffer
		a := &application{stdout: &before}
		root := a.command()
		args := append(strings.Fields(path), "--help")
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		a = &application{stdout: &after}
		root = a.command()
		root.SetArgs(booleanOptionArgs(root, args))
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		if before.String() != after.String() {
			t.Fatalf("help changed for %s", path)
		}
	}
}

// Supply the locked yargs-parser 21.1.1 entry point to run a live differential
// check without adding a Node dependency to the Go unit suite.
func TestBooleanOptionYargsParserParity(t *testing.T) {
	parser := os.Getenv("DCB_TEST_YARGS_PARSER")
	if parser == "" {
		t.Skip("set DCB_TEST_YARGS_PARSER to the locked yargs-parser entry point")
	}
	corpus := [][]string{{"--flag"}, {"--flag=true"}, {"--flag=false"}, {"--flag", "true"}, {"--flag", "false"}}
	for _, token := range []string{"TRUE", "FALSE", "True", "False", "1", "0", "yes", "no", "on", "off", "", "example", "true ", " false"} {
		corpus = append(corpus, []string{"--flag", token})
	}
	corpus = append(corpus, []string{"--flag", "false", "--", "--flag", "true"}, []string{"--no-flag"})
	for _, args := range corpus {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			code := `const p=require(process.argv[1]); const r=p(JSON.parse(process.argv[2]),{boolean:['flag'],configuration:{'boolean-negation':false,'parse-positional-numbers':false}}); console.log(JSON.stringify({value:r.flag,args:r._,unknown:!!r['no-flag']}))`
			output, err := exec.Command("node", "-e", code, parser, string(raw)).Output()
			if err != nil {
				t.Fatal(err)
			}
			var want struct {
				Value   bool
				Args    []string
				Unknown bool
			}
			if err := json.Unmarshal(output, &want); err != nil {
				t.Fatal(err)
			}
			cmd := &cobra.Command{Use: "example"}
			cmd.Flags().Bool("flag", false, "")
			err = cmd.ParseFlags(booleanOptionArgs(cmd, args))
			if want.Unknown {
				if err == nil {
					t.Fatal("unknown negation accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := cmd.Flags().GetBool("flag")
			if got != want.Value || !reflect.DeepEqual(cmd.Flags().Args(), want.Args) {
				t.Fatalf("value=%v args=%q; upstream value=%v args=%q", got, cmd.Flags().Args(), want.Value, want.Args)
			}
		})
	}
}
