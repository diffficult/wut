package terminal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeRunner records invocations and returns canned results, so every capture
// and prompt assertion runs without a live tmux/screen session.
type fakeRunner struct {
	name      string
	args      []string
	outPath   string
	runErr    error
	calls     int
	file      string
	output    string
	outputErr error
}

func (f *fakeRunner) Output(name string, args []string) (string, error) {
	f.name = name
	f.args = args
	f.calls++
	return f.output, f.outputErr
}

func (f *fakeRunner) RunToFile(name string, args []string, outPath string) error {
	f.name = name
	f.args = args
	f.outPath = outPath
	f.calls++
	if f.runErr != nil {
		return f.runErr
	}
	if f.file == "" {
		return nil
	}
	return os.WriteFile(outPath, []byte(f.file), 0o600)
}

func TestDetectMultiplexer(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"tmux only", map[string]string{"TMUX": "/tmp/tmux-0/default,1,0"}, MultiplexerTmux},
		{"screen only", map[string]string{"STY": "1.pts-0.host"}, MultiplexerScreen},
		{"tmux wins over screen", map[string]string{"TMUX": "/tmp/tmux-0/default,1,0", "STY": "1.pts-0.host"}, MultiplexerTmux},
		{"neither", map[string]string{}, ""},
		{"empty values count as unset", map[string]string{"TMUX": "", "STY": ""}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMultiplexer(func(key string) string { return tt.env[key] })
			if got != tt.want {
				t.Fatalf("DetectMultiplexer() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPaneCommandTmux(t *testing.T) {
	name, args, err := PaneCommand(MultiplexerTmux, "/tmp/capture.1")
	if err != nil {
		t.Fatalf("PaneCommand() error = %v", err)
	}
	if name != "tmux" {
		t.Fatalf("PaneCommand() name = %q, want %q", name, "tmux")
	}
	want := []string{"capture-pane", "-p", "-S", "-"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("PaneCommand() args = %q, want %q", args, want)
	}
}

func TestPaneCommandScreen(t *testing.T) {
	name, args, err := PaneCommand(MultiplexerScreen, "/tmp/capture.1")
	if err != nil {
		t.Fatalf("PaneCommand() error = %v", err)
	}
	if name != "screen" {
		t.Fatalf("PaneCommand() name = %q, want %q", name, "screen")
	}
	want := []string{"-X", "hardcopy", "-h", "/tmp/capture.1"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("PaneCommand() args = %q, want %q", args, want)
	}
}

func TestPaneCommandRejectsUnsupportedMultiplexer(t *testing.T) {
	if _, _, err := PaneCommand("", "/tmp/capture.1"); !errors.Is(err, ErrNoMultiplexer) {
		t.Fatalf("PaneCommand(\"\") error = %v, want ErrNoMultiplexer", err)
	}
	if _, _, err := PaneCommand("emacs", "/tmp/capture.1"); !errors.Is(err, ErrUnsupportedMultiplexer) {
		t.Fatalf("PaneCommand(\"emacs\") error = %v, want ErrUnsupportedMultiplexer", err)
	}
}

func TestCapturePaneTmuxReadsCapturedFile(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "capture")
	runner := &fakeRunner{file: "user@host:~$ ls -l\ntotal 0\n"}

	got, err := CapturePane(MultiplexerTmux, outPath, runner)
	if err != nil {
		t.Fatalf("CapturePane() error = %v", err)
	}
	if got != "user@host:~$ ls -l\ntotal 0\n" {
		t.Fatalf("CapturePane() = %q", got)
	}
	if runner.name != "tmux" {
		t.Fatalf("CapturePane() invoked %q, want tmux", runner.name)
	}
	if runner.outPath != outPath {
		t.Fatalf("CapturePane() outPath = %q, want %q", runner.outPath, outPath)
	}
	if strings.Join(runner.args, "\x00") != "capture-pane\x00-p\x00-S\x00-" {
		t.Fatalf("CapturePane() args = %q", runner.args)
	}
}

func TestCapturePaneScreenPassesHardcopyPath(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "hardcopy")
	runner := &fakeRunner{file: "screen pane\n"}

	got, err := CapturePane(MultiplexerScreen, outPath, runner)
	if err != nil {
		t.Fatalf("CapturePane() error = %v", err)
	}
	if got != "screen pane\n" {
		t.Fatalf("CapturePane() = %q", got)
	}
	if runner.name != "screen" {
		t.Fatalf("CapturePane() invoked %q, want screen", runner.name)
	}
	want := []string{"-X", "hardcopy", "-h", outPath}
	if strings.Join(runner.args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("CapturePane() args = %q, want %q", runner.args, want)
	}
}

