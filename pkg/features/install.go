package features

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Install layout inside the image, matching upstream
// (reference/cli/src/spec-configuration/containerFeaturesConfiguration.ts):
//
//	/tmp/dev-container-features/devcontainer-features.builtin.env
//	/tmp/dev-container-features/<feature>/devcontainer-features.env
//	/tmp/dev-container-features/<feature>/devcontainer-features-install.sh
//	/tmp/dev-container-features/<feature>/install.sh (and the rest of the feature)
//
// The install wrapper exports both env files and runs install.sh from the
// feature directory.
const (
	// InstallDir is the directory features are installed from.
	InstallDir = "/tmp/dev-container-features"
	// BuiltinEnvFile holds _CONTAINER_USER, _REMOTE_USER and their homes.
	BuiltinEnvFile = "devcontainer-features.builtin.env"
	// EnvFile holds a feature's option values.
	EnvFile = "devcontainer-features.env"
	// InstallWrapperFile is the script that runs a feature's install.sh.
	InstallWrapperFile = "devcontainer-features-install.sh"
)

var (
	nonWordRE      = regexp.MustCompile(`[^\w_]`)
	leadingDigitRE = regexp.MustCompile(`^[\d_]+`)
)

// SafeEnvID converts a feature option name or id to an environment
// variable name like upstream's getSafeId: non-word characters become
// "_", a leading run of digits and underscores becomes a single "_", and
// the result is upper-cased.
func SafeEnvID(s string) string {
	s = nonWordRE.ReplaceAllString(s, "_")
	s = leadingDigitRE.ReplaceAllString(s, "_")
	return strings.ToUpper(s)
}

// ContainerUsers returns the container and remote users features install
// for, like upstream's findContainerUsers: the container user is the
// configured containerUser, else the image user, else root; the remote
// user is the configured remoteUser, else the container user.
func ContainerUsers(containerUser, remoteUser, imageUser string) (string, string) {
	if containerUser == "" {
		containerUser = imageUser
	}
	if containerUser == "" {
		containerUser = "root"
	}
	if remoteUser == "" {
		remoteUser = containerUser
	}
	return containerUser, remoteUser
}

// OptionValues returns the feature's effective option values: the
// declared defaults overlaid with the configured options, like upstream's
// getFeatureValueObject.
func (r Resolved) OptionValues() map[string]any {
	out := map[string]any{}
	for name, opt := range r.Metadata.Options {
		if opt.Default != nil {
			out[name] = opt.Default
		}
	}
	for name, v := range r.Options {
		out[name] = v
	}
	return out
}

// EnvFileContent returns the devcontainer-features.env content: one
// NAME="value" line per option, with names from SafeEnvID.
func (r Resolved) EnvFileContent() string {
	return strings.Join(r.envLines(), "\n") + "\n"
}

func (r Resolved) envLines() []string {
	values := r.OptionValues()
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, SafeEnvID(name)+"="+doubleQuote(jsString(values[name])))
	}
	return lines
}

// BuiltinEnvContent returns the initial devcontainer-features.builtin.env
// content. The home directories are appended inside the image by
// BuiltinHomesCommand, since they depend on the image's /etc/passwd.
func BuiltinEnvContent(containerUser, remoteUser string) string {
	return "_CONTAINER_USER=" + doubleQuote(containerUser) + "\n" +
		"_REMOTE_USER=" + doubleQuote(remoteUser) + "\n"
}

// BuiltinHomesCommand returns a shell command that appends
// _CONTAINER_USER_HOME and _REMOTE_USER_HOME to the builtin env file at
// path, looked up from the image's passwd database.
func BuiltinHomesCommand(path, containerUser, remoteUser string) string {
	file := singleQuote(path)
	return fmt.Sprintf(`echo "_CONTAINER_USER_HOME=$(%s | cut -d: -f6)" >> %s && echo "_REMOTE_USER_HOME=$(%s | cut -d: -f6)" >> %s`,
		passwdLookup(containerUser), file, passwdLookup(remoteUser), file)
}

