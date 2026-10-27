// relationships:
//   references: [git, git_config_include, git_command_config, git_config_value]

package workspace

import "strings"

// gitConfigValues records typed-value failures from the full config read.
// Git first fills its configset from files, includes and command-line entries,
// then calls git_default_config for each entry. Record a failure without
// stopping those reads; a later valid value does not erase an invalid one.
// A nil receiver is the very-early read, which does not apply these types.
// Reference: https://github.com/git/git/blob/v2.56.0/config.c
// Reference: https://github.com/git/git/blob/v2.56.0/environment.c
// Reference: https://github.com/git/git/blob/v2.56.0/ident.c
// Other typed keys and includeIf conditions are not applied.
type gitConfigValues struct {
	invalidBoolean bool
}

func (v *gitConfigValues) add(entry gitConfigEntry) {
	if v != nil && (entry.name == "core.ignorecase" || entry.name == "core.filemode" || entry.name == "core.symlinks" || entry.name == "core.trustctime" || entry.name == "core.quotepath" || entry.name == "core.ignorestat" || entry.name == "core.sparsecheckout" || entry.name == "core.sparsecheckoutcone" || entry.name == "core.protecthfs" || entry.name == "core.protectntfs" || entry.name == "core.precomposeunicode" || entry.name == "core.fsyncobjectfiles" || entry.name == "core.safecrlf" || entry.name == "core.autocrlf" || entry.name == "core.lockfilepid" || entry.name == "sparse.expectfilesoutsideofpatterns" || entry.name == "pager.color" || entry.name == "color.pager" || entry.name == "user.useconfigonly") {
		// core.safecrlf also accepts warn, without applying that exception to
		// other Boolean keys or erasing an earlier invalid entry.
		// core.autocrlf separately accepts input, also without regard to case.
		warn := entry.name == "core.safecrlf" && entry.value != nil && strings.EqualFold(*entry.value, "warn")
		input := entry.name == "core.autocrlf" && entry.value != nil && strings.EqualFold(*entry.value, "input")
		if _, ok := gitParseBool(entry.value); !ok && !warn && !input {
			v.invalidBoolean = true
		}
	}
}
