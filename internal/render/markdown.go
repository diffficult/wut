// Package render prints an LLM answer so a terminal can read it.
//
// The providers are asked to answer in Markdown, but a terminal does not
// render Markdown: fences, emphasis markers and heading markers reach the
// screen as noise. The Python implementation printed the answer through
// rich's Markdown renderer, so the Go implementation translates the same
// constructs here: prose stays prose, fenced code is printed verbatim and
// indented, and the emphasis markers are replaced by ANSI styling when the
// destination is a real terminal.
//
// The renderer uses the standard library only. The full-featured Go Markdown
// terminal renderers (glamour and friends) pull in a CommonMark engine, a
// terminal style engine and a syntax highlighter, which is roughly twenty
// modules for a single print of a short answer. A focused line based renderer
// covers what the provider prompt asks for, keeps the binary and the build
// dependency-free, and degrades to literal text for anything it does not
// understand, so an answer is never silently dropped or truncated.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// ANSI styling used when the destination is a terminal.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiItalic = "\x1b[3m"
	ansiDim    = "\x1b[2m"
	ansiStrike = "\x1b[9m"
	ansiCode   = "\x1b[36m"
)

// Literal text inserted while translating Markdown.
const (
	bullet      = "•"
	quotePrefix = "│ "
	rule        = "---"
	codeIndent  = "  "
	// wordGap separates a list marker from its text.
	wordGap = " "
)

// style is the presentation of a piece of text.
type style int

const (
	plainText style = iota
	boldText
	italicText
	codeText
	dimText
	strikeText
)

// ansi returns the escape sequence for a style, or an empty string for plain
// text and for rendering without color.
func (s style) ansi(color bool) string {
	if !color || s == plainText {
		return ""
	}
	switch s {
	case boldText:
		return ansiBold
	case italicText:
		return ansiItalic
	case codeText:
		return ansiCode
	case dimText:
		return ansiDim
	case strikeText:
		return ansiStrike
	default:
		return ""
	}
}

// Markdown writes source to w, styled for a terminal. Color is used only when w
// looks like a terminal and the user has not opted out with NO_COLOR, so
// redirected output stays free of escape sequences.
func Markdown(w io.Writer, source string) error {
	return MarkdownWithColor(w, source, ColorEnabled(w))
}

// MarkdownNoColor writes source to w without any styling. It is the explicit
// form of Markdown for a destination that must stay plain.
func MarkdownNoColor(w io.Writer, source string) error {
	return MarkdownWithColor(w, source, false)
}

// MarkdownWithColor writes source to w with the requested coloring. A write
// failure is reported, and a source that produced no output at all is written
// verbatim rather than swallowed.
func MarkdownWithColor(w io.Writer, source string, color bool) error {
	out := neverSuppress(source, render(source, color))
	if out == "" {
		return nil
	}
	if _, err := io.WriteString(w, out); err != nil {
		return fmt.Errorf("writing rendered answer: %w", err)
	}
	return nil
}

// ColorEnabled reports whether w is a terminal that should receive styling.
func ColorEnabled(w io.Writer) bool {
	return colorEnabled(w, os.Getenv)
}

// colorEnabled resolves the styling decision. NO_COLOR and TERM=dumb win over
// a terminal destination, and only a character device counts as a terminal, so
// a file or a pipe never receives escape sequences.
func colorEnabled(w io.Writer, getenv func(string) string) bool {
	if getenv != nil {
		if getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
			return false
		}
	}

	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// render translates Markdown into terminal text. Every line it emits is
