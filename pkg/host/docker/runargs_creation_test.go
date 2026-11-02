// relationships:
//   references: [runargs, ../../../reference/cli/src/spec-node/singleContainer.ts]

package docker

import (
	"math"
	"reflect"
	"testing"
)

func TestRunArgsCPUCount(t *testing.T) {
	_, h := applyForTest(t, []string{"--cpu-count=010", "--cpu-count=0x10"})
	if h.CPUCount != 16 {
		t.Fatalf("CPUCount=%d", h.CPUCount)
	}
	_, h = applyForTest(t, []string{"--cpu-count=16", "--cpu-count=0"})
	if h.CPUCount != 0 {
		t.Fatalf("CPUCount zero=%d", h.CPUCount)
	}
}
func TestRunArgsCPUPercent(t *testing.T) {
	_, h := applyForTest(t, []string{"--cpu-percent=010", "--cpu-percent=0x10"})
	if h.CPUPercent != 16 {
		t.Fatalf("CPUPercent=%d", h.CPUPercent)
	}
	_, h = applyForTest(t, []string{"--cpu-percent=16", "--cpu-percent=0"})
	if h.CPUPercent != 0 {
		t.Fatalf("CPUPercent zero=%d", h.CPUPercent)
	}
}
func TestRunArgsIOMaxIOPS(t *testing.T) {
	_, h := applyForTest(t, []string{"--io-maxiops=010", "--io-maxiops=18446744073709551615"})
	if h.IOMaximumIOps != math.MaxUint64 {
		t.Fatalf("IOMaximumIOps=%d", h.IOMaximumIOps)
	}
	_, h = applyForTest(t, []string{"--io-maxiops=16", "--io-maxiops=0"})
	if h.IOMaximumIOps != 0 {
		t.Fatalf("IOPS zero=%d", h.IOMaximumIOps)
	}
}
func TestRunArgsIOMaxBandwidth(t *testing.T) {
	_, h := applyForTest(t, []string{"--io-maxbandwidth=1k", "--io-maxbandwidth=1.5mb"})
	if h.IOMaximumBandwidth != 1572864 {
		t.Fatalf("IOMaximumBandwidth=%d", h.IOMaximumBandwidth)
	}
	_, h = applyForTest(t, []string{"--io-maxbandwidth=16", "--io-maxbandwidth=0"})
	if h.IOMaximumBandwidth != 0 {
		t.Fatalf("bandwidth zero=%d", h.IOMaximumBandwidth)
	}
}
func TestRunArgsStorageOptions(t *testing.T) {
	_, h := applyForTest(t, []string{"--storage-opt", "size=1G", "--storage-opt=size=2G", "--storage-opt=sample=a=b", "--storage-opt=empty=", "--storage-opt==value"})
	want := map[string]string{"size": "2G", "sample": "a=b", "empty": "", "": "value"}
	if !reflect.DeepEqual(h.StorageOpt, want) {
		t.Fatalf("StorageOpt=%v want %v", h.StorageOpt, want)
	}
}
func TestRunArgsCreationInvalid(t *testing.T) {
	for _, args := range [][]string{{"--cpu-count=1.5"}, {"--cpu-count=9223372036854775808"}, {"--cpu-percent=08"}, {"--cpu-percent=invalid"}, {"--io-maxiops=-1"}, {"--io-maxiops=18446744073709551616"}, {"--io-maxbandwidth=invalid"}, {"--storage-opt=missing"}, {"--cpu-count"}, {"--cpu-percent"}, {"--io-maxiops"}, {"--io-maxbandwidth"}, {"--storage-opt"}} {
		if _, _, err := tryApply(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
