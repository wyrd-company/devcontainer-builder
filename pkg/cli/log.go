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
// With a mask, secret values are masked before any record is formed, and a
// tail that could still become a secret waits for the next write or Flush.
type logWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	json    bool
	pending []byte
	mask    *secretMask
	held    []byte
}

func (w *logWriter) setMask(m *secretMask) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mask = m
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	data := p
	if w.mask != nil {
		data, w.held = w.mask.scan(append(w.held, p...), false)
	}
	if err := w.write(data); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *logWriter) write(p []byte) error {
	if !w.json {
		if len(p) == 0 {
			return nil
		}
		_, err := w.dst.Write(p)
		return err
	}
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		if err := w.emit(string(w.pending[:i])); err != nil {
			return err
		}
		w.pending = w.pending[i+1:]
	}
	return nil
}

func (w *logWriter) emit(s string) error {
	return json.NewEncoder(w.dst).Encode(map[string]string{"type": "output", "text": s})
}

func (w *logWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.held) > 0 {
		data, _ := w.mask.scan(w.held, true)
		w.held = nil
		if err := w.write(data); err != nil {
			return err
		}
	}
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
	text = w.mask.String(text)
	if !w.json {
		if level == "error" {
			text = "dcb: error: " + text
		}
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
