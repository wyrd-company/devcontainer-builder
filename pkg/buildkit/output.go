// relationships:
//
//	references: [export, client]
package buildkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client"
	"github.com/tonistiigi/go-csvvalue"
)

// OutputSpec describes one buildx --output value. Attrs are exporter options;
// Destination is the client-side file or directory receiving the result.
type OutputSpec struct {
	Type        string
	Attrs       map[string]string
	Destination string
}

// ParseOutput parses buildx CSV attributes and its short destination form.
// It has no filesystem side effects; the exporter opens its output on demand.
func ParseOutput(raw string) (OutputSpec, error) {
	out := OutputSpec{Attrs: map[string]string{}}
	if !strings.Contains(raw, "=") {
		out.Type, out.Destination = client.ExporterLocal, raw
		if raw == "-" {
			out.Type = client.ExporterTar
		}
	} else {
		fields, err := csvvalue.Fields(raw, nil)
		if err != nil {
			return out, fmt.Errorf("invalid --output CSV: %w", err)
		}
		for _, field := range fields {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				return out, fmt.Errorf("invalid --output field %q: expected key=value", field)
			}
			key = strings.ToLower(key)
			switch key {
			case "type":
				out.Type = value
			case "dest":
				out.Destination = value
			default:
				out.Attrs[key] = value
			}
		}
	}
	if out.Type == "" {
		return out, fmt.Errorf("--output requires type=<type>")
	}
	if _, ok := out.Attrs["output"]; ok {
		return out, fmt.Errorf("--output uses dest=, not output=")
	}
	if out.Directory() {
		if out.Destination == "" {
			return out, fmt.Errorf("--output directory is required for %s exporter", out.Type)
		}
		if out.Type == client.ExporterLocal && out.Destination == "-" {
			return out, fmt.Errorf("local exporter cannot write to stdout")
		}
	} else if !out.Archive() && out.Destination != "" {
		return out, fmt.Errorf("destination is not supported by %s exporter", out.Type)
	}
	return out, nil
}

// Directory reports whether the exporter writes a directory or OCI layout.
func (o OutputSpec) Directory() bool {
	if o.Type == client.ExporterLocal {
		return true
	}
	if o.Type == client.ExporterOCI || o.Type == client.ExporterDocker {
		tar, err := strconv.ParseBool(o.Attrs["tar"])
		return err == nil && !tar
	}
	return false
}

// Archive reports whether the exporter writes an archive stream.
func (o OutputSpec) Archive() bool {
	return o.Type == client.ExporterTar || ((o.Type == client.ExporterOCI || o.Type == client.ExporterDocker) && !o.Directory())
}

// ToStdout reports whether archive bytes replace the normal stdout result.
func (o OutputSpec) ToStdout() bool {
	return o.Archive() && (o.Destination == "-" || (o.Destination == "" && o.Type != client.ExporterDocker))
}

func specifiedExport(ctx context.Context, opt SolveOptions, conn *Connection, multi bool) (*exportPlan, error) {
	out, err := ParseOutput(opt.Output)
	if err != nil {
		return nil, err
	}
	if opt.Push || opt.Load || opt.OutputDir != "" {
		return nil, fmt.Errorf("--output cannot be combined with --push, --load or OutputDir")
	}
	if multi && out.Type == client.ExporterDocker {
		return nil, fmt.Errorf("docker exporter does not support multi-platform images")
	}
	entry := client.ExportEntry{Type: out.Type, Attrs: out.Attrs}
	if out.Type == "cacheonly" {
		return &exportPlan{}, nil
	}
	if out.Type == "registry" {
		entry.Type = client.ExporterImage
		entry.Attrs["push"] = "true"
	}
	if entry.Type == client.ExporterImage || entry.Type == client.ExporterDocker || entry.Type == client.ExporterOCI {
		if _, ok := entry.Attrs["name"]; !ok && len(opt.Tags) > 0 {
			entry.Attrs["name"] = strings.Join(opt.Tags, ",")
		}
	}
	if out.Directory() {
		entry.OutputDir = out.Destination
	}
	if out.Archive() && !(out.Type == client.ExporterDocker && out.Destination == "") {
		entry.Output = archiveOutput(out, opt.OutputWriter)
	}
	// Docker's built-in worker exports images through moby. Preserve all image
	// attributes, but serve pushes and Docker archives through the Engine API,
	// as the classic store's moby exporter cannot perform either operation.
	if conn.Daemon && (entry.Type == client.ExporterImage || entry.Type == client.ExporterDocker) {
		push, err := outputPush(entry.Attrs)
		if err != nil {
			return nil, err
		}
		names := strings.Split(entry.Attrs["name"], ",")
		if entry.Attrs["name"] == "" && (push || entry.Output != nil) {
			return nil, fmt.Errorf("image name is required for push or Docker archive export")
		}
		if entry.Type == client.ExporterDocker && out.Directory() {
			// OCI layouts need the native exporter; no Engine save conversion applies.
			return &exportPlan{entries: []client.ExportEntry{entry}}, nil
		}
		archive := entry.Output
		entry.Type, entry.Output = exporterMoby, nil
		delete(entry.Attrs, "push")
		plan := &exportPlan{entries: []client.ExportEntry{entry}}
		if push || archive != nil {
			plan.finish = func(ctx context.Context) error {
				if push {
					if err := pushTags(ctx, conn.Docker, names, registryAuth(opt), progressOut(opt)); err != nil {
						return err
					}
				}
				if archive != nil {
					w, err := archive(nil)
					if err != nil {
						return err
					}
					defer w.Close()
					return saveTags(ctx, conn.Docker, names, w)
				}
				return nil
			}
		}
		return plan, nil
	}
	// A Docker exporter without a destination means load into the Engine.
	if entry.Type == client.ExporterDocker && out.Destination == "" && !out.Directory() {
		load := newEngineLoad(ctx, conn.Docker)
		entry.Output = load.output
		return &exportPlan{entries: []client.ExportEntry{entry}, load: load}, nil
	}
	return &exportPlan{entries: []client.ExportEntry{entry}}, nil
}

func outputPush(attrs map[string]string) (bool, error) {
	if raw, ok := attrs["push"]; ok {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return false, fmt.Errorf("invalid image exporter push value %q", raw)
		}
		return value, nil
	}
	return false, nil
}

func archiveOutput(out OutputSpec, stdout io.Writer) func(map[string]string) (io.WriteCloser, error) {
	return func(map[string]string) (io.WriteCloser, error) {
		if out.ToStdout() {
			if stdout == nil {
				stdout = os.Stdout
			}
			return nopCloser{stdout}, nil
		}
		return os.Create(out.Destination)
	}
}
