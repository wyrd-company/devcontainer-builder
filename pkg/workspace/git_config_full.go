// relationships:
//   references: [git, git_config_include, git_command_config, git_config_value]

package workspace

// gitConfigValues records typed-value failures from the full config read.
// Git first fills its configset from files, includes and command-line entries,
// then calls git_default_config for each entry. Record a failure without
// stopping those reads; a later valid value does not erase an invalid one.
// A nil receiver is the very-early read, which does not apply these types.
// Reference: https://github.com/git/git/blob/v2.56.0/config.c
// Reference: https://github.com/git/git/blob/v2.56.0/environment.c
// Other typed keys and includeIf conditions are not applied.
type gitConfigValues struct {
	invalidBoolean bool
}

func (v *gitConfigValues) add(entry gitConfigEntry) {
	if v != nil && (entry.name == "core.ignorecase" || entry.name == "core.filemode" || entry.name == "core.symlinks" || entry.name == "core.trustctime" || entry.name == "core.quotepath") {
		if _, ok := gitParseBool(entry.value); !ok {
			v.invalidBoolean = true
		}
	}
}