func TestCapturePanePropagatesRunnerError(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{runErr: errors.New("exit status 1")}

	_, err := CapturePane(MultiplexerTmux, filepath.Join(dir, "capture"), runner)
	if err == nil {
		t.Fatal("CapturePane() error = nil, want runner error")
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("CapturePane() error = %v, want it to wrap the runner error", err)
	}
}

func TestCapturePaneRejectsNilRunner(t *testing.T) {
	if _, err := CapturePane(MultiplexerTmux, filepath.Join(t.TempDir(), "capture"), nil); err == nil {
		t.Fatal("CapturePane(nil runner) error = nil, want an error")
	}
}

// ---------------------------------------------------------------------------
// Kitty fallback: third candidate, after tmux and screen.
// ---------------------------------------------------------------------------

// sourceRunner answers per-command-name results and records every invocation,
// so candidate order and fall-through are observable without a live multiplexer
// or a live Kitty instance.
type sourceRunner struct {
	outputs map[string]string
	errs    map[string]error
	names   []string
	argvs   []string
	toFile  int
}

func (r *sourceRunner) record(name string, args []string) {
	r.names = append(r.names, name)
	r.argvs = append(r.argvs, strings.Join(args, "\x00"))
}

func (r *sourceRunner) Output(name string, args []string) (string, error) {
	r.record(name, args)
	if err, ok := r.errs[name]; ok {
		return "", err
	}
	return r.outputs[name], nil
}

func (r *sourceRunner) RunToFile(name string, args []string, outPath string) error {
	r.toFile++
	if err, ok := r.errs[name]; ok {
		return err
	}
	return os.WriteFile(outPath, []byte(r.outputs[name]), 0o600)
}

func (r *sourceRunner) invoked(name string) bool {
	for _, called := range r.names {
		if called == name {
			return true
		}
	}
	return false
}

func kittyEnv(id string) map[string]string {
	return map[string]string{"KITTY_WINDOW_ID": id}
}

func TestCaptureCandidatesOrder(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"tmux only", map[string]string{"TMUX": "/tmp/tmux-0/default,1,0"}, []string{MultiplexerTmux}},
		{"screen only", map[string]string{"STY": "1.pts-0.host"}, []string{MultiplexerScreen}},
		{"kitty only", map[string]string{"KITTY_WINDOW_ID": "7"}, []string{MultiplexerKitty}},
		{
			"tmux wins, kitty last",
			map[string]string{"TMUX": "/tmp/tmux-0/default,1,0", "STY": "1.pts-0.host", "KITTY_WINDOW_ID": "7"},
			[]string{MultiplexerTmux, MultiplexerScreen, MultiplexerKitty},
		},
		{
			"screen before kitty",
			map[string]string{"STY": "1.pts-0.host", "KITTY_WINDOW_ID": "7"},
			[]string{MultiplexerScreen, MultiplexerKitty},
		},
		{"nothing", map[string]string{}, nil},
		{"empty values count as unset", map[string]string{"TMUX": "", "STY": "", "KITTY_WINDOW_ID": ""}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CaptureCandidates(func(key string) string { return tt.env[key] })
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("CaptureCandidates() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKittyTextCommandTargetsOnlyTheCurrentWindow(t *testing.T) {
	name, args, err := KittyTextCommand("7")
	if err != nil {
		t.Fatalf("KittyTextCommand() error = %v", err)
	}
	if name != "kitten" {
		t.Fatalf("KittyTextCommand() name = %q, want kitten", name)
	}
	want := []string{"@", "get-text", "--match", "id:7", "--extent=all"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("KittyTextCommand() args = %q, want %q", args, want)
	}
}

func TestKittyTextCommandRejectsAnUnspecifiedWindow(t *testing.T) {
	for _, id := range []string{"", "   ", "all", "id:1", "../../etc", "7 8"} {
		if _, _, err := KittyTextCommand(id); !errors.Is(err, ErrKittyWindowID) {
			t.Fatalf("KittyTextCommand(%q) error = %v, want ErrKittyWindowID", id, err)
		}
	}
}

func TestCaptureKittyKeepsABoundedTailWithoutATempFile(t *testing.T) {
	old := strings.Repeat("old output line\n", 40_000) // ~640 KiB of scrollback.
	tail := "user@host:~$ ls\nfile.txt\n"
	runner := &sourceRunner{outputs: map[string]string{"kitten": old + tail}}

	got, err := CaptureKitty("7", runner)
	if err != nil {
		t.Fatalf("CaptureKitty() error = %v", err)
	}
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("CaptureKitty() = %q..., want the newest output kept at the end", got[len(got)-40:])
	}
	if len(got) != KittyMaxCaptureBytes {
		t.Fatalf("CaptureKitty() kept %d bytes, want the %d byte bound", len(got), KittyMaxCaptureBytes)
	}
	if runner.toFile != 0 {
		t.Fatalf("CaptureKitty() wrote %d capture files, want none for the unbounded kitty response", runner.toFile)
	}
}

