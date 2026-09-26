package render

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sampleAnswer is the shape the provider is asked to answer in: prose, a
// fenced command block, bullets, a quote, and inline emphasis.
const sampleAnswer = `**Git** does not have a ` + "`create-pr`" + ` command.

To open a pull request you typically need to:

` + "```sh" + `
git push -u origin my-branch
` + "```" + `

- push the branch
- open the merge request

> Only warnings are bold.

1. First step
2. Second step
`

// The exact output is pinned here so the rendering a user sees is reviewable
// and any accidental change is visible in the diff.
func ExampleMarkdownNoColor() {
	var out bytes.Buffer
	answer := "**Warning:** git has no `create-pr` command.\n" +
		"\n" +
		"## What to do\n" +
		"\n" +
		"1. Push the branch\n" +
		"2. Open the merge request\n" +
		"\n" +
		"```sh\n" +
		"git push -u origin my-branch\n" +
		"```\n"

	if err := MarkdownNoColor(&out, answer); err != nil {
		fmt.Println(err)
	}
	fmt.Print(out.String())
	// Output:
	// Warning: git has no create-pr command.
	//
	// What to do
	//
	// 1. Push the branch
	// 2. Open the merge request
	//
	//   git push -u origin my-branch
}

func plain(t *testing.T, source string) string {
	t.Helper()

	var buf bytes.Buffer
	if err := MarkdownNoColor(&buf, source); err != nil {
		t.Fatalf("MarkdownNoColor() error = %v", err)
	}
	return buf.String()
}

// The answer is Markdown, not terminal text, so the markup the user sees today
// has to be translated: the words stay, the markers go.
func TestMarkdownNoColorRemovesMarkupAndKeepsText(t *testing.T) {
	got := plain(t, sampleAnswer)

	for _, want := range []string{
		"Git does not have a create-pr command.",
		"push the branch",
		"open the merge request",
		"First step",
		"Second step",
		"Only warnings are bold.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output = %q, want it to contain %q", got, want)
		}
	}

	for _, unwanted := range []string{"**", "```", "```sh", "`create-pr`"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rendered output = %q, want it to drop %q", got, unwanted)
		}
	}
}

// Commands come back in fenced blocks; the user must be able to read and copy
// every line of them, and the fence itself must not reach the terminal.
func TestMarkdownNoColorPreservesFencedCodeContent(t *testing.T) {
	got := plain(t, "Run this:\n\n```sh\npipx install wut\nwut --query 'why?'\n```\n")

	if strings.Contains(got, "```") {
		t.Fatalf("rendered output = %q, want no code fence markers", got)
	}
	for _, want := range []string{"pipx install wut", "wut --query 'why?'"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output = %q, want it to keep the command %q", got, want)
		}
	}
}

func TestMarkdownNoColorNormalizesFencesAndBullets(t *testing.T) {
	got := plain(t, "~~~console\n$ wut\n~~~\n\n* one\n+ two\n- three\n")

	if !strings.Contains(got, "$ wut") {
		t.Fatalf("rendered output = %q, want the tilde fence content", got)
	}
	if strings.Contains(got, "~~~") {
		t.Fatalf("rendered output = %q, want no tilde fence markers", got)
	}
	for _, want := range []string{"• one", "• two", "• three"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output = %q, want the bullet %q", got, want)
		}
	}
}

func TestMarkdownNoColorRendersHeadingsQuotesAndRules(t *testing.T) {
	got := plain(t, "## Heading\n\n> quoted line\n\n---\n")

	for _, want := range []string{"Heading", "│ quoted line", "---"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "##") {
		t.Fatalf("rendered output = %q, want the heading markers dropped", got)
	}
}

func TestMarkdownNoColorRendersLinksWithTheirTarget(t *testing.T) {
	got := plain(t, "See [the docs](https://example.com/wut) for more.")

	if !strings.Contains(got, "the docs (https://example.com/wut)") {
		t.Fatalf("rendered output = %q, want the link target kept", got)
	}
}

