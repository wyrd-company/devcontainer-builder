// relationships:
//
//	references: [features, ../../reference/cli/src/spec-configuration/containerFeaturesConfiguration]
package features

// featureIdentifier follows upstream's backward-compatible shorthand mapping.
// Explicit references and deprecated features folded into options stay unchanged.
func (r *Resolver) featureIdentifier(id string) string {
	if r.SkipAutoMapping {
		return id
	}
	mapped := id
	switch id {
	case "aws-cli", "azure-cli", "desktop-lite", "docker-in-docker", "docker-from-docker", "dotnet", "git", "git-lfs", "github-cli", "java", "kubectl-helm-minikube", "node", "powershell", "python", "ruby", "rust", "sshd", "terraform":
	case "golang":
		mapped = "go"
	case "common":
		mapped = "common-utils"
	default:
		return id
	}
	r.logf("warning: using deprecated feature identifier %s; use ghcr.io/devcontainers/features/%s:1", id, mapped)
	return "ghcr.io/devcontainers/features/" + mapped + ":1"
}
