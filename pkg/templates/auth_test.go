// relationships:
//
//	references: [metadata_test, ../registryauth/auth]
package templates

import "github.com/wyrd-company/devcontainer-builder/pkg/registryauth"

type sampleCredentials struct{}

func (sampleCredentials) Credentials(string) (registryauth.Credentials, error) {
	return registryauth.Credentials{Username: "user", Password: "pass"}, nil
}
