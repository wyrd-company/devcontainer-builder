// relationships:
//
//	references: [options_exporters, build]
package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildExporterFlag(t *testing.T) {
	for _, raw := range []string{"results", "type=local,dest=results", "type=tar,dest=archive.tar", "type=oci,dest=archive.tar", "type=docker", "type=image,push=true", "type=registry"} {
		t.Run(raw, func(t *testing.T) {
			a := &application{stdout: io.Discard, logs: &logWriter{dst: io.Discard}, result: map[string]any{}}
			var opts buildOptions
			cmd := a.buildCommand(false)
			// Exercise the actual command's flag registration, then the shared options
			// preparation used by its action with a separate options value.
			if err := cmd.ParseFlags([]string{"--output", raw}); err != nil {
				t.Fatal(err)
			}
			if got := cmd.Flags().Lookup("output").Value.String(); got != raw {
				t.Fatalf("flag %q", got)
			}
			// Binding belongs to buildOptions.flags, so exercise that constructor too.
			bound := &cobra.Command{Use: "build"}
			opts.flags(bound, false)
			if err := bound.ParseFlags([]string{"--output", raw}); err != nil {
				t.Fatal(err)
			}
			got, err := opts.prepare(a, &spec.DevContainer{}, "/workspace", false)
			if err != nil || got.Output != raw || got.Load || got.OutputWriter != a.stdout {
				t.Fatalf("options %+v, %v", got, err)
			}
			a.buildResult(got, nil)
			if raw == "results" && a.result["outputDir"] != "results" {
				t.Fatalf("legacy directory result %+v", a.result)
			}
			if a.result["output"] != raw {
				t.Fatalf("result %+v", a.result)
			}
		})
	}
	a := &application{}
	if err := a.prepareExporter("type=local"); err == nil {
		t.Fatal("invalid exporter accepted")
	}
	var stderr strings.Builder
	a = &application{stderr: &stderr}
	if err := a.prepareExporter("type=tar"); err != nil || !a.exportStdout {
		t.Fatalf("stdout mode %v, %v", a.exportStdout, err)
	}
}

func TestExporterDocumentation(t *testing.T) {
	data, err := os.ReadFile("../../docs/user/usage.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"type=local,dest=", "type=tar,dest=", "type=oci,dest=", "type=docker,dest=", "type=image,name=", "type=registry,name=", "no JSON result line"} {
		if !strings.Contains(string(data), value) {
			t.Errorf("usage missing %q", value)
		}
	}
}
