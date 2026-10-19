// relationships:
//
//	references: [config, metadata, host, lifecycle]
package cli

import (
	"encoding/json"
	"strings"

	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

type configurationOptions struct{ configuration, merged bool }

func (o *configurationOptions) flags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.configuration, "include-configuration", false, "Include configuration in the result")
	cmd.Flags().BoolVar(&o.merged, "include-merged-configuration", false, "Include configuration merged with container metadata in the result")
}

func (o configurationOptions) result(a *application, cmd *cobra.Command, cli *dockerclient.Client, dc *spec.DevContainer, workspace, id string) error {
	if !o.configuration && !o.merged {
		return nil
	}
	info, err := cli.ContainerInspect(cmd.Context(), id)
	if err != nil {
		return err
	}
	var configuration map[string]any
	err = json.Unmarshal(dc.Raw, &configuration)
	if err != nil {
		return err
	}
	labels, env := map[string]string{}, map[string]string{}
	if info.Config != nil {
		labels = info.Config.Labels
		for _, pair := range info.Config.Env {
			k, v, ok := strings.Cut(pair, "=")
			if ok {
				env[k] = v
			}
		}
	}
	sub := substitutionContext(dc, workspace, labels)
	resolve := func(s string) string {
		return substitute.ContainerSubstitute(substitute.ContainerContext{ContainerEnv: env}, substitute.Substitute(sub, s))
	}
	configuration = transformConfiguration(configuration, resolve).(map[string]any)
	configuration["configFilePath"] = dc.Path
	if o.configuration {
		a.result["configuration"] = configuration
	}
	if o.merged {
		raw, err := json.Marshal(configuration)
		if err != nil {
			return err
		}
		var snapshot spec.DevContainer
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return err
		}
		snapshot.Path, snapshot.Raw = dc.Path, raw
		entries, err := a.containerMetadata(&snapshot, labels)
		if err != nil {
			return err
		}
		entries, err = metadata.Substitute(entries, resolve)
		if err != nil {
			return err
		}
		a.result["mergedConfiguration"] = metadata.Merge(&snapshot, entries)
	}
	return nil
}

func transformConfiguration(v any, resolve func(string) string) any {
	switch v := v.(type) {
	case string:
		return resolve(v)
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = transformConfiguration(x, resolve)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = transformConfiguration(x, resolve)
		}
		return out
	default:
		return v
	}
}
