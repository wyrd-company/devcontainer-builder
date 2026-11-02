// ---
// relationships:
//
//	references: [README, overview, install, usage, configuration, library]
//
// ---
package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// foreignFlags are flags the documentation names for other tools: the
// upstream CLI and the docker run vocabulary that runArgs accepts.
var foreignFlags = map[string]bool{"--docker-path": true, "--entrypoint": true, "--cap-add": true, "--name": true, "--rm": true}

var (
	inlineCode = regexp.MustCompile("`([^`\n]+)`")
	flagSpan   = regexp.MustCompile(`^--[a-zA-Z][a-zA-Z-]*`)
	envPrefix  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=\S*$`)
)

// TestDocumentedCommandsExist checks that every dcb command line and every
// flag the public documentation names exists in the command tree.
func TestDocumentedCommandsExist(t *testing.T) {
	root := (&application{}).command()
	initHelp(root)

	files, err := filepath.Glob("../../docs/user/*.md")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "../../README.md")
	commands, flags := 0, 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines, spans := documentedText(string(data))
		for _, line := range lines {
			if words := commandWords(line); words != nil {
				commands++
				for _, problem := range checkCommand(root, words) {
					t.Errorf("%s: %q: %s", file, line, problem)
				}
			}
		}
		for _, span := range spans {
			flag := flagSpan.FindString(span)
			if flag == "" || foreignFlags[flag] {
				continue
			}
			flags++
			if !hasFlag(root, strings.TrimPrefix(flag, "--")) {
				t.Errorf("%s: flag %s is not a dcb flag", file, flag)
			}
		}
	}
	t.Logf("checked %d command lines and %d inline flags", commands, flags)
	if commands < 25 || flags < 25 {
		t.Fatalf("checked %d command lines and %d inline flags; the extraction is broken", commands, flags)
	}
}

// TestResultLineRuleAgrees holds the exceptions to "one JSON result line" the
// same in every public statement of the rule: help, a valid exec invocation and
// archive exports to stdout.
func TestResultLineRuleAgrees(t *testing.T) {
	const prose = "except help, a valid `exec` invocation, and archive exports to stdout"
	const manifest = "except help, a valid exec and archive exports to stdout,"
	for _, tc := range []struct {
		file, want string
		count      int
	}{
		{"../../README.md", prose, 1},
		{"../../docs/user/overview.md", prose, 2}, // the list and the comparison table
		{"../../docs/user/usage.md", prose, 1},
		{"../../docs/user/docs.yml", manifest, 2}, // the blurb and the highlight
	} {
		data, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(data)), " ")
		if got := strings.Count(text, tc.want); got != tc.count {
			t.Errorf("%s states the result-line exceptions %d times as %q, want %d", tc.file, got, tc.want, tc.count)
		}
	}
}

func TestCheckCommand(t *testing.T) {
	root := (&application{}).command()
	initHelp(root)
	for _, tc := range []struct {
		line string
		ok   bool
	}{
		{"dcb up --log-level debug --workspace-folder .", true},
		{"dcb up --allow-project-conflict --workspace-folder .", true},
		{"dcb exec --workspace-folder . -- ls --all", true},
		{"dcb skills install --dir .agents/skills", true},
		{"dcb build --load=false", true},
		{"dcb up --push", false},
		{"dcb up --rm", false},
		{"dcb skills --dir x", false},
		{"dcb features generate-docs --namespace example/collection --projectFolder .", true},
		{"dcb features generate-docs --namespace example/collection --projectMissing .", false},
	} {
		problems := checkCommand(root, commandWords(tc.line))
		if (len(problems) == 0) != tc.ok {
			t.Errorf("%q: problems %v, want ok=%v", tc.line, problems, tc.ok)
		}
	}
}

func TestDocumentedText(t *testing.T) {
	doc := "Run `dcb up --tty` with `--log-level` and `npm`.\n\n" +
		"```bash\nBUILDKIT_HOST=tcp://x:1 \\\n  dcb build --push\nnpm install --global x\n```\n" +
		"After `--keep`.\n"
	lines, spans := documentedText(doc)
	wantLines := []string{"dcb up --tty", "BUILDKIT_HOST=tcp://x:1    dcb build --push", "npm install --global x"}
	if strings.Join(lines, "|") != strings.Join(wantLines, "|") {
		t.Errorf("lines = %q, want %q", lines, wantLines)
	}
	if want := []string{"--log-level", "npm", "--keep"}; strings.Join(spans, "|") != strings.Join(want, "|") {
		t.Errorf("spans = %q, want %q", spans, want)
	}
	if got := strings.Join(commandWords(lines[1]), " "); got != "build --push" {
		t.Errorf("commandWords skipped the environment prefix wrongly: %q", got)
	}
	if commandWords(lines[2]) != nil {
		t.Errorf("npm line read as a dcb command")
	}
}

// documentedText returns the code-block lines, with continuations joined, and
// the inline code spans outside code blocks. Inline spans that hold a command
// line are returned as lines too.
func documentedText(doc string) (lines, spans []string) {
	inFence, pending := false, ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if strings.HasSuffix(line, "\\") {
				pending += strings.TrimSuffix(line, "\\") + " "
				continue
			}
			lines = append(lines, pending+line)
			pending = ""
			continue
		}
		for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
			if strings.HasPrefix(m[1], "dcb ") {
				lines = append(lines, m[1])
			} else {
				spans = append(spans, m[1])
			}
		}
	}
	return lines, spans
}

// commandWords returns the arguments of a dcb command line, or nil when the
// line does not run dcb. Leading environment assignments are skipped.
func commandWords(line string) []string {
	words := strings.Fields(line)
	for len(words) > 0 && envPrefix.MatchString(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 || words[0] != "dcb" {
		return nil
	}
	return words[1:]
}

// checkCommand walks subcommands, then checks each flag before "--" against
// the selected command. initHelp has merged inherited flags into Flags.
func checkCommand(root *cobra.Command, args []string) []string {
	cmd, subcommands := root, true
	var problems []string
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "--") {
			subcommands = false
			name := strings.SplitN(strings.TrimPrefix(arg, "--"), "=", 2)[0]
			if cmd.Flags().Lookup(name) == nil {
				problems = append(problems, cmd.CommandPath()+" has no --"+name)
			}
			continue
		}
		if !subcommands {
			continue
		}
		next := childNamed(cmd, arg)
		if next == nil {
			subcommands = false
			continue
		}
		cmd = next
	}
	return problems
}

func childNamed(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

// initHelp adds the help flag, which cobra otherwise adds only during
// execution.
func initHelp(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	for _, child := range cmd.Commands() {
		initHelp(child)
	}
}

// hasFlag reports whether any command in the tree defines the flag.
func hasFlag(cmd *cobra.Command, name string) bool {
	if cmd.Flags().Lookup(name) != nil || cmd.PersistentFlags().Lookup(name) != nil {
		return true
	}
	for _, child := range cmd.Commands() {
		if hasFlag(child, name) {
			return true
		}
	}
	return false
}

// TestExecInvalidInvocationContract holds the documented exec contract for an
// invalid invocation: one JSON error result, empty stderr, and exit 2.
func TestExecInvalidInvocationContract(t *testing.T) {
	for _, args := range [][]string{{"exec"}, {"exec", "--not-a-flag", "--", "ls"}} {
		if _, stderr := invoke(t, 2, args...); stderr != "" {
			t.Errorf("%v: stderr = %q, want empty at the default log level", args, stderr)
		}
	}
}
