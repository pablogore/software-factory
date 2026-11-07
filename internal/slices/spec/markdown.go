package spec

import (
	"regexp"
	"strings"
)

// These helpers reproduce the regular expressions of specgate on main, which
// use lookaheads RE2 doesn't have, by walking the text line by line.

// line is one line of a text, without its newline. nl says a newline follows.
type line struct {
	start, end int
	nl         bool
}

func splitLines(text string) []line {
	var out []line
	start := 0
	for {
		i := strings.IndexByte(text[start:], '\n')
		if i < 0 {
			return append(out, line{start: start, end: len(text)})
		}
		out = append(out, line{start: start, end: start + i, nl: true})
		start += i + 1
	}
}

// isHeading says whether l is `## <name>` followed by blanks and a newline.
func isHeading(text string, l line, name string) bool {
	return l.nl && strings.TrimRight(text[l.start:l.end], " \t") == "## "+name
}

// sectionEnd is the offset where the section whose body starts at line i
// ends: the next line starting with `## `, or the end of the text.
func sectionEnd(text string, lines []line, i int) (int, int) {
	for ; i < len(lines); i++ {
		if strings.HasPrefix(text[lines[i].start:], "## ") {
			return lines[i].start, i
		}
	}
	return len(text), len(lines)
}

// section is the body of the first `## <heading>` section, and false when
// there is none.
func section(text, heading string) (string, bool) {
	lines := splitLines(text)
	for i, l := range lines {
		if isHeading(text, l, heading) {
			end, _ := sectionEnd(text, lines, i+1)
			return text[l.end+1 : end], true
		}
	}
	return "", false
}

// removeSections drops every `## <name>` section, heading and body, for the
// names given.
func removeSections(text string, names ...string) string {
	lines := splitLines(text)
	var b strings.Builder
	for i := 0; i < len(lines); {
		l := lines[i]
		if matchesAny(text, l, names) {
			_, i = sectionEnd(text, lines, i+1)
			continue
		}
		b.WriteString(text[l.start:l.end])
		if l.nl {
			b.WriteByte('\n')
		}
		i++
	}
	return b.String()
}

func matchesAny(text string, l line, names []string) bool {
	for _, n := range names {
		if isHeading(text, l, n) {
			return true
		}
	}
	return false
}

var (
	mermaidOpen  = regexp.MustCompile("^```[ \t]*mermaid[ \t]*$")
	mermaidClose = regexp.MustCompile("^```[ \t]*$")
)

// mermaid returns the bodies of the ```mermaid fences and the text without
// them.
func mermaid(text string) (diagrams []string, rest string) {
	lines := splitLines(text)
	var b strings.Builder
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if l.nl && mermaidOpen.MatchString(text[l.start:l.end]) {
			if j := closingFence(text, lines, i+1); j >= 0 {
				diagrams = append(diagrams, text[l.end+1:lines[j].start])
				i = j
				continue
			}
		}
		b.WriteString(text[l.start:l.end])
		if l.nl {
			b.WriteByte('\n')
		}
	}
	return diagrams, b.String()
}

func closingFence(text string, lines []line, from int) int {
	for j := from; j < len(lines); j++ {
		if mermaidClose.MatchString(text[lines[j].start:lines[j].end]) {
			return j
		}
	}
	return -1
}

func words(text string) int { return len(strings.Fields(text)) }

var (
	numberedItem = regexp.MustCompile(`^\s*\d+\.\s+`)
	bulletItem   = regexp.MustCompile(`^\s*[-*]\s+`)
)

// items are the list items of a section, each with its continuation lines
// joined.
func items(body string, numbered bool) []string {
	start := bulletItem
	if numbered {
		start = numberedItem
	}
	var out []string
	open := false
	for _, l := range strings.Split(body, "\n") {
		switch {
		case start.MatchString(l):
			out = append(out, start.ReplaceAllString(l, ""))
			open = true
		case strings.TrimSpace(l) == "":
			open = false // a blank line ends the item: prose after it isn't part of it
		case open && !strings.HasPrefix(l, "#"):
			out[len(out)-1] += " " + strings.TrimSpace(l)
		}
	}
	return out
}

// pyRepr quotes s the way Python's repr does, so messages match main's.
func pyRepr(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = '"'
	}
	var b strings.Builder
	b.WriteByte(q)
	for _, r := range s {
		switch {
		case r == '\\' || r == rune(q):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x`)
			b.WriteString(hex2(byte(r)))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(q)
	return b.String()
}

func hex2(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&0xf]})
}
