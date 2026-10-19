// relationships:
//
//	references: [dockerfile]
package buildkit

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/tonistiigi/fsutil"
	"github.com/tonistiigi/fsutil/types"
)

// syntaxOmittingFS changes only the bytes served to the Dockerfile frontend.
// The build context, Dockerfile-specific ignore file and authored file stay intact.
type syntaxOmittingFS struct {
	fsutil.FS
	filename string
	data     []byte
}

func omitSyntaxFS(fs fsutil.FS, filename string) (fsutil.FS, error) {
	file, err := fs.Open(filename)
	if errors.Is(err, os.ErrNotExist) && filename == "Dockerfile" {
		filename = "dockerfile"
		file, err = fs.Open(filename)
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	// Keep other parser directives at the start of the preamble. DetectSyntax applies
	// BuildKit's preamble rules, including case and whitespace handling.
	if _, _, locations, ok := parser.DetectSyntax(data); ok {
		lines := bytes.Split(data, []byte("\n"))
		removed := map[int]bool{}
		for _, loc := range locations {
			for line := loc.Start.Line; line <= loc.End.Line; line++ {
				if line > 0 && line <= len(lines) {
					removed[line-1] = true
				}
			}
		}
		kept := lines[:0]
		for i, line := range lines {
			if !removed[i] {
				kept = append(kept, line)
			}
		}
		data = bytes.Join(kept, []byte("\n"))
	}
	filtered, err := fsutil.NewFilterFS(fs, &fsutil.FilterOpt{Map: func(name string, stat *types.Stat) fsutil.MapResult {
		if filepath.Clean(name) == filepath.Clean(filename) {
			stat.Size = int64(len(data))
		}
		return fsutil.MapResultKeep
	}})
	if err != nil {
		return nil, err
	}
	return syntaxOmittingFS{FS: filtered, filename: filename, data: data}, nil
}

func (fs syntaxOmittingFS) Open(name string) (io.ReadCloser, error) {
	if filepath.Clean(name) == filepath.Clean(fs.filename) {
		return io.NopCloser(bytes.NewReader(fs.data)), nil
	}
	return fs.FS.Open(name)
}
