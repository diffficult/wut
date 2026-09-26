package terminal

import (
	"strings"
	"testing"
)

const testPrompt = "$ "

func TestGetCommandsExcludesCurrentWutInvocation(t *testing.T) {
	pane := strings.Join([]string{
		"user@host:~$ ls -l",
		"total 0",
		"user@host:~$ cat foo",
		"cat: foo: No such file or directory",
		"user@host:~$ wut",
		"",
	}, "\n")

	commands := GetCommands(pane, testPrompt)
	if len(commands) != 2 {
		t.Fatalf("GetCommands() returned %d commands, want 2: %+v", len(commands), commands)
	}
	if commands[0].Text != "cat foo" {
		t.Fatalf("newest command text = %q, want %q", commands[0].Text, "cat foo")
	}
	if commands[0].Output != "cat: foo: No such file or directory" {
		t.Fatalf("newest command output = %q", commands[0].Output)
	}
	if commands[1].Text != "ls -l" {
		t.Fatalf("older command text = %q, want %q", commands[1].Text, "ls -l")
	}
	if commands[1].Output != "total 0" {
		t.Fatalf("older command output = %q", commands[1].Output)
	}
}

func TestGetCommandsReturnsNewestFirst(t *testing.T) {
	pane := "$ one\n$ two\n$ three\n$ wut"
	commands := GetCommands(pane, testPrompt)
	if len(commands) != 3 {
		t.Fatalf("GetCommands() returned %d commands, want 3", len(commands))
	}
	want := []string{"three", "two", "one"}
	for i, w := range want {
		if commands[i].Text != w {
			t.Fatalf("command[%d].Text = %q, want %q", i, commands[i].Text, w)
		}
	}
}

func TestGetCommandsSkipsBlankOutputLines(t *testing.T) {
	pane := "$ ls\n\ntotal 0\n\n$ wut"
	commands := GetCommands(pane, testPrompt)
	if len(commands) != 1 {
		t.Fatalf("GetCommands() returned %d commands, want 1", len(commands))
	}
	if commands[0].Output != "total 0" {
		t.Fatalf("command output = %q, want %q", commands[0].Output, "total 0")
	}
}

func TestGetCommandsSplitsOnFirstPromptOccurrence(t *testing.T) {
	pane := "noise $ echo $1\nhi\n$ wut"
	commands := GetCommands(pane, testPrompt)
	if len(commands) != 1 {
		t.Fatalf("GetCommands() returned %d commands, want 1", len(commands))
	}
	if commands[0].Text != "echo $1" {
		t.Fatalf("command text = %q, want %q", commands[0].Text, "echo $1")
	}
}

// Parity note: the Python implementation detects the prompt case-insensitively
// but splits case-sensitively, which raises when only the case differs. The Go
// port keeps the case-insensitive match and splits at the matched offset, so a
// differently-cased prompt still yields the command text.
func TestGetCommandsPromptMatchIsCaseInsensitive(t *testing.T) {
	commands := GetCommands("USER@HOST:~$ ls\nUSER@HOST:~$ wut", "user@host:~$ ")
	if len(commands) != 1 {
		t.Fatalf("GetCommands() returned %d commands, want 1", len(commands))
	}
	if commands[0].Text != "ls" {
		t.Fatalf("command text = %q, want %q", commands[0].Text, "ls")
	}
}

func TestGetCommandsEmptyPane(t *testing.T) {
	if got := GetCommands("", testPrompt); len(got) != 0 {
		t.Fatalf("GetCommands(\"\") = %+v, want no commands", got)
	}
	if got := GetCommands("\n\n", testPrompt); len(got) != 0 {
		t.Fatalf("GetCommands(blank) = %+v, want no commands", got)
	}
}

func TestGetCommandsWithoutPromptReturnsNothing(t *testing.T) {
	// An empty prompt matches every line in the Python implementation, which
	// then fails to split. The Go port fails safe and returns nothing, so
	// BuildContext falls back to the raw pane instead of mis-parsing it.
	if got := GetCommands("$ ls\n$ wut", ""); len(got) != 0 {
		t.Fatalf("GetCommands(empty prompt) = %+v, want no commands", got)
	}
}

func TestGetCommandsOnlyWutInvocation(t *testing.T) {
	if got := GetCommands("$ wut\n", testPrompt); len(got) != 0 {
		t.Fatalf("GetCommands() = %+v, want no commands after excluding wut", got)
	}
}

