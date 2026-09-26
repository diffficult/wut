package terminal

import (
	"fmt"
	"strings"
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
