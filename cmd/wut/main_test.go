package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffficult/wut/internal/config"
	"github.com/diffficult/wut/internal/terminal"
)

const samplePane = "user@host:~$ ls -l\ntotal 0\nuser@host:~$ cat foo\nno such file\nuser@host:~$ wut\n"

type harness struct {
	app             *app
	stdout          *bytes.Buffer
	stderr          *bytes.Buffer
	gotSources      [][]string
	gotSource       string
	gotContext      string
	gotQuery        string
	gotConfig       *config.Config
	capturedContext bool
	response        string
}

// validConfig is a configuration with one usable provider, which is what the
// CLI requires before it captures anything.
func validConfig(t *testing.T) *config.Config {
	return config.Load(func(key string) string {
		if key == "OPENAI_API_KEY" {
			return "k"
		}
		return ""
	}, config.WithPath(filepath.Join(t.TempDir(), "absent")))
}

func newHarness(t *testing.T, env map[string]string, pane string, captureErr error, explainErr error) *harness {
	t.Helper()

	cfg := validConfig(t)
	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, response: "the answer"}
	h.app = &app{
		shell:      func() terminal.Shell { return terminal.Shell{Path: "/bin/bash", Name: "bash", Prompt: "$ "} },
		getenv:     func(key string) string { return env[key] },
		loadConfig: func() *config.Config { return cfg },
		capture: func(sources []string) (string, string, error) {
			h.gotSources = append(h.gotSources, sources)
			source := ""
			if len(sources) > 0 {
				source = sources[0]
			}
			h.gotSource = source
			return source, pane, captureErr
		},
		explain: func(cfg *config.Config, context string, query string) (string, error) {
			h.capturedContext = true
			h.gotConfig = cfg
			h.gotContext = context
			h.gotQuery = query
			if explainErr != nil {
				return "", explainErr
			}
			return h.response, nil
		},
		stdout: h.stdout,
		stderr: h.stderr,
	}
	return h
}

func tmuxEnv() map[string]string {
	return map[string]string{"TMUX": "/tmp/tmux-1000/default,1234,0"}
}

// The configuration is validated before the pane is captured, so a missing
// provider never disturbs the terminal.
func TestRunReportsInvalidConfigBeforeCapturingThePane(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	h.app.loadConfig = func() *config.Config {
		return config.Load(func(string) string { return "" }, config.WithPath(filepath.Join(t.TempDir(), "absent")))
	}

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if len(h.gotSources) != 0 {
		t.Fatalf("run() captured %v before validating the config", h.gotSources)
	}
	if h.capturedContext {
		t.Fatal("run() called the provider without a configured provider")
	}
	errOutput := h.stderr.String()
	for _, want := range []string{"No valid LLM provider configuration found", "~/.config/wut/config", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OLLAMA_MODEL"} {
		if !strings.Contains(errOutput, want) {
			t.Fatalf("stderr = %q, want it to mention %q", errOutput, want)
		}
	}
}

func TestRunForwardsTheLoadedConfigToTheProvider(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	cfg := validConfig(t)
	h.app.loadConfig = func() *config.Config { return cfg }

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}
	if h.gotConfig != cfg {
		t.Fatal("run() did not hand the loaded configuration to the provider")
	}
}

func TestRunAcceptsEnvironmentOnlyConfiguration(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	h.app.loadConfig = func() *config.Config {
		return config.Load(func(key string) string {
			if key == "ANTHROPIC_API_KEY" {
				return "k"
			}
			return ""
		}, config.WithPath(filepath.Join(t.TempDir(), "absent")))
	}

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}
	if h.gotConfig.ActiveProvider() != config.ProviderAnthropic {
		t.Fatalf("provider = %q, want anthropic from the environment", h.gotConfig.ActiveProvider())
	}
}

func TestRunReportsAMalformedConfigFileInDebugMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("not an ini file\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	h.app.loadConfig = func() *config.Config {
		return config.Load(func(key string) string {
			if key == "OPENAI_API_KEY" {
				return "k"
			}
			return ""
		}, config.WithPath(path))
	}

	if code := h.app.run([]string{"--debug"}); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Configuration file problem") {
		t.Fatalf("stdout = %q, want the config failure reported in debug mode", h.stdout.String())
	}
}

func TestExplainUsesTheConfiguredProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("[ollama]\nmodel = llama3\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")

	cfg := config.Load(func(string) string { return "" }, config.WithPath(path))

	if _, err := explain(cfg, "<terminal_history>x</terminal_history>", ""); err == nil {
		t.Fatal("explain() error = nil, want the unreachable provider to be reported")
	}
}

func TestRunOutsideMultiplexer(t *testing.T) {
	h := newHarness(t, map[string]string{}, samplePane, nil, nil)

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if len(h.gotSources) != 0 {
		t.Fatalf("capture was invoked %v, want no capture outside a multiplexer or kitty", h.gotSources)
	}
	if !strings.Contains(h.stderr.String(), "must be run inside a tmux or screen session") {
		t.Fatalf("stderr = %q, want the tmux/screen requirement", h.stderr.String())
	}
}

func TestRunEmptyEnvironmentValuesCountAsUnset(t *testing.T) {
	h := newHarness(t, map[string]string{"TMUX": "", "STY": "", "KITTY_WINDOW_ID": ""}, samplePane, nil, nil)

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1 for empty TMUX/STY/KITTY_WINDOW_ID", code)
	}
}

func TestRunInsideTmuxSucceeds(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}
	if got := strings.Join(h.gotSources[0], ","); got != "tmux" {
		t.Fatalf("capture saw %v, want [tmux]", h.gotSources[0])
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
	if got := strings.Join(h.gotSources[0], ","); got != "screen" {
		t.Fatalf("capture saw %v, want [screen]", h.gotSources[0])
	}
}

func TestRunPrefersTmuxOverScreen(t *testing.T) {
	env := tmuxEnv()
	env["STY"] = "1234.pts-0.host"
	h := newHarness(t, env, samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if got := strings.Join(h.gotSources[0], ","); got != "tmux,screen" {
		t.Fatalf("capture candidates = %q, want [tmux screen] with tmux first", got)
	}
	if h.gotSource != "tmux" {
		t.Fatalf("selected source = %q, want tmux", h.gotSource)
	}
}

// Kitty is the third candidate: with no multiplexer present, the window id
// alone makes it a usable source.
func TestRunInsideKittyUsesTheKittyCapture(t *testing.T) {
	h := newHarness(t, map[string]string{"KITTY_WINDOW_ID": "7"}, samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}
	if got := strings.Join(h.gotSources[0], ","); got != "kitty" {
		t.Fatalf("capture candidates = %q, want [kitty]", got)
	}
	if h.gotSource != "kitty" {
		t.Fatalf("selected source = %q, want kitty", h.gotSource)
	}
	if !h.capturedContext {
		t.Fatal("run() did not ask the provider for an answer")
	}
	// Kitty returns plain text, so the same prompt-aware parser applies.
	if !strings.Contains(h.gotContext, "<last_command>") || !strings.Contains(h.gotContext, "$  cat foo") {
		t.Fatalf("context = %q, want the last command parsed from the kitty text", h.gotContext)
	}
	if strings.Contains(h.gotContext, "wut") {
		t.Fatalf("context = %q, must not include the current wut invocation", h.gotContext)
	}
}

// tmux still wins inside a kitty-in-tmux session; the later candidates stay in
// the list only as fallbacks.
func TestRunPrefersTmuxAndKeepsKittyAsLastCandidate(t *testing.T) {
	env := tmuxEnv()
	env["STY"] = "1234.pts-0.host"
	env["KITTY_WINDOW_ID"] = "7"
	h := newHarness(t, env, samplePane, nil, nil)

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if got := strings.Join(h.gotSources[0], ","); got != "tmux,screen,kitty" {
		t.Fatalf("capture candidates = %q, want [tmux screen kitty]", got)
	}
	if h.gotSource != "tmux" {
		t.Fatalf("selected source = %q, want tmux", h.gotSource)
	}
}

func TestRunDebugReportsTheSelectedCaptureSource(t *testing.T) {
	h := newHarness(t, map[string]string{"STY": "1234.pts-0.host"}, samplePane, nil, nil)

	if code := h.app.run([]string{"--debug"}); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Captured terminal context from screen") {
		t.Fatalf("stdout = %q, want the selected capture source reported", h.stdout.String())
	}
}

