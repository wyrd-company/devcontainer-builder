package docker

import (
	"reflect"
	"testing"

	"github.com/docker/docker/api/types/mount"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestApplyRequestIdentity(t *testing.T) {
	build := func(dc *spec.DevContainer) ([]mount.Mount, map[string]string, func(UpRequest) ([]mount.Mount, map[string]string)) {
		cfg, host, _, err := buildContainerConfig(dc, nil, "img:latest", "/workspaces/proj/app", "/host/proj/app", "/host/proj/app/.devcontainer/devcontainer.json")
		if err != nil {
			t.Fatal(err)
		}
		return host.Mounts, cfg.Labels, func(req UpRequest) ([]mount.Mount, map[string]string) {
			applyRequestIdentity(cfg, host, dc, req)
			return host.Mounts, cfg.Labels
		}
	}
	root := mount.Mount{Type: mount.TypeBind, Source: "/host/proj", Target: "/workspaces/proj", Consistency: mount.ConsistencyCached}
	extra := []mount.Mount{{Type: mount.TypeVolume, Source: "cache", Target: "/cache"}, {Type: mount.TypeBind, Source: "/host/shared", Target: "/shared"}}
	specMount := []any{"type=bind,source=/host/data,target=/data"}

	t.Run("defaults", func(t *testing.T) {
		before, labels, apply := build(&spec.DevContainer{Mounts: specMount})
		before = append([]mount.Mount(nil), before...)
		mounts, after := apply(UpRequest{})
		if !reflect.DeepEqual(mounts, before) || after[LabelLocalFolder] != "/host/proj/app" || labels[LabelConfigFile] == "" {
			t.Fatalf("mounts=%+v labels=%v", mounts, after)
		}
	})
	t.Run("workspace mount and mounts", func(t *testing.T) {
		_, _, apply := build(&spec.DevContainer{Mounts: specMount})
		mounts, _ := apply(UpRequest{WorkspaceMount: &root, Mounts: extra})
		want := append([]mount.Mount{root, {Type: mount.TypeBind, Source: "/host/data", Target: "/data"}}, extra...)
		if !reflect.DeepEqual(mounts, want) {
			t.Fatalf("mounts=%+v\nwant %+v", mounts, want)
		}
	})
	t.Run("spec workspaceMount wins", func(t *testing.T) {
		_, _, apply := build(&spec.DevContainer{WorkspaceMount: "type=bind,source=/host/custom,target=/custom"})
		mounts, _ := apply(UpRequest{WorkspaceMount: &root})
		if len(mounts) != 1 || mounts[0].Source != "/host/custom" {
			t.Fatalf("mounts=%+v", mounts)
		}
	})
	t.Run("id labels replace the workspace labels", func(t *testing.T) {
		_, _, apply := build(&spec.DevContainer{})
		_, labels := apply(UpRequest{IDLabels: map[string]string{"example.id": "one", "example.role": "test"}})
		if labels["example.id"] != "one" || labels["example.role"] != "test" {
			t.Fatalf("labels=%v", labels)
		}
		if _, ok := labels[LabelLocalFolder]; ok {
			t.Fatalf("workspace label kept: %v", labels)
		}
		if _, ok := labels[LabelConfigFile]; ok {
			t.Fatalf("config label kept: %v", labels)
		}
	})
}
