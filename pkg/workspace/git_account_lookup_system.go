//go:build !(((unix && !android) || (js && wasm) || wasip1) && ((!cgo && !darwin) || osusergo))

// relationships:
//   references: [git_config_include]

package workspace

// gitAccountLookupPasswdOnly is false when os/user asks the host's account
// services for an account name, as Git does: cgo builds and macOS builds.
const gitAccountLookupPasswdOnly = false