func TestCaptureKittyKeepsShortOutputUnchanged(t *testing.T) {
	runner := &sourceRunner{outputs: map[string]string{"kitten": "user@host:~$ ls\nfile.txt\n"}}

	got, err := CaptureKitty("7", runner)
	if err != nil {
		t.Fatalf("CaptureKitty() error = %v", err)
	}
	if got != "user@host:~$ ls\nfile.txt\n" {
		t.Fatalf("CaptureKitty() = %q", got)
	}
}

func TestCaptureKittyEmptyTextIsAnError(t *testing.T) {
	runner := &sourceRunner{outputs: map[string]string{"kitten": "  \n\n"}}

	if _, err := CaptureKitty("7", runner); !errors.Is(err, ErrNoPaneOutput) {
		t.Fatalf("CaptureKitty() error = %v, want ErrNoPaneOutput", err)
	}
}

func TestCaptureKittyFailureExplainsThePermissionSetup(t *testing.T) {
	runner := &sourceRunner{errs: map[string]error{"kitten": errors.New("kitty is not running")}}

	_, err := CaptureKitty("7", runner)
	if !errors.Is(err, ErrKittyRemoteControl) {
		t.Fatalf("CaptureKitty() error = %v, want ErrKittyRemoteControl", err)
	}
	for _, want := range []string{"kitten", "kitty", "remote control", "get-text", "KITTY_WINDOW_ID", KittySetupDocsURL} {
		if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
			t.Fatalf("CaptureKitty() error = %v, want it to mention %q", err, want)
		}
	}
}

func TestCaptureKittyRejectsNilRunner(t *testing.T) {
	if _, err := CaptureKitty("7", nil); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("CaptureKitty(nil) error = %v, want ErrNoRunner", err)
	}
}

func TestCapturePaneFromSourcesStopsAtTheFirstSuccess(t *testing.T) {
	env := map[string]string{"TMUX": "/tmp/tmux-0/default,1,0", "STY": "1.pts-0.host", "KITTY_WINDOW_ID": "7"}
	runner := &sourceRunner{outputs: map[string]string{"tmux": "pane text\n", "screen": "screen text\n", "kitten": "kitty text\n"}}

	source, text, err := CapturePaneFromSources(
		CaptureCandidates(func(key string) string { return env[key] }),
		func(key string) string { return env[key] },
		filepath.Join(t.TempDir(), "capture"),
		runner,
	)
	if err != nil {
		t.Fatalf("CapturePaneFromSources() error = %v", err)
	}
	if source != MultiplexerTmux || text != "pane text\n" {
		t.Fatalf("CapturePaneFromSources() = (%q, %q), want the tmux capture", source, text)
	}
	if runner.invoked("kitten") {
		t.Fatalf("runner saw %q, want no kitty invocation after a successful tmux capture", runner.names)
	}
}

func TestCapturePaneFromSourcesFallsThroughAStaleMultiplexer(t *testing.T) {
	env := map[string]string{"TMUX": "/tmp/tmux-0/default,1,0", "KITTY_WINDOW_ID": "7"}
	runner := &sourceRunner{
		outputs: map[string]string{"kitten": "kitty text\n"},
		errs:    map[string]error{"tmux": errors.New("no server running")},
	}

	source, text, err := CapturePaneFromSources(
		CaptureCandidates(func(key string) string { return env[key] }),
		func(key string) string { return env[key] },
		filepath.Join(t.TempDir(), "capture"),
		runner,
	)
	if err != nil {
		t.Fatalf("CapturePaneFromSources() error = %v", err)
	}
	if source != MultiplexerKitty || text != "kitty text\n" {
		t.Fatalf("CapturePaneFromSources() = (%q, %q), want the kitty capture", source, text)
	}
}

func TestCapturePaneFromSourcesFallsThroughAnEmptyMultiplexerCapture(t *testing.T) {
	env := map[string]string{"STY": "1.pts-0.host", "KITTY_WINDOW_ID": "7"}
	runner := &sourceRunner{
		outputs: map[string]string{"screen": "\n \n", "kitten": "kitty text\n"},
	}

	source, _, err := CapturePaneFromSources(
		CaptureCandidates(func(key string) string { return env[key] }),
		func(key string) string { return env[key] },
		filepath.Join(t.TempDir(), "capture"),
		runner,
	)
	if err != nil {
		t.Fatalf("CapturePaneFromSources() error = %v", err)
	}
	if source != MultiplexerKitty {
		t.Fatalf("CapturePaneFromSources() = %q, want kitty after an empty screen capture", source)
	}
}