// already free of markup, and the result ends with exactly one newline, or is
// empty when there was nothing to print.
func render(source string, color bool) string {
	lines := parse(strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n"))
	if len(lines) == 0 {
		return ""
	}

	var out strings.Builder
	for i, line := range lines {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(line.String(color))
	}
	out.WriteString("\n")
	return out.String()
}

// neverSuppress guarantees that a non-empty answer is never printed as
// nothing: if the translation produced no output at all, the source is used
// unchanged, so a renderer change can only cost styling, never content.
func neverSuppress(source string, rendered string) string {
	if rendered != "" {
		return rendered
	}
	if strings.TrimSpace(source) == "" {
		return ""
	}
	return strings.TrimRight(source, "\n") + "\n"
}

// line is one rendered terminal line: an optional literal prefix, such as a
// bullet or a quote bar, and the content whose inline markup was translated.
type line struct {
	prefix  string
	prefixS style
	content string
	base    style
}

// String formats the line, applying ANSI styling only when color is set.
func (l line) String(color bool) string {
	var out strings.Builder
	out.WriteString(styled(l.prefix, l.prefixS, color))
	for _, span := range spans(l.content) {
		if span.style == plainText {
			span.style = l.base
		}
		out.WriteString(styled(span.text, span.style, color))
	}
	return out.String()
}

// styled wraps text in the escape sequence for a style when color is set.
func styled(text string, s style, color bool) string {
	if text == "" {
		return ""
	}
	escape := s.ansi(color)
	if escape == "" {
		return text
	}
	return escape + text + ansiReset
}

// parse walks the source line by line and classifies each one. Blank lines
// collapse into a single separator and never survive at the start or the end.
func parse(raw []string) []line {
	var (
		out     []line
		fence   string
		indent  string
		pending bool
	)

	emit := func(l line) {
		if pending && len(out) > 0 {
			out = append(out, line{})
		}
		pending = false
		out = append(out, l)
	}

	for _, text := range raw {
		trimmed := strings.TrimSpace(text)

		if fence != "" {
			if isFence(trimmed, fence) {
				fence, indent = "", ""
				// A finished block is separated from what follows.
				pending = true
				continue
			}
			// The fence indentation is removed and the command keeps its own
			// relative indentation, so it can still be copied and re-run.
			emit(line{prefix: codeIndent, content: strings.TrimPrefix(text, indent), base: codeText})
			continue
		}

		if trimmed == "" {
			pending = true
			continue
		}

		if marker, open := fenceMarker(trimmed); open {
			fence, indent = marker, leadingSpace(text)
			// Keep code blocks apart from the prose around them.
			pending = true
			continue
		}

		if isRule(trimmed) {
			emit(line{content: rule, base: dimText})
			continue
		}

		if rest, ok := cutHeading(trimmed); ok {
			emit(line{content: rest, base: boldText})
			continue
		}

		if marker, rest, quoted := cutQuote(trimmed); quoted {
			emit(line{prefix: strings.Repeat(quotePrefix, marker), prefixS: dimText, content: rest})
			continue
		}

		if indentText, marker, rest, ok := cutListItem(text); ok {
			emit(line{prefix: indentText + marker + wordGap, content: rest})
			continue
		}

		emit(line{content: strings.TrimRight(text, " \t")})
	}

	return out
}

// fenceMarker reports the fence of a line that opens a code block, such as
// ``` or ~~~.
func fenceMarker(trimmed string) (string, bool) {
	if len(trimmed) < 3 {
		return "", false
	}
	marker := trimmed[:3]
	if marker != "```" && marker != "~~~" {
		return "", false
	}
	return marker, true
}

// isFence reports whether a line closes an open code block: a line made only
// of the fence character, optionally followed by its info string.
func isFence(trimmed string, fence string) bool {
	return strings.Trim(trimmed, string(fence[0])+" ") == ""
}

// leadingSpace returns the leading whitespace of a line, which is the fence
// indentation CommonMark removes from the content of the block.
func leadingSpace(text string) string {
	return text[:len(text)-len(strings.TrimLeft(text, " \t"))]
}

// isRule reports whether a line is a thematic break.
func isRule(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	marker := trimmed[0]
	if marker != '-' && marker != '*' && marker != '_' {
		return false
	}
	return strings.Trim(trimmed, string(marker)) == ""
}

// cutHeading recognizes an ATX heading and returns its text.
func cutHeading(trimmed string) (string, bool) {
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return "", false
	}
	rest := trimmed[level:]
	if rest != "" && !strings.HasPrefix(rest, " ") {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimRight(rest, "#")
	return strings.TrimSpace(rest), true
}

// cutQuote recognizes a block quote and returns how many quoting levels it has
// plus the quoted content.
func cutQuote(trimmed string) (int, string, bool) {
	levels := 0
	for strings.HasPrefix(trimmed, ">") {
		levels++
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
	}
	return levels, trimmed, levels > 0
}

// cutListItem recognizes a bullet or ordered list item and returns the
// indentation, the normalized marker, and the item text.
func cutListItem(text string) (string, string, string, bool) {
	indent := leadingSpace(text)
	trimmed := strings.TrimLeft(text, " \t")

	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return indent, bullet, strings.TrimSpace(trimmed[1:]), true
	}

	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits+1 < len(trimmed) {
		delimiter := trimmed[digits]
		if (delimiter == '.' || delimiter == ')') && trimmed[digits+1] == ' ' {
			return indent, trimmed[:digits+1], strings.TrimSpace(trimmed[digits+1:]), true
		}
	}

	return "", "", "", false
}

// span is a piece of a line with a resolved style.
type span struct {
	text  string
	style style
}

