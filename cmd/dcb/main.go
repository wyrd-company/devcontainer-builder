// Command dcb builds devcontainer-spec images via docker buildx.
//
// Subcommands:
//
//	build     Build a devcontainer image from devcontainer.json.
//	generate  Generate the wrapper Dockerfile and build context only.
//	features  Resolve and stage features into a directory.
//	server    Download a VS Code server tarball.
//	version   Print version information.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/buildx"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

// Version is overridden at link time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var err error
	switch cmd {
	case "build":
		err = cmdBuild(ctx, args)
	case "generate":
		err = cmdGenerate(ctx, args)
	case "features":
		err = cmdFeatures(ctx, args)
	case "server":
		err = cmdServer(ctx, args)
	case "version", "--version", "-v":
		err = cmdVersion(ctx)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.SetFlags(0)
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `dcb - build devcontainer-spec images with docker buildx

Usage:
  dcb <command> [flags]

Commands:
  build      Build a devcontainer image
  generate   Generate the wrapper Dockerfile and build context, do not build
  features   Resolve and stage devcontainer features into a directory
  server     Download a VS Code server tarball
  version    Print version information
  help       Show this help

Run "dcb <command> -h" for command-specific flags.`)
}

// stringSlice is a flag.Value that collects repeated occurrences.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

// keyValSlice parses repeated --foo=KEY=VAL into a map.
type keyValSlice map[string]string

func (k keyValSlice) String() string {
	parts := make([]string, 0, len(k))
	for kk, vv := range k {
		parts = append(parts, kk+"="+vv)
	}
	return strings.Join(parts, ",")
}

func (k keyValSlice) Set(v string) error {
	eq := strings.IndexByte(v, '=')
	if eq < 0 {
		return fmt.Errorf("expected KEY=VAL, got %q", v)
	}
	k[v[:eq]] = v[eq+1:]
	return nil
}

func cmdBuild(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	var (
		workspace = fs.String("workspace", ".", "workspace folder containing .devcontainer/")
		specPath  = fs.String("spec", "", "path to devcontainer.json (overrides workspace lookup)")
		engine    = fs.String("engine", "buildkit", "build engine: buildkit | buildx")
		bkAddr    = fs.String("buildkit-addr", "", "buildkitd address (default: $BUILDKIT_HOST or unix socket)")
		builderID = fs.String("builder", "", "buildx builder name (buildx engine only)")
		progress  = fs.String("progress", "auto", "progress display (auto|plain|tty)")
		outputDir = fs.String("output", "", "export filesystem to local dir (buildkit engine only)")
		workDir   = fs.String("work-dir", "", "directory for staged build context (default: temp dir)")
		keepWork  = fs.Bool("keep", false, "keep the staged work directory after build")
		push      = fs.Bool("push", false, "push the resulting image (mutually exclusive with --load)")
		load      = fs.Bool("load", false, "load the resulting image into local docker store")
		noCache   = fs.Bool("no-cache", false, "disable build cache")
		pull      = fs.Bool("pull", false, "always pull referenced images")

		skipFeatures = fs.Bool("skip-features", false, "do not resolve or install devcontainer features")
		skipServer   = fs.Bool("skip-vscode-server", false, "do not inject the VS Code server")

		serverQuality = fs.String("vscode-quality", "stable", "VS Code server channel: stable|insider")
		serverCommit  = fs.String("vscode-commit", "", "pin VS Code server to a specific commit (default: latest)")
		serverAlpine  = fs.Bool("vscode-alpine", false, "download the alpine/musl variant of the server")
		serverPlat    = fs.String("vscode-platform", "", "VS Code server platform override (e.g., linux/arm64)")

		tags      stringSlice
		platforms stringSlice
		cacheFrom stringSlice
		cacheTo   stringSlice
		buildArgs = keyValSlice{}
		labels    = keyValSlice{}
	)
	fs.Var(&tags, "tag", "image tag (repeat for multiple)")
	fs.Var(&platforms, "platform", "target platform (repeat for multiple)")
	fs.Var(&cacheFrom, "cache-from", "buildx --cache-from spec (repeat)")
	fs.Var(&cacheTo, "cache-to", "buildx --cache-to spec (repeat)")
	fs.Var(buildArgs, "build-arg", "KEY=VAL build arg (repeat)")
	fs.Var(labels, "label", "KEY=VAL image label (repeat)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *push && *load {
		return fmt.Errorf("--push and --load are mutually exclusive")
	}
	if len(tags) == 0 && (*push || *load) {
		return fmt.Errorf("--tag is required with --push/--load")
	}
	switch *engine {
	case string(builder.EngineBuildKit), string(builder.EngineBuildx):
	default:
		return fmt.Errorf("invalid --engine %q (want buildkit|buildx)", *engine)
	}

	logger := stderrLogger("build")

	opts := builder.Options{
		Engine:           builder.Engine(*engine),
		SpecPath:         *specPath,
		Workspace:        *workspace,
		Tags:             tags,
		Platforms:        platforms,
		Builder:          *builderID,
		BuildKitAddress:  *bkAddr,
		BuildArgs:        buildArgs,
		Labels:           labels,
		CacheFrom:        cacheFrom,
		CacheTo:          cacheTo,
		Push:             *push,
		Load:             *load,
		NoCache:          *noCache,
		Pull:             *pull,
		Progress:         *progress,
		OutputDir:        *outputDir,
		SkipFeatures:     *skipFeatures,
		SkipVSCodeServer: *skipServer,
		WorkDir:          *workDir,
		KeepWorkDir:      *keepWork,
		Logger:           logger,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		VSCode: vscode.Spec{
			Quality:    vscode.Quality(*serverQuality),
			Commit:     *serverCommit,
			AlpineLibc: *serverAlpine,
		},
	}
	if *serverPlat != "" {
		p, err := platform.Parse(*serverPlat)
		if err != nil {
			return err
		}
		opts.VSCode.Platform = p
	}

	res, err := builder.New().Build(ctx, opts)
	if err != nil {
		if res != nil {
			fmt.Fprintf(os.Stderr, "build artifacts retained at %s\n", res.ContextDir)
		}
		return err
	}
	fmt.Fprintln(os.Stderr, "built image")
	if res.VSCodeServer != nil {
		fmt.Fprintf(os.Stderr, "  vscode server: %s @ %s (%d bytes)\n",
			res.VSCodeServer.Quality, res.VSCodeServer.Commit, res.VSCodeServer.Bytes)
	}
	if len(res.Features) > 0 {
		fmt.Fprintln(os.Stderr, "  features:")
		for _, f := range res.Features {
			fmt.Fprintf(os.Stderr, "    - %s (%s)\n", f.Metadata.ID, f.Source)
		}
	}
	return nil
}

