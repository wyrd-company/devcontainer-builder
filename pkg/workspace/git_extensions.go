// relationships:
//   references: [git_config, ../../reference/cli/src/spec-common/git]

package workspace

import "strings"

// gitExtensions follows the extension handling of check_repo_format and
// verify_repository_format in Git 2.55's setup.c. Git checks each
// extensions.* value while it reads the config, whatever the repository
// version. After the read, a version 1 repository rejects an extension Git
// does not know, and a version 0 repository rejects a version 1 extension.
// Reference: https://github.com/git/git/blob/master/setup.c
type gitExtensions struct {
	worktreeConfig bool
	v1Only         bool
	unknown        bool
	objectFormat   string
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
		e.objectFormat = *value
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
// compatibility hash equal to the object format also fails: Git built with
// Rust stops on it, and Git built without Rust rejects any compatibility hash.
// A different compatibility hash is accepted, as Git built with Rust does.
func (e *gitExtensions) verify(version int) bool {
	switch {
	case version >= 1 && e.unknown:
		return false
	case version == 0 && e.v1Only:
		return false
	}
	objectFormat := e.objectFormat
	if objectFormat == "" {
		objectFormat = "sha1"
	}
	return version < 1 || e.compat != objectFormat
}

// gitHashName follows hash_algo_by_name: a case-sensitive algorithm name.
func gitHashName(value *string) bool {
	return value != nil && (*value == "sha1" || *value == "sha256")
}
