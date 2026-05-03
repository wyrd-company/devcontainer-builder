package dockercompose

import (
	"sort"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestContainerLabels_PrimaryHasDevcontainerLabels(t *testing.T) {
	deps := []dependency{
		{Name: "db", Condition: types.ServiceConditionHealthy, Required: true},
		{Name: "cache", Condition: types.ServiceConditionStarted, Required: true},
		{Name: "seeder", Condition: types.ServiceConditionCompletedSuccessfully, Required: false},
	}
	got := containerLabels("proj_abc", "/work", "/work/.devcontainer/devcontainer.json", "app", deps, true)
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
		t.Errorf("local folder label on primary: got %q", got[LabelLocalFolder])
	}
	if got[LabelConfigFile] != "/work/.devcontainer/devcontainer.json" {
		t.Errorf("config file label on primary: got %q", got[LabelConfigFile])
	}
	want := "cache:service_started:true,db:service_healthy:true,seeder:service_completed_successfully:false"
	if got[LabelDependencies] != want {
		t.Errorf("depends_on label: got %q want %q", got[LabelDependencies], want)
	}
}

func TestContainerLabels_NonPrimaryOmitsDevcontainerLabels(t *testing.T) {
	got := containerLabels("proj_abc", "/work", "/work/.devcontainer/devcontainer.json", "db", nil, false)
	if _, ok := got[LabelLocalFolder]; ok {
		t.Errorf("non-primary services must not carry %s; got %q", LabelLocalFolder, got[LabelLocalFolder])
	}
	if _, ok := got[LabelConfigFile]; ok {
		t.Errorf("non-primary services must not carry %s; got %q", LabelConfigFile, got[LabelConfigFile])
	}
	if _, ok := got[LabelPrimary]; ok {
		t.Errorf("non-primary services must not carry %s", LabelPrimary)
	}
	if got[LabelService] != "db" {
		t.Errorf("service label: got %q", got[LabelService])
	}
	if got[LabelProject] != "proj_abc" {
		t.Errorf("project label: got %q", got[LabelProject])
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

func TestDependsOnList_SortedAndDefaulted(t *testing.T) {
	svc := types.ServiceConfig{
		DependsOn: types.DependsOnConfig{
			"zeta":  types.ServiceDependency{Condition: types.ServiceConditionHealthy, Required: true},
			"alpha": types.ServiceDependency{Required: true}, // condition empty -> service_started
		},
	}
	got := dependsOnList(svc)
	names := make([]string, 0, len(got))
	for _, d := range got {
		names = append(names, d.Name)
	}
	if !sort.StringsAreSorted(names) || names[0] != "alpha" || names[1] != "zeta" {
		t.Fatalf("dependsOnList sort: got %v", names)
	}
	if got[0].Condition != types.ServiceConditionStarted {
		t.Errorf("alpha condition default: got %q", got[0].Condition)
	}
	if got[1].Condition != types.ServiceConditionHealthy {
		t.Errorf("zeta condition: got %q", got[1].Condition)
	}
}
