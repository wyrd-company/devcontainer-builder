// relationships:
//
// references: [outdated]
package cli

import (
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"math"
	"strings"
	"testing"
)

func TestOutdatedJavaScriptNumberText(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{float64(1e20), "100000000000000000000"}, {float64(1e-7), "1e-7"}, {float64(9007199254740992), "9007199254740992"},
		{float64(1e21), "1e+21"}, {float64(1e-6), "0.000001"}, {math.Copysign(0, -1), "0"},
		{[]any{float64(1e20), []any{float64(1e-7)}}, "100000000000000000000,1e-7"},
	} {
		if got := versionText(tc.value); got != tc.want {
			t.Errorf("%v: %q want%q", tc.value, got, tc.want)
		}
	}
}
func TestOutdatedTableTrimsTrailingWhitespace(t *testing.T) {
	report := features.VersionReport{Order: []string{"registry.example/collection/sample:1"}, Features: map[string]features.FeatureVersionInfo{"registry.example/collection/sample:1": {Current: " 9.0.0 ", Wanted: " 9.0.0 ", Latest: " 9.0.0 "}}}
	text := outdatedTable(report)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Errorf("trailing space %q", line)
		}
	}
	if !strings.Contains(text, " 9.0.0 ") {
		t.Fatal("internal padding lost")
	}
}