func TestCapturePaneFromSourcesUsesKittyWhenNoMultiplexerIsPresent(t *testing.T) {
	runner := &sourceRunner{outputs: map[string]string{"kitten": "kitty text\n"}}

	source, text, err := CapturePaneFromSources(
		CaptureCandidates(func(key string) string { return kittyEnv("7")[key] }),
		func(key string) string { return kittyEnv("7")[key] },
		filepath.Join(t.TempDir(), "capture"),
		runner,
	)
	if err != nil {
		t.Fatalf("CapturePaneFromSources() error = %v", err)
	}
	if source != MultiplexerKitty || text != "kitty text\n" {
		t.Fatalf("CapturePaneFromSources() = (%q, %q), want the kitty capture", source, text)
	}
	if runner.toFile != 0 {
		t.Fatalf("CapturePaneFromSources() wrote %d capture files, want none for kitty", runner.toFile)
	}
}

// Without a window id there is nothing to target: kitty must not be invoked, and
// the run must fail instead of guessing a window.
func TestCapturePaneFromSourcesNeverInvokesKittyWithoutAnID(t *testing.T) {
	runner := &sourceRunner{outputs: map[string]string{"kitten": "kitty text\n"}}

	_, _, err := CapturePaneFromSources(
		[]string{MultiplexerKitty},
		func(string) string { return "" },
		filepath.Join(t.TempDir(), "capture"),
		runner,
	)
	if err == nil {
		t.Fatal("CapturePaneFromSources() error = nil, want a failure without a kitty window id")
	}
	if runner.invoked("kitten") {
		t.Fatalf("runner saw %q, want no kitty invocation without KITTY_WINDOW_ID", runner.names)
	}
}

func TestCapturePaneFromSourcesWithoutCandidatesRunsNothing(t *testing.T) {
	runner := &sourceRunner{}

	_, _, err := CapturePaneFromSources(nil, func(string) string { return "" }, filepath.Join(t.TempDir(), "capture"), runner)
	if !errors.Is(err, ErrNoMultiplexer) {
		t.Fatalf("CapturePaneFromSources() error = %v, want ErrNoMultiplexer", err)
	}
	if len(runner.names) != 0 {
		t.Fatalf("runner saw %q, want no command without a candidate", runner.names)
	}
}

func TestCapturePaneFromSourcesReportsEveryFailure(t *testing.T) {
	env := map[string]string{"TMUX": "x", "STY": "y", "KITTY_WINDOW_ID": "7"}
	runner := &sourceRunner{
		errs: map[string]error{
			"tmux":   errors.New("no server running"),
			"screen": errors.New("no screen session"),
			"kitten": errors.New("kitty is not running"),
		},
	}

	_, _, err := CapturePaneFromSources(
		CaptureCandidates(func(key string) string { return env[key] }),
		func(key string) string { return env[key] },
		filepath.Join(t.TempDir(), "capture"),
		runner,
	)
	if err == nil {
		t.Fatal("CapturePaneFromSources() error = nil, want every failure reported")
	}
	for _, want := range []string{"tmux", "screen", "kitty", KittySetupDocsURL} {
		if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
			t.Fatalf("CapturePaneFromSources() error = %v, want it to mention %q", err, want)
		}
	}
}

func TestBoundedTailIsRuneSafe(t *testing.T) {
	// Each emoji is four bytes, so a byte-sliced tail would split one.
	text := strings.Repeat("🙂", 100) + "tail"
	got := BoundedTail(text, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("BoundedTail() = %q, want valid UTF-8", got)
	}
	if !strings.HasSuffix(got, "tail") {
		t.Fatalf("BoundedTail() = %q, want the newest text kept", got)
	}
}

