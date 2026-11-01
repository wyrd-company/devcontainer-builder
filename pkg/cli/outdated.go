// relationships:
//
//	references: [cli, config, ../features/outdated]
package cli

import (
	"encoding/json"
	"fmt"
	"golang.org/x/term"
	"os"
	"strings"
	"unicode/utf16"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

func (a *application) outdatedCommand() *cobra.Command {
	var cfg configOptions
	var format, dataFolder string
	cmd := &cobra.Command{Use: "outdated", Short: "Show current and available Feature versions", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&cfg.workspace, "workspace-folder", ".", "Local workspace folder")
	cmd.Flags().StringVar(&cfg.path, "config", "", "Path to devcontainer.json")
	cmd.Flags().StringVar(&dataFolder, "user-data-folder", "", "Persistent host data folder")
	cmd.Flags().StringVar(&format, "output-format", "text", "Output format: text or json")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if format != "text" && format != "json" {
			return usageError("--output-format must be text or json")
		}
		if a.logLevel != "info" && a.logLevel != "debug" && a.logLevel != "trace" {
			return usageError("--log-level must be info, debug or trace")
		}
		a.exportStdout = true
		dc, err := features.ReadVersionConfig(cfg.workspace, cfg.path)
		if err != nil {
			return err
		}
		report, err := features.LoadVersionInfo(nil, dc)
		if err != nil {
			return err
		}
		var output []byte
		if format == "json" {
			if file, ok := a.stdout.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
				output, err = json.MarshalIndent(report, "", "  ")
			} else {
				output, err = json.Marshal(report)
			}
		} else {
			output = []byte(outdatedTable(report))
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(a.stdout, string(output))
		if err != nil {
			a.outputErr = err
		}
		return err
	}
	return cmd
}

func outdatedTable(report features.VersionReport) string {
	rows := [][]string{{"Feature", "Current", "Wanted", "Latest"}}
	for _, id := range report.Order {
		v := report.Features[id]
		row := []string{features.FeatureIDWithoutVersion(id), versionText(v.Current), v.Wanted, v.Latest}
		for i := range row {
			if i >= 2 && row[i] == "" {
				row[i] = "-"
			}
		}
		rows = append(rows, row)
	}
	widths := make([]int, 4)
	size := func(s string) int { return len(utf16.Encode([]rune(s))) }
	for _, row := range rows {
		for i, v := range row {
			if n := size(v); n > widths[i] {
				widths[i] = n
			}
		}
	}
	lines := make([]string, len(rows))
	for j, row := range rows {
		var b strings.Builder
		for i, v := range row {
			b.WriteString(v)
			if i < 3 {
				b.WriteString(strings.Repeat(" ", widths[i]-size(v)+2))
			}
		}
		lines[j] = strings.TrimRight(b.String(), " \t\r\n")
	}
	return strings.Join(lines, "\n")
}

// versionText uses JavaScript string conversion for the text report cells.
func versionText(value any) string {
	switch v := value.(type) {
	case nil:
		return "-"
	case string:
		return v
	case float64:
		if v == 0 {
			return "0"
		}
		encoded, _ := json.Marshal(v)
		return string(encoded)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item != nil {
				parts[i] = versionText(item)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	default:
		return fmt.Sprint(v)
	}
}
