package runner

import (
	"context"
	"fmt"
	"strings"
)

// markerCheckScript is a shell one-liner that mirrors upstream's
// updateMarkerFile in injectHeadless.ts. It atomically:
//   - ensures the marker's parent directory exists,
//   - reads the current marker contents (or "ENOENT"),
//   - if they differ from the expected content, writes the expected
//     content and exits 0 (=> doRun=true),
//   - otherwise exits non-zero (=> doRun=false).
//
// The shell quoting follows upstream verbatim — both create the marker
// and decide the rerun bit in a single call so there's no TOCTOU
// window between probe and write.
const markerCheckScript = `mkdir -p '%s' && CONTENT="$(cat '%s' 2>/dev/null || echo ENOENT)" && [ "${CONTENT:-%s}" != '%s' ] && echo '%s' > '%s'`

// shouldRunMarker returns true if the phase needs to run, having
// updated the marker file as a side-effect when so. WhenAttach phases
// short-circuit to true (no marker, always run).
func shouldRunMarker(ctx context.Context, execer Execer, user, markerPath, expected string) (bool, error) {
	if markerPath == "" {
		return true, nil
	}
	dir := markerPath[:strings.LastIndexByte(markerPath, '/')]
	if dir == "" {
		dir = "/"
	}
	expectedQ := shellSQ(expected)
	script := fmt.Sprintf(markerCheckScript,
		shellSQRaw(dir), shellSQRaw(markerPath),
		expectedQ,
		expectedQ,
		expectedQ, shellSQRaw(markerPath))
	res, err := execer.Exec(ctx, ExecRequest{
		Cmd:  []string{"/bin/sh", "-c", script},
		User: user,
	})
	if err != nil {
		return false, err
	}
	// exit 0 means "marker was stale / missing, now updated" => run.
	return res.ExitCode == 0, nil
}

// shellSQ wraps a string in single quotes, escaping any embedded
// single-quote with the standard '\'' construction. Use shellSQRaw
// for values that go between literal single quotes in the script
// template (no extra outer quotes).
func shellSQ(s string) string {
	return "'" + shellSQRaw(s) + "'"
}

func shellSQRaw(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
