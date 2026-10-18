// relationships:
//
//	references: README
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func upstreamVersion(root string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, "reference/cli/package.json"))
	if err != nil {
		return "", fmt.Errorf("reference/cli missing; run git submodule update --init --depth 1")
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(body, &pkg); err != nil {
		return "", err
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("reference/cli/package.json has no version")
	}
	return pkg.Version, nil
}
func prerequisites(root string) (string, string) {
	for _, name := range []string{"node", "npx", "docker"} {
		if _, err := exec.LookPath(name); err != nil {
			return "", "missing " + name + " on PATH"
		}
	}
	version, err := upstreamVersion(root)
	if err != nil {
		return "", err.Error()
	}
	return version, ""
}
func writeReport(dir string, report Report) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "score.json"), append(body, '\n'), 0644); err != nil {
		return err
	}
	var md strings.Builder
	fmt.Fprintf(&md, "# Parity report\n\nUpstream: `@devcontainers/cli@%s`\n\nGo adapter: %s\n\nScore: **%d/%d (%.2f%%)**\n", report.Version, report.Adapter, report.Passed, report.Total, report.Score*100)
	if report.Skipped != "" {
		fmt.Fprintf(&md, "\nSkipped: %s\n", report.Skipped)
	}
	md.WriteString("\n| Fixture | Dimension | Result | Detail |\n| --- | --- | --- | --- |\n")
	for _, f := range report.Fixtures {
		for _, name := range dimensions {
			d := f.Dimensions[name]
			status := "FAIL"
			if d.Pass {
				status = "PASS"
			}
			detail := d.Upstream.Error + " " + d.Go.Error
			if strings.TrimSpace(detail) == "" && !d.Pass {
				detail = "values differ; see score.json"
			}
			fmt.Fprintf(&md, "| %s | %s | %s | %s |\n", f.Name, name, status, strings.ReplaceAll(detail, "|", "\\|"))
		}
	}
	for _, err := range report.CleanupErrors {
		fmt.Fprintf(&md, "\nCleanup error: %s\n", err)
	}
	md.WriteString("\nFull normalized observations: `score.json`. CLI output: `run.log`. Failed or unavailable observations never count as matches. Empty probe maps mean the fixture has no probe in that dimension.\n")
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(md.String()), 0644)
}
func run(ctx context.Context, root, output string) error {
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	version, skip := prerequisites(root)
	report := Report{Version: version, Skipped: skip, Adapter: DCBAdapter{}.Name()}
	if skip != "" {
		fmt.Println("SKIP parity:", skip)
		return writeReport(output, report)
	}
	log, err := os.Create(filepath.Join(output, "run.log"))
	if err != nil {
		return err
	}
	defer log.Close()
	if err := os.MkdirAll(filepath.Join(root, ".tmp"), 0755); err != nil {
		return err
	}
	r, err := newRunner(root, log)
	if err != nil {
		return err
	}
	defer os.RemoveAll(r.temp)
	if _, err := r.command(ctx, "docker", "info"); err != nil {
		report.Skipped = "Docker daemon unavailable (see run.log)"
		fmt.Println("SKIP parity:", report.Skipped)
		return writeReport(output, report)
	}
	// Teardown uses a fresh context so interrupting a run does not cancel cleanup.
	defer func() {
		if errors := r.cleanup(context.Background()); len(errors) > 0 {
			fmt.Fprintln(os.Stderr, "parity cleanup:", strings.Join(errors, "; "))
		}
	}()
	adapter, err := buildAdapter(ctx, r)
	if err != nil {
		return err
	}
	for _, f := range fixtures {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Println("Parity:", f.Name)
		upstream := r.observe(ctx, UpstreamAdapter{version}, f, "upstream")
		local := r.observe(ctx, adapter, f, "go")
		result := compare(f.Name, upstream, local)
		report.Fixtures = append(report.Fixtures, result)
		report.Passed += result.Passed
		report.Total += result.Total
	}
	report.Score = float64(report.Passed) / float64(report.Total)
	report.CleanupErrors = r.cleanup(context.Background())
	if err := writeReport(output, report); err != nil {
		return err
	}
	fmt.Printf("Parity: %d/%d (%.2f%%); %s\n", report.Passed, report.Total, report.Score*100, output)
	if len(report.CleanupErrors) > 0 {
		return fmt.Errorf("cleanup incomplete; see report.md")
	}
	return nil
}
func main() {
	root := flag.String("root", ".", "repository root")
	output := flag.String("output", ".tmp/parity", "report directory relative to root")
	flag.Parse()
	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := *output
	if !filepath.IsAbs(out) {
		out = filepath.Join(abs, out)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, abs, out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
