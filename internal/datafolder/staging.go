// relationships:
//
// references: [../../pkg/cli/options_data_folders, ../../pkg/builder/builder]
package datafolder

import (
	"os"
	"path/filepath"
)

// NewStaging allocates a private run folder within the selected host data
// folder. No reader discovers these folders as cached or completed output;
// consumers receive their path only after staging completes. Callers clean up
// only the returned folder, never the supplied parent or its existing contents.
func NewStaging(parent, prefix string) (string, error) {
	if parent != "" {
		var err error
		parent, err = filepath.Abs(parent)
		if err != nil {
			return "", err
		}
		if err = os.MkdirAll(parent, 0o755); err != nil {
			return "", err
		}
	}
	return os.MkdirTemp(parent, prefix)
}
