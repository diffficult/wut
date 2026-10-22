package terminal

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxChars bounds the terminal context handed to the LLM.
	MaxChars = 10000
	// MaxCommands bounds how many previous commands are included.
	MaxCommands = 3
)

// Command is one shell command with the output it produced.
type Command struct {
	Text   string
	Output string
}

// GetCommands splits pane output into commands, newest first, using the shell
// prompt as the separator. The most recent entry is the running wut invocation
// itself, so it is excluded.
func GetCommands(paneOutput string, prompt string) []Command {
	if prompt == "" {
		return nil
	}

	var commands []Command
	var buffer []string

	for _, line := range reversed(strings.Split(paneOutput, "\n")) {
		if strings.TrimSpace(line) == "" {
			continue
		}

		index := indexFold(line, prompt)
		if index < 0 {
			buffer = append(buffer, line)
			continue
		}

		commands = append(commands, Command{
			Text:   strings.TrimSpace(line[index+len(prompt):]),
			Output: strings.TrimSpace(strings.Join(reversed(buffer), "\n")),
		})
		buffer = nil
	}

	if len(commands) == 0 {
		return nil
	}
	return commands[1:] // Exclude the wut command itself
}

// indexFold returns the byte offset of the first case-insensitive match of
// substr in s, or -1. It never relies on index positions inside a lowercased
// copy, because Unicode case folding can change byte lengths.
func indexFold(s string, substr string) int {
	if substr == "" {
		return 0
	}

	width := len(substr)
	for i := 0; i+width <= len(s); i++ {
		if !utf8.RuneStart(s[i]) || !runeBoundaryAt(s, i+width) {
			continue
		}
		if strings.EqualFold(s[i:i+width], substr) {
			return i
		}
	}
	return -1
}

// runeBoundaryAt reports whether index sits on a rune boundary or at the end
// of s.
func runeBoundaryAt(s string, index int) bool {
	return index == len(s) || utf8.RuneStart(s[index])
}

// TruncateCommands keeps at most maxCommands commands, dropping the oldest ones
// once the maxChars budget is exhausted.
func TruncateCommands(commands []Command, maxCommands int, maxChars int) []Command {
	var truncated []Command
	numChars := 0

	for _, command := range commands {
		if len(truncated) >= maxCommands {
			break
		}

		commandChars := len(command.Text)
		if commandChars+numChars > maxChars {
			break
		}
		numChars += commandChars

		var lines []string
		for _, line := range reversed(strings.Split(command.Output, "\n")) {
			lineChars := len(line)
			if lineChars+numChars > maxChars {
				break
			}
			lines = append(lines, line)
			numChars += lineChars
		}

		truncated = append(truncated, Command{
			Text:   command.Text,
			Output: strings.Join(reversed(lines), "\n"),
		})
	}

	return truncated
}

// TruncatePaneOutput drops leading blank lines and the trailing wut invocation,
// then keeps the newest maxChars characters.
func TruncatePaneOutput(output string, maxChars int) string {
	hitNonEmptyLine := false
	var lines []string // Newest to oldest.

	for _, line := range reversed(strings.Split(output, "\n")) {
		if strings.TrimSpace(line) != "" {
			hitNonEmptyLine = true
		}
		if hitNonEmptyLine {
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return ""
	}

	lines = lines[1:] // Remove the wut command.
	truncated := strings.Join(reversed(lines), "\n")
	if maxChars > 0 && len(truncated) > maxChars {
		truncated = truncated[len(truncated)-maxChars:]
	}
	return strings.TrimSpace(truncated)
}

// CommandToString renders a command with its prompt and output.
func CommandToString(command Command, prompt string) string {
	if prompt == "" {
		prompt = "$"
	}

	out := fmt.Sprintf("%s %s", prompt, command.Text)
	if strings.TrimSpace(command.Output) != "" {
		out += "\n" + command.Output
	}
	return out
}

// BuildContext renders the terminal context for the LLM. Without a prompt the
// commands cannot be separated reliably, so the raw pane is used instead.
func BuildContext(paneOutput string, prompt string, maxCommands int, maxChars int) string {
	if strings.TrimSpace(paneOutput) == "" {
		return "<terminal_history>No terminal output found.</terminal_history>"
	}

	if prompt == "" {
		return fmt.Sprintf("<terminal_history>\n%s\n</terminal_history>", TruncatePaneOutput(paneOutput, maxChars))
	}

	commands := TruncateCommands(GetCommands(paneOutput, prompt), maxCommands, maxChars)
	if len(commands) == 0 {
		return fmt.Sprintf("<terminal_history>\n%s\n</terminal_history>", TruncatePaneOutput(paneOutput, maxChars))
	}

	return renderContext(commands, prompt)
}

// BuildContextAt renders the terminal context ending at the back-th most recent
// command: 1 is the last command, 2 the one before it, and so on. The selected
// command becomes the last command of the context and the older ones become its
// previous commands; anything newer is left out. It fails when the number is
// below 1, when the shell prompt is unknown and the pane cannot be split, or
// when the pane shows fewer commands than requested.
func BuildContextAt(paneOutput string, prompt string, back int, maxCommands int, maxChars int) (string, error) {
	if back < 1 {
		return "", fmt.Errorf("output number must be at least 1, got %d", back)
	}
	if back == 1 {
		return BuildContext(paneOutput, prompt, maxCommands, maxChars), nil
	}

	if strings.TrimSpace(paneOutput) == "" {
		return "", fmt.Errorf("cannot reach output %d: no terminal output was captured", back)
	}
	if prompt == "" {
		return "", fmt.Errorf("cannot reach output %d: the shell prompt could not be determined", back)
	}

	commands := GetCommands(paneOutput, prompt)
	if len(commands) < back {
		return "", fmt.Errorf("cannot reach output %d: the terminal shows only %d previous command(s)", back, len(commands))
	}

	commands = TruncateCommands(commands[back-1:], maxCommands, maxChars)
	if len(commands) == 0 {
		return "", fmt.Errorf("cannot reach output %d: the command does not fit in the context limit", back)
	}

	return renderContext(commands, prompt), nil
}

// InferPrompt derives the shell prompt from the pane when the shell cannot
// report it. The newest line holds the running wut invocation. When invocation
// is present in that line, the text before it is the prompt; otherwise the
// longest common prefix with an older line is cut at the last prompt-like
// boundary. It returns "" when no prompt can be inferred.
func InferPrompt(paneOutput string, invocation string) string {
	var lines []string
	for _, line := range strings.Split(paneOutput, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, "\r"))
		}
	}
	if len(lines) < 2 {
		return ""
	}

	newest := lines[len(lines)-1]
	earlier := lines[:len(lines)-1]

	if prompt, ok := promptFromInvocation(newest, earlier, invocation); ok {
		return prompt
	}

	best := ""
	for _, line := range earlier {
		if prefix := commonPrefix(newest, line); len(prefix) > len(best) {
			best = prefix
		}
	}
	return trimToPrompt(best)
}

