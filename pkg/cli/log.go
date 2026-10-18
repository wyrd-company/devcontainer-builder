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
	"io"
	"sync"
)

// logWriter serializes concurrent lifecycle and build streams. JSON mode wraps
// each complete output line; Flush retains a final line without a newline.
type logWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	json    bool
	pending []byte
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.json {
		return w.dst.Write(p)
	}
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		if err := w.emit(string(w.pending[:i])); err != nil {
			return 0, err
		}
		w.pending = w.pending[i+1:]
	}
	return len(p), nil
}

func (w *logWriter) emit(s string) error {
	return json.NewEncoder(w.dst).Encode(map[string]string{"type": "output", "text": s})
}

func (w *logWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	err := w.emit(string(w.pending))
	w.pending = nil
	return err
}

// diagnostic shares the stream lock so concurrent output cannot split a record.
func (w *logWriter) diagnostic(level, text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.json {
		_, err := io.WriteString(w.dst, text+"\n")
		return err
	}
	if len(w.pending) > 0 {
		if err := w.emit(string(w.pending)); err != nil {
			return err
		}
		w.pending = nil
	}
	return json.NewEncoder(w.dst).Encode(map[string]string{"type": "diagnostic", "level": level, "text": text})
}
