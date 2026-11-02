// relationships:
//   references: [builder, build_options, ../buildkit/client]

package builder

import (
	"fmt"
	"regexp"

	buildx "github.com/docker/buildx/build"
	"github.com/docker/buildx/util/buildflags"
	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// buildCacheSpecs preserves the pinned upstream argv order. NoCache suppresses
// generated import flags, while explicit build.options imports still apply.
func buildCacheSpecs(dc *spec.DevContainer, opts Options) ([]string, []string, error) {
	parsed, err := parseBuildOptions(dc)
	if err != nil {
		return nil, nil, err
	}
	var imports []string
	if !opts.NoCache {
		imports = append(imports, opts.CacheFrom...)
		imports = append(imports, buildCacheFrom(dc)...)
	}
	imports = append(imports, parsed.CacheFrom...)
	exports := append(append([]string(nil), opts.CacheTo...), parsed.CacheTo...)
	return imports, exports, nil
}

func validateBuildCaches(dc *spec.DevContainer, opts Options) error {
	imports, exports, err := buildCacheSpecs(dc, opts)
	if err != nil {
		return err
	}
	if _, err = buildflags.ParseCacheEntry(imports); err != nil {
		return fmt.Errorf("cache-from: %w", err)
	}
	if _, err = buildflags.ParseCacheEntry(exports); err != nil {
		return fmt.Errorf("cache-to: %w", err)
	}
	return nil
}

func buildCaches(dc *spec.DevContainer, opts Options) ([]buildkit.CacheRef, []buildkit.CacheRef, error) {
	imports, exports, err := buildCacheSpecs(dc, opts)
	if err != nil {
		return nil, nil, err
	}
	from, err := parseBuildCaches(imports)
	if err != nil {
		return nil, nil, fmt.Errorf("cache-from: %w", err)
	}
	to, err := parseBuildCaches(exports)
	if err != nil {
		return nil, nil, fmt.Errorf("cache-to: %w", err)
	}
	return from, to, nil
}

func parseBuildCaches(values []string) ([]buildkit.CacheRef, error) {
	entries, err := buildflags.ParseCacheEntry(values)
	if err != nil {
		return nil, err
	}
	var refs []buildkit.CacheRef
	for _, entry := range buildx.CreateCaches(entries) {
		refs = append(refs, buildkit.CacheRef{Type: entry.Type, Attrs: entry.Attrs})
	}
	return refs, nil
}

// The pinned CLI enables inline cache unless its explicit export is inline.
var inlineCacheTo = regexp.MustCompile(`(?i)type\s*=\s*inline`)

func defaultCacheBuildArgs(opts Options) map[string]string {
	for _, value := range opts.CacheTo {
		if inlineCacheTo.MatchString(value) {
			return nil
		}
	}
	return map[string]string{"BUILDKIT_INLINE_CACHE": "1"}
}