func TestTruncateCommandsKeepsNewestFirst(t *testing.T) {
	commands := []Command{
		{Text: "three", Output: "3"},
		{Text: "two", Output: "2"},
		{Text: "one", Output: "1"},
	}

	got := TruncateCommands(commands, 2, 1000)
	if len(got) != 2 {
		t.Fatalf("TruncateCommands() returned %d commands, want 2", len(got))
	}
	if got[0].Text != "three" || got[1].Text != "two" {
		t.Fatalf("TruncateCommands() = %+v, want newest two in order", got)
	}
}

func TestTruncateCommandsRespectsCharBudget(t *testing.T) {
	commands := []Command{{Text: "aaaaaaaa", Output: "1111\n2222\n3333"}}

	got := TruncateCommands(commands, 3, 12)
	if len(got) != 1 {
		t.Fatalf("TruncateCommands() returned %d commands, want 1", len(got))
	}
	if got[0].Text != "aaaaaaaa" {
		t.Fatalf("command text = %q, want %q", got[0].Text, "aaaaaaaa")
	}
	if got[0].Output != "3333" {
		t.Fatalf("command output = %q, want only the newest output line %q", got[0].Output, "3333")
	}
}

func TestTruncateCommandsEmptyInput(t *testing.T) {
	if got := TruncateCommands(nil, 3, 100); len(got) != 0 {
		t.Fatalf("TruncateCommands(nil) = %+v, want none", got)
	}
}

func TestTruncateCommandsNonPositiveLimits(t *testing.T) {
	commands := []Command{{Text: "ls", Output: ""}}
	if got := TruncateCommands(commands, 0, 100); len(got) != 0 {
		t.Fatalf("TruncateCommands(limit 0) = %+v, want none", got)
	}
	if got := TruncateCommands(commands, 3, 0); len(got) != 0 {
		t.Fatalf("TruncateCommands(budget 0) = %+v, want none", got)
	}
}

func TestTruncatePaneOutputDropsWutCommandAndLeadingBlanks(t *testing.T) {
	// Parity: the Python implementation drops the trailing wut line and the
	// blank lines above it, keeping the remaining lines in pane order.
	got := TruncatePaneOutput("old\n\nnew\nwut", 1000)
	if got != "old\n\nnew" {
		t.Fatalf("TruncatePaneOutput() = %q, want %q", got, "old\n\nnew")
	}
}

func TestTruncatePaneOutputKeepsNewestChars(t *testing.T) {
	got := TruncatePaneOutput("old\nnew\nwut", 3)
	if got != "new" {
		t.Fatalf("TruncatePaneOutput() = %q, want %q", got, "new")
	}
}

func TestTruncatePaneOutputEmpty(t *testing.T) {
	if got := TruncatePaneOutput("", 100); got != "" {
		t.Fatalf("TruncatePaneOutput(\"\") = %q, want empty", got)
	}
	if got := TruncatePaneOutput("wut", 100); got != "" {
		t.Fatalf("TruncatePaneOutput(wut only) = %q, want empty", got)
	}
}

// Parity: the Python implementation renders f"{prompt} {text}", so a prompt
// that already ends in a space produces two spaces.
func TestCommandToString(t *testing.T) {
	got := CommandToString(Command{Text: "ls -l", Output: "total 0"}, "$")
	want := "$ ls -l\ntotal 0"
	if got != want {
		t.Fatalf("CommandToString() = %q, want %q", got, want)
	}
	if got := CommandToString(Command{Text: "ls"}, "$ "); got != "$  ls" {
		t.Fatalf("CommandToString() = %q, want %q", got, "$  ls")
	}
}

func TestCommandToStringOmitsBlankOutput(t *testing.T) {
	got := CommandToString(Command{Text: "ls", Output: "   \n"}, "$")
	if got != "$ ls" {
		t.Fatalf("CommandToString() = %q, want %q", got, "$ ls")
	}
}

func TestCommandToStringDefaultsPrompt(t *testing.T) {
	got := CommandToString(Command{Text: "ls"}, "")
	if got != "$ ls" {
		t.Fatalf("CommandToString() = %q, want %q", got, "$ ls")
	}
}

func TestBuildContextWithoutPaneOutput(t *testing.T) {
	got := BuildContext("", testPrompt, 3, 1000)
	want := "<terminal_history>No terminal output found.</terminal_history>"
	if got != want {
		t.Fatalf("BuildContext() = %q, want %q", got, want)
	}
}

func TestBuildContextWithoutPromptFallsBackToRawPane(t *testing.T) {
	got := BuildContext("old\n\nnew\nwut", "", 3, 1000)
	want := "<terminal_history>\nold\n\nnew\n</terminal_history>"
	if got != want {
		t.Fatalf("BuildContext() = %q, want %q", got, want)
	}
}