func TestShellName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/bin/bash", "bash"},
		{"/usr/bin/zsh", "zsh"},
		{"/usr/local/bin/fish", "fish"},
		{"/bin/tcsh", "tcsh"},
		{"/usr/bin/pwsh.exe", "pwsh"},
		{"/bin/PowerShell", "powershell"},
		{"/bin/csh", "csh"},
		{"bash", "bash"},
		{"BASH", "bash"},
		{"/bin/sh", ""},
		{"/bin/zsh-5.9", ""},
		{"", ""},
		{"/usr/bin/python3", ""},
	}

	for _, tt := range tests {
		if got := ShellName(tt.path); got != tt.want {
			t.Errorf("ShellName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestShellPromptCommand(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		wantN string
		wantA []string
		ok    bool
	}{
		{"zsh", "/bin/zsh", "/bin/zsh", []string{"/bin/zsh", "-c", "print -P $PS1"}, true},
		{"bash", "/bin/bash", "echo", []string{`"${PS1@P}"`}, true},
		{"fish", "/usr/bin/fish", "/usr/bin/fish", []string{"/usr/bin/fish", "fish_prompt"}, true},
		{"csh", "/bin/csh", "/bin/csh", []string{"/bin/csh", "-c", "echo $prompt"}, true},
		{"tcsh", "/bin/tcsh", "/bin/tcsh", []string{"/bin/tcsh", "-c", "echo $prompt"}, true},
		{"pwsh", "/usr/bin/pwsh", "/usr/bin/pwsh", []string{"/usr/bin/pwsh", "-c", "Write-Host $prompt"}, true},
		{"powershell", "/usr/bin/powershell", "/usr/bin/powershell", []string{"/usr/bin/powershell", "-c", "Write-Host $prompt"}, true},
		{"sh", "/bin/sh", "", nil, false},
		{"", "", "", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotArgs, ok := ShellPromptCommand(tt.name, tt.path)
			if ok != tt.ok {
				t.Fatalf("ShellPromptCommand(%q) ok = %v, want %v", tt.name, ok, tt.ok)
			}
			if !ok {
				return
			}
			if gotName != tt.wantN {
				t.Fatalf("ShellPromptCommand(%q) name = %q, want %q", tt.name, gotName, tt.wantN)
			}
			if strings.Join(gotArgs, "\x00") != strings.Join(tt.wantA, "\x00") {
				t.Fatalf("ShellPromptCommand(%q) args = %q, want %q", tt.name, gotArgs, tt.wantA)
			}
		})
	}
}

func TestShellPrompt(t *testing.T) {
	runner := &fakeRunner{output: "user@host:~$ \n"}
	got := ShellPrompt("bash", "/bin/bash", runner)
	if got != "user@host:~$" {
		t.Fatalf("ShellPrompt() = %q, want %q", got, "user@host:~$")
	}
}

func TestShellPromptEmptyResult(t *testing.T) {
	runner := &fakeRunner{output: "  \n"}
	if got := ShellPrompt("zsh", "/bin/zsh", runner); got != "" {
		t.Fatalf("ShellPrompt() = %q, want empty string", got)
	}
}

func TestShellPromptBashUnsupportedParameterExpansion(t *testing.T) {
	runner := &fakeRunner{output: `"${PS1@P}"` + "\n"}
	if got := ShellPrompt("bash", "/bin/bash", runner); got != "" {
		t.Fatalf("ShellPrompt() = %q, want empty string when PS1 is unset", got)
	}
}

func TestShellPromptIgnoresRunnerError(t *testing.T) {
	runner := &fakeRunner{outputErr: errors.New("boom")}
	if got := ShellPrompt("fish", "/usr/bin/fish", runner); got != "" {
		t.Fatalf("ShellPrompt() = %q, want empty string on error", got)
	}
}

func TestShellPromptUnknownShell(t *testing.T) {
	runner := &fakeRunner{output: "nope"}
	if got := ShellPrompt("sh", "/bin/sh", runner); got != "" {
		t.Fatalf("ShellPrompt() = %q, want empty string for unknown shell", got)
	}
	if runner.calls != 0 {
		t.Fatalf("ShellPrompt() ran %d commands for an unknown shell, want 0", runner.calls)
	}
}

func TestDetectShellUsesShellEnv(t *testing.T) {
	runner := &fakeRunner{output: "$ "}
	shell := DetectShell(func(key string) string {
		if key == "SHELL" {
			return "/bin/zsh"
		}
		return ""
	}, runner)

	if shell.Name != "zsh" || shell.Path != "/bin/zsh" {
		t.Fatalf("DetectShell() = %+v, want zsh at /bin/zsh", shell)
	}
	if shell.Prompt != "$" {
		t.Fatalf("DetectShell() prompt = %q, want %q", shell.Prompt, "$")
	}
}

func TestDetectShellFallsBackToTFShell(t *testing.T) {
	runner := &fakeRunner{output: "$ "}
	shell := DetectShell(func(key string) string {
		if key == "TF_SHELL" {
			return "/bin/fish"
		}
		return ""
	}, runner)

	if shell.Name != "fish" {
		t.Fatalf("DetectShell() = %+v, want fish", shell)
	}
}

func TestDetectShellKeepsUnknownPathWithoutPrompt(t *testing.T) {
	runner := &fakeRunner{output: "$ "}
	shell := DetectShell(func(key string) string { return "/bin/sh" }, runner)

	if shell.Name != "" || shell.Path != "/bin/sh" || shell.Prompt != "" {
		t.Fatalf("DetectShell() = %+v, want unknown shell with empty name/prompt", shell)
	}
	if runner.calls != 0 {
		t.Fatalf("DetectShell() ran %d commands for an unknown shell, want 0", runner.calls)
	}
}

