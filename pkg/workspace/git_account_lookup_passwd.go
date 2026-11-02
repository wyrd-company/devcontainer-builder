//go:build ((unix && !android) || (js && wasm) || wasip1) && ((!cgo && !darwin) || osusergo)

// relationships:
//   references: [git_config_include]

package workspace

// gitAccountLookupPasswdOnly is true when os/user looks an account name up in
// /etc/passwd only. Its lookup_unix.go does that with this build constraint:
// builds without cgo for Linux and Unix hosts other than macOS, such as the
// Linux release builds, and builds with the osusergo tag. Git asks every account service that the host
// names, such as LDAP or systemd, which this build cannot ask.
const gitAccountLookupPasswdOnly = true