func TestRunKittyCaptureFailureExplainsTheSetup(t *testing.T) {
	h := newHarness(t, map[string]string{"KITTY_WINDOW_ID": "7"}, "", terminal.ErrKittyRemoteControl, nil)

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if h.capturedContext {
		t.Fatal("run() called the provider after a failed kitty capture")
	}
	if !strings.Contains(h.stderr.String(), "kitty") {
		t.Fatalf("stderr = %q, want the capture source named", h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), terminal.KittySetupDocsURL) {
		t.Fatalf("stderr = %q, want a link to the kitty setup docs", h.stderr.String())
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
	if !strings.Contains(h.stderr.String(), "capturing the terminal from tmux") {
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
	if len(h.gotSources) != 0 {
		t.Fatalf("--help captured the pane: %v", h.gotSources)
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

// The provider answers in Markdown, and the terminal is the only place the
// user sees it: the answer must arrive translated, not as raw markup.
func TestRunRendersTheMarkdownAnswer(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	h.response = "**Warning:** git has no `create-pr` command.\n\nRun this:\n\n```sh\ngit push -u origin my-branch\n```\n"

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, h.stderr.String())
	}

	out := h.stdout.String()
	for _, want := range []string{"Warning:", "git has no create-pr command.", "Run this:", "git push -u origin my-branch"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want it to contain %q", out, want)
		}
	}
	for _, unwanted := range []string{"**", "```"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("stdout = %q, want the %q markup translated away", out, unwanted)
		}
	}
}

// A provider that answers with nothing is not a failure.
func TestRunEmptyAnswerSucceedsAndPrintsNothing(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	h.response = ""

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0 for an empty answer", code)
	}
	if h.stdout.String() != "" {
		t.Fatalf("stdout = %q, want nothing printed for an empty answer", h.stdout.String())
	}
}

// The rendered answer is written without escape sequences when stdout is not a
// terminal, so piping or redirecting wut stays readable.
func TestRunPipesPlainTextWhenStdoutIsNotATerminal(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	h.response = "**bold** answer\n"

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if strings.Contains(h.stdout.String(), "\x1b[") {
		t.Fatalf("stdout = %q, want no ANSI styling for a non-terminal stdout", h.stdout.String())
	}
}

// A rendering failure must never cost the user the answer: the raw response is
// printed instead of exiting with an error.
func TestRunFallsBackToTheRawAnswerWhenRenderingFails(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	h.response = "**bold** answer\n"
	failing := &failOnceWriter{w: h.stdout}
	h.app.stdout = failing

	if code := h.app.run(nil); code != 0 {
		t.Fatalf("run() = %d, want 0 when rendering fails", code)
	}
	if !strings.Contains(h.stdout.String(), "**bold** answer") {
		t.Fatalf("stdout = %q, want the raw provider answer", h.stdout.String())
	}
	if !failing.failed {
		t.Fatal("stdout never received the rendered answer")
	}
}

// failOnceWriter rejects the first write and then behaves normally, which is
// what a broken pipe or a closed terminal looks like mid-render.
type failOnceWriter struct {
	w      *bytes.Buffer
	failed bool
}

func (f *failOnceWriter) Write(p []byte) (int, error) {
	if !f.failed {
		f.failed = true
		return 0, errors.New("write failed")
	}
	return f.w.Write(p)
}

// The shipped sample configuration is documentation the user copies verbatim,
// so it must load through the real loader and produce a usable provider. The
// test lives here because the sample is a user-facing artifact of the command,
// next to the usage text it documents.
func TestConfigExampleLoadsAndSelectsAProvider(t *testing.T) {
	path := filepath.Join("..", "..", "config.example")

	cfg := config.Load(func(string) string { return "" }, config.WithPath(path))
	if cfg.Err() != nil {
		t.Fatalf("loading %s: %v", path, cfg.Err())
	}
	if !cfg.HasValidConfig() {
		t.Fatalf("loading %s: no usable provider", path)
	}
	if provider := cfg.ActiveProvider(); provider != config.ProviderOpenAI {
		t.Fatalf("loading %s: provider = %q, want openai from the sample keys", path, provider)
	}

	providers := cfg.Providers()
	if providers.OpenAI.Model != config.DefaultOpenAIModel {
		t.Fatalf("openai model = %q, want the documented default %q", providers.OpenAI.Model, config.DefaultOpenAIModel)
	}
	if providers.Anthropic.Model != config.DefaultAnthropicModel {
		t.Fatalf("anthropic model = %q, want the documented default %q", providers.Anthropic.Model, config.DefaultAnthropicModel)
	}
	if providers.OpenAI.BaseURL != "" {
		t.Fatalf("openai base_url = %q, want it left commented out in the sample", providers.OpenAI.BaseURL)
	}
}

