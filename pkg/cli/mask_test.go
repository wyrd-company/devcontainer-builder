// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestSecretMaskString(t *testing.T) {
	m := newSecretMask(map[string]string{"A": sampleSecret, "B": "sample", "C": ""})
	quoted := strconv.Quote(sampleSecret)
	encoded, _ := json.Marshal(sampleSecret)
	for in, want := range map[string]string{
		"value " + sampleSecret + " end": "value ******** end",
		"quoted " + quoted:               "quoted \"********\"",
		"encoded " + string(encoded):     "encoded \"********\"",
		"short sample and samples":       "short ******** and ********s",
		"no secret here":                 "no secret here",
		sampleSecret + sampleSecret:      "****************",
		"":                               "",
	} {
		if got := m.String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
	if newSecretMask(map[string]string{"EMPTY": ""}) != nil || newSecretMask(nil) != nil {
		t.Error("a mask without a nonempty value must be nil")
	}
	var none *secretMask
	if none.String("text") != "text" {
		t.Error("a nil mask must not change text")
	}
}

// A secret split across writes, in either log format, is masked, and a held
// tail that does not become a secret is written by Flush.
func TestLogWriterMasksSplitSecrets(t *testing.T) {
	multiLine := "sample-line-one\nsample-line-two"
	secrets := map[string]string{"A": sampleSecret, "B": multiLine, "C": "sample-line"}
	stream := "before " + sampleSecret + " middle " + multiLine + " and sample-line-x after sample-li"
	for _, jsonMode := range []bool{false, true} {
		for split := 0; split <= len(stream); split++ {
			var out bytes.Buffer
			w := &logWriter{dst: &out, json: jsonMode}
			w.setMask(newSecretMask(secrets))
			for _, part := range []string{stream[:split], stream[split:]} {
				if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			if jsonMode {
				text = jsonOutputText(t, out.Bytes())
			}
			if want := "before ******** middle ******** and ********-x after sample-li"; text != want {
				t.Fatalf("json=%v split=%d: output %q, want %q", jsonMode, split, text, want)
			}
		}
	}
}

// Byte-at-a-time writes, as a terminal stream can deliver, are masked too.
func TestLogWriterMasksByteWrites(t *testing.T) {
	var out bytes.Buffer
	w := &logWriter{dst: &out}
	w.setMask(newSecretMask(map[string]string{"A": sampleSecret}))
	for _, b := range []byte("x" + sampleSecret + "y\n") {
		if _, err := w.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "x********y\n" {
		t.Fatalf("output %q", out.String())
	}
}

func TestLogWriterMasksDiagnostics(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		var out bytes.Buffer
		w := &logWriter{dst: &out, json: jsonMode}
		w.setMask(newSecretMask(map[string]string{"A": sampleSecret}))
		if err := w.diagnostic("error", "failed with "+strconv.Quote(sampleSecret)); err != nil {
			t.Fatal(err)
		}
		assertNoSecret(t, "diagnostic", out.String())
		if !strings.Contains(out.String(), secretMaskText) {
			t.Errorf("json=%v: diagnostic %q lacks the mask", jsonMode, out.String())
		}
	}
}

func TestSecretMaskResult(t *testing.T) {
	m := newSecretMask(map[string]string{"A": sampleSecret})
	got := m.result(map[string]any{
		"outcome":   "error",
		"message":   "failed: " + sampleSecret,
		"phasesRun": []phaseResult{{Hook: "postCreateCommand", Origin: sampleSecret}},
		"count":     3,
	})
	encoded, _ := json.Marshal(got)
	assertNoSecret(t, "result", string(encoded))
	want := `{"count":3,"message":"failed: ********","outcome":"error","phasesRun":[{"hook":"postCreateCommand","origin":"********"}]}`
	if string(encoded) != want {
		t.Errorf("result = %s, want %s", encoded, want)
	}
	if failed := m.result(map[string]any{"bad": make(chan int)}); failed["outcome"] != "error" || len(failed) != 3 {
		t.Errorf("unencodable result = %v, want a fixed error", failed)
	}
}

// jsonOutputText joins the text of output records with newlines.
func jsonOutputText(t *testing.T, data []byte) string {
	t.Helper()
	var lines []string
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var record map[string]string
		if err := dec.Decode(&record); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, record["text"])
	}
	return strings.Join(lines, "\n")
}
