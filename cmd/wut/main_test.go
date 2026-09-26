package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffficult/wut/internal/terminal"
)

const samplePane = "user@host:~$ ls -l\ntotal 0\nuser@host:~$ cat foo\nno such file\nuser@host:~$ wut\n"

type harness struct {
	app             *app
	stdout          *bytes.Buffer
	stderr          *bytes.Buffer
	gotMultiplexer  []string
	gotContext      string
	gotQuery        string
	capturedContext bool
}

func newHarness(t *testing.T, env map[string]string, pane string, captureErr error, explainErr error) *harness {
	t.Helper()

	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	h.app = &app{
		shell:  func() terminal.Shell { return terminal.Shell{Path: "/bin/bash", Name: "bash", Prompt: "$ "} },
		getenv: func(key string) string { return env[key] },
		capture: func(multiplexer string) (string, error) {
			h.gotMultiplexer = append(h.gotMultiplexer, multiplexer)
			return pane, captureErr
		},
		explain: func(context string, query string) (string, error) {
			h.capturedContext = true
			h.gotContext = context
			h.gotQuery = query
			if explainErr != nil {
				return "", explainErr
			}
			return "the answer", nil
		},
		stdout: h.stdout,
		stderr: h.stderr,
	}
	return h
}

func tmuxEnv() map[string]string {
	return map[string]string{"TMUX": "/tmp/tmux-1000/default,1234,0"}
}

func TestRunOutsideMultiplexer(t *testing.T) {
	h := newHarness(t, map[string]string{}, samplePane, nil, nil)

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if len(h.gotMultiplexer) != 0 {
		t.Fatalf("capture was invoked %v, want no capture outside a multiplexer", h.gotMultiplexer)
	}
	if !strings.Contains(h.stderr.String(), "must be run inside a tmux or screen session") {
		t.Fatalf("stderr = %q, want the tmux/screen requirement", h.stderr.String())
	}
}

func TestRunEmptyEnvironmentValuesCountAsUnset(t *testing.T) {
	h := newHarness(t, map[string]string{"TMUX": "", "STY": ""}, samplePane, nil, nil)

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1 for empty TMUX/STY", code)
	}
}

func TestRunInsideTmuxSucceeds(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}
	if strings.Join(h.gotMultiplexer, ",") != "tmux" {
		t.Fatalf("capture saw %v, want [tmux]", h.gotMultiplexer)
	}
	if !strings.Contains(h.stdout.String(), "the answer") {
		t.Fatalf("stdout = %q, want the provider response", h.stdout.String())
	}
}

func TestRunInsideScreenUsesScreenCapture(t *testing.T) {
	h := newHarness(t, map[string]string{"STY": "1234.pts-0.host"}, samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}
	if strings.Join(h.gotMultiplexer, ",") != "screen" {
		t.Fatalf("capture saw %v, want [screen]", h.gotMultiplexer)
	}
}

func TestRunPrefersTmuxOverScreen(t *testing.T) {
	env := tmuxEnv()
	env["STY"] = "1234.pts-0.host"
	h := newHarness(t, env, samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if strings.Join(h.gotMultiplexer, ",") != "tmux" {
		t.Fatalf("capture saw %v, want [tmux]", h.gotMultiplexer)
	}
}

func TestRunBuildsPromptAwareContextWithoutTheWutInvocation(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if !h.capturedContext {
		t.Fatal("run() did not ask the provider for an answer")
	}
	if !strings.Contains(h.gotContext, "<last_command>") || !strings.Contains(h.gotContext, "$  cat foo") {
		t.Fatalf("context = %q, want the last command with the prompt", h.gotContext)
	}
	if strings.Contains(h.gotContext, "wut") {
		t.Fatalf("context = %q, must not include the current wut invocation", h.gotContext)
	}
}

func TestRunForwardsQuery(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)

	if code := h.app.run([]string{"--query", "why did that fail?"}); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if h.gotQuery != "why did that fail?" {
		t.Fatalf("query = %q, want %q", h.gotQuery, "why did that fail?")
	}
}

