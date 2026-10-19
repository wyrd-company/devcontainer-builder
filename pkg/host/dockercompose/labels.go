package dockercompose

import (
	"fmt"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// projectLabels is the compose-standard label set applied to every
// project resource (containers, networks, volumes). They make a
// project rediscoverable by `docker ps -f label=...` and by
// `docker compose down`. Devcontainer-discovery labels
// (devcontainer.local_folder, devcontainer.config_file) are
// deliberately *not* included here — they belong only on the primary
// service container so editor extensions can find a unique target.
func projectLabels(projectName, workingDir string) map[string]string {
	return map[string]string{
		LabelProject:    projectName,
		LabelVersion:    LabelComposeVer,
		LabelWorkingDir: workingDir,
	}
}

// labelScope identifies the project a container belongs to. WorkingDir and
// ComposeFiles are the Compose project's; LocalWorkspace, SpecPath and
// IDLabels are the devcontainer's.
type labelScope struct {
	Project, WorkingDir      string
	ComposeFiles             []string
	LocalWorkspace, SpecPath string
	IDLabels                 map[string]string
}

// containerLabels extends projectLabels with the per-service keys
// docker compose writes on each container, plus — only on the primary
// — the devcontainer-discovery labels editor tooling looks up.
func containerLabels(scope labelScope, serviceName, configHash string, deps []dependency, isPrimary bool) map[string]string {
	out := projectLabels(scope.Project, scope.WorkingDir)
	out[LabelService] = serviceName
	out[LabelOneoff] = "False"
	out[LabelContainerN] = "1"
	if configHash != "" {
		out[LabelConfigHash] = configHash
	}
	if len(scope.ComposeFiles) > 0 {
		// Compose records the files it loaded, comma-joined.
		out[LabelConfigFiles] = strings.Join(scope.ComposeFiles, ",")
	}
	if len(deps) > 0 {
		// Compose serializes depends_on as a comma-joined list of
		// `<svc>:<condition>:<required>` triples. Encoded verbatim so
		// `docker compose ps` (and any tool that decodes the label)
		// sees the same condition the spec declared.
		parts := make([]string, 0, len(deps))
		for _, d := range deps {
			parts = append(parts, fmt.Sprintf("%s:%s:%t", d.Name, d.Condition, d.Required))
		}
		sort.Strings(parts)
		out[LabelDependencies] = strings.Join(parts, ",")
	}
	if isPrimary {
		// Only the primary service is rediscoverable as the
		// devcontainer for this workspace. Stamping these labels on
		// every container would make the workspace ambiguous to VS
		// Code's Dev Container extension and the upstream CLI.
		out[LabelPrimary] = "true"
		out[LabelLocalFolder] = scope.LocalWorkspace
		if scope.SpecPath != "" {
			out[LabelConfigFile] = scope.SpecPath
		}
		// Id labels add to the ownership labels; they never replace one.
		for key, value := range scope.IDLabels {
			if _, managed := out[key]; !managed {
				out[key] = value
			}
		}
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

// dependency captures a single depends_on entry the engine API and
// the compose label both need.
type dependency struct {
	Name      string
	Condition string
	Required  bool
}

// dependsOnList returns the depends_on entries for a service in a
// stable, value-preserving form. Conditions default to
// `service_started` to match the compose spec; required defaults to
// the value compose-go normalized.
func dependsOnList(svc types.ServiceConfig) []dependency {
	if len(svc.DependsOn) == 0 {
		return nil
	}
	out := make([]dependency, 0, len(svc.DependsOn))
	for n, d := range svc.DependsOn {
		cond := d.Condition
		if cond == "" {
			cond = types.ServiceConditionStarted
		}
		out = append(out, dependency{Name: n, Condition: cond, Required: d.Required})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
