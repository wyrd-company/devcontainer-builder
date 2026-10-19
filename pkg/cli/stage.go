// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"context"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/internal/datafolder"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

func (a *application) resolveFeatures(dc *spec.DevContainer) ([]features.Resolved, func(), error) {
	if dc.Features.Len() == 0 {
		return nil, func() {}, nil
	}
	dir, err := datafolder.NewStaging(a.userDataFolder, "dcb-features-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	resolver := features.New()
	resolver.Logger = a.logf
	state, err := a.featureLockfile(dc, lockfileOptions{})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	resolved, err := resolver.ResolveWithLockfile(context.Background(), dc.Features, dc.OverrideFeature, dir, filepath.Dir(dc.Path), state.Lockfile)
	if err == nil {
		err = state.Prepare(resolved)
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return resolved, cleanup, nil
}

func (a *application) featuresCommand() *cobra.Command {
	var cfg configOptions
	var out string
	cmd := &cobra.Command{Use: "features", Short: "Resolve and stage devcontainer features", Args: cobra.NoArgs}
	cfg.flags(cmd)
	cmd.Flags().StringVar(&out, "out", "", "Feature staging directory (required)")
	_ = cmd.MarkFlagRequired("out")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if out == "" {
			return usageError("--out must not be empty")
		}
		dc, _, err := cfg.load()
		if err != nil {
			return err
		}
		resolver := features.New()
		resolver.Logger = a.logf
		state, err := a.featureLockfile(dc, lockfileOptions{})
		if err != nil {
			return err
		}
		var lock *features.Lockfile
		if state != nil {
			lock = state.Lockfile
		}
		resolved, err := resolver.ResolveWithLockfile(cmd.Context(), dc.Features, dc.OverrideFeature, out, filepath.Dir(dc.Path), lock)
		if err != nil {
			return err
		}
		if err := state.Prepare(resolved); err != nil {
			return err
		}
		staged := make([]map[string]string, 0, len(resolved))
		for _, f := range resolved {
			staged = append(staged, map[string]string{"id": f.Metadata.ID, "version": f.Metadata.Version, "dir": f.Dir})
		}
		a.result["features"] = staged
		return nil
	}
	return cmd
}

func (a *application) serverCommand() *cobra.Command {
	var out, quality, commit string
	var plat platform.Platform
	var alpine bool
	cmd := &cobra.Command{Use: "server", Short: "Download a VS Code server archive", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&out, "out", ".", "Destination directory")
	cmd.Flags().StringVar(&quality, "quality", "stable", "Server channel: stable or insider")
	cmd.Flags().StringVar(&commit, "commit", "", "Server commit (default: latest)")
	cmd.Flags().BoolVar(&alpine, "alpine", false, "Download the musl variant")
	cmd.Flags().Var(&platformValue{target: &plat}, "platform", "Server platform (default: current)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if plat.OS == "" {
			plat = platform.Current()
		}
		downloader := vscode.NewDownloader()
		downloader.Logger = a.logf
		res, err := downloader.Fetch(cmd.Context(), vscode.Spec{Quality: vscode.Quality(quality), Commit: commit, Platform: plat, AlpineLibc: alpine}, out)
		if err != nil {
			return err
		}
		a.result["path"], a.result["commit"], a.result["quality"] = res.TarballPath, res.Commit, res.Quality
		a.result["sha256"], a.result["bytes"], a.result["url"] = res.SHA256, res.Bytes, res.URL
		return nil
	}
	return cmd
}