func TestRunDefaultsToEmptyQuery(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if h.gotQuery != "" {
		t.Fatalf("query = %q, want empty", h.gotQuery)
	}
}

func TestRunCaptureErrorExitsWithContext(t *testing.T) {
	h := newHarness(t, tmuxEnv(), "", terminal.ErrNoPaneOutput, nil)

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "capturing tmux pane") {
		t.Fatalf("stderr = %q, want the capture context", h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), terminal.ErrNoPaneOutput.Error()) {
		t.Fatalf("stderr = %q, want the underlying cause", h.stderr.String())
	}
	if h.capturedContext {
		t.Fatal("run() called the provider after a failed capture")
	}
}

func TestRunEmptyCaptureIsReportedAsFailure(t *testing.T) {
	h := newHarness(t, tmuxEnv(), "", nil, nil)

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1 for an empty capture", code)
	}
	if h.capturedContext {
		t.Fatal("run() called the provider with no captured output")
	}
	if !strings.Contains(h.stderr.String(), "no output") {
		t.Fatalf("stderr = %q, want a no-output message", h.stderr.String())
	}
}

func TestRunWhitespaceOnlyCaptureIsReportedAsFailure(t *testing.T) {
	h := newHarness(t, tmuxEnv(), "\n \n\t\n", nil, nil)

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1 for a whitespace-only capture", code)
	}
	if h.capturedContext {
		t.Fatal("run() called the provider with a whitespace-only capture")
	}
}

func TestRunExplainErrorExitsWithMessage(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, errors.New("no provider configured"))

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "no provider configured") {
		t.Fatalf("stderr = %q, want the provider error", h.stderr.String())
	}
	if strings.Contains(h.stdout.String(), "the answer") {
		t.Fatalf("stdout = %q, want no response on failure", h.stdout.String())
	}
}

func TestRunDebugPrintsContext(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)

	if code := h.app.run([]string{"--debug"}); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	out := h.stdout.String()
	for _, want := range []string{"wut | ", "Retrieved shell information", "Retrieved terminal context", "Sending request to LLM"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want it to contain %q", out, want)
		}
	}
}

func TestRunWithoutDebugStaysQuiet(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if strings.Contains(h.stdout.String(), "wut |") {
		t.Fatalf("stdout = %q, want no debug output without --debug", h.stdout.String())
	}
}

func TestRunHelpPrintsUsageAndExitsZero(t *testing.T) {
	h := newHarness(t, map[string]string{}, samplePane, nil, nil)

	if code := h.app.run([]string{"--help"}); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if !strings.Contains(h.stdout.String(), "Usage:") {
		t.Fatalf("stdout = %q, want usage", h.stdout.String())
	}
	if len(h.gotMultiplexer) != 0 {
		t.Fatalf("--help captured the pane: %v", h.gotMultiplexer)
	}
}