// Terminals wrap on their own; a renderer that also wraps can break a command
// in half or mangle indentation, so lines are never re-wrapped.
func TestMarkdownNoColorDoesNotRewrapLongLines(t *testing.T) {
	line := strings.Repeat("word ", 200)
	got := plain(t, line)

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("rendered output has %d lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "word") {
		t.Fatalf("rendered output = %q, want the long line intact", lines[0])
	}
}

func TestMarkdownNoColorCollapsesRunsOfBlankLines(t *testing.T) {
	got := plain(t, "first\n\n\n\n\nsecond\n")

	if got != "first\n\nsecond\n" {
		t.Fatalf("rendered output = %q, want one blank line between paragraphs", got)
	}
}

// A malformed or unclosed construct must degrade to the literal text, never to
// dropped content: the user always sees the answer the provider returned.
func TestMarkdownKeepsUnclosedConstructsLiteral(t *testing.T) {
	got := plain(t, "half **bold and half *em with `code")

	for _, want := range []string{"**", "*em", "`code"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output = %q, want the literal %q", got, want)
		}
	}
}

// Underscores inside identifiers are common in terminal output; they must not
// be mistaken for emphasis and must survive untouched.
func TestMarkdownKeepsIdentifierUnderscores(t *testing.T) {
	got := plain(t, "Set OPENAI_BASE_URL and GENERAL_PROVIDER before running it.")

	if !strings.Contains(got, "OPENAI_BASE_URL") {
		t.Fatalf("rendered output = %q, want the identifier intact", got)
	}
}

// The response is the user's answer: no word of it may disappear.
func TestMarkdownNeverLosesResponseWords(t *testing.T) {
	sources := []string{
		sampleAnswer,
		"# Title\n\nSome **bold** and *italic* text with `inline_code`.\n",
		"1. alpha\n2. beta\n\n- gamma\n- delta\n",
		"```\nplain command --flag value\n```\n",
		"[docs](https://example.com) and https://example.com/bare\n",
	}

	marker := regexp.MustCompile(`[^[:alnum:]_]+`)
	for _, source := range sources {
		for _, word := range marker.Split(source, -1) {
			word = strings.Trim(word, "_")
			if len(word) < 3 {
				continue
			}
			if !strings.Contains(plain(t, source), word) {
				t.Fatalf("rendered output for %q lost the word %q", source, word)
			}
		}
	}
}

// The answer must never reach the user as nothing: if the translation ever
// produced no output for a non-empty answer, the source is printed unchanged.
func TestNeverSuppressFallsBackToTheSource(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		rendered string
		want     string
	}{
		{name: "rendered wins", source: "**bold**", rendered: "bold\n", want: "bold\n"},
		{name: "empty rendering falls back", source: "**bold**", rendered: "", want: "**bold**\n"},
		{name: "blank source stays empty", source: "  \n\n", rendered: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := neverSuppress(tt.source, tt.rendered); got != tt.want {
				t.Fatalf("neverSuppress(%q, %q) = %q, want %q", tt.source, tt.rendered, got, tt.want)
			}
		})
	}
}

// Nested items keep their indentation, and a construct the renderer does not
// interpret, such as a table, keeps its words.
func TestMarkdownKeepsNestingAndUnknownBlocksReadable(t *testing.T) {
	got := plain(t, "Steps:\n\n1. First\n   - nested one\n   - nested two\n\n| flag | meaning |\n| --- | --- |\n| -q | query |\n")

	for _, want := range []string{"1. First", "  • nested one", "  • nested two", "flag", "meaning", "query"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output = %q, want it to contain %q", got, want)
		}
	}
}

