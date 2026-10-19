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
	// DotfilesInstalled means the clone and install command succeeded.
	DotfilesInstalled DotfilesOutcome = "installed"
	// DotfilesSkipped means the container's dotfiles marker already exists.
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

// script is upstream's install script without its marker line. Values are
// interpolated unquoted as upstream does, so that "~" in the target expands.
// A missing git is a failure here; upstream reports it and exits 0.
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
	fmt.Fprintf(&b, "[ -e %s ] || git clone --depth 1 %s %s || exit $?\n", target, d.repositoryURL(), target)
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

// createFileCommand is upstream's atomic marker creation: it fails when the
// file exists, so two concurrent runs cannot both install.
func createFileCommand(location string) string {
	return fmt.Sprintf(`test ! -f '%[1]s' && set -o noclobber && mkdir -p '%[2]s' && { > '%[1]s' ; } 2> /dev/null`, shellSQRaw(location), shellSQRaw(path.Dir(location)))
}

// installDotfiles claims the once-per-container marker, then clones and
// installs. Unlike upstream, a failed install removes the marker so that the
// next run retries. Failures are logged as warnings and never returned.
func installDotfiles(ctx context.Context, execer Execer, rt Runtime, opts Options) DotfilesOutcome {
	d := rt.Dotfiles
	marker := path.Join(rt.UserDataFolder, ".dotfilesMarker")
	claim, err := execer.Exec(ctx, ExecRequest{Cmd: []string{"/bin/sh", "-c", createFileCommand(marker)}, User: rt.User})
	if err != nil {
		logf(opts, "warning: dotfiles install from %s failed: marker: %v", d.Repository, err)
		return DotfilesFailed
	}
	if claim.ExitCode != 0 {
		logf(opts, "skipping dotfiles install (marker found)")
		return DotfilesSkipped
	}
	logf(opts, "installing dotfiles from %s", d.Repository)
	res, err := execer.Exec(ctx, ExecRequest{Cmd: []string{"/bin/sh", "-c", d.script()}, User: rt.User, Env: rt.Env, Stdout: opts.Stdout, Stderr: opts.Stderr})
	if err == nil && res.ExitCode == 0 {
		return DotfilesInstalled
	}
	if err == nil {
		err = fmt.Errorf("exit %d", res.ExitCode)
	}
	logf(opts, "warning: dotfiles install from %s failed: %v; the next run retries", d.Repository, err)
	release, rmErr := execer.Exec(ctx, ExecRequest{Cmd: []string{"/bin/sh", "-c", "rm -f '" + shellSQRaw(marker) + "'"}, User: rt.User})
	if rmErr == nil && release.ExitCode != 0 {
		rmErr = fmt.Errorf("exit %d", release.ExitCode)
	}
	if rmErr != nil {
		logf(opts, "warning: remove dotfiles marker %s: %v", marker, rmErr)
	}
	return DotfilesFailed
}