func TestDetectShellFallsBackToProcessTree(t *testing.T) {
	runner := &fakeRunner{output: "$ "}
	shell := DetectShellFromProcessTree(4242, runner, staticProcessTree{
		names:   map[int]string{4242: "wut", 4241: "node", 4200: "zsh"},
		parents: map[int]int{4242: 4241, 4241: 4200, 4200: 1},
	})

	if shell.Name != "zsh" || shell.Path != "zsh" {
		t.Fatalf("DetectShellFromProcessTree() = %+v, want zsh", shell)
	}
	if shell.Prompt != "$" {
		t.Fatalf("DetectShellFromProcessTree() prompt = %q, want %q", shell.Prompt, "$")
	}
}

func TestDetectShellFromProcessTreeStopsAtInit(t *testing.T) {
	runner := &fakeRunner{output: "$ "}
	shell := DetectShellFromProcessTree(4242, runner, staticProcessTree{
		names:   map[int]string{4242: "systemd"},
		parents: map[int]int{4242: 1},
	})

	if shell.Name != "" {
		t.Fatalf("DetectShellFromProcessTree() = %+v, want no shell found", shell)
	}
}

type staticProcessTree struct {
	names   map[int]string
	parents map[int]int
}

func (s staticProcessTree) name(pid int) string { return s.names[pid] }

func (s staticProcessTree) parent(pid int) (int, bool) {
	parent, ok := s.parents[pid]
	return parent, ok
}

func TestCapturePaneWithoutMultiplexerRunsNothing(t *testing.T) {
	runner := &fakeRunner{file: "should not be used"}
	_, err := CapturePane("", filepath.Join(t.TempDir(), "capture"), runner)
	if !errors.Is(err, ErrNoMultiplexer) {
		t.Fatalf("CapturePane(\"\") error = %v, want ErrNoMultiplexer", err)
	}
	if runner.calls != 0 {
		t.Fatalf("CapturePane() ran %d commands without a multiplexer, want 0", runner.calls)
	}
}

// Edge-case correction: an empty or missing capture file is a capture failure,
// not a silent empty context.
func TestCapturePaneEmptyOutputIsAnError(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{file: ""}

	_, err := CapturePane(MultiplexerTmux, filepath.Join(dir, "capture"), runner)
	if !errors.Is(err, ErrNoPaneOutput) {
		t.Fatalf("CapturePane() error = %v, want ErrNoPaneOutput", err)
	}
}

func TestCapturePaneMissingOutputFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{} // Writes nothing.

	_, err := CapturePane(MultiplexerTmux, filepath.Join(dir, "capture"), runner)
	if !errors.Is(err, ErrNoPaneOutput) {
		t.Fatalf("CapturePane() error = %v, want ErrNoPaneOutput", err)
	}
}

func TestCapturePaneWhitespaceOnlyOutputIsAnError(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{file: "\n \n\t\n"}

	_, err := CapturePane(MultiplexerTmux, filepath.Join(dir, "capture"), runner)
	if !errors.Is(err, ErrNoPaneOutput) {
		t.Fatalf("CapturePane() error = %v, want ErrNoPaneOutput", err)
	}
}

func TestDetectShellWithTreeKeepsEnvironmentPath(t *testing.T) {
	runner := &fakeRunner{output: "user@host:~$ "}
	shell := DetectShellWithTree(func(key string) string {
		if key == "SHELL" {
			return "/bin/sh" // Unknown shell: the prompt must come from the tree.
		}
		return ""
	}, runner, 4242, staticProcessTree{
		names:   map[int]string{4242: "wut", 4200: "/bin/zsh"},
		parents: map[int]int{4242: 4200, 4200: 1},
	})

	if shell.Name != "zsh" {
		t.Fatalf("DetectShellWithTree() = %+v, want the shell from the process tree", shell)
	}
	if shell.Path != "/bin/sh" {
		t.Fatalf("DetectShellWithTree() path = %q, want the environment path", shell.Path)
	}
	if shell.Prompt != "user@host:~$" {
		t.Fatalf("DetectShellWithTree() prompt = %q, want the prompt from /bin/zsh", shell.Prompt)
	}
	if runner.name != "/bin/zsh" {
		t.Fatalf("prompt was queried with %q, want the tree shell path", runner.name)
	}
}

func TestDetectShellWithTreeUsesEnvironmentWhenKnown(t *testing.T) {
	runner := &fakeRunner{output: "$ "}
	shell := DetectShellWithTree(func(key string) string {
		if key == "SHELL" {
			return "/bin/bash"
		}
		return ""
	}, runner, 4242, staticProcessTree{
		names:   map[int]string{4242: "wut"},
		parents: map[int]int{4242: 1},
	})

	if shell.Name != "bash" || shell.Path != "/bin/bash" || shell.Prompt != "$" {
		t.Fatalf("DetectShellWithTree() = %+v, want bash from the environment", shell)
	}
}