// passwdLookup prints the passwd entry for a user name or uid. The
// leading space keeps "$(" + "(" from parsing as arithmetic expansion.
func passwdLookup(user string) string {
	re := regexp.QuoteMeta(user)
	return fmt.Sprintf(` (command -v getent >/dev/null 2>&1 && getent passwd %s || grep -E %s /etc/passwd || true)`,
		singleQuote(user), singleQuote("^"+re+":|^[^:]*:[^:]*:"+re+":"))
}

// InstallCommand returns the shell command that runs the install wrapper
// of a feature staged at dir.
func InstallCommand(dir string) string {
	return fmt.Sprintf("chmod -R 0755 %[1]s && cd %[1]s && chmod +x ./%[2]s && ./%[2]s",
		singleQuote(dir), InstallWrapperFile)
}

// InstallWrapperContent returns the devcontainer-features-install.sh
// script for the feature, like upstream's getFeatureInstallWrapperScript.
// It exports the builtin and option env files, then runs install.sh. The
// feature's entrypoint is never run at install time.
func (r Resolved) InstallWrapperContent() string {
	m := r.Metadata
	name := m.Name
	if name == "" {
		name = "Unknown"
	}
	id := r.SourceWithoutVersion()
	lines := r.envLines()
	for i, l := range lines {
		lines[i] = "    " + l
	}
	var warning string
	if m.Deprecated {
		warning = "echo " + singleQuote(fmt.Sprintf("(!) WARNING: Using the deprecated Feature %q. This Feature will no longer receive any further updates/support.", m.ID)) + "\n"
	}
	troubleshooting := ""
	if m.DocumentationURL != "" {
		troubleshooting = " Look at the documentation at " + m.DocumentationURL + " for help troubleshooting this error."
	}
	errorMessage := fmt.Sprintf("ERROR: Feature %q (%s) failed to install!%s", name, id, troubleshooting)

	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n\n")
	b.WriteString("on_exit () {\n\t[ $? -eq 0 ] && exit\n\techo " + singleQuote(errorMessage) + "\n}\n\n")
	b.WriteString("trap on_exit EXIT\n\n")
	b.WriteString("echo ===========================================================================\n")
	b.WriteString(warning)
	b.WriteString("echo " + singleQuote("Feature       : "+name) + "\n")
	b.WriteString("echo " + singleQuote("Description   : "+m.Description) + "\n")
	b.WriteString("echo " + singleQuote("Id            : "+id) + "\n")
	b.WriteString("echo " + singleQuote("Version       : "+m.Version) + "\n")
	b.WriteString("echo " + singleQuote("Documentation : "+m.DocumentationURL) + "\n")
	b.WriteString("echo 'Options       :'\n")
	b.WriteString("echo " + singleQuote(strings.Join(lines, "\n")) + "\n")
	b.WriteString("echo ===========================================================================\n\n")
	b.WriteString("set -a\n. ../" + BuiltinEnvFile + "\n. ./" + EnvFile + "\nset +a\n\n")
	b.WriteString("chmod +x ./install.sh\n./install.sh\n")
	return b.String()
}

// SourceWithoutVersion returns the feature source without its tag or
// digest.
func (r Resolved) SourceWithoutVersion() string {
	if r.Reference == nil {
		return r.Source
	}
	s := r.Source
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i > strings.LastIndex(s, "/") {
		s = s[:i]
	}
	return s
}

// jsString formats an option value the way a JavaScript template literal
// does, which is how upstream writes option values.
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	default:
		return fmt.Sprint(v)
	}
}

// doubleQuote quotes s for a POSIX shell so that sourcing NAME="..."
// yields s exactly.
func doubleQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return `"` + r.Replace(s) + `"`
}

// singleQuote quotes s for a POSIX shell.
func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
