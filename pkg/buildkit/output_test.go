// relationships:
//
//	references: [output]
package buildkit

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	buildctl "github.com/moby/buildkit/cmd/buildctl/build"
)

func TestParseOutput(t *testing.T) {
	for _, tc := range []struct {
		raw, typ, dest string
		attrs          map[string]string
	}{
		{"results", "local", "results", map[string]string{}},
		{"-", "tar", "-", map[string]string{}},
		{"type=local,dest=results,platform-split=false", "local", "results", map[string]string{"platform-split": "false"}},
		{`type=image,"name=example.test/sample:one,example.test/sample:two",push=true`, "image", "", map[string]string{"name": "example.test/sample:one,example.test/sample:two", "push": "true"}},
		{"type=registry,compression=zstd", "registry", "", map[string]string{"compression": "zstd"}},
		{"TYPE=oci,DEST=archive.tar,oci-mediatypes=true", "oci", "archive.tar", map[string]string{"oci-mediatypes": "true"}},
		{"type=docker", "docker", "", map[string]string{}},
		{"type=oci,tar=false,dest=layout", "oci", "layout", map[string]string{"tar": "false"}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParseOutput(tc.raw)
			if err != nil || got.Type != tc.typ || got.Destination != tc.dest || !reflect.DeepEqual(got.Attrs, tc.attrs) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
	for _, raw := range []string{"", "dest=result", "name=sample", "type=local", "type=local,dest=-", "type=tar,bad", `type=local,"dest=broken`, "type=image,dest=somewhere", "type=oci,tar=false", "type=tar,output=x"} {
		t.Run("invalid/"+raw, func(t *testing.T) {
			if _, err := ParseOutput(raw); err == nil {
				t.Fatal("accepted invalid output")
			}
		})
	}
}

// Compare the common CSV grammar and destinations against BuildKit's parser.
func TestOutputBuildKitAgreement(t *testing.T) {
	for _, raw := range []string{"type=local,dest=result,platform-split=false", "type=image,name=example.test/sample,push=true", `type=image,"name=example.test/one,example.test/two"`, "type=oci,tar=false,dest=layout", "type=docker,tar=false,dest=layout", "type=local", "type=image,dest=bad", "type=tar,bad"} {
		ours, err := ParseOutput(raw)
		theirs, refErr := buildctl.ParseOutput([]string{raw})
		if (err == nil) != (refErr == nil) {
			t.Fatalf("%q: ours %v, BuildKit %v", raw, err, refErr)
		}
		if err == nil && (ours.Type != theirs[0].Type || !reflect.DeepEqual(ours.Attrs, theirs[0].Attrs) || (ours.Directory() && ours.Destination != theirs[0].OutputDir)) {
			t.Fatalf("%q: ours %+v, BuildKit %+v", raw, ours, theirs)
		}
	}
}

func TestSpecifiedExport(t *testing.T) {
	for _, daemon := range []bool{true, false} {
		for _, tc := range []struct {
			raw, typ     string
			finish, load bool
		}{
			{"type=image,name=example.test/sample", "image", false, false},
			{"type=registry,name=example.test/sample", "image", true, false},
			{"type=image,name=example.test/sample,push=true", "image", true, false},
			{"type=image,name=example.test/sample,push=false", "image", false, false},
			{"type=docker", "docker", false, true},
			{"type=docker,dest=archive.tar", "docker", true, false},
			{"type=tar,dest=archive.tar", "tar", false, false},
			{"type=oci,dest=archive.tar", "oci", false, false},
			{"type=oci,tar=false,dest=layout", "oci", false, false},
			{"type=local,dest=results", "local", false, false},
		} {
			t.Run(tc.raw+map[bool]string{true: "/daemon", false: "/remote"}[daemon], func(t *testing.T) {
				plan, err := exportsFor(context.Background(), SolveOptions{Output: tc.raw, Tags: []string{"example.test/default"}}, &Connection{Daemon: daemon}, false)
				if err != nil {
					t.Fatal(err)
				}
				typ := tc.typ
				if daemon && (typ == "image" || typ == "docker") {
					typ = exporterMoby
				}
				if len(plan.entries) != 1 || plan.entries[0].Type != typ {
					t.Fatalf("entries %+v", plan.entries)
				}
				parsed, _ := ParseOutput(tc.raw)
				if parsed.Directory() && plan.entries[0].OutputDir != parsed.Destination {
					t.Fatalf("directory destination: %+v", plan.entries[0])
				}
				if parsed.Archive() && parsed.Destination != "" && !daemon && plan.entries[0].Output == nil {
					t.Fatal("archive output missing")
				}
				if tc.raw == "type=docker" && plan.entries[0].Attrs["name"] != "example.test/default" {
					t.Fatalf("default image name: %+v", plan.entries[0])
				}

				if (plan.finish != nil) != (daemon && tc.finish) || (plan.load != nil) != (!daemon && tc.load) {
					t.Fatalf("unexpected plan %+v", plan)
				}
				if tc.typ == "image" && !daemon && tc.finish && plan.entries[0].Attrs["push"] != "true" {
					t.Fatalf("missing push: %+v", plan.entries[0])
				}
			})
		}
	}
	cache, err := exportsFor(context.Background(), SolveOptions{Output: "type=cacheonly"}, &Connection{}, false)
	if err != nil || len(cache.entries) != 0 {
		t.Fatalf("cache export: %+v, %v", cache, err)
	}
	for _, opt := range []SolveOptions{{Output: "type=registry"}, {Output: "type=docker,dest=result.tar"}, {Output: "type=tar,dest=x", OutputDir: "x"}, {Output: "type=tar,dest=x", Load: true}, {Output: "type=local,dest=x", Push: true}, {Output: "type=image,push=invalid"}} {
		if _, err := exportsFor(context.Background(), opt, &Connection{Daemon: true}, false); err == nil {
			t.Fatalf("accepted %+v", opt)
		}
	}
	if _, err := exportsFor(context.Background(), SolveOptions{Output: "type=docker"}, &Connection{}, true); err == nil {
		t.Fatal("docker multi-platform accepted")
	}
}

func TestOutputDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.tar")
	if err := os.WriteFile(path, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := ParseOutput("type=tar,dest=" + path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := exportsFor(context.Background(), SolveOptions{Output: "type=tar,dest=" + path}, &Connection{}, false)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "previous" {
		t.Fatal("planning truncated destination")
	}
	if plan.entries[0].Output == nil {
		t.Fatal("archive callback missing")
	}
	w, err := plan.entries[0].Output(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(w, "result")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "result" {
		t.Fatalf("archive %q", data)
	}
	var stdout bytes.Buffer
	out.Destination = "-"
	w, err = archiveOutput(out, &stdout)(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "stream")
	_ = w.Close()
	if stdout.String() != "stream" {
		t.Fatalf("stdout %q", stdout.String())
	}
	for _, raw := range []string{"type=tar", "type=oci", "type=docker,dest=-", "-"} {
		o, err := ParseOutput(raw)
		if err != nil || !o.ToStdout() {
			t.Fatalf("%s %+v %v", raw, o, err)
		}
	}
	o, _ := ParseOutput("type=docker")
	if o.ToStdout() {
		t.Fatal("docker default must load")
	}
}
