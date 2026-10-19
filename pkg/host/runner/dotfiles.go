package runner

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// DefaultDotfilesTargetPath is upstream's --dotfiles-target-path default.
const DefaultDotfilesTargetPath = "~/dotfiles"

// dotfilesInstallScripts is upstream's search order when no install command
// is given (src/spec-common/dotfiles.ts installCommands).
var dotfilesInstallScripts = []string{"install.sh", "install", "bootstrap.sh", "bootstrap", "script/bootstrap", "setup.sh", "setup", "script/setup"}

// Dotfiles configures the personal dotfiles install that upstream runs after
// postCreateCommand and before postStartCommand. A zero Repository installs
// nothing.
type Dotfiles struct {
	// Repository is a Git URL or container path. A value without ":" that
	// does not start with "/", "./" or "../" is a GitHub owner/repository.
	Repository string
	// InstallCommand runs in the clone. Empty runs the first file of
	// install.sh, install, bootstrap.sh, bootstrap, script/bootstrap,
	// setup.sh, setup, script/setup; without one, dotfiles are linked to ~.
	InstallCommand string
	// TargetPath is the clone location; empty means DefaultDotfilesTargetPath.
	// The shell expands it, so "~" names the remote user's home.
	TargetPath string
}

// DotfilesOutcome reports what the dotfiles step did in one run.
type DotfilesOutcome string

const (
	// DotfilesNotRun means no repository was configured or a lifecycle
	// command failed before the dotfiles step.
	DotfilesNotRun DotfilesOutcome = ""
	// DotfilesInstalled means the clone and install command succeeded. If
	// the marker could not be written, a warning says the next run installs
	// again.
	DotfilesInstalled DotfilesOutcome = "installed"
	// DotfilesSkipped means the completion marker of an earlier successful
	// install exists.
	DotfilesSkipped DotfilesOutcome = "skipped"
	// DotfilesFailed means the install failed. Like upstream, the failure
	// does not stop the lifecycle; the runner logs a warning.
	DotfilesFailed DotfilesOutcome = "failed"
)

var relativePath = regexp.MustCompile(`^\.{0,2}/`)

// repositoryURL expands GitHub shorthand as upstream does.
func (d Dotfiles) repositoryURL() string {
	if !strings.Contains(d.Repository, ":") && !relativePath.MatchString(d.Repository) {
		return "https://github.com/" + d.Repository + ".git"
	}
	return d.Repository
}

// script is upstream's install script without its marker line, with a staged
// clone. Values are interpolated unquoted as upstream does, so that "~" in
// the target expands. A missing git is a failure here; upstream reports it
// and exits 0.
func (d Dotfiles) script() string {
	target := d.TargetPath
	if target == "" {
		target = DefaultDotfilesTargetPath
	}
	var b strings.Builder
	header := "# Clone & install dotfiles"
	if d.InstallCommand != "" {
		header += " via '" + d.InstallCommand + "'"
	}
	fmt.Fprintf(&b, "%s\ncommand -v git >/dev/null 2>&1 || { echo git not found; exit 127; }\n", header)
	// The clone goes to a folder unique to this run and is renamed into place
	// only when complete, so no run reads another run's partial clone. mv -T
	// does not replace a published clone; this run then uses that one.
	fmt.Fprintf(&b, `if [ ! -e %[1]s ]
then
	stage=%[1]s.clone.$$
	rm -rf "$stage"
	git clone --depth 1 %[2]s "$stage" || { code=$?; rm -rf "$stage"; exit $code; }
	if ! mv -T "$stage" %[1]s 2>/dev/null
	then
		rm -rf "$stage"
		[ -e %[1]s ] || exit 1
		echo Using the dotfiles clone that another run published
	fi
fi
`, target, d.repositoryURL())
	if d.InstallCommand != "" {
		cmd := d.InstallCommand
		fmt.Fprintf(&b, "echo Setting current directory to '%s'\ncd %s || exit $?\n\n", target, target)
		fmt.Fprintf(&b, `if [ -f "./%[1]s" ]
then
	if [ ! -x "./%[1]s" ]
	then
		echo Setting './%[1]s' as executable
		chmod +x "./%[1]s"
	fi
	echo Executing command './%[1]s'..

	"./%[1]s"
elif [ -f "%[1]s" ]
then
	if [ ! -x "%[1]s" ]
	then
		echo Setting '%[1]s' as executable
		chmod +x "%[1]s"
	fi
	echo Executing command '%[1]s'...

	"%[1]s"
else
	echo Could not locate '%[1]s'...

	exit 126
fi
`, cmd)
		return b.String()
	}
	fmt.Fprintf(&b, "echo Setting current directory to %s\ncd %s || exit $?\n", target, target)
	fmt.Fprintf(&b, `for f in %[2]s
do
	if [ -e $f ]
	then
		installCommand=$f
		break
	fi
done
if [ -z "$installCommand" ]
then
	dotfiles=$(ls -d %[1]s/.* 2>/dev/null | grep -v -E '/(.|..|.git)$')
	if [ ! -z "$dotfiles" ]
	then
		echo Linking dotfiles: $dotfiles
		ln -sf $dotfiles ~ 2>/dev/null
	else
		echo No dotfiles found.
	fi
else
	if [ ! -x "$installCommand" ]
	then
	   echo Setting '%[1]s'/"$installCommand" as executable
	   chmod +x "$installCommand"
	fi

	echo Executing command '%[1]s'/"$installCommand"...

	./"$installCommand"
fi
`, target, strings.Join(dotfilesInstallScripts, " "))
	return b.String()
}

