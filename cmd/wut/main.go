// Command wut explains the output of the latest terminal command by capturing
// the active tmux or GNU screen pane, falling back to the current Kitty window.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/diffficult/wut/internal/config"
	"github.com/diffficult/wut/internal/llm"
	"github.com/diffficult/wut/internal/render"
	"github.com/diffficult/wut/internal/terminal"
)

// Exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = `wut - understand the output of your latest terminal command.

Usage:
  wut [--query <question>] [--debug]
  wut --help

Flags:
  --query <question>  Ask a specific question about what's on your terminal.
  --debug             Print debug information.
  --help              Print this help.

wut must be run inside a tmux or screen session, or in a Kitty window.
`

const configHelp = `No valid LLM provider configuration found.
Please either:
  1. Create ~/.config/wut/config with your API keys and models, or
  2. Set environment variables (OPENAI_API_KEY, ANTHROPIC_API_KEY, or OLLAMA_MODEL)`

// options holds the parsed command-line flags.
type options struct {
	query string
	debug bool
	help  bool
}

// app wires the CLI to its environment so every step can be exercised without
// a live tmux/screen session or provider credentials.
type app struct {
	getenv     func(string) string
	loadConfig func() *config.Config
	// capture walks the available sources in preference order and returns the
	// source it used along with its text.
	capture func(sources []string) (string, string, error)
	shell   func() terminal.Shell
	explain func(cfg *config.Config, context string, query string) (string, error)
	stdout  io.Writer
	stderr  io.Writer
}

func main() {
	runner := terminal.ExecRunner{Stderr: os.Stderr}

	a := &app{
		getenv:     os.Getenv,
		loadConfig: func() *config.Config { return config.Load(os.Getenv) },
		capture: func(sources []string) (string, string, error) {
			return capturePane(sources, os.Getenv, runner)
		},
		shell: func() terminal.Shell {
			return detectShell(os.Getenv, runner)
		},
		explain: explain,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
	}

	os.Exit(a.run(os.Args[1:]))
}

func (a *app) run(args []string) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(a.stderr, "wut: %v\n\n%s", err, usage)
		return exitUsage
	}

	if opts.help {
		fmt.Fprint(a.stdout, usage)
		return exitOK
	}

	debugf := func(format string, values ...any) {
		if opts.debug {
			fmt.Fprintf(a.stdout, "wut | "+format+"\n", values...)
		}
	}

	// The capture sources are tried in order: tmux, then screen, then the
	// current Kitty window. Only a missing source is a failure here; a stale or
	// unusable multiplexer falls through to the next candidate at capture time.
	sources := terminal.CaptureCandidates(a.getenv)
	if len(sources) == 0 {
		fmt.Fprintln(a.stderr, "wut must be run inside a tmux or screen session, or in a Kitty window.")
		return exitFailure
	}

	// The configuration is validated before the pane is captured, so a missing
	// provider never disturbs the terminal.
	cfg := a.loadConfig()
	if cfg == nil {
		cfg = config.Load(a.getenv)
	}
	if cfg.Err() != nil {
		debugf("Configuration file problem: %v", cfg.Err())
	}
	if !cfg.HasValidConfig() {
		fmt.Fprintln(a.stderr, configHelp)
		return exitFailure
	}
	debugf("Using LLM provider: %s", cfg.ActiveProvider())

	source, pane, err := a.capture(sources)
	if err != nil {
		fmt.Fprintf(a.stderr, "wut: capturing the terminal from %s: %v\n", strings.Join(sources, ", "), err)
		return exitFailure
	}
	debugf("Captured terminal context from %s", source)
	if strings.TrimSpace(pane) == "" {
		fmt.Fprintf(a.stderr, "wut: no output captured from %s.\n", source)
		return exitFailure
	}

	shell := a.shell()
	debugf("Retrieved shell information: %+v", shell)

	context := terminal.BuildContext(pane, shell.Prompt, terminal.MaxCommands, terminal.MaxChars)
	debugf("Retrieved terminal context:\n%s", context)
	debugf("Sending request to LLM...")

	response, err := a.explain(cfg, context, opts.query)
	if err != nil {
		fmt.Fprintf(a.stderr, "wut: %v\n", err)
		return exitFailure
	}

	// The provider answers in Markdown; a terminal reads the translated form,
	// not the raw markup, the way the Python implementation printed it through
	// rich. A rendering failure alone still delivers the answer, because the
	// raw response is printed instead. If that write fails too, the user got
	// nothing and the run is a failure, not a success.
	if err := render.Markdown(a.stdout, response); err != nil {
		debugf("Rendering the answer as Markdown failed: %v", err)
		if _, writeErr := fmt.Fprintln(a.stdout, response); writeErr != nil {
			fmt.Fprintf(a.stderr, "wut: writing the answer failed: %v (rendering failed: %v)\n", writeErr, err)
			return exitFailure
		}
	}
	return exitOK
}

// detectShell resolves the interactive shell for this process.
func detectShell(getenv func(string) string, runner terminal.Runner) terminal.Shell {
	return detectShellFrom(getenv, runner, os.Getpid())
}

// detectShellFrom resolves the interactive shell, using the process table built
// with the portable ps utility when the environment does not name a known
// shell. When ps is unavailable the environment answer is used on its own: a
// missing shell is a degraded context, not a failure.
func detectShellFrom(getenv func(string) string, runner terminal.Runner, startPID int) terminal.Shell {
	shell := terminal.DetectShell(getenv, runner)
	if shell.Name != "" {
		return shell
	}

	table, err := terminal.NewPsProcessTable(runner)
	if err != nil {
		return shell
	}

	return terminal.DetectShellWithTree(getenv, runner, startPID, table.Tree())
}

// capturePane captures from the first source that works, in the order given by
// the caller, and reports which one answered. tmux and screen write a pane
// capture to a temporary file that is removed afterwards; the Kitty fallback
// reads stdout only, so no file is created when it is the only candidate. The
// pane is never read from stdin.
func capturePane(sources []string, getenv func(string) string, runner terminal.Runner) (string, string, error) {
	if !needsCaptureFile(sources) {
		return terminal.CapturePaneFromSources(sources, getenv, "", runner)
	}

	dir, err := captureDir()
	if err != nil {
		return "", "", fmt.Errorf("creating capture file: %w", err)
	}

	file, err := os.CreateTemp(dir, "wut-pane-*")
	if err != nil {
		return "", "", fmt.Errorf("creating capture file: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", "", fmt.Errorf("creating capture file: %w", err)
	}
	defer os.Remove(path)

	return terminal.CapturePaneFromSources(sources, getenv, path, runner)
}

// needsCaptureFile reports whether any candidate needs a pane capture file. The
// Kitty answer can be the whole scrollback, so it is never written to disk.
func needsCaptureFile(sources []string) bool {
	for _, source := range sources {
		if source != terminal.MultiplexerKitty {
			return true
		}
	}
	return false
}

// captureDir resolves the directory for pane capture files. The path must be
// absolute because GNU screen resolves a relative hardcopy path against its own
// working directory, not the caller's.
func captureDir() (string, error) {
	dir, err := filepath.Abs(os.TempDir())
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", errors.New("empty temporary directory")
	}
	return dir, nil
}

// explain returns the provider answer for the given terminal context. The
// prompts and the provider choice come from the configuration, so the terminal
// context is built and sent exactly like the Python implementation did.
func explain(cfg *config.Config, terminalContext string, query string) (string, error) {
	return llm.NewClient(nil).Explain(context.Background(), cfg, terminalContext, query)
}

// parseArgs parses wut flags without the flag package's exit-on-error behavior,
// so argument handling stays testable and messages are ours. Only the
// documented long flags are accepted.
func parseArgs(args []string) (options, error) {
	var opts options

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--query":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--query requires a value")
			}
			i++
			opts.query = args[i]
		case strings.HasPrefix(arg, "--query="):
			opts.query = strings.TrimPrefix(arg, "--query=")
		case arg == "--debug":
			opts.debug = true
		case arg == "--help":
			opts.help = true
		default:
			return opts, fmt.Errorf("unknown argument %q", arg)
		}
	}

	return opts, nil
}