// If the answer cannot be written at all, the run failed: reporting success
// would hide that the user got nothing.
func TestRunFailsWhenTheAnswerCannotBeWritten(t *testing.T) {
	h := newHarness(t, tmuxEnv(), samplePane, nil, nil)
	h.response = "**bold** answer\n"
	h.app.stdout = failingStdoutWriter{}

	if code := h.app.run(nil); code != 1 {
		t.Fatalf("run() = %d, want 1 when the answer cannot be written", code)
	}
	errOutput := h.stderr.String()
	if !strings.Contains(errOutput, "writing the answer failed") {
		t.Fatalf("stderr = %q, want the write failure reported", errOutput)
	}
	if !strings.Contains(errOutput, "stdout closed") {
		t.Fatalf("stderr = %q, want the underlying cause", errOutput)
	}
}

// failingStdoutWriter rejects every write, the way a closed terminal or a
// broken pipe does.
type failingStdoutWriter struct{}

func (failingStdoutWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

// The sample configuration is copied verbatim by users, and the INI parser
// keeps everything after the first "=" or ":" as the value: an inline comment
// would silently become part of an API endpoint or a model name.
func TestConfigExampleAvoidsInlineComments(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "config.example"))
	if err != nil {
		t.Fatalf("reading config.example: %v", err)
	}

	for number, raw := range strings.Split(string(contents), "\n") {
		delimiter := strings.IndexAny(raw, "=:")
		if delimiter < 0 {
			continue
		}
		if value := raw[delimiter+1:]; strings.ContainsAny(value, "#;") {
			t.Fatalf("config.example line %d = %q, want a whole-line comment instead of an inline one", number+1, raw)
		}
	}
}

// The documented build output is the plain `wut` binary in the repository root:
// the legacy Python package directory is gone, so `go build -o wut ./cmd/wut` is
// the correct instruction and no pip/pipx install path may come back.
func TestReadmeDocumentsAWorkingGoInstall(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	readme := string(contents)

	for _, want := range []string{"go build -o wut ./cmd/wut", "./wut --help", "go install ./cmd/wut", "tmux", "screen", "--query", "KITTY_WINDOW_ID", "kitten", "get-text", "allow_remote_control"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README.md does not document %q", want)
		}
	}
	for _, unwanted := range []string{"pipx", "pip install", "setup.py", "Pipfile", "wut-go", "Python package directory", `wut "how do i`} {
		if strings.Contains(readme, unwanted) {
			t.Fatalf("README.md still contains %q", unwanted)
		}
	}
}

func TestReadmeNeverSuggestsEnablingKittyRemoteControlWholesale(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}

	for number, raw := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(raw)
		if !strings.HasPrefix(trimmed, "allow_remote_control") {
			continue
		}
		if strings.HasPrefix(trimmed, "allow_remote_control no") {
			continue // Documenting the safe default is required.
		}
		t.Fatalf("README.md line %d = %q, want the narrow permission_only guidance, not a broad allow_remote_control yes", number+1, raw)
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
	if !strings.Contains(strings.ToLower(usage), "kitty") {
		t.Fatalf("usage does not mention the kitty fallback: %s", usage)
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
	source, pane, err := capturePane([]string{terminal.MultiplexerTmux}, emptyEnv, runner)
	if err != nil {
		t.Fatalf("capturePane() error = %v", err)
	}
	if source != terminal.MultiplexerTmux {
		t.Fatalf("capturePane() source = %q, want tmux", source)
	}
	if pane != samplePane {
		t.Fatalf("capturePane() = %q, want the captured pane", pane)
	}
}

