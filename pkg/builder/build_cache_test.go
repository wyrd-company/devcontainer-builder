// relationships:
//   references: [build_cache, builder]

package builder

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildCachesGrammar(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   []string
		bad    bool
	}{
		{"bare-list", []string{"example.test/a,example.test/b"}, []string{"registry:example.test/a", "registry:example.test/b"}, false},
		{"quoted", []string{`type=local,"src=sample,cache",TAG=first,tag=last`}, []string{"local:sample,cache:last"}, false},
		{"repeated", []string{"example.test/a", "", "example.test/a"}, []string{"registry:example.test/a", "registry:example.test/a"}, false},
		{"missing-type", []string{"ref=example.test/a"}, nil, true},
		{"invalid-field", []string{"type=local,broken"}, nil, true},
		{"invalid-csv", []string{`type=local,"src=broken`}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parseBuildCaches(tc.values)
			if (err != nil) != tc.bad {
				t.Fatalf("entries=%+v err=%v", entries, err)
			}
			if tc.bad {
				return
			}
			var got []string
			for _, e := range entries {
				value := e.Attrs["ref"]
				if e.Type == "local" {
					value = e.Attrs["src"] + ":" + e.Attrs["tag"]
				}
				got = append(got, e.Type+":"+value)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestBuildCachesOrder(t *testing.T) {
	dc := &spec.DevContainer{Build: &spec.Build{CacheFrom: spec.StringOrSlice{"configured"}, Options: []string{"--cache-from=option-first", "--cache-from", "option-last", "--cache-to=type=local,dest=option-export"}}}
	for _, noCache := range []bool{false, true} {
		opts := Options{CacheFrom: []string{"library"}, CacheTo: []string{"type=inline"}, NoCache: noCache}
		got, err := solveOptions(opts, t.TempDir(), dc)
		if err != nil {
			t.Fatal(err)
		}
		var imports []string
		for _, e := range got.CacheImports {
			imports = append(imports, e.Attrs["ref"])
		}
		want := []string{"library", "configured", "option-first", "option-last"}
		if noCache {
			want = want[2:]
		}
		if !reflect.DeepEqual(imports, want) {
			t.Fatalf("noCache=%v imports=%v want=%v", noCache, imports, want)
		}
		if len(got.CacheExports) != 2 || got.CacheExports[0].Type != "inline" || got.CacheExports[1].Attrs["dest"] != "option-export" {
			t.Fatalf("exports=%+v", got.CacheExports)
		}
	}
}

func TestBuildCachesErrors(t *testing.T) {
	for _, flag := range []string{"--cache-from", "--cache-to"} {
		for _, value := range []string{"ref=missing-type", "type=local,broken"} {
			dc := &spec.DevContainer{Build: &spec.Build{Options: []string{flag, value}}}
			if _, err := solveOptions(Options{}, t.TempDir(), dc); err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(flag, "--")) {
				t.Fatalf("%s=%s err=%v", flag, value, err)
			}
		}
	}
}

func TestCacheBuildArgumentDefault(t *testing.T) {
	if got := defaultCacheBuildArgs(Options{}); got["BUILDKIT_INLINE_CACHE"] != "1" {
		t.Fatal(got)
	}
	if got := defaultCacheBuildArgs(Options{CacheTo: []string{"type=inline"}}); got != nil {
		t.Fatal(got)
	}
}

func TestBuildCacheCredentials(t *testing.T) {
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "sample-token")
	t.Setenv("ACTIONS_CACHE_URL", "https://cache.example.test")
	t.Setenv("ACTIONS_RESULTS_URL", "")
	t.Setenv("ACTIONS_CACHE_SERVICE_V2", "")
	refs, err := parseBuildCaches([]string{"type=gha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Attrs["token"] != "sample-token" || refs[0].Attrs["url"] != "https://cache.example.test" {
		t.Fatalf("native credential defaults missing")
	}
	refs, err = parseBuildCaches([]string{"type=gha,token=explicit,url=https://explicit.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Attrs["token"] != "explicit" {
		t.Fatal("explicit credential overridden")
	}
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "")
	t.Setenv("ACTIONS_CACHE_URL", "")
	refs, err = parseBuildCaches([]string{"type=gha"})
	if err != nil || len(refs) != 0 {
		t.Fatal("inactive native cache must be omitted")
	}
}

func TestBuildCacheRejectBeforeStaging(t *testing.T) {
	work := filepath.Join(t.TempDir(), "unallocated")
	opts := Options{Spec: &spec.DevContainer{Build: &spec.Build{Dockerfile: "Dockerfile", Options: []string{"--cache-from=ref=missing-type"}}}, WorkDir: work}
	_, allocated, _, _, _, _, _, _, _, err := New().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
	if err == nil || allocated != "" {
		t.Fatalf("err=%v allocated=%q", err, allocated)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("staging: %v", err)
	}
}

func TestInlineCacheArgumentPrecedence(t *testing.T) {
	dc := &spec.DevContainer{Path: filepath.Join(t.TempDir(), "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile"}}
	for _, stage := range []string{"default", "configured", "option", "library"} {
		opts := Options{}
		want := "1"
		if stage != "default" {
			dc.Build.Args = map[string]string{"BUILDKIT_INLINE_CACHE": "false"}
			want = "false"
		}
		if stage == "option" || stage == "library" {
			dc.Build.Options = []string{"--build-arg=BUILDKIT_INLINE_CACHE=true"}
			want = "true"
		}
		if stage == "library" {
			opts.BuildArgs = map[string]string{"BUILDKIT_INLINE_CACHE": "0"}
			want = "0"
		}
		_, upstream, err := prepareBaseImage(dc, opts)
		if err != nil || upstream.BuildArgs["BUILDKIT_INLINE_CACHE"] != want {
			t.Fatalf("stage=%s upstream=%+v err=%v", stage, upstream, err)
		}
	}
}
