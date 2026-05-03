package dockercompose

import (
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
)

func TestEvalCondition_Healthy(t *testing.T) {
	cases := []struct {
		name      string
		state     *container.State
		wantDone  bool
		wantErr   string // substring; "" expects no error
	}{
		{
			name:    "healthy",
			state:   &container.State{Health: &container.Health{Status: container.Healthy}},
			wantDone: true,
		},
		{
			name:    "starting",
			state:   &container.State{Health: &container.Health{Status: container.Starting}},
			wantDone: false,
		},
		{
			name:    "unhealthy",
			state:   &container.State{Health: &container.Health{Status: container.Unhealthy}},
			wantErr: "unhealthy",
		},
		{
			name:    "no healthcheck",
			state:   &container.State{},
			wantErr: "no healthcheck",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			done, err := evalCondition("dep", types.ServiceConditionHealthy, tc.state)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if done != tc.wantDone {
				t.Fatalf("done = %v, want %v", done, tc.wantDone)
			}
		})
	}
}

func TestEvalCondition_CompletedSuccessfully(t *testing.T) {
	cases := []struct {
		name     string
		state    *container.State
		wantDone bool
		wantErr  string
	}{
		{
			name:     "exited zero",
			state:    &container.State{Status: "exited", ExitCode: 0},
			wantDone: true,
		},
		{
			name:     "still running",
			state:    &container.State{Status: "running"},
			wantDone: false,
		},
		{
			name:    "exited non-zero",
			state:   &container.State{Status: "exited", ExitCode: 7},
			wantErr: "exited with code 7",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			done, err := evalCondition("dep", types.ServiceConditionCompletedSuccessfully, tc.state)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if done != tc.wantDone {
				t.Fatalf("done = %v, want %v", done, tc.wantDone)
			}
		})
	}
}

func TestEvalCondition_UnknownConditionRejected(t *testing.T) {
	if _, err := evalCondition("dep", "service_alien", &container.State{}); err == nil {
		t.Fatal("expected error for unknown condition")
	}
}