// promptFromInvocation returns the text before invocation on the newest line,
// which is the prompt plus the command word, when that text ends in whitespace
// and also starts an older line. The older-line check rejects an invocation
// word that appears inside the command's own arguments.
func promptFromInvocation(newest string, earlier []string, invocation string) (string, bool) {
	if invocation == "" {
		return "", false
	}
	index := strings.LastIndex(newest, invocation)
	if index <= 0 {
		return "", false
	}

	candidate := newest[:index]
	if last := candidate[len(candidate)-1]; last != ' ' && last != '\t' {
		return "", false
	}
	for _, line := range earlier {
		if strings.HasPrefix(line, candidate) {
			return candidate, true
		}
	}
	return "", false
}

// trimToPrompt cuts a shared prefix at the boundary that ends the prompt. The
// last whitespace whose preceding character is not a letter or digit wins, so a
// repeated "/path/wut" argument does not turn into part of the prompt; when
// there is no such boundary, the first whitespace is used.
func trimToPrompt(prefix string) string {
	firstSpace := -1
	lastSymbolSpace := -1
	for i, r := range prefix {
		if r != ' ' && r != '\t' {
			continue
		}
		if firstSpace < 0 {
			firstSpace = i
		}
		if i == 0 {
			continue
		}
		previous, _ := utf8.DecodeLastRuneInString(prefix[:i])
		if !unicode.IsLetter(previous) && !unicode.IsDigit(previous) {
			lastSymbolSpace = i
		}
	}

	switch {
	case lastSymbolSpace >= 0:
		return prefix[:lastSymbolSpace+1]
	case firstSpace >= 0:
		return prefix[:firstSpace+1]
	default:
		return ""
	}
}

// commonPrefix returns the longest common prefix of a and b.
func commonPrefix(a string, b string) string {
	length := len(a)
	if len(b) < length {
		length = len(b)
	}
	i := 0
	for i < length && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// renderContext formats commands, newest first, as the terminal history sent to
// the provider: the newest command is the last command and the rest are context.
func renderContext(commands []Command, prompt string) string {
	reversedCommands := reversed(commands) // Oldest to newest.
	previous := reversedCommands[:len(reversedCommands)-1]
	last := reversedCommands[len(reversedCommands)-1]

	var context strings.Builder
	context.WriteString("<terminal_history>\n")
	context.WriteString("<previous_commands>\n")
	for i, command := range previous {
		if i > 0 {
			context.WriteString("\n")
		}
		context.WriteString(CommandToString(command, prompt))
	}
	context.WriteString("\n</previous_commands>\n")
	context.WriteString("\n<last_command>\n")
	context.WriteString(CommandToString(last, prompt))
	context.WriteString("\n</last_command>")
	context.WriteString("\n</terminal_history>")

	return context.String()
}

// reversed returns a new slice in reverse order.
func reversed[T any](values []T) []T {
	out := make([]T, len(values))
	for i, value := range values {
		out[len(values)-1-i] = value
	}
	return out
}
