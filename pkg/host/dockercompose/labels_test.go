package dockercompose

import (
	"sort"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestContainerLabels_PrimaryAndDeps(t *testing.T) {
	got := containerLabels("proj_abc", "/work", "/work/.devcontainer/devcontainer.json", "app", []string{"db", "cache"}, true)
	if got[LabelProject] != "proj_abc" {
		t.Errorf("project label: got %q", got[LabelProject])
	}
	if got[LabelService] != "app" {
		t.Errorf("service label: got %q", got[LabelService])
	}
	if got[LabelPrimary] != "true" {
		t.Errorf("primary label: got %q", got[LabelPrimary])
	}
	if got[LabelLocalFolder] != "/work" {
		t.Errorf("local folder label: got %q", got[LabelLocalFolder])
	}
	if got[LabelConfigFile] != "/work/.devcontainer/devcontainer.json" {
		t.Errorf("config file label: got %q", got[LabelConfigFile])
	}
	if got[LabelDependencies] != "cache:service_started:true,db:service_started:true" {
		t.Errorf("depends_on label: got %q", got[LabelDependencies])
	}
}

func TestResourceName_DefaultsToProjectPrefix(t *testing.T) {
	if got := resourceName("proj_abc", "default", ""); got != "proj_abc_default" {
		t.Errorf("got %q want proj_abc_default", got)
	}
	if got := resourceName("proj_abc", "default", "explicit"); got != "explicit" {
		t.Errorf("explicit override: got %q want explicit", got)
	}
}

func TestDependsOnNames_Sorted(t *testing.T) {
	svc := types.ServiceConfig{
		DependsOn: types.DependsOnConfig{
			"zeta": types.ServiceDependency{},
			"alpha": types.ServiceDependency{},
		},
	}
	got := dependsOnNames(svc)
	want := []string{"alpha", "zeta"}
	if !sort.StringsAreSorted(got) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("dependsOnNames: got %v want %v", got, want)
	}
}
