//go:build linux && !amd64 && !arm64

// relationships:
//   references: [git_chdir_linux]

package workspace

// gitCloneCwd reports that a thread cannot start: these hosts are not
// release targets and have no thread code.
func gitCloneCwd(int) (path string, isolated bool, err error) {
	return "", false, nil
}