func TestBuildContextWithPrompt(t *testing.T) {
	pane := strings.Join([]string{
		"user@host:~$ ls -l",
		"total 0",
		"user@host:~$ cat foo",
		"no such file",
		"user@host:~$ wut",
	}, "\n")

	got := BuildContext(pane, "$ ", 2, 1000)
	want := strings.Join([]string{
		"<terminal_history>",
		"<previous_commands>",
		"$  ls -l",
		"total 0",
		"</previous_commands>",
		"",
		"<last_command>",
		"$  cat foo",
		"no such file",
		"</last_command>",
		"</terminal_history>",
	}, "\n")
	if got != want {
		t.Fatalf("BuildContext() =\n%s\nwant\n%s", got, want)
	}
}

func TestBuildContextWithSingleCommand(t *testing.T) {
	got := BuildContext("$ ls\ntotal 0\n$ wut", "$ ", 3, 1000)
	want := strings.Join([]string{
		"<terminal_history>",
		"<previous_commands>",
		"",
		"</previous_commands>",
		"",
		"<last_command>",
		"$  ls",
		"total 0",
		"</last_command>",
		"</terminal_history>",
	}, "\n")
	if got != want {
		t.Fatalf("BuildContext() =\n%s\nwant\n%s", got, want)
	}
}

func TestBuildContextFallsBackWhenNoCommandMatchesPrompt(t *testing.T) {
	got := BuildContext("alpha\nbeta\nwut", "$$ ", 3, 1000)
	want := "<terminal_history>\nalpha\nbeta\n</terminal_history>"
	if got != want {
		t.Fatalf("BuildContext() = %q, want %q", got, want)
	}
}

func TestBuildContextNeverEmitsTheWutInvocation(t *testing.T) {
	pane := "$ sleep 10\n$ wut --query what happened"
	if strings.Contains(BuildContext(pane, "$ ", 3, 1000), "wut --query") {
		t.Fatal("BuildContext() leaked the current wut invocation into the context")
	}
}

func TestBuildContextWithWhitespaceOnlyPane(t *testing.T) {
	got := BuildContext("   \n\t\n", testPrompt, 3, 1000)
	want := "<terminal_history>No terminal output found.</terminal_history>"
	if got != want {
		t.Fatalf("BuildContext() = %q, want %q", got, want)
	}
}

func TestBuildContextFallsBackWhenOnlyOneCommandIsPresent(t *testing.T) {
	// Only the newest command is dropped as the wut invocation, so a pane
	// without a trailing wut line falls back to the raw pane with its newest
	// line removed. Matches the Python behavior.
	got := BuildContext("$ ls\ntotal 0", testPrompt, 3, 1000)
	want := "<terminal_history>\n$ ls\n</terminal_history>"
	if got != want {
		t.Fatalf("BuildContext() = %q, want %q", got, want)
	}
}

func FuzzGetCommandsNoPanic(f *testing.F) {
	f.Add("$ ls\ntotal 0\n$ wut", "$ ")
	f.Add("", "")
	f.Add("no prompt here", "$ ")
	f.Fuzz(func(t *testing.T, pane, prompt string) {
		for _, command := range GetCommands(pane, prompt) {
			_ = CommandToString(command, prompt)
		}
	})
}

func FuzzBuildContextNoPanic(f *testing.F) {
	f.Add("$ ls\ntotal 0\n$ wut", "$ ")
	f.Add("a\r\nb", "")
	f.Fuzz(func(t *testing.T, pane, prompt string) {
		ctx := BuildContext(pane, prompt, MaxCommands, MaxChars)
		if !strings.HasPrefix(ctx, "<terminal_history>") {
			t.Fatalf("BuildContext() = %q, want a terminal_history context", ctx)
		}
	})
}

// Regression: case folding these inputs changes byte length, so a match index
// computed on a lowercased copy overran the original line.
func TestGetCommandsUnicodeCaseFolding(t *testing.T) {
	commands := GetCommands("\xf20", "0")
	if len(commands) != 0 {
		t.Fatalf("GetCommands() = %+v, want no commands", commands)
	}
}

func TestGetCommandsUnicodeCaseFoldingInContext(t *testing.T) {
	commands := GetCommands("0\x9cA\xa9\xa3\nA\xbd", "A\xbd")
	if len(commands) != 0 {
		t.Fatalf("GetCommands() = %+v, want no commands", commands)
	}
}