func TestDetectShellWithTreeWithoutAnyShell(t *testing.T) {
	runner := &fakeRunner{output: "$ "}
	shell := DetectShellWithTree(func(string) string { return "" }, runner, 4242, staticProcessTree{
		names:   map[int]string{4242: "systemd"},
		parents: map[int]int{4242: 1},
	})

	if shell.Name != "" || shell.Prompt != "" {
		t.Fatalf("DetectShellWithTree() = %+v, want no shell", shell)
	}
	if runner.calls != 0 {
		t.Fatalf("DetectShellWithTree() ran %d commands, want 0", runner.calls)
	}
}

// Guard regressions: the bash prompt probe expands nothing without a shell, so
// any echo of the raw parameter expansion means the prompt is unavailable. A
// garbage prompt would silently corrupt command extraction.
func TestShellPromptBashRawExpansionIsNotAPrompt(t *testing.T) {
	for _, out := range []string{`"${PS1@P}"`, `${PS1@P}`, `echo "${PS1@P}"`, "  \"${PS1@P}\"  \n"} {
		runner := &fakeRunner{output: out}
		if got := ShellPrompt("bash", "/bin/bash", runner); got != "" {
			t.Fatalf("ShellPrompt() with %q = %q, want empty string", out, got)
		}
	}
}

func TestShellPromptBashKeepsARealPrompt(t *testing.T) {
	runner := &fakeRunner{output: "user@host:~$ "}
	if got := ShellPrompt("bash", "/bin/bash", runner); got != "user@host:~$" {
		t.Fatalf("ShellPrompt() = %q, want %q", got, "user@host:~$")
	}
	if strings.Join(runner.args, "\x00") != `"${PS1@P}"` {
		t.Fatalf("bash probe args = %q, want a single argument holding the expansion", runner.args)
	}
}

// ---------------------------------------------------------------------------
// Portable process metadata via ps (Linux, macOS, BSD).
// ---------------------------------------------------------------------------

const linuxPsOutput = `    1     0 systemd
  4200  4150 /bin/zsh
  4241  4200 node
  4242  4241 wut
`

func TestNewPsProcessTableInvokesPsPortably(t *testing.T) {
	runner := &fakeRunner{output: linuxPsOutput}

	if _, err := NewPsProcessTable(runner); err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}
	if runner.name != "ps" {
		t.Fatalf("NewPsProcessTable() ran %q, want ps", runner.name)
	}
	want := []string{"ax", "-o", "pid=,ppid=,comm="}
	if strings.Join(runner.args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("NewPsProcessTable() args = %q, want %q", runner.args, want)
	}
	if runner.calls != 1 {
		t.Fatalf("NewPsProcessTable() ran ps %d times, want a single snapshot", runner.calls)
	}
}

func TestPsProcessTableName(t *testing.T) {
	table, err := NewPsProcessTable(&fakeRunner{output: linuxPsOutput})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}

	if got := table.Name(4200); got != "/bin/zsh" {
		t.Fatalf("Name(4200) = %q, want %q", got, "/bin/zsh")
	}
	if got := table.Name(4242); got != "wut" {
		t.Fatalf("Name(4242) = %q, want %q", got, "wut")
	}
}

func TestPsProcessTableNameUnknownPID(t *testing.T) {
	table, err := NewPsProcessTable(&fakeRunner{output: linuxPsOutput})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}

	if got := table.Name(9999); got != "" {
		t.Fatalf("Name(9999) = %q, want empty for an unknown pid", got)
	}
}

func TestPsProcessTableParent(t *testing.T) {
	table, err := NewPsProcessTable(&fakeRunner{output: linuxPsOutput})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}

	parent, ok := table.Parent(4242)
	if !ok || parent != 4241 {
		t.Fatalf("Parent(4242) = (%d, %v), want (4241, true)", parent, ok)
	}
}

func TestPsProcessTableParentUnknownPID(t *testing.T) {
	table, err := NewPsProcessTable(&fakeRunner{output: linuxPsOutput})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}

	if parent, ok := table.Parent(9999); ok {
		t.Fatalf("Parent(9999) = (%d, true), want no parent", parent)
	}
}

func TestPsProcessTableParentOfKernelProcess(t *testing.T) {
	table, err := NewPsProcessTable(&fakeRunner{output: linuxPsOutput})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}

	if parent, ok := table.Parent(1); ok {
		t.Fatalf("Parent(1) = (%d, true), want no parent so the walk stops", parent)
	}
}

func TestNewPsProcessTablePropagatesRunnerError(t *testing.T) {
	runner := &fakeRunner{outputErr: errors.New("ps: command not found")}

	_, err := NewPsProcessTable(runner)
	if err == nil {
		t.Fatal("NewPsProcessTable() error = nil, want the runner error")
	}
	if !errors.Is(err, ErrNoProcessTable) {
		t.Fatalf("NewPsProcessTable() error = %v, want ErrNoProcessTable", err)
	}
	if !strings.Contains(err.Error(), "ps: command not found") {
		t.Fatalf("NewPsProcessTable() error = %v, want it to wrap the runner error", err)
	}
}