func TestCapturePaneRemovesTheTemporaryFile(t *testing.T) {
	runner := &recordingRunner{content: samplePane}
	if _, _, err := capturePane([]string{terminal.MultiplexerTmux}, emptyEnv, runner); err != nil {
		t.Fatalf("capturePane() error = %v", err)
	}
	if runner.outPath == "" {
		t.Fatal("capturePane() did not pass a temporary path to the runner")
	}
	if _, err := os.Stat(runner.outPath); !os.IsNotExist(err) {
		t.Fatalf("temporary capture file %q still exists", runner.outPath)
	}
}

// The kitty answer can hold the whole scrollback, so nothing unbounded is
// written to disk: the fallback needs no capture file at all.
func TestCapturePaneWithoutAMultiplexerUsesNoTemporaryFile(t *testing.T) {
	runner := &kittyRecordingRunner{text: samplePane}

	source, pane, err := capturePane([]string{terminal.MultiplexerKitty}, func(key string) string {
		if key == terminal.KittyWindowIDEnv {
			return "7"
		}
		return ""
	}, runner)
	if err != nil {
		t.Fatalf("capturePane() error = %v", err)
	}
	if source != terminal.MultiplexerKitty {
		t.Fatalf("capturePane() source = %q, want kitty", source)
	}
	if pane != samplePane {
		t.Fatalf("capturePane() = %q, want the kitty text", pane)
	}
	if runner.toFile != 0 {
		t.Fatalf("capturePane() wrote %d capture files for kitty, want none", runner.toFile)
	}
}

// A stale TMUX must not end the run when kitty is available.
func TestCapturePaneFallsThroughToKittyWhenTmuxFails(t *testing.T) {
	runner := &failingRunnerKitty{}

	source, pane, err := capturePane(
		[]string{terminal.MultiplexerTmux, terminal.MultiplexerKitty},
		func(key string) string {
			if key == terminal.KittyWindowIDEnv {
				return "7"
			}
			return "x"
		},
		runner,
	)
	if err != nil {
		t.Fatalf("capturePane() error = %v", err)
	}
	if source != terminal.MultiplexerKitty {
		t.Fatalf("capturePane() source = %q, want kitty after a failed tmux capture", source)
	}
	if pane != "kitty text\n" {
		t.Fatalf("capturePane() = %q, want the kitty text", pane)
	}
}

// emptyEnv is an environment that names no capture source.
func emptyEnv(string) string { return "" }

// kittyRecordingRunner answers kitten with text and refuses any file write, so a
// capture file for the kitty fallback shows up as a failure.
type kittyRecordingRunner struct {
	text   string
	toFile int
}

func (r *kittyRecordingRunner) Output(string, []string) (string, error) { return r.text, nil }

func (r *kittyRecordingRunner) RunToFile(string, []string, string) error {
	r.toFile++
	return errors.New("no capture file expected")
}

// failingRunnerKitty fails the multiplexer hardcopy and answers kitten.
type failingRunnerKitty struct{}

func (failingRunnerKitty) Output(string, []string) (string, error) { return "kitty text\n", nil }

func (failingRunnerKitty) RunToFile(string, []string, string) error {
	return errors.New("no server running")
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
	if _, _, err := capturePane([]string{terminal.MultiplexerScreen}, emptyEnv, runner); err != nil {
		t.Fatalf("capturePane() error = %v", err)
	}
	if !filepath.IsAbs(runner.outPath) {
		t.Fatalf("capture path %q is not absolute", runner.outPath)
	}
}

func TestCapturePaneReportsTempFileFailure(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))

	runner := &recordingRunner{content: samplePane}
	_, _, err := capturePane([]string{terminal.MultiplexerTmux}, emptyEnv, runner)
	if err == nil {
		t.Fatal("capturePane() error = nil, want a temp file failure")
	}
	if !strings.Contains(err.Error(), "capture file") {
		t.Fatalf("capturePane() error = %v, want it to mention the capture file", err)
	}
}

func TestCapturePanePropagatesRunnerFailure(t *testing.T) {
	_, _, err := capturePane([]string{terminal.MultiplexerTmux}, emptyEnv, failingRunner{})
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
