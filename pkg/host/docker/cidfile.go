// relationships:
//   references: [create_controls]

package docker

import (
	"fmt"
	"os"
)

// cidFile follows the native Docker client's reservation lifecycle. A failed
// create/write removes the reservation; a written ID survives start failures.
type cidFile struct {
	path    string
	file    *os.File
	written bool
}

func newCIDFile(path string) (*cidFile, error) {
	c := &cidFile{path: path}
	if path == "" {
		return c, nil
	}
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("container ID file found, make sure the other container isn't running or delete %s", path)
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create the container ID file: %w", err)
	}
	c.file = f
	return c, nil
}
func (c *cidFile) Write(id string) error {
	if c.file == nil {
		return nil
	}
	if _, err := c.file.WriteString(id); err != nil {
		return fmt.Errorf("failed to write the container ID (%s) to file: %w", id, err)
	}
	c.written = true
	return nil
}
func (c *cidFile) Close() {
	if c.file == nil {
		return
	}
	_ = c.file.Close()
	if !c.written {
		_ = os.Remove(c.path)
	}
}