func cmdGenerate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	var (
		workspace = fs.String("workspace", ".", "workspace folder")
		specPath  = fs.String("spec", "", "path to devcontainer.json")
		workDir   = fs.String("work-dir", "", "where to stage the build context")
		platStr   = fs.String("platform", "", "platform for VS Code server (e.g., linux/amd64)")
		quality   = fs.String("vscode-quality", "stable", "stable|insider")
		commit    = fs.String("vscode-commit", "", "pin VS Code server commit")
		skipSrv   = fs.Bool("skip-vscode-server", false, "do not download the server")
		skipFeat  = fs.Bool("skip-features", false, "do not resolve features")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	logger := stderrLogger("generate")
	opts := builder.Options{
		SpecPath:         *specPath,
		Workspace:        *workspace,
		WorkDir:          *workDir,
		KeepWorkDir:      true,
		Logger:           logger,
		SkipFeatures:     *skipFeat,
		SkipVSCodeServer: *skipSrv,
		Progress:         "plain",
		VSCode: vscode.Spec{
			Quality: vscode.Quality(*quality),
			Commit:  *commit,
		},
	}
	if *platStr != "" {
		p, err := platform.Parse(*platStr)
		if err != nil {
			return err
		}
		opts.VSCode.Platform = p
		opts.Platforms = []string{*platStr}
	}
	res, err := builder.New().Generate(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Println(res.FinalDockerfile)
	fmt.Fprintf(os.Stderr, "context: %s\n", res.ContextDir)
	if res.VSCodeServer != nil {
		fmt.Fprintf(os.Stderr, "vscode-server: %s (%s, %d bytes)\n",
			res.VSCodeServer.TarballPath, res.VSCodeServer.Commit, res.VSCodeServer.Bytes)
	}
	return nil
}

func cmdFeatures(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("features", flag.ExitOnError)
	var (
		workspace = fs.String("workspace", ".", "workspace folder")
		specPath  = fs.String("spec", "", "path to devcontainer.json")
		out       = fs.String("out", "", "directory to stage features into (required)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("--out is required")
	}
	_ = ctx

	logger := stderrLogger("features")
	dc, err := loadSpec(*specPath, *workspace)
	if err != nil {
		return err
	}
	if dc.Features.Len() == 0 {
		fmt.Fprintln(os.Stderr, "no features in spec")
		return nil
	}
	r := features.New()
	r.Logger = logger
	resolved, err := r.Resolve(dc.Features, dc.OverrideFeature, *out, filepath.Dir(dc.Path))
	if err != nil {
		return err
	}
	fmt.Printf("%-50s %-15s %s\n", "ID", "VERSION", "DIR")
	for _, f := range resolved {
		fmt.Printf("%-50s %-15s %s\n", f.Metadata.ID, f.Metadata.Version, f.Dir)
	}
	return nil
}

func cmdServer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	var (
		out     = fs.String("out", ".", "destination directory")
		platStr = fs.String("platform", "", "platform (default: current)")
		quality = fs.String("quality", "stable", "stable|insider")
		commit  = fs.String("commit", "", "pin to a specific commit")
		alpine  = fs.Bool("alpine", false, "fetch alpine/musl build")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	logger := stderrLogger("server")
	d := vscode.NewDownloader()
	d.Logger = logger
	s := vscode.Spec{
		Quality:    vscode.Quality(*quality),
		Commit:     *commit,
		AlpineLibc: *alpine,
	}
	if *platStr != "" {
		p, err := platform.Parse(*platStr)
		if err != nil {
			return err
		}
		s.Platform = p
	} else {
		s.Platform = platform.Current()
	}
	res, err := d.Fetch(ctx, s, *out)
	if err != nil {
		return err
	}
	fmt.Printf("path:    %s\n", res.TarballPath)
	fmt.Printf("commit:  %s\n", res.Commit)
	fmt.Printf("quality: %s\n", res.Quality)
	fmt.Printf("sha256:  %s\n", res.SHA256)
	fmt.Printf("size:    %d\n", res.Bytes)
	fmt.Printf("url:     %s\n", res.URL)
	return nil
}

func cmdVersion(ctx context.Context) error {
	fmt.Printf("dcb %s\n", Version)
	if v, err := buildx.Version(ctx); err == nil {
		fmt.Printf("  buildx: %s\n", v)
	}
	return nil
}

func loadSpec(specPath, workspace string) (*spec.DevContainer, error) {
	if specPath != "" {
		return spec.Load(specPath)
	}
	return spec.LoadFromWorkspace(workspace)
}

func stderrLogger(prefix string) func(string, ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[%s] ", prefix)
		fmt.Fprintf(os.Stderr, format, args...)
		fmt.Fprintln(os.Stderr)
	}
}