func TestNewPsProcessTableRejectsEmptyOutput(t *testing.T) {
	_, err := NewPsProcessTable(&fakeRunner{output: "   \n\n"})
	if !errors.Is(err, ErrNoProcessTable) {
		t.Fatalf("NewPsProcessTable() error = %v, want ErrNoProcessTable", err)
	}
}

func TestNewPsProcessTableRejectsNilRunner(t *testing.T) {
	if _, err := NewPsProcessTable(nil); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("NewPsProcessTable(nil) error = %v, want ErrNoRunner", err)
	}
}

func TestPsProcessTableSkipsMalformedLines(t *testing.T) {
	output := "  4200  4150 /bin/zsh\n" +
		"garbage line without numbers\n" +
		"   4242 4241 wut extra words\n" +
		"  xxxx  yyyy /bin/fish\n" +
		"  4243\n" // too few fields

	table, err := NewPsProcessTable(&fakeRunner{output: output})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}
	if len(table.entries) != 2 {
		t.Fatalf("parsed %d entries, want 2: %+v", len(table.entries), table.entries)
	}
	if got := table.Name(4200); got != "/bin/zsh" {
		t.Fatalf("Name(4200) = %q, want %q", got, "/bin/zsh")
	}
	// Everything after the third field belongs to the command.
	if got := table.Name(4242); got != "wut extra words" {
		t.Fatalf("Name(4242) = %q, want %q", got, "wut extra words")
	}
}

func TestPsProcessTableHandlesCarriageReturnsAndBlankLines(t *testing.T) {
	output := "  4200  4150 /bin/zsh\r\n\r\n  4242  4200 wut\r\n"

	table, err := NewPsProcessTable(&fakeRunner{output: output})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}
	if got := table.Name(4200); got != "/bin/zsh" {
		t.Fatalf("Name(4200) = %q, want %q", got, "/bin/zsh")
	}
}

func TestPsProcessTableHandlesDuplicatePIDs(t *testing.T) {
	output := "  4200  4150 /bin/zsh\n  4200  4150 /bin/fish\n"

	table, err := NewPsProcessTable(&fakeRunner{output: output})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}
	if len(table.entries) != 1 {
		t.Fatalf("parsed %d entries, want the last one to win", len(table.entries))
	}
	if got := table.Name(4200); got != "/bin/fish" {
		t.Fatalf("Name(4200) = %q, want the last reported command", got)
	}
}

func TestPsProcessTableTreeWalksToTheShell(t *testing.T) {
	output := "    1     0 /sbin/launchd\n  4150  1 login\n  4200  4150 /bin/zsh\n  4242  4200 wut\n"
	table, err := NewPsProcessTable(&fakeRunner{output: output})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}

	runner := &fakeRunner{output: "user@host:~$ "}
	shell := DetectShellFromProcessTree(4242, runner, table.Tree())
	if shell.Name != "zsh" {
		t.Fatalf("DetectShellFromProcessTree() = %+v, want zsh", shell)
	}
	if shell.Path != "/bin/zsh" || shell.Prompt != "user@host:~$" {
		t.Fatalf("DetectShellFromProcessTree() = %+v, want the ps path and prompt", shell)
	}
}

func TestPsProcessTableTreeWithoutAShell(t *testing.T) {
	output := "    1     0 /sbin/launchd\n  4242  1 /usr/libexec/sshd\n"
	table, err := NewPsProcessTable(&fakeRunner{output: output})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}

	runner := &fakeRunner{output: "$ "}
	if shell := DetectShellFromProcessTree(4242, runner, table.Tree()); shell.Name != "" {
		t.Fatalf("DetectShellFromProcessTree() = %+v, want no shell", shell)
	}
}

func TestPsProcessTableTreeHandlesUnknownStartPID(t *testing.T) {
	table, err := NewPsProcessTable(&fakeRunner{output: linuxPsOutput})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}

	runner := &fakeRunner{output: "$ "}
	if shell := DetectShellFromProcessTree(9999, runner, table.Tree()); shell.Name != "" {
		t.Fatalf("DetectShellFromProcessTree() = %+v, want no shell", shell)
	}
	if runner.calls != 0 {
		t.Fatalf("DetectShellFromProcessTree() ran %d commands, want 0", runner.calls)
	}
}

func TestPsProcessTableTreeImplementsProcessTree(t *testing.T) {
	table, err := NewPsProcessTable(&fakeRunner{output: linuxPsOutput})
	if err != nil {
		t.Fatalf("NewPsProcessTable() error = %v", err)
	}
	var _ ProcessTree = table.Tree()
}