// An answer holding an empty code block must not crash and must not lose the
// rest of the answer. An answer that is nothing but an empty block has no
// visible content, and the never-suppress rule prints it unchanged rather than
// silently printing nothing.
func TestMarkdownHandlesAnEmptyCodeBlock(t *testing.T) {
	got := plain(t, "```\n```\nNothing to run here.\n")

	if !strings.Contains(got, "Nothing to run here.") {
		t.Fatalf("rendered output = %q, want the surrounding answer intact", got)
	}
	if got != "Nothing to run here.\n" {
		t.Fatalf("rendered output = %q, want the empty block dropped", got)
	}

	if degenerate := plain(t, "```\n```\n"); degenerate != "```\n```\n" {
		t.Fatalf("rendered output = %q, want the source unchanged rather than nothing", degenerate)
	}
}

func TestMarkdownNoColorWritesNoEscapeSequences(t *testing.T) {
	got := plain(t, sampleAnswer)

	if strings.Contains(got, "\x1b[") {
		t.Fatalf("rendered output = %q, want no ANSI styling without color", got)
	}
}

// A terminal gets styling, and styling must not change the text it wraps.
func TestMarkdownStylesForAColoredTerminal(t *testing.T) {
	styled := render(sampleAnswer, true)
	if !strings.Contains(styled, "\x1b[") {
		t.Fatalf("styled output = %q, want ANSI styling", styled)
	}
	if styled == plain(t, sampleAnswer) {
		t.Fatal("styled output equals plain output, want the styling applied")
	}
	if stripANSI(styled) != stripANSI(render(sampleAnswer, false)) {
		t.Fatalf("styled text %q, want the same text as %q", stripANSI(styled), stripANSI(render(sampleAnswer, false)))
	}
}

func TestMarkdownOfBlankSourceWritesNothing(t *testing.T) {
	for _, source := range []string{"", "   ", "\n\n\t\n"} {
		if got := plain(t, source); got != "" {
			t.Fatalf("render(%q) = %q, want empty output", source, got)
		}
	}
}

func TestMarkdownEndsWithASingleNewline(t *testing.T) {
	got := plain(t, "done\n\n\n")

	if got != "done\n" {
		t.Fatalf("rendered output = %q, want exactly one trailing newline", got)
	}
}

func TestMarkdownReportsWriteErrors(t *testing.T) {
	err := MarkdownNoColor(errorWriter{}, sampleAnswer)
	if err == nil {
		t.Fatal("MarkdownNoColor() error = nil, want the write failure")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("MarkdownNoColor() error = %v, want it to wrap the write failure", err)
	}
}

// The renderer must stay plain when the destination is a file or a pipe, and
// stay plain when the user asked for no color.
func TestColorEnabled(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("creating file: %v", err)
	}
	defer file.Close()

	if ColorEnabled(file) {
		t.Fatal("ColorEnabled(file) = true, want false for a regular file")
	}
	if ColorEnabled(&bytes.Buffer{}) {
		t.Fatal("ColorEnabled(buffer) = true, want false for a non-file writer")
	}
	// /dev/null is a character device, so it stands in for a terminal.
	device, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("opening %s: %v", os.DevNull, err)
	}
	defer device.Close()

	if colorEnabled(device, func(string) string { return "1" }) {
		t.Fatal("colorEnabled() = true, want false when NO_COLOR is set")
	}
	if !colorEnabled(device, func(string) string { return "" }) {
		t.Skip("os.DevNull is not a character device on this platform")
	}
	if colorEnabled(device, func(key string) string {
		if key == "TERM" {
			return "dumb"
		}
		return ""
	}) {
		t.Fatal("colorEnabled() = true, want false for TERM=dumb")
	}
}

func TestMarkdownColorFollowsTheWriter(t *testing.T) {
	var buf bytes.Buffer
	if err := Markdown(&buf, "**bold**"); err != nil {
		t.Fatalf("Markdown() error = %v", err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("output = %q, want no styling for a non-terminal writer", buf.String())
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// stripANSI removes escape sequences so styled and plain text can be compared.
func stripANSI(s string) string {
	matcher := regexp.MustCompile("\x1b\\[[0-9;]*m")
	return matcher.ReplaceAllString(s, "")
}
