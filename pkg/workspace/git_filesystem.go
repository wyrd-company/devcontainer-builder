// relationships:
//   references: [git, git_config_value]

package workspace

import (
	"errors"
	"os"
	"syscall"
)

// gitDiscoveryDevice follows the start of Git's repository discovery. Unless
// GIT_DISCOVERY_ACROSS_FILESYSTEM is true, discovery stays on the file system
// of the caller folder, and oneFilesystem reports that device. Git reads the
// variable as a boolean and stats the folder before it tests any repository,
// so a value Git rejects, or a folder whose device cannot be read, gives ok
// false: Git's probe fails and the upstream CLI finds no Git root.
// Reference: https://github.com/git/git/blob/master/setup.c
// (setup_git_directory_gently_1).
func gitDiscoveryDevice(folder string) (device uint64, oneFilesystem, ok bool) {
	if value, set := os.LookupEnv("GIT_DISCOVERY_ACROSS_FILESYSTEM"); set {
		across, valid := gitParseBool(&value)
		if !valid {
			return 0, false, false
		}
		if across {
			return 0, false, true
		}
	}
	device, err := gitDevice(folder)
	return device, true, err == nil
}

// gitDevice returns the device of path, following symbolic links as Git's
// stat does. Bind mounts of one file system share a device.
func gitDevice(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no device for " + path)
	}
	return uint64(stat.Dev), nil
}
