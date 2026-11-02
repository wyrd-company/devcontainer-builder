// relationships:
//
//	implements: ../../../docs/audits/schema-conformance.md
package docker

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func TestRunArgsHealth(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want *container.HealthConfig
	}{
		{"absent", nil, nil},
		{"zero", []string{"--health-cmd=", "--health-interval=0", "--health-retries=0", "--no-healthcheck=false"}, nil},
		{"disable", []string{"--no-healthcheck"}, &container.HealthConfig{Test: []string{"NONE"}}},
		{"all", []string{"--health-cmd", "exit 0", "--health-interval=2s", "--health-timeout=3s", "--health-start-period=4s", "--health-start-interval=5s", "--health-retries=0x6"}, &container.HealthConfig{Test: []string{"CMD-SHELL", "exit 0"}, Interval: 2 * time.Second, Timeout: 3 * time.Second, StartPeriod: 4 * time.Second, StartInterval: 5 * time.Second, Retries: 6}},
		{"inherit-command", []string{"--health-interval=2s"}, &container.HealthConfig{Interval: 2 * time.Second}},
		{"final-values", []string{"--health-cmd=old", "--health-cmd=new", "--health-interval=-1s", "--health-interval=2s", "--health-retries=-1", "--health-retries=2", "--no-healthcheck", "--no-healthcheck=false"}, &container.HealthConfig{Test: []string{"CMD-SHELL", "new"}, Interval: 2 * time.Second, Retries: 2}},
		{"cleared-command", []string{"--health-cmd=old", "--health-cmd=", "--no-healthcheck"}, &container.HealthConfig{Test: []string{"NONE"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg container.Config
			var host container.HostConfig
			if _, err := applyRunArgs(tt.args, &cfg, &host); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.Healthcheck, tt.want) {
				t.Fatalf("health guard: got %#v want %#v", cfg.Healthcheck, tt.want)
			}
		})
	}
}

func TestRunArgsHealthErrors(t *testing.T) {
	for _, flag := range []string{"--health-cmd", "--health-interval", "--health-timeout", "--health-start-period", "--health-start-interval", "--health-retries", "--log-driver", "--log-opt"} {
		t.Run("missing"+flag, func(t *testing.T) {
			var cfg container.Config
			var host container.HostConfig
			_, err := applyRunArgs([]string{flag}, &cfg, &host)
			if err == nil || !strings.Contains(err.Error(), flag) {
				t.Fatalf("missing value guard: %v", err)
			}
		})
	}
	for _, flag := range []string{"--health-interval", "--health-timeout", "--health-start-period", "--health-start-interval", "--health-retries"} {
		for _, value := range []string{"-1", "invalid"} {
			t.Run(flag+value, func(t *testing.T) {
				if value == "-1" && flag != "--health-retries" {
					value = "-1s"
				}
				var cfg container.Config
				var host container.HostConfig
				_, err := applyRunArgs([]string{flag + "=" + value}, &cfg, &host)
				if err == nil || !strings.Contains(err.Error(), flag) {
					t.Fatalf("invalid health guard: %v", err)
				}
			})
		}
	}
	for _, args := range [][]string{{"--no-healthcheck", "--health-cmd=exit 0"}, {"--health-interval=1s", "--no-healthcheck"}, {"--no-healthcheck=invalid"}} {
		var cfg container.Config
		var host container.HostConfig
		_, err := applyRunArgs(args, &cfg, &host)
		if err == nil || !strings.Contains(err.Error(), "--no-healthcheck") {
			t.Fatalf("conflict guard %v: %v", args, err)
		}
	}
}

func TestRunArgsLogging(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		driver  string
		options map[string]string
	}{
		{"absent", nil, "", map[string]string{}},
		{"none", []string{"--log-driver=none"}, "none", map[string]string{}},
		{"options", []string{"--log-driver=none", "--log-driver=json-file", "--log-opt=max-size=1m", "--log-opt", "max-size=2m", "--log-opt=tag=one=two", "--log-opt=bare", "--log-opt=empty="}, "json-file", map[string]string{"max-size": "2m", "tag": "one=two", "bare": "", "empty": ""}},
		{"daemon-default", []string{"--log-opt=tag=sample"}, "", map[string]string{"tag": "sample"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var cfg container.Config
			var host container.HostConfig
			if _, err := applyRunArgs(tt.args, &cfg, &host); err != nil {
				t.Fatal(err)
			}
			if host.LogConfig.Type != tt.driver || !reflect.DeepEqual(host.LogConfig.Config, tt.options) {
				t.Fatalf("logging guard: %#v", host.LogConfig)
			}
		})
	}
	for _, args := range [][]string{{"--log-driver=none", "--log-opt=tag=sample"}, {"--log-opt=", "--log-driver=none"}} {
		var cfg container.Config
		var host container.HostConfig
		_, err := applyRunArgs(args, &cfg, &host)
		if err == nil || !strings.Contains(err.Error(), "logging opts") {
			t.Fatalf("logging conflict guard: %v", err)
		}
	}
}
