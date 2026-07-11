// Package tui provides a tiny, dependency-light terminal UI: an arrow-key model
// picker used by `ollama-lite launch` when no --model is given. It uses only
// golang.org/x/term (already in the build via golang.org/x/crypto) for raw mode
// and ANSI escapes for rendering — no Bubbletea or other heavy TUI stack.
package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrCanceled is returned by SelectModel when the user aborts the picker
// (Esc, Ctrl+C, or q).
var ErrCanceled = errors.New("selection canceled")

// Interactive reports whether both stdin and stdout are terminals, i.e. whether
// an interactive picker can run. When false, callers should fall back to a
// non-interactive default instead of prompting.
func Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// key is a decoded keypress relevant to the picker.
type key int

const (
	keyOther key = iota // any unhandled key
	keyUp
	keyDown
	keyEnter
	keyCancel
)

// parseKey decodes the first keypress in b, returning the key and the number of
// bytes it consumed. Recognizes ANSI arrow escapes (ESC [ A / ESC [ B, and the
// SS3 forms ESC O A / ESC O B), Enter (CR/LF), and cancel (Ctrl+C, q/Q, or a
// lone ESC). A bare ESC (no following bytes) is treated as cancel. Returns
// (keyOther, 0) when b is empty.
func parseKey(b []byte) (key, int) {
	if len(b) == 0 {
		return keyOther, 0
	}
	switch b[0] {
	case '\r', '\n':
		return keyEnter, 1
	case 0x03, 'q', 'Q': // Ctrl+C or quit
		return keyCancel, 1
	case 0x1b: // ESC
		if len(b) >= 3 && (b[1] == '[' || b[1] == 'O') {
			switch b[2] {
			case 'A':
				return keyUp, 3
			case 'B':
				return keyDown, 3
			}
			return keyOther, 3 // some other escape sequence; ignore it
		}
		if len(b) == 1 {
			return keyCancel, 1 // lone ESC
		}
		return keyOther, len(b) // unknown/partial escape; consume it
	case 'k': // vi-style up
		return keyUp, 1
	case 'j': // vi-style down
		return keyDown, 1
	}
	return keyOther, 1
}

// nextIndex returns the cursor index after applying k to a list of n items
// currently at cur. Up/Down wrap around; other keys leave the index unchanged.
func nextIndex(cur, n int, k key) int {
	if n <= 0 {
		return 0
	}
	switch k {
	case keyUp:
		return (cur - 1 + n) % n
	case keyDown:
		return (cur + 1) % n
	default:
		return cur
	}
}

// mergeDefault returns models with def moved to the front if present, or
// prepended if missing, de-duplicating and dropping blanks. This guarantees the
// saved default is always selectable and can be the initial cursor row.
func mergeDefault(models []string, def string) []string {
	def = strings.TrimSpace(def)
	seen := make(map[string]struct{}, len(models)+1)
	out := make([]string, 0, len(models)+1)
	add := func(s string) {
		if s = strings.TrimSpace(s); s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	add(def)
	for _, m := range models {
		add(m)
	}
	return out
}

// SelectModel shows an arrow-key list of models (with def pre-selected, merged
// in if missing) and returns the chosen entry. It renders to stderr so app
// stdout is never polluted. Returns ErrCanceled if the user aborts. Callers are
// expected to gate this behind Interactive().
func SelectModel(prompt string, models []string, def string) (string, error) {
	items := mergeDefault(models, def)
	if len(items) == 0 {
		return "", errors.New("no models to choose from")
	}
	if len(items) == 1 {
		return items[0], nil // nothing to pick
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState)

	enableVTOutput() // no-op except on Windows conhost

	out := os.Stderr
	cur := 0 // items[0] is def when set, so it starts pre-selected

	render(out, prompt, items, cur, false)
	reader := bufio.NewReader(os.Stdin)
	buf := make([]byte, 0, 8)
	for {
		b, err := reader.ReadByte()
		if err != nil {
			finish(out)
			if err == io.EOF {
				return "", ErrCanceled
			}
			return "", err
		}
		// Drain the rest of this keypress (e.g. the "[A" of an arrow escape),
		// which the terminal delivers together, so parseKey sees a whole
		// sequence rather than a stray byte at a time.
		buf = append(buf[:0], b)
		for reader.Buffered() > 0 {
			nb, err := reader.ReadByte()
			if err != nil {
				break
			}
			buf = append(buf, nb)
		}

		k, _ := parseKey(buf)
		switch k {
		case keyEnter:
			finish(out)
			return items[cur], nil
		case keyCancel:
			finish(out)
			return "", ErrCanceled
		case keyUp, keyDown:
			cur = nextIndex(cur, len(items), k)
			render(out, prompt, items, cur, true)
		}
	}
}

// render draws the prompt and item list. When redraw is true it first moves the
// cursor back up over the previously drawn item lines so the list updates in
// place.
func render(out io.Writer, prompt string, items []string, cur int, redraw bool) {
	if redraw {
		// Move up over the item lines drawn last time (prompt line stays put).
		fmt.Fprintf(out, "\x1b[%dA", len(items))
	} else {
		fmt.Fprintf(out, "%s\r\n", prompt)
		fmt.Fprint(out, "\x1b[2m(↑/↓ to move, Enter to select, Esc to cancel)\x1b[0m\r\n")
	}
	for i, it := range items {
		cursor := "  "
		line := it
		if i == cur {
			cursor = "\x1b[36m▸\x1b[0m " // cyan arrow
			line = "\x1b[36m" + it + "\x1b[0m"
		}
		fmt.Fprintf(out, "\r\x1b[K%s%s\r\n", cursor, line)
	}
}

// finish resets the cursor to the start of a fresh line so subsequent output
// begins cleanly after the picker exits.
func finish(out io.Writer) {
	fmt.Fprint(out, "\r")
}
