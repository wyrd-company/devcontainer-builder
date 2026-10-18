package buildkit

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	digest "github.com/opencontainers/go-digest"
)

func TestParseCacheRefs(t *testing.T) {
	got := ParseCacheRefs([]string{
		"type=registry,ref=example.test/cache:main",
		"type=local,src=.cache,mode=max",
		"example.test/bare:cache",
		"",
		"ref=no-type",
	})
	want := []CacheRef{
		{Type: "registry", Attrs: map[string]string{"ref": "example.test/cache:main"}},
		{Type: "local", Attrs: map[string]string{"src": ".cache", "mode": "max"}},
		{Type: "registry", Attrs: map[string]string{"ref": "example.test/bare:cache"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseCacheRefs = %#v, want %#v", got, want)
	}
}

func TestResolveMode(t *testing.T) {
	cases := []struct {
		name   string
		pull   bool
		daemon bool
		want   string
	}{
		{"daemon prefers local images", false, true, pb.AttrImageResolveModePreferLocal},
		{"separate buildkit resolves from registry", false, false, pb.AttrImageResolveModeDefault},
		{"pull forces registry on daemon", true, true, pb.AttrImageResolveModeForcePull},
		{"pull forces registry on separate buildkit", true, false, pb.AttrImageResolveModeForcePull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveMode(SolveOptions{Pull: c.pull}, &Connection{Daemon: c.daemon})
			if got != c.want {
				t.Fatalf("resolveMode = %q, want %q", got, c.want)
			}
		})
	}
}

func TestExportsFor(t *testing.T) {
	daemon := &Connection{Daemon: true}
	remote := &Connection{Address: "tcp://buildkit.test:1234"}
	tags := []string{"example.test/app:1", "example.test/app:latest"}
	plan := func(t *testing.T, opt SolveOptions, conn *Connection, multi bool) *exportPlan {
		t.Helper()
		p, err := exportsFor(context.Background(), opt, conn, multi)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("load on daemon exports into the image store", func(t *testing.T) {
		p := plan(t, SolveOptions{Load: true, Tags: tags}, daemon, false)
		if p.load != nil || p.finish != nil {
			t.Fatal("daemon load needs no Engine API step")
		}
		assertSingleExport(t, p.entries, exporterMoby, map[string]string{"name": "example.test/app:1,example.test/app:latest"})
		if p.entries[0].Output != nil {
			t.Fatal("moby exporter must not have an output stream")
		}
	})

	t.Run("load on separate buildkit streams to the engine", func(t *testing.T) {
		p := plan(t, SolveOptions{Load: true, Tags: tags}, remote, false)
		if p.load == nil {
			t.Fatal("separate buildkit load must stream through the image-load endpoint")
		}
		assertSingleExport(t, p.entries, client.ExporterDocker, map[string]string{"name": "example.test/app:1,example.test/app:latest"})
		if p.entries[0].Output == nil {
			t.Fatal("docker exporter needs an output stream")
		}
	})

	t.Run("load writer on separate buildkit receives the docker exporter tarball", func(t *testing.T) {
		var buf bytes.Buffer
		p := plan(t, SolveOptions{Load: true, Tags: tags[:1], LoadWriter: &buf}, remote, false)
		if p.load != nil || p.finish != nil {
			t.Fatal("LoadWriter must not load into the daemon")
		}
		assertSingleExport(t, p.entries, client.ExporterDocker, map[string]string{"name": "example.test/app:1"})
	})

	t.Run("load writer on daemon exports into the store, then saves", func(t *testing.T) {
		var buf bytes.Buffer
		p := plan(t, SolveOptions{Load: true, Tags: tags[:1], LoadWriter: &buf}, daemon, false)
		assertSingleExport(t, p.entries, exporterMoby, map[string]string{"name": "example.test/app:1"})
		if p.finish == nil || p.load != nil {
			t.Fatal("daemon LoadWriter must save through the Engine API after the export")
		}
	})

	t.Run("push on daemon exports into the store, then pushes", func(t *testing.T) {
		p := plan(t, SolveOptions{Push: true, Tags: tags[:1]}, daemon, true)
		assertSingleExport(t, p.entries, exporterMoby, map[string]string{"name": "example.test/app:1"})
		if p.finish == nil {
			t.Fatal("daemon push must push through the Engine API after the export")
		}
	})

	t.Run("push on separate buildkit pushes from the exporter", func(t *testing.T) {
		p := plan(t, SolveOptions{Push: true, Tags: tags[:1]}, remote, true)
		assertSingleExport(t, p.entries, client.ExporterImage, map[string]string{"name": "example.test/app:1", "push": "true", "name-canonical": "true"})
		if p.finish != nil || p.load != nil {
			t.Fatal("separate buildkit push needs no Engine API step")
		}
	})

	t.Run("rejections", func(t *testing.T) {
		for _, c := range []struct {
			name  string
			opt   SolveOptions
			multi bool
			want  string
		}{
			{"push and load", SolveOptions{Push: true, Load: true, Tags: tags}, false, "mutually exclusive"},
			{"push without tag", SolveOptions{Push: true}, false, "--push requires"},
			{"load without tag", SolveOptions{Load: true}, false, "--load requires"},
			{"load multi-platform", SolveOptions{Load: true, Tags: tags}, true, "multi-platform"},
		} {
			_, err := exportsFor(context.Background(), c.opt, daemon, c.multi)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
			}
		}
	})

	t.Run("no output requested exports nothing", func(t *testing.T) {
		p := plan(t, SolveOptions{Tags: tags}, daemon, false)
		if p.entries != nil || p.load != nil || p.finish != nil {
			t.Fatalf("plan = %+v; want none", p)
		}
	})
}

func assertSingleExport(t *testing.T, exports []client.ExportEntry, typ string, attrs map[string]string) {
	t.Helper()
	if len(exports) != 1 {
		t.Fatalf("exports = %d, want 1", len(exports))
	}
	if exports[0].Type != typ {
		t.Fatalf("export type = %q, want %q", exports[0].Type, typ)
	}
	if !reflect.DeepEqual(exports[0].Attrs, attrs) {
		t.Fatalf("export attrs = %v, want %v", exports[0].Attrs, attrs)
	}
}

func TestEngineLoad_UnstartedLoadFailsAfterSuccessfulSolve(t *testing.T) {
	l := newEngineLoad(context.Background(), nil)
	if err := l.wait(nil); err == nil {
		t.Fatal("a successful solve that never opened the load stream must be reported")
	}
	if err := l.wait(context.Canceled); err != nil {
		t.Fatalf("a failed solve reports its own error, got load error %v", err)
	}
}

func TestMarshalOpts_NoCacheIgnoresCacheOnEveryVertex(t *testing.T) {
	st := llb.Image("docker.io/library/alpine:latest").Run(llb.Shlex("true")).Root()
	def, err := st.Marshal(context.Background(), marshalOpts(SolveOptions{NoCache: true})...)
	if err != nil {
		t.Fatal(err)
	}
	if n := countIgnoreCache(t, def.ToPB()); n.total == 0 || n.ignored != n.total {
		t.Fatalf("NoCache: %d of %d vertices ignore cache, want all", n.ignored, n.total)
	}

	def, err = st.Marshal(context.Background(), marshalOpts(SolveOptions{})...)
	if err != nil {
		t.Fatal(err)
	}
	if n := countIgnoreCache(t, def.ToPB()); n.total == 0 || n.ignored != 0 {
		t.Fatalf("default: %d of %d vertices ignore cache, want none", n.ignored, n.total)
	}
}

type cacheCount struct{ ignored, total int }

// countIgnoreCache counts the operation vertices of def that ignore the
// cache. The terminal vertex, which only names the result, is skipped.
func countIgnoreCache(t *testing.T, def *pb.Definition) cacheCount {
	t.Helper()
	var n cacheCount
	for _, dt := range def.Def[:len(def.Def)-1] {
		n.total++
		if md, ok := def.Metadata[string(digest.FromBytes(dt))]; ok && md.IgnoreCache {
			n.ignored++
		}
	}
	return n
}

func TestConnect_SelectsBuildKit(t *testing.T) {
	cases := []struct {
		name, addr, env string
		wantDaemon      bool
		wantAddr        string
	}{
		{"default is the docker daemon", "", "", true, ""},
		{"BUILDKIT_HOST selects a separate buildkit", "", "tcp://127.0.0.1:1", false, "tcp://127.0.0.1:1"},
		{"explicit address wins over BUILDKIT_HOST", "tcp://127.0.0.1:2", "tcp://127.0.0.1:1", false, "tcp://127.0.0.1:2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("BUILDKIT_HOST", c.env)
			// Neither client dials until first use.
			conn, err := Connect(context.Background(), c.addr, nil)
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			defer conn.Close()
			if conn.Daemon != c.wantDaemon || conn.Address != c.wantAddr {
				t.Fatalf("Daemon=%v Address=%q, want Daemon=%v Address=%q", conn.Daemon, conn.Address, c.wantDaemon, c.wantAddr)
			}
			if conn.Docker == nil || conn.BuildKit == nil {
				t.Fatal("Connect must set both clients")
			}
		})
	}
}