func TestRunUnknownArgumentExitsTwo(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)

	code := h.app.run([]string{"--nope"})
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if !strings.Contains(h.stderr.String(), `unknown argument "--nope"`) {
		t.Fatalf("stderr = %q, want the unknown argument", h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q, want usage after the error", h.stderr.String())
	}
	if h.capturedContext {
		t.Fatal("run() continued after a usage error")
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    options
		wantErr string
	}{
		{name: "no arguments", args: nil, want: options{}},
		{name: "query separate", args: []string{"--query", "why?"}, want: options{query: "why?"}},
		{name: "query equals", args: []string{"--query=why?"}, want: options{query: "why?"}},
		{name: "query empty", args: []string{"--query="}, want: options{query: ""}},
		{name: "debug", args: []string{"--debug"}, want: options{debug: true}},
		{name: "query and debug", args: []string{"--query", "why?", "--debug"}, want: options{query: "why?", debug: true}},
		{name: "help", args: []string{"--help"}, want: options{help: true}},
		{name: "query without value", args: []string{"--query"}, wantErr: "--query requires a value"},
		{name: "unknown long flag", args: []string{"--verbose"}, wantErr: `unknown argument "--verbose"`},
		{name: "undocumented short query", args: []string{"-q", "why?"}, wantErr: `unknown argument "-q"`},
		{name: "undocumented short debug", args: []string{"-d"}, wantErr: `unknown argument "-d"`},
		{name: "undocumented short help", args: []string{"-h"}, wantErr: `unknown argument "-h"`},
		{name: "positional argument", args: []string{"why?"}, wantErr: `unknown argument "why?"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseArgs(%q) error = nil, want %q", tt.args, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseArgs(%q) error = %v, want it to contain %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseArgs(%q) error = %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("parseArgs(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestUsageDocumentsOnlyLongFlags(t *testing.T) {
	for _, flag := range []string{"--query", "--debug", "--help"} {
		if !strings.Contains(usage, flag) {
			t.Fatalf("usage does not document %s: %s", flag, usage)
		}
	}

	for _, line := range strings.Split(usage, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		if strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "--") {
			t.Fatalf("usage documents an undocumented short flag %q", trimmed)
		}
	}
}

func TestUsageMentionsTheMultiplexerRequirement(t *testing.T) {
	if !strings.Contains(usage, "tmux") || !strings.Contains(usage, "screen") {
		t.Fatalf("usage does not mention the tmux/screen requirement: %s", usage)
	}
}

type recordingRunner struct {
	terminal.Runner
	outPath string
	content string
}

func (r *recordingRunner) Output(string, []string) (string, error) { return "$ ", nil }

func (r *recordingRunner) RunToFile(_ string, _ []string, outPath string) error {
	r.outPath = outPath
	return os.WriteFile(outPath, []byte(r.content), 0o600)
}

func TestCapturePaneReturnsCapturedContent(t *testing.T) {
	runner := &recordingRunner{content: samplePane}
	pane, err := capturePane("tmux", runner)
	if err != nil {
		t.Fatalf("capturePane() error = %v", err)
	}
	if pane != samplePane {
		t.Fatalf("capturePane() = %q, want the captured pane", pane)
	}
}

func TestCapturePaneRemovesTheTemporaryFile(t *testing.T) {
	runner := &recordingRunner{content: samplePane}
	if _, err := capturePane("tmux", runner); err != nil {
		t.Fatalf("capturePane() error = %v", err)
	}
	if runner.outPath == "" {
		t.Fatal("capturePane() did not pass a temporary path to the runner")
	}
	if _, err := os.Stat(runner.outPath); !os.IsNotExist(err) {
		t.Fatalf("temporary capture file %q still exists", runner.outPath)
	}
}

// GNU screen resolves a relative hardcopy path against its own working
// directory, so the directory handed to it must be absolute.
func TestCaptureDirIsAbsoluteWithRelativeTempDir(t *testing.T) {
	t.Setenv("TMPDIR", "relative-tmp-dir")

	dir, err := captureDir()
	if err != nil {
		t.Fatalf("captureDir() error = %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("captureDir() = %q, want an absolute path", dir)
	}
}

func TestCaptureDirIsAbsoluteByDefault(t *testing.T) {
	dir, err := captureDir()
	if err != nil {
		t.Fatalf("captureDir() error = %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("captureDir() = %q, want an absolute path", dir)
	}
}

func TestCapturePaneUsesAnAbsoluteTemporaryPath(t *testing.T) {
	runner := &recordingRunner{content: samplePane}
	if _, err := capturePane("screen", runner); err != nil {
		t.Fatalf("capturePane() error = %v", err)
	}
	if !filepath.IsAbs(runner.outPath) {
		t.Fatalf("capture path %q is not absolute", runner.outPath)
	}
}

func TestCapturePaneReportsTempFileFailure(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))

	runner := &recordingRunner{content: samplePane}
	_, err := capturePane("tmux", runner)
	if err == nil {
		t.Fatal("capturePane() error = nil, want a temp file failure")
	}
	if !strings.Contains(err.Error(), "capture file") {
		t.Fatalf("capturePane() error = %v, want it to mention the capture file", err)
	}
}

func TestCapturePanePropagatesRunnerFailure(t *testing.T) {
	_, err := capturePane("tmux", failingRunner{})
	if err == nil {
		t.Fatal("capturePane() error = nil, want the runner failure")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("capturePane() error = %v, want it to wrap the runner failure", err)
	}
}

type failingRunner struct{}

func (failingRunner) Output(string, []string) (string, error) { return "", nil }

func (failingRunner) RunToFile(string, []string, string) error { return errors.New("boom") }

// The shell walk must work on any host with ps, not only on Linux /proc.
func TestDetectShellUsesPsBackedProcessTree(t *testing.T) {
	psOutput := "    1     0 /sbin/launchd\n  4200  1 /bin/zsh\n  4242  4200 wut\n"
	runner := &psRunner{output: psOutput, prompt: "user@host:~$ "}

	shell := detectShellFrom(func(key string) string {
		if key == "SHELL" {
			return "/bin/sh" // Unknown shell: name and prompt come from ps.
		}
		return ""
	}, runner, 4242)

	if shell.Name != "zsh" {
		t.Fatalf("detectShell() = %+v, want zsh discovered through ps", shell)
	}
	if shell.Path != "/bin/sh" {
		t.Fatalf("detectShell() path = %q, want the environment path preserved", shell.Path)
	}
	if shell.Prompt != "user@host:~$" {
		t.Fatalf("detectShell() prompt = %q, want the prompt from the ps-discovered shell", shell.Prompt)
	}
}

func TestDetectShellPrefersEnvironmentShell(t *testing.T) {
	psOutput := "  4200  1 /bin/zsh\n  4242  4200 wut\n"
	runner := &psRunner{output: psOutput, prompt: "$ "}

	shell := detectShellFrom(func(key string) string {
		if key == "SHELL" {
			return "/bin/fish"
		}
		return ""
	}, runner, 4242)

	if shell.Name != "fish" || shell.Path != "/bin/fish" {
		t.Fatalf("detectShell() = %+v, want fish from the environment", shell)
	}
	if len(runner.psCalls) != 0 {
		t.Fatalf("detectShell() ran ps %d times for a known shell, want 0", len(runner.psCalls))
	}
}

func TestDetectShellDegradesWhenPsIsUnavailable(t *testing.T) {
	runner := &psRunner{psErr: errors.New("ps: command not found"), prompt: "$ "}

	shell := detectShellFrom(func(key string) string {
		if key == "SHELL" {
			return "/bin/sh"
		}
		return ""
	}, runner, 4242)

	if shell.Path != "/bin/sh" || shell.Name != "" || shell.Prompt != "" {
		t.Fatalf("detectShell() = %+v, want the environment path with no shell name", shell)
	}
}

func TestDetectShellWithoutEnvironmentOrPs(t *testing.T) {
	runner := &psRunner{psErr: errors.New("ps: command not found")}

	shell := detectShellFrom(func(string) string { return "" }, runner, 4242)
	if shell.Name != "" || shell.Prompt != "" {
		t.Fatalf("detectShell() = %+v, want an empty shell", shell)
	}
}

// psRunner answers ps invocations and prompt probes separately.
type psRunner struct {
	output   string
	psErr    error
	prompt   string
	psCalls  []string
	otherErr error
}

func (r *psRunner) Output(name string, args []string) (string, error) {
	if name == "ps" {
		r.psCalls = append(r.psCalls, strings.Join(args, " "))
		if r.psErr != nil {
			return "", r.psErr
		}
		return r.output, nil
	}
	if r.otherErr != nil {
		return "", r.otherErr
	}
	return r.prompt, nil
}

func (r *psRunner) RunToFile(string, []string, string) error { return errors.New("unused") }
