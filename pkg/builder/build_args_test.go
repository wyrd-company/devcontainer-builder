// relationships:
//
//	references: [build_options, builder, metadata, imageuser]
package builder

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildOptionsArgs(t *testing.T) {
	t.Setenv("SAMPLE_VALUE", "inherited")
	t.Setenv("SAMPLE_EMPTY", "")
	t.Setenv("SAMPLE_UNSET", "")
	if err := os.Unsetenv("SAMPLE_UNSET"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		args    []string
		want    map[string]string
		invalid bool
	}{
		{"separate", []string{"--build-arg", "VALUE=sample"}, map[string]string{"VALUE": "sample"}, false},
		{"text", []string{"--build-arg= sample =a=b,c"}, map[string]string{" sample ": "a=b,c"}, false},
		{"last", []string{"--build-arg=VALUE=first", "--build-arg=VALUE=last"}, map[string]string{"VALUE": "last"}, false},
		{"bare", []string{"--build-arg=SAMPLE_VALUE"}, map[string]string{"SAMPLE_VALUE": "inherited"}, false},
		{"empty", []string{"--build-arg=SAMPLE_VALUE="}, map[string]string{"SAMPLE_VALUE": ""}, false},
		{"inherited-empty", []string{"--build-arg=SAMPLE_EMPTY"}, map[string]string{"SAMPLE_EMPTY": ""}, false},
		{"unset", []string{"--build-arg=SAMPLE_UNSET"}, map[string]string{}, false},
		{"unset-retains", []string{"--build-arg=SAMPLE_UNSET=earlier", "--build-arg=SAMPLE_UNSET"}, map[string]string{"SAMPLE_UNSET": "earlier"}, false},
		{"empty-key", []string{"--build-arg==bad"}, nil, true},
		{"earlier-empty-key", []string{"--build-arg==bad", "--build-arg=VALUE=valid"}, nil, true},
		{"empty-arg", []string{"--build-arg="}, nil, true},
		{"missing", []string{"--build-arg"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Build: &spec.Build{Options: tc.args}}
			parsed, err := parseBuildOptions(dc)
			if (err != nil) != tc.invalid {
				t.Fatalf("parsed=%+v err=%v", parsed, err)
			}
			if !tc.invalid && !reflect.DeepEqual(parsed.Args, tc.want) {
				t.Fatalf("args=%v want=%v", parsed.Args, tc.want)
			}
		})
	}
}

func TestBuildOptionsArgsPrecedence(t *testing.T) {
	dc := &spec.DevContainer{Path: filepath.Join(t.TempDir(), "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Args: map[string]string{"VALUE": "config", "KEEP": "config"}, Options: []string{"--build-arg=VALUE=option", "--build-arg=EXTRA=option"}}}
	for _, explicit := range []map[string]string{nil, {"VALUE": "library"}} {
		_, df, err := prepareBaseImage(dc, Options{BuildArgs: explicit})
		if err != nil {
			t.Fatal(err)
		}
		want := "option"
		if explicit != nil {
			want = "library"
		}
		if df.BuildArgs["VALUE"] != want || df.BuildArgs["KEEP"] != "config" || df.BuildArgs["EXTRA"] != "option" {
			t.Fatalf("args=%v", df.BuildArgs)
		}
		df.BuildArgs["KEEP"] = "changed"
		if dc.Build.Args["KEEP"] != "config" {
			t.Fatal("mutated config args")
		}
	}
}

func TestBuildOptionsArgsRejectBeforeStaging(t *testing.T) {
	work := filepath.Join(t.TempDir(), "unallocated")
	opts := Options{Spec: &spec.DevContainer{Build: &spec.Build{Dockerfile: "Dockerfile", Options: []string{"--build-arg==bad"}}}, WorkDir: work}
	_, allocated, _, _, _, _, _, _, _, err := New().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
	if err == nil || allocated != "" {
		t.Fatalf("err=%v allocated=%q", err, allocated)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("staging: %v", err)
	}
}

// Image user inference follows config args, independently of build.options.
func TestBuildOptionsArgsUserInference(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\nARG ACCOUNT=default\nUSER ${ACCOUNT}\n")
	dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Args: map[string]string{"ACCOUNT": "config"}, Options: []string{"--build-arg=ACCOUNT=option"}}}
	got, err := offlineMetadataBuilder().imageUser(context.Background(), dc, Options{SkipImageMetaResolve: true})
	if err != nil || got != "config" {
		t.Fatalf("user=%q err=%v", got, err)
	}
}
