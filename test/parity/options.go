// relationships:
//
//	references: [options_source, README, ../../pkg/cli/inventory]
package main

import (
	"fmt"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/cli"
)

type OptionResult struct {
	Command string `json:"command"`
	sourceOption
	Covered bool `json:"covered"`
}

type OptionCoverage struct {
	Complete bool           `json:"complete"`
	Covered  int            `json:"covered"`
	Total    int            `json:"total"`
	Score    float64        `json:"score"`
	Error    string         `json:"error,omitempty"`
	Options  []OptionResult `json:"options"`
}

func measureOptions(upstream map[string][]sourceOption, local map[string][]string) OptionCoverage {
	out := OptionCoverage{}
	for _, command := range sortedCommands(upstream) {
		accepted := map[string]bool{}
		for _, flag := range local[command] {
			accepted[flag] = true
		}
		for _, flag := range upstream[command] {
			result := OptionResult{Command: command, sourceOption: flag, Covered: accepted[flag.Name]}
			out.Options = append(out.Options, result)
			out.Total++
			if result.Covered {
				out.Covered++
			}
		}
	}
	if out.Total == 0 {
		out.Error = "no upstream options found"
		return out
	}
	out.Complete = true
	out.Score = float64(out.Covered) / float64(out.Total)
	return out
}

func optionCoverage(root string) OptionCoverage {
	upstream, err := readUpstreamOptions(root)
	if err != nil {
		return OptionCoverage{Error: err.Error()}
	}
	return measureOptions(upstream, cli.RegisteredOptions())
}

func (o OptionCoverage) markdown() string {
	var md strings.Builder
	md.WriteString("\n## Option coverage\n\n")
	if !o.Complete {
		fmt.Fprintf(&md, "INCOMPLETE: %s\n", o.Error)
		return md.String()
	}
	fmt.Fprintf(&md, "Registered long flags: **%d/%d (%.2f%%)**. Each command/option pair counts once, including inherited help/version, hidden options and absent commands. Aliases and positional arguments are recorded by the audit but do not add score units. No design exclusions are subtracted. Registration does not prove value or behavior parity.\n\n", o.Covered, o.Total, o.Score*100)
	md.WriteString("| Command | Covered | Total |\n| --- | --- | --- |\n")
	totals := map[string][2]int{}
	for _, option := range o.Options {
		counts := totals[option.Command]
		counts[1]++
		if option.Covered {
			counts[0]++
		}
		totals[option.Command] = counts
	}
	for _, command := range sortedCommands(totals) {
		counts := totals[command]
		fmt.Fprintf(&md, "| %s | %d | %d |\n", displayCommand(command), counts[0], counts[1])
	}
	md.WriteString("\nMissing flags and source locations: `score.json`. Dispositions and implementation groups: `docs/audits/upstream-options.md`.\n")
	return md.String()
}

func displayCommand(command string) string {
	if command == "" {
		return "(root)"
	}
	return command
}
