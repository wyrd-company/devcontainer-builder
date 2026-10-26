// relationships:
//   references: [git_config, ../../reference/cli/src/spec-common/git]

package workspace

import "strings"

// gitExtensions follows the extension handling of Git 2.55 built without
// Rust: check_repo_format and verify_repository_format in setup.c, and
// repo_set_compat_hash_algo in repository.c. Git checks each extensions.*
// value while it reads the config, whatever the repository version. After the
// read, a version 1 repository rejects an extension Git does not know, and a
// version 0 repository rejects a version 1 extension.
// Reference: https://github.com/git/git/blob/v2.55.0/setup.c
type gitExtensions struct {
	worktreeConfig bool
	v1Only         bool
	unknown        bool
	compat         string
}

// add records one extension by its name after "extensions.". It reports false
// for a value that Git rejects.
func (e *gitExtensions) add(name string, value *string) bool {
	// Git respects these version 0 extensions in every repository.
	switch name {
	case "noop":
		return true
	case "preciousobjects":
		_, ok := gitParseBool(value)
		return ok
	case "partialclone":
		return value != nil
	case "worktreeconfig":
		worktreeConfig, ok := gitParseBool(value)
		e.worktreeConfig = worktreeConfig
		return ok
	}
	switch name {
	case "noop-v1":
	case "objectformat":
		if !gitHashName(value) {
			return false
		}
	case "compatobjectformat":
		// Git accepts compatObjectFormat once only.
		if !gitHashName(value) || e.compat != "" {
			return false
		}
		e.compat = *value
	case "refstorage":
		if value == nil {
			return false
		}
		// A reference storage URI names its format before "://".
		format, _, _ := strings.Cut(*value, "://")
		if format != "files" && format != "reftable" {
			return false
		}
	case "relativeworktrees", "submodulepathconfig":
		if _, ok := gitParseBool(value); !ok {
			return false
		}
	default:
		e.unknown = true
		return true
	}
	e.v1Only = true
	return true
}

// verify follows verify_repository_format for a version that Git reads. A
// version 1 repository with a compatibility hash also fails: Git built without
// Rust stops on any compatibility hash when it sets up the repository.
func (e *gitExtensions) verify(version int) bool {
	switch {
	case version >= 1 && e.unknown:
		return false
	case version == 0 && e.v1Only:
		return false
	}
	return version < 1 || e.compat == ""
}

// gitHashName follows hash_algo_by_name: a case-sensitive algorithm name.
func gitHashName(value *string) bool {
	return value != nil && (*value == "sha1" || *value == "sha256")
}
