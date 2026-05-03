package dockercompose

import (
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// projectLabels returns the per-resource label set that compose
// applies to every container/network/volume it creates. They make a
// project rediscoverable by `docker ps -f label=...` and by
// `docker compose down`.
func projectLabels(projectName, workspace, configFile string) map[string]string {
	out := map[string]string{
		LabelProject:     projectName,
		LabelVersion:     LabelComposeVer,
		LabelWorkingDir:  workspace,
		LabelLocalFolder: workspace,
	}
	if configFile != "" {
		out[LabelConfigFile] = configFile
		out[LabelConfigFiles] = configFile
	}
	return out
}

// containerLabels extends projectLabels with the per-service keys
// docker compose writes on each container.
func containerLabels(
	projectName, workspace, configFile, serviceName string,
	deps []string, isPrimary bool,
) map[string]string {
	out := projectLabels(projectName, workspace, configFile)
	out[LabelService] = serviceName
	out[LabelOneoff] = "False"
	out[LabelContainerN] = "1"
	if len(deps) > 0 {
		// Compose serializes depends_on as a comma-joined list of
		// `<svc>:<condition>:<required>` triples. We always emit
		// service_started (the only condition we honor in this iteration).
		parts := make([]string, 0, len(deps))
		for _, d := range deps {
			parts = append(parts, d+":service_started:true")
		}
		sort.Strings(parts)
		out[LabelDependencies] = strings.Join(parts, ",")
	}
	if isPrimary {
		out[LabelPrimary] = "true"
	}
	return out
}

// resourceName returns the engine-side name compose uses for a
// network or volume: the explicit `name` if the user set one,
// otherwise `<project>_<key>`. Mirrors compose's own behavior so
// resources are reused across `docker compose` and this package.
func resourceName(projectName, key, explicit string) string {
	if explicit != "" {
		return explicit
	}
	return projectName + "_" + key
}

// dependsOnNames extracts the service names a service depends on,
// sorted for deterministic output.
func dependsOnNames(svc types.ServiceConfig) []string {
	if len(svc.DependsOn) == 0 {
		return nil
	}
	names := make([]string, 0, len(svc.DependsOn))
	for n := range svc.DependsOn {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
