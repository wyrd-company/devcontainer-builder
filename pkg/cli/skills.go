// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

//go:embed skills/*/SKILL.md
var bundledSkills embed.FS

func (a *application) skillsCommand() *cobra.Command {
	var list bool
	cmd := &cobra.Command{Use: "skills [name]", Short: "Print bundled agent skills, or list them with --list", Args: cobra.MaximumNArgs(1)}
	cmd.Flags().BoolVar(&list, "list", false, "List skill names without their content")
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		if len(args) > 0 && args[0] != "dcb" {
			return usageError("unknown skill %q", args[0])
		}
		skill := map[string]string{"name": "dcb"}
		if !list {
			body, err := bundledSkills.ReadFile("skills/dcb/SKILL.md")
			if err != nil {
				return err
			}
			skill["content"] = string(body)
		}
		a.result["skills"] = []map[string]string{skill}
		return nil
	}
	var dir string
	install := &cobra.Command{Use: "install", Short: "Install bundled skills into a skills directory", Args: cobra.NoArgs}
	install.Flags().StringVar(&dir, "dir", ".agents/skills", "Target skills directory")
	install.RunE = func(_ *cobra.Command, _ []string) error {
		if dir == "" {
			return usageError("--dir must not be empty")
		}
		body, err := bundledSkills.ReadFile("skills/dcb/SKILL.md")
		if err != nil {
			return err
		}
		destination, err := filepath.Abs(filepath.Join(dir, "dcb", "SKILL.md"))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(destination, body, 0o644); err != nil {
			return err
		}
		a.result["installed"] = []string{destination}
		return nil
	}
	cmd.AddCommand(install)
	return cmd
}