// spans translates the inline markup of one line: code spans, emphasis, strong
// emphasis, strikethrough, links and backslash escapes. Markup that is not
// closed is left as literal text.
func spans(text string) []span {
	var out []span
	var plain strings.Builder

	flush := func() {
		if plain.Len() > 0 {
			out = append(out, span{text: plain.String(), style: plainText})
			plain.Reset()
		}
	}
	emit := func(text string, s style) {
		flush()
		out = append(out, span{text: text, style: s})
	}

	runes := []rune(text)
	for i := 0; i < len(runes); {
		switch runes[i] {
		case '\\':
			if i+1 < len(runes) && isPunct(runes[i+1]) {
				plain.WriteRune(runes[i+1])
				i += 2
				continue
			}
			plain.WriteRune('\\')
			i++

		case '`':
			end, code, ok := codeSpan(runes, i)
			if !ok {
				plain.WriteRune('`')
				i++
				continue
			}
			emit(code, codeText)
			i = end

		case '*', '_', '~':
			end, inner, s, ok := emphasis(runes, i)
			if !ok {
				run := markerRun(runes, i)
				plain.WriteString(strings.Repeat(string(runes[i]), run))
				i += run
				continue
			}
			emit(inner, s)
			i = end

		case '[':
			end, label, s := link(runes, i)
			if end == 0 {
				plain.WriteRune('[')
				i++
				continue
			}
			emit(label, s)
			i = end

		default:
			plain.WriteRune(runes[i])
			i++
		}
	}
	flush()

	return out
}

// codeSpan finds a closed code span starting at start and returns the index
// after it, its content, and whether it was closed.
func codeSpan(runes []rune, start int) (int, string, bool) {
	ticks := markerRun(runes, start)
	for i := start + ticks; i < len(runes); {
		if runes[i] != '`' {
			i++
			continue
		}
		run := markerRun(runes, i)
		if run == ticks {
			content := strings.TrimSpace(string(runes[start+ticks : i]))
			return i + run, content, true
		}
		i += run
	}
	return 0, "", false
}

// emphasis finds a closed emphasis run starting at start. The marker must sit
// on a word boundary, which keeps shell globs such as *.log, arithmetic such as
// 2*3, and identifiers such as OPENAI_BASE_URL intact.
func emphasis(runes []rune, start int) (int, string, style, bool) {
	marker := runes[start]
	run := markerRun(runes, start)
	if start > 0 && isWord(runes[start-1]) {
		return 0, "", plainText, false
	}
	if marker == '~' && run != 2 {
		return 0, "", plainText, false
	}

	for i := start + run; i < len(runes); {
		if runes[i] != marker {
			i++
			continue
		}
		// A closing run must be at least as long as the opening run, so the
		// second star of **bold** closes the run instead of ending it.
		length := markerRun(runes, i)
		if length < run {
			i += length
			continue
		}
		if i == start+run || isSpace(runes[i-1]) {
			i += length
			continue
		}
		if end := i + length; end < len(runes) && isWord(runes[end]) {
			i += length
			continue
		}
		return i + length, string(runes[start+run : i]), emphasisStyle(marker, run), true
	}
	return 0, "", plainText, false
}

// emphasisStyle maps a marker and its length to a style.
func emphasisStyle(marker rune, run int) style {
	switch {
	case marker == '~':
		return strikeText
	case run >= 2:
		return boldText
	default:
		return italicText
	}
}

// markerRun returns how many times a marker repeats at the given index.
func markerRun(runes []rune, start int) int {
	if start >= len(runes) {
		return 0
	}
	run := 0
	for start+run < len(runes) && runes[start+run] == runes[start] {
		run++
	}
	return run
}

// link translates a Markdown link or image into text. The target is kept so no
// information is lost, and the index after the link is returned.
func link(runes []rune, start int) (int, string, style) {
	image := start > 0 && runes[start-1] == '!'

	bracket := indexOf(runes, start, "](")
	if bracket < 0 {
		return 0, "", plainText
	}
	end := indexOf(runes, bracket+2, ")")
	if end < 0 {
		return 0, "", plainText
	}

	label := strings.TrimSpace(string(runes[start+1 : bracket]))
	target := strings.TrimSpace(string(runes[bracket+2 : end]))
	if target == "" {
		return end + 1, label, plainText
	}
	if image {
		return end + 1, label + " (image: " + target + ")", plainText
	}
	return end + 1, label + " (" + target + ")", plainText
}

// indexOf returns the index of the first occurrence of needle at or after
// start, or -1. The needle is ASCII, but the surrounding text may hold
// multibyte runes, so the byte offset is converted back to a rune index.
func indexOf(runes []rune, start int, needle string) int {
	if start >= len(runes) {
		return -1
	}
	rest := string(runes[start:])
	index := strings.Index(rest, needle)
	if index < 0 {
		return -1
	}
	return start + utf8.RuneCountInString(rest[:index])
}

func isWord(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

func isPunct(r rune) bool {
	return strings.ContainsRune("\\`*_{}[]()#+-.!|<>~\"'", r)
}
