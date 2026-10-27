// relationships:
//   references: [git, git_global_config, git_config_value, ../../reference/cli/src/spec-common/git]

package workspace

import "os"

// gitSystemConfigRead follows Git's read of its system config file in one of
// its config reads, from folder, and reports false when the read stops the
// probe. Git reads the system file before the global files. A
// GIT_CONFIG_NOSYSTEM value that Git reads as true skips the file, and a value
// that Git does not read as a boolean stops the probe. Otherwise
// GIT_CONFIG_SYSTEM names the file, after gitNormalizePath; relative paths
// start at folder. The first read skips a file that access(2) denies with the
// real credentials, as for the global files. The second read, after setup,
// stops on it. Without GIT_CONFIG_SYSTEM, Git reads the system file that its
// build names; that file is not read.
// Reference: https://github.com/git/git/blob/master/config.c
func gitSystemConfigRead(folder *gitConfigCwd, second bool, prefix string, values *gitConfigValues) bool {
	if value, set := os.LookupEnv("GIT_CONFIG_NOSYSTEM"); set {
		skip, ok := gitParseBool(&value)
		if !ok {
			return false
		}
		if skip {
			return true
		}
	}
	path, set := os.LookupEnv("GIT_CONFIG_SYSTEM")
	if !set {
		return true
	}
	return gitConfigFileRead(folder, gitNormalizePath(path), !second, second, prefix, values)
}

// gitNormalizePath follows normalize_path_copy in Git's path.c, which
// git_system_config applies in place. It removes repeated separators and .
// components, and removes a .. component with the component before it, without
// reading the file system. When a .. component has no component before it, Git
// stops and keeps the partly rewritten path, which then has the original
// length.
// Reference: https://github.com/git/git/blob/master/path.c
func gitNormalizePath(path string) string {
	buf := []byte(path)
	at := func(i int) byte {
		if i < len(buf) {
			return buf[i]
		}
		return 0
	}
	src, dst := 0, 0
	if at(0) == '/' {
		// The root separator is copied onto itself.
		src, dst = 1, 1
	}
	start := dst
	skipSeparators := func() {
		for at(src) == '/' {
			src++
		}
	}
	skipSeparators()
	for {
		if at(src) == '.' {
			up := false
			switch {
			case at(src+1) == 0:
				src++
			case at(src+1) == '/':
				src += 2
				skipSeparators()
				continue
			case at(src+1) == '.' && at(src+2) == 0:
				src += 2
				up = true
			case at(src+1) == '.' && at(src+2) == '/':
				src += 3
				skipSeparators()
				up = true
			}
			if up {
				dst--
				if dst <= start {
					return string(buf)
				}
				for start < dst && buf[dst-1] != '/' {
					dst--
				}
				continue
			}
		}
		var c byte
		for {
			c = at(src)
			src++
			if c == 0 || c == '/' {
				break
			}
			buf[dst] = c
			dst++
		}
		if c == 0 {
			return string(buf[:dst])
		}
		buf[dst] = '/'
		dst++
		skipSeparators()
	}
}
