// relationships:
//
//	references: [config, ../spec/mounts]
package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestObjectMountValidationBeforeDocker(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	for _, mode := range []string{`"image":"alpine"`, `"dockerComposeFile":"compose.yml","service":"app"`} {
		_, workspace := gitWorkspace(t, `{`+mode+`,"mounts":[{"type":"bind","source":"/host/data","target":"/data","readOnly":true}]}`)
		for _, command := range []string{"up", "read-configuration", "build"} {
			result, _ := invoke(t, 1, command, "--workspace-folder", workspace)
			message, _ := result["message"].(string)
			if !strings.Contains(message, `unsupported object property "readOnly"`) || !strings.Contains(message, ",readonly") {
				t.Fatalf("%s: %v", command, result)
			}
		}
	}
}
