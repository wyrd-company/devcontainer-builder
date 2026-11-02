// relationships:
//
//	references: [gateway, ../builder/build_target]
package buildkit

import (
	"context"
	"encoding/json"

	"github.com/moby/buildkit/client/llb"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
)

// DevcontainerStage identifies the selected part of the generated build.
type DevcontainerStage int

const (
	FinalStage DevcontainerStage = iota
	AuthoredStage
	NormalizeStage
)

// solveSelectedStage exports an intermediate generated stage without final
// feature installs, container environment, or generated metadata.
func solveSelectedStage(ctx context.Context, gc gateway.Client, state llb.State, cfg []byte, graph GraphInputs, stage DevcontainerStage, labels map[string]string, bs buildSettings) (gateway.Reference, []byte, error) {
	var img dockerspec.DockerOCIImage
	if err := json.Unmarshal(cfg, &img); err != nil {
		return nil, nil, err
	}
	if stage == NormalizeStage {
		user := graph.ImageUser
		if user == "" {
			user = img.Config.User
		}
		effective := metadata.Merge(graph.Spec, metadata.Collect(metadata.Read(img.Config.Labels, nil), graph.Features, graph.Spec))
		graph.Spec = effective.DevContainer
		containerUser, remote := features.ContainerUsers(graph.Spec.ContainerUser, remoteUser(graph), user)
		data := "_CONTAINER_USER=" + containerUser + "\n_REMOTE_USER=" + remote + "\n"
		state = state.User("root").File(llb.Mkdir("/tmp/build-features", 0755, llb.WithParents(true))).File(llb.Mkfile("/tmp/build-features/"+features.BuiltinEnvFile, 0755, []byte(data)))
		state = state.Run(llb.User("root"), llb.Args([]string{"chmod", "-R", "0755", "/tmp/build-features/"})).Root()
		img.Config.User = "root"
	}
	constraints := []llb.ConstraintsOpt{llb.Platform(graph.ociPlatform())}
	if bs.noCache {
		constraints = append(constraints, llb.IgnoreCache)
	}
	def, err := state.Marshal(ctx, constraints...)
	if err != nil {
		return nil, nil, err
	}
	res, err := gc.Solve(ctx, gateway.SolveRequest{Definition: def.ToPB(), CacheImports: bs.cacheImports, Evaluate: true})
	if err != nil {
		return nil, nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, nil, err
	}
	if img.Config.Labels == nil {
		img.Config.Labels = map[string]string{}
	}
	for k, v := range labels {
		img.Config.Labels[k] = v
	}
	cfg, err = json.Marshal(img)
	return ref, cfg, err
}
