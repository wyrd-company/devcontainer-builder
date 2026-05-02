package docker

import "github.com/wyrd-company/devcontainer-builder/pkg/substitute"

// substituteCtxForTest returns a substitute.Context fixture used by
// multiple test files. Keeping it package-private to the docker tests
// avoids reaching into other packages for assertions.
func substituteCtxForTest() substitute.Context {
	return substitute.Context{
		LocalWorkspaceFolder:     "/host/proj",
		ContainerWorkspaceFolder: "/workspaces/proj",
		LocalEnv:                 map[string]string{"GREETING": "hello"},
	}
}
