// Package stderrtail keeps the last lines an agent writes on stderr, so a
// failed attempt can cite them without the event growing with what the
// agent writes.
package stderrtail

import (
	"bytes"
	"sync"
	"unicode/utf8"
)

const (
	// MaxLines is how many of the last lines a Tail keeps.
	MaxLines = 20
	// MaxLineBytes is how long a kept line may be; a longer one is cut
	// there, on a character boundary, and ends with Cut.
	MaxLineBytes = 500
	// Cut ends a line that was cut.
	Cut = "…"
)

// Tail is an io.Writer that keeps the last MaxLines lines written to it,
// each of at most MaxLineBytes. It is safe for one writer and any readers
// at once.
type Tail struct {
	mu    sync.Mutex
	lines []string
	cur   []byte
	cut   bool
}

// Write keeps p's lines and never fails.
func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		chunk, rest, ended := bytes.Cut(p, []byte("\n"))
		t.add(chunk)
		if ended {
			t.push(t.line())
			t.cur, t.cut = t.cur[:0], false
		}
		p = rest
	}
	return n, nil
}

// Lines is the kept lines, oldest first, with the one not yet ended last;
// nil when nothing was written.
func (t *Tail) Lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 && len(t.cur) == 0 && !t.cut {
		return nil
	}
	out := append([]string(nil), t.lines...)
	if len(t.cur) > 0 || t.cut {
		out = append(out, t.line())
		if len(out) > MaxLines {
			out = out[1:]
		}
	}
	return out
}

// add appends b to the current line up to MaxLineBytes and drops the rest.
func (t *Tail) add(b []byte) {
	room := MaxLineBytes - len(t.cur)
	if len(b) > room {
		b, t.cut = b[:room], true
	}
	t.cur = append(t.cur, b...)
}

// line is the current line without a trailing CR, cut on a character
// boundary when it was too long.
func (t *Tail) line() string {
	b := bytes.TrimSuffix(t.cur, []byte("\r"))
	if !t.cut {
		return string(b)
	}
	for i := 0; i < utf8.UTFMax-1 && len(b) > 0; i++ {
		if r, size := utf8.DecodeLastRune(b); r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return string(b) + Cut
}

func (t *Tail) push(line string) {
	if len(t.lines) == MaxLines {
		t.lines = append(t.lines[:0], t.lines[1:]...)
	}
	t.lines = append(t.lines, line)
}
