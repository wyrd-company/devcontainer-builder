// relationships:
//   references: [build_sessions, build_options, builder]

package builder

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/docker/buildx/util/buildflags"
	"github.com/moby/buildkit/session/secrets"
	"github.com/moby/buildkit/session/sshforward"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildSessionsSecrets(t *testing.T) {
	t.Setenv("SAMPLE_BUILD_VALUE", "sample environment")
	file := filepath.Join(t.TempDir(), "sample,value")
	if err := os.WriteFile(file, []byte("sample file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		options []string
		want    string
		bad     bool
	}{
		{"env", []string{"--secret=id=sample,env=SAMPLE_BUILD_VALUE"}, "sample environment", false},
		{"env-type", []string{"--secret", "type=env,id=sample,src=SAMPLE_BUILD_VALUE"}, "sample environment", false},
		{"file-csv", []string{"--secret", "id=sample,\"src=" + file + "\""}, "sample file", false},
		{"last-id", []string{"--secret=id=sample,env=UNSET_SAMPLE_VALUE", "--secret=id=sample,env=SAMPLE_BUILD_VALUE"}, "sample environment", false},
		{"unset-env", []string{"--secret=id=sample,env=UNSET_SAMPLE_VALUE"}, "", false},
		{"missing-id", []string{"--secret=env=SAMPLE_BUILD_VALUE"}, "", true},
		{"missing-file", []string{"--secret=id=sample,src=" + file + "-missing"}, "", true},
		{"bad-key", []string{"--secret=id=sample,unknown=x"}, "", true},
		{"bad-type", []string{"--secret=type=unknown,id=sample"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Build: &spec.Build{Options: tc.options}}
			opt, err := solveOptions(Options{}, t.TempDir(), dc)
			if tc.bad {
				if err == nil {
					t.Fatal("accepted invalid source")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(opt.Session) != 1 {
				t.Fatalf("providers=%d", len(opt.Session))
			}
			server, ok := opt.Session[0].(secrets.SecretsServer)
			if !ok {
				t.Fatalf("provider=%T", opt.Session[0])
			}
			got, err := server.GetSecret(context.Background(), &secrets.GetSecretRequest{ID: "sample"})
			if err != nil || string(got.Data) != tc.want {
				t.Fatalf("secret=%v err=%v", got, err)
			}
			if _, err := server.GetSecret(context.Background(), &secrets.GetSecretRequest{ID: "absent"}); err == nil {
				t.Fatal("missing secret accepted")
			}
		})
	}
	t.Run("implicit-env", func(t *testing.T) {
		dc := &spec.DevContainer{Build: &spec.Build{Options: []string{"--secret=id=SAMPLE_BUILD_VALUE"}}}
		providers, err := buildSessions(dc)
		if err != nil {
			t.Fatal(err)
		}
		got, err := providers[0].(secrets.SecretsServer).GetSecret(context.Background(), &secrets.GetSecretRequest{ID: "SAMPLE_BUILD_VALUE"})
		if err != nil || string(got.Data) != "sample environment" {
			t.Fatalf("got=%v err=%v", got, err)
		}
	})
}

func TestBuildSessionsSSH(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("SSH_AUTH_SOCK", socket)
	for _, value := range []string{"default", "sample=" + socket, "=" + socket, "default="} {
		dc := &spec.DevContainer{Build: &spec.Build{Options: []string{"--ssh", value}}}
		opt, err := solveOptions(Options{}, t.TempDir(), dc)
		if err != nil {
			t.Fatal(err)
		}
		if len(opt.Session) != 1 {
			t.Fatalf("providers=%d", len(opt.Session))
		}
		server := opt.Session[0].(sshforward.SSHServer)
		id := "default"
		if value == "sample="+socket {
			id = "sample"
		}
		if _, err := server.CheckAgent(context.Background(), &sshforward.CheckAgentRequest{ID: id}); err != nil {
			t.Fatal(err)
		}
		if _, err := server.CheckAgent(context.Background(), &sshforward.CheckAgentRequest{ID: "absent"}); err == nil {
			t.Fatal("unknown SSH ID accepted")
		}
	}
	for _, values := range [][]string{{"default", "default"}, {"sample=" + socket + "-missing"}} {
		options := []string{}
		for _, v := range values {
			options = append(options, "--ssh", v)
		}
		if _, err := buildSessions(&spec.DevContainer{Build: &spec.Build{Options: options}}); err == nil {
			t.Fatalf("accepted %v", values)
		}
	}
}

func TestBuildSessionsNativeGrammar(t *testing.T) {
	options := []string{"--secret=", "--secret=id=sample,env=SAMPLE_VALUE", "--ssh=", "--ssh=sample=/sample/a,/sample/b"}
	dc := &spec.DevContainer{Build: &spec.Build{Options: options}}
	got, err := parseBuildOptions(dc)
	if err != nil {
		t.Fatal(err)
	}
	wantSecrets, err := buildflags.ParseSecretSpecs([]string{"", "id=sample,env=SAMPLE_VALUE"})
	if err != nil {
		t.Fatal(err)
	}
	wantSSH, err := buildflags.ParseSSHSpecs([]string{"", "sample=/sample/a,/sample/b"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Secrets, wantSecrets) || !reflect.DeepEqual(got.SSH, wantSSH) {
		t.Fatalf("grammar=%+v", got)
	}
	empty, err := buildSessions(&spec.DevContainer{Build: &spec.Build{Options: []string{"--secret=", "--ssh="}}})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=%v err=%v", empty, err)
	}
}
