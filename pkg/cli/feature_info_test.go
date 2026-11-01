// relationships:
//
//	references: [feature_info, cli_test]
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestFeatureInfoRegisteredWithoutStagingFlags(t *testing.T) {
	flags := RegisteredOptions()["features info"]
	if len(flags) == 0 {
		t.Fatal("info command missing")
	}
	found := false
	for _, flag := range flags {
		if flag == "output-format" {
			found = true
		}
		if flag == "out" {
			t.Fatal("staging flags inherited")
		}
	}
	if !found {
		t.Fatal("output-format missing")
	}
}
func TestFeatureInfoInvalidInvocation(t *testing.T) {
	for _, args := range [][]string{{"features", "info"}, {"features", "info", "other", "invalid"}, {"features", "info", "tags", "invalid", "--output-format", "other"}, {"features", "info", "tags", "invalid", "--log-level", "off"}} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, "test", strings.NewReader(""), &stdout, &stderr)
		if code != 2 {
			t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, &stdout, &stderr)
		}
	}
}
