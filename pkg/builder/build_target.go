// relationships:
//
//	references: [build_options, builder, ../buildkit/build_target]
package builder

import (
	"os"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Options select from the generated Dockerfile, after the generated final target.
// Config target still selects the base and remains the metadata inference input.
func selectBuildTarget(dc *spec.DevContainer, parsed dockerBuildOptions, upstream *buildkit.DockerfileBuild) error {
	if !parsed.TargetSet || parsed.Target == "" || strings.EqualFold(parsed.Target, "dev_containers_target_stage") {
		return nil
	}
	if strings.EqualFold(parsed.Target, "dev_containers_feature_content_normalize") && dc.Features.Len() > 0 {
		upstream.Stage = buildkit.NormalizeStage
		return nil
	}
	upstream.Stage = buildkit.AuthoredStage
	upstream.Target = parsed.Target
	// Upstream names an unnamed final FROM when config target is absent.
	if (dc.Build == nil || dc.Build.Target == "") && strings.EqualFold(parsed.Target, "dev_container_auto_added_stage_label") {
		file, err := os.Open(dc.DockerfileFile())
		if err != nil {
			return err
		}
		defer file.Close()
		ast, err := parser.Parse(file)
		if err != nil {
			return err
		}
		stages, _, err := instructions.Parse(ast.AST, nil)
		if err != nil {
			return err
		}
		if len(stages) > 0 && stages[len(stages)-1].Name == "" {
			upstream.Target = ""
		}
	}
	return nil
}
