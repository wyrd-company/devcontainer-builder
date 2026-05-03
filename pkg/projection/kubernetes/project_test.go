package kubernetes

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestProject_Minimal(t *testing.T) {
	dc := &spec.DevContainer{
		Image: "alpine:3",
	}
	pj, err := Project(ProjectRequest{
		Spec:  dc,
		Image: "alpine:3",
		Name:  "alice",
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if pj.Pod == nil {
		t.Fatal("Pod is nil")
	}
	if pj.Pod.Name != "alice" {
		t.Errorf("Name = %q, want alice", pj.Pod.Name)
	}
	if got := pj.Pod.Spec.RestartPolicy; got != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", got)
	}
	if len(pj.Pod.Spec.Containers) != 1 {
		t.Fatalf("Containers len = %d, want 1", len(pj.Pod.Spec.Containers))
	}
	c := pj.Pod.Spec.Containers[0]
	if c.Name != DefaultContainerName {
		t.Errorf("Container.Name = %q, want %q", c.Name, DefaultContainerName)
	}
	if c.Image != "alpine:3" {
		t.Errorf("Image = %q, want alpine:3", c.Image)
	}
	// Keepalive entrypoint mirrors the docker host's; SIGTERM gets
	// trapped so kubectl delete stays fast.
	if got := strings.Join(c.Command, " "); got != "/bin/sh" {
		t.Errorf("Command = %q, want /bin/sh", got)
	}
	if len(c.Args) != 3 || c.Args[0] != "-c" || !strings.Contains(c.Args[1], "trap") {
		t.Errorf("Args = %q, want sh -c keepalive", c.Args)
	}
}

func TestProject_RequiresSpecAndImage(t *testing.T) {
	if _, err := Project(ProjectRequest{}); err == nil {
		t.Error("expected error for missing Spec")
	}
	if _, err := Project(ProjectRequest{Spec: &spec.DevContainer{}}); err == nil {
		t.Error("expected error for missing Image")
	}
}

func TestProject_Annotations(t *testing.T) {
	pj, err := Project(ProjectRequest{
		Spec:           &spec.DevContainer{},
		SpecPath:       "/host/.devcontainer/devcontainer.json",
		LocalWorkspace: "/host/proj",
		Image:          "alpine:3",
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := pj.Pod.Annotations[AnnotationLocalFolder]; got != "/host/proj" {
		t.Errorf("AnnotationLocalFolder = %q, want /host/proj", got)
	}
	if got := pj.Pod.Annotations[AnnotationConfigFile]; got != "/host/.devcontainer/devcontainer.json" {
		t.Errorf("AnnotationConfigFile = %q", got)
	}
}

func TestProject_ContainerWorkspace(t *testing.T) {
	cases := []struct {
		name string
		req  ProjectRequest
		want string
	}{
		{
			"explicit override",
			ProjectRequest{ContainerWorkspace: "/srv", Spec: &spec.DevContainer{}, Image: "x"},
			"/srv",
		},
		{
			"spec workspaceFolder",
			ProjectRequest{Spec: &spec.DevContainer{WorkspaceFolder: "/spec"}, Image: "x"},
			"/spec",
		},
		{
			"derived from local workspace",
			ProjectRequest{Spec: &spec.DevContainer{}, LocalWorkspace: "/host/proj", Image: "x"},
			"/workspaces/proj",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pj, err := Project(tc.req)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			if got := pj.Pod.Spec.Containers[0].WorkingDir; got != tc.want {
				t.Errorf("WorkingDir = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProject_ContainerEnvAndFeatureMerge(t *testing.T) {
	dc := &spec.DevContainer{
		ContainerEnv: map[string]any{
			"FOO": "1",
			"BAR": "2",
		},
	}
	feats := []features.Resolved{
		{
			Source: "ghcr.io/example/node:1",
			Metadata: features.Metadata{
				ContainerEnv: map[string]string{
					"BAR":      "from-feature",
					"NODE_ENV": "development",
				},
			},
		},
	}
	pj, err := Project(ProjectRequest{Spec: dc, Image: "x", Features: feats})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	got := envAsMap(pj.Pod.Spec.Containers[0].Env)
	if got["FOO"] != "1" {
		t.Errorf("FOO = %q, want 1", got["FOO"])
	}
	// Feature value overrides the spec value (matches docker host's
	// upsertEnv behavior).
	if got["BAR"] != "from-feature" {
		t.Errorf("BAR = %q, want from-feature", got["BAR"])
	}
	if got["NODE_ENV"] != "development" {
		t.Errorf("NODE_ENV = %q, want development", got["NODE_ENV"])
	}
}

func TestProject_ContainerUserNumeric(t *testing.T) {
	pj, err := Project(ProjectRequest{
		Spec:  &spec.DevContainer{ContainerUser: "1000:1000"},
		Image: "x",
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	sc := pj.Pod.Spec.Containers[0].SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
		t.Fatalf("RunAsUser = %v, want 1000", sc)
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup != 1000 {
		t.Fatalf("RunAsGroup = %v, want 1000", sc.RunAsGroup)
	}
}

func TestProject_ContainerUserNamedRejected(t *testing.T) {
	_, err := Project(ProjectRequest{
		Spec:  &spec.DevContainer{ContainerUser: "node"},
		Image: "x",
	})
	if err == nil {
		t.Fatal("expected error for non-numeric containerUser")
	}
}

func TestProject_PrivilegedAndCaps(t *testing.T) {
	priv := true
	dc := &spec.DevContainer{
		Privileged: &priv,
		CapAdd:     []string{"NET_ADMIN", "SYS_PTRACE"},
		SecurityOpt: []string{
			"no-new-privileges:true",
			"seccomp=unconfined",
		},
	}
	pj, err := Project(ProjectRequest{Spec: dc, Image: "x"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	sc := pj.Pod.Spec.Containers[0].SecurityContext
	if sc == nil {
		t.Fatal("SecurityContext is nil")
	}
	if sc.Privileged == nil || !*sc.Privileged {
		t.Error("Privileged not set")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("AllowPrivilegeEscalation should be false (no-new-privileges)")
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeUnconfined {
		t.Errorf("SeccompProfile = %+v, want Unconfined", sc.SeccompProfile)
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Add) != 2 {
		t.Errorf("Capabilities.Add = %+v, want NET_ADMIN, SYS_PTRACE", sc.Capabilities)
	}
}

func TestProject_WorkspaceVolumeDefaultHostPath(t *testing.T) {
	pj, err := Project(ProjectRequest{
		Spec:           &spec.DevContainer{},
		LocalWorkspace: "/host/proj",
		Image:          "x",
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	vols := pj.Pod.Spec.Volumes
	if len(vols) != 1 {
		t.Fatalf("Volumes len = %d, want 1", len(vols))
	}
	if vols[0].Name != "workspace" {
		t.Errorf("Volume name = %q, want workspace", vols[0].Name)
	}
	if vols[0].HostPath == nil || vols[0].HostPath.Path != "/host/proj" {
		t.Errorf("HostPath = %+v, want /host/proj", vols[0].HostPath)
	}
	mnt := pj.Pod.Spec.Containers[0].VolumeMounts
	if len(mnt) != 1 || mnt[0].Name != "workspace" || mnt[0].MountPath != "/workspaces/proj" {
		t.Errorf("VolumeMounts = %+v, want workspace -> /workspaces/proj", mnt)
	}
}

func TestProject_WorkspaceVolumeOverride(t *testing.T) {
	pvc := &corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "alice-workspace"},
	}
	pj, err := Project(ProjectRequest{
		Spec:                  &spec.DevContainer{WorkspaceFolder: "/srv"},
		LocalWorkspace:        "/host/proj",
		WorkspaceVolumeSource: pvc,
		Image:                 "x",
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	v := pj.Pod.Spec.Volumes[0]
	if v.PersistentVolumeClaim == nil || v.PersistentVolumeClaim.ClaimName != "alice-workspace" {
		t.Errorf("Volume = %+v, want PVC alice-workspace", v)
	}
	mnt := pj.Pod.Spec.Containers[0].VolumeMounts[0]
	if mnt.MountPath != "/srv" {
		t.Errorf("MountPath = %q, want /srv", mnt.MountPath)
	}
}

func TestProject_BindMount(t *testing.T) {
	dc := &spec.DevContainer{
		Mounts: []any{
			"type=bind,source=/host/secrets,target=/etc/secrets,readonly",
		},
	}
	pj, err := Project(ProjectRequest{Spec: dc, Image: "x"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	var found bool
	for _, vm := range pj.Pod.Spec.Containers[0].VolumeMounts {
		if vm.MountPath == "/etc/secrets" {
			found = true
			if !vm.ReadOnly {
				t.Error("VolumeMount should be ReadOnly")
			}
		}
	}
	if !found {
		t.Error("expected VolumeMount at /etc/secrets")
	}
}

func TestProject_RejectsVolumeTypeMount(t *testing.T) {
	dc := &spec.DevContainer{
		Mounts: []any{
			"type=volume,source=node-modules,target=/workspaces/proj/node_modules",
		},
	}
	_, err := Project(ProjectRequest{Spec: dc, Image: "x"})
	if err == nil {
		t.Fatal("expected error for type=volume mount")
	}
	if !strings.Contains(err.Error(), "type=volume") {
		t.Errorf("error %q should mention type=volume", err)
	}
}

func TestProject_RejectsRelativeBindSource(t *testing.T) {
	dc := &spec.DevContainer{
		Mounts: []any{
			"type=bind,source=relative/path,target=/x",
		},
	}
	_, err := Project(ProjectRequest{Spec: dc, Image: "x"})
	if err == nil {
		t.Fatal("expected error for relative bind source")
	}
}

// envAsMap is a test helper that flattens []corev1.EnvVar for compare.
func envAsMap(env []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		m[e.Name] = e.Value
	}
	return m
}
