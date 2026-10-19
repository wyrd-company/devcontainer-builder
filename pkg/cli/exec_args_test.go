// relationships:
//
//	references: [exec, options_boolean, ../../reference/cli/src/spec-node/devContainersSpecCLI]
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func execArgumentCorpus() []struct{ options, command []string } {
	return []struct{ options, command []string }{
		{nil, []string{"ls", "-la"}},
		{nil, []string{"sh", "-c", "printf '%s' sample"}},
		{nil, []string{"cmd", "--workspace-folder", "x", "--remote-env", "K=X"}},
		{nil, []string{"cmd", "--tty", "true", "--help", "false", "--version", "true", "--", "tail"}},
		{[]string{"--workspace-folder", "example", "--remote-env", "K=V"}, []string{"cmd", "--workspace-folder", "x"}},
		{[]string{"--workspace-folder=example", "--remote-env=K=V"}, []string{"cmd", "false", "true"}},
		{[]string{"--mount-workspace-git-root", "false"}, []string{"cmd", "--mount-workspace-git-root", "true"}},
		{[]string{"--mount-workspace-git-root", "true"}, []string{"false", "--help", "false"}},
		{[]string{"--mount-workspace-git-root=false"}, []string{"true", "--help", "true"}},
		{[]string{"--mount-workspace-git-root"}, []string{"cmd", "--help", "false"}},
		{nil, []string{"cmd", "", "001", "false", "true"}},
		{nil, []string{"-", "--help", "false"}},
	}
}

// Exercise the production command tree and normalization, replacing only the
// Docker action so the test can inspect the arguments delivered to it.
func parseExecArguments(t *testing.T, args []string) ([]string, *cobra.Command) {
	t.Helper()
	a := &application{result: map[string]any{}, stdout: io.Discard, stderr: io.Discard}
	root := a.command()
	cmd, _, err := root.Find([]string{"exec"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	cmd.RunE = func(_ *cobra.Command, args []string) error { got = append([]string{}, args...); return nil }
	root.SetArgs(booleanOptionArgs(root, append([]string{"exec"}, args...)))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("exec action did not run")
	}
	return got, cmd
}

func TestExecArgumentBoundary(t *testing.T) {
	for _, tc := range execArgumentCorpus() {
		t.Run(strings.Join(append(append([]string{}, tc.options...), tc.command...), "_"), func(t *testing.T) {
			_, baseline := parseExecArguments(t, append(append([]string{}, tc.options...), "--", "cmd"))
			for _, explicit := range []bool{false, true} {
				input := append([]string{}, tc.options...)
				if explicit {
					input = append(input, "--")
				}
				input = append(input, tc.command...)
				got, cmd := parseExecArguments(t, input)
				if !reflect.DeepEqual(got, tc.command) {
					t.Fatalf("explicit=%v got=%q want=%q", explicit, got, tc.command)
				}
				for _, name := range []string{"workspace-folder", "remote-env", "mount-workspace-git-root", "help", "version", "tty"} {
					if cmd.Flags().Lookup(name).Value.String() != baseline.Flags().Lookup(name).Value.String() {
						t.Fatalf("command tail changed --%s", name)
					}
				}
			}
		})
	}
}

func TestExecHelpAndMissingCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"exec", "--help"}, "test", nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), "exec [flags]") || errOut.Len() != 0 {
		t.Fatalf("help code/output: %d %s %s", code, &out, &errOut)
	}
	for _, args := range [][]string{{"exec"}, {"exec", "--"}, {"exec", "--mount-workspace-git-root", "false"}} {
		invoke(t, 2, args...)
	}
}

// Use the locked upstream parser directly without a Node runtime dependency
// for ordinary unit tests. Upstream enables halt-at-non-option for exec at
// devContainersSpecCLI.ts:58-64,92; parser 21.1.1 stops at lines 354-361.
func TestExecYargsParserParity(t *testing.T) {
	parser := os.Getenv("DCB_TEST_YARGS_PARSER")
	if parser == "" {
		t.Skip("set DCB_TEST_YARGS_PARSER to the locked yargs-parser entry point")
	}
	for _, tc := range execArgumentCorpus() {
		for _, explicit := range []bool{false, true} {
			input := append([]string{}, tc.options...)
			if explicit {
				input = append(input, "--")
			}
			input = append(input, tc.command...)
			raw, _ := json.Marshal(input)
			code := `const p=require(process.argv[1]);const r=p(JSON.parse(process.argv[2]),{string:['workspace-folder','remote-env'],boolean:['mount-workspace-git-root'],default:{'workspace-folder':'.','mount-workspace-git-root':true},configuration:{'boolean-negation':false,'halt-at-non-option':true}});console.log(JSON.stringify({args:r._,workspace:r['workspace-folder'],env:r['remote-env'],mount:r['mount-workspace-git-root']}))`
			output, err := exec.Command("node", "-e", code, parser, string(raw)).Output()
			if err != nil {
				t.Fatal(err)
			}
			var want struct {
				Args      []string
				Workspace string
				Env       string
				Mount     bool
			}
			if err := json.Unmarshal(output, &want); err != nil {
				t.Fatal(err)
			}
			got, cmd := parseExecArguments(t, input)
			workspace, _ := cmd.Flags().GetString("workspace-folder")
			env, _ := cmd.Flags().GetStringArray("remote-env")
			mount, _ := cmd.Flags().GetBool("mount-workspace-git-root")
			if !reflect.DeepEqual(got, want.Args) || workspace != want.Workspace || strings.Join(env, "") != want.Env || mount != want.Mount {
				t.Fatalf("input=%q got=%q %q %q %v upstream=%+v", input, got, workspace, env, mount, want)
			}
		}
	}
}