// markerExistsCommand succeeds only when the completion marker is a file.
// Any other result, including an error, means not installed.
func markerExistsCommand(location string) string {
	return fmt.Sprintf(`test -f '%s'`, shellSQRaw(location))
}

// writeMarkerCommand writes the completion marker atomically: a temporary
// file in the same folder, then a rename.
func writeMarkerCommand(location string) string {
	return fmt.Sprintf(`mkdir -p '%[2]s' && tmp='%[1]s.tmp.'$$ && : > "$tmp" && mv -f "$tmp" '%[1]s'`, shellSQRaw(location), shellSQRaw(path.Dir(location)))
}

// installDotfiles installs unless the completion marker exists, and writes
// the marker only after a successful install. Unlike upstream, which writes
// the marker first, a failed or interrupted install runs again on the next
// run. Failures are logged as warnings and never returned.
func installDotfiles(ctx context.Context, execer Execer, rt Runtime, opts Options) DotfilesOutcome {
	d := rt.Dotfiles
	marker := path.Join(rt.UserDataFolder, ".dotfilesMarker")
	check, err := execer.Exec(ctx, ExecRequest{Cmd: []string{"/bin/sh", "-c", markerExistsCommand(marker)}, User: rt.User})
	if err == nil && check.ExitCode == 0 {
		logf(opts, "skipping dotfiles install (marker found)")
		return DotfilesSkipped
	}
	if err != nil {
		logf(opts, "warning: check dotfiles marker %s: %v; installing", marker, err)
	}
	logf(opts, "installing dotfiles from %s", d.Repository)
	res, err := execer.Exec(ctx, ExecRequest{Cmd: []string{"/bin/sh", "-c", d.script()}, User: rt.User, Env: rt.Env, Stdout: opts.Stdout, Stderr: opts.Stderr})
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("exit %d", res.ExitCode)
	}
	if err != nil {
		logf(opts, "warning: dotfiles install from %s failed: %v; the next run retries", d.Repository, err)
		return DotfilesFailed
	}
	write, err := execer.Exec(ctx, ExecRequest{Cmd: []string{"/bin/sh", "-c", writeMarkerCommand(marker)}, User: rt.User})
	if err == nil && write.ExitCode != 0 {
		err = fmt.Errorf("exit %d", write.ExitCode)
	}
	if err != nil {
		logf(opts, "warning: write dotfiles marker %s: %v; the next run installs again", marker, err)
	}
	return DotfilesInstalled
}
