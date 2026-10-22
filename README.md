# wut

**Understand the output of your last command, without leaving the terminal.**

[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen.svg)](go.mod)
[![Branch](https://img.shields.io/badge/branch-refactor%2Fgo-blue.svg)](https://github.com/diffficult/wut/tree/refactor/go)

`wut` reads the active pane of your terminal, finds the command you just ran, and asks an LLM to
explain what happened. No copy-paste, no second window.

![Demo](./demo.gif)

> **This is a Go rewrite.** The `refactor/go` branch is a from-scratch Go port of
> [shobrook/wut](https://github.com/shobrook/wut): same idea, same prompts, a single self-contained
> binary, and zero third-party modules. `master` still holds the original Python implementation.
> The Go source, tests, and this README live here.

## Why this fork

- **One binary, no runtime.** No Python, no virtualenv, no Pipenv. Build once, copy anywhere.
- **Older outputs on demand.** `wut 2` explains the command before the last one, which matters when
  the newest entry is a failed `wut` run itself.
- **Real configuration.** A single INI file plus prefixed environment variables, with predictable
  precedence.
- **Any OpenAI-compatible endpoint.** OpenAI, Azure, a local server, or
  [OpenCode Console and Go](#opencode-console-and-opencode-go) through `base_url`.
- **Terminal-native output.** Markdown is rendered for the terminal; fenced commands come out as
  indented, copy-ready blocks.
- **Works where you already are.** tmux, GNU screen, or a plain Kitty window.

## Requirements

- A Unix-like system with **tmux**, **GNU screen**, or **Kitty**. `wut` reads the active tmux/screen
  pane, or the current Kitty window; outside one of those it prints
  `wut must be run inside a tmux or screen session, or in a Kitty window.` and exits.
- An API key for OpenAI, Anthropic, or a reachable OpenAI-compatible endpoint, or a local
  [Ollama](https://ollama.com) model.
- **Go 1.22 or newer** to build from source. Nothing else is needed at build time.

## Quick start

```console
$ git clone -b refactor/go https://github.com/diffficult/wut.git
$ cd wut
$ go build -trimpath -ldflags="-s -w" -o wut ./cmd/wut
$ mkdir -p ~/.local/bin
$ install -m 0755 wut ~/.local/bin/wut
$ wut --help
```

Then put a key in `~/.config/wut/config` (or export one) and run `wut` after any command:

```console
$ git create-pr
git: 'create-pr' is not a git command. See 'git --help'.
$ wut
```

`~/.local/bin` must be on your `PATH`. If you prefer the Go toolchain to place the binary, install
it from the clone with `go install ./cmd/wut` (below).

## Installation

### Build and install from source

```console
$ git clone -b refactor/go https://github.com/diffficult/wut.git
$ cd wut
$ go build -trimpath -ldflags="-s -w" -o wut ./cmd/wut
$ mkdir -p ~/.local/bin
$ install -m 0755 wut ~/.local/bin/wut   # or /usr/local/bin, or anywhere on PATH
```

`-trimpath` keeps build paths out of the binary and `-ldflags="-s -w"` strips debug symbols; both are
optional. Drop them for a plain `go build`.

### Install with the Go toolchain

From the clone, `go install` puts the binary in `$(go env GOBIN)` or, when unset,
`$(go env GOPATH)/bin`:

```console
$ go install ./cmd/wut
$ wut --help    # GOBIN or GOPATH/bin must be on PATH
```

### Verify the build

```console
$ go test ./...
```

## Usage

Run a command, then run `wut` in the same pane.

### Explain the last command

```console
$ python app.py
Traceback (most recent call last):
  File "app.py", line 3, in <module>
    import requests
ModuleNotFoundError: No module named 'requests'
$ wut
```

```text
The traceback means the active Python environment does not have the `requests`
package installed.

Install it with the interpreter that runs the script:

    python -m pip install requests

If you use a virtualenv, activate it first; `which python` should point inside
the environment.
```

### Ask a specific question

```console
$ brew install jq
...
$ wut --query "where did Homebrew put jq?"
```

`--query` still sends the captured terminal context, so the model can answer about the command you
just ran instead of guessing.

### Explain an older output

The newest entry in the pane is often a `wut` run of its own. Pass a number to count back from the
newest:

```console
$ wut
wut: openai request failed: unexpected status 500
$ wut 2
```

`wut 2` explains the command before that failed run; `wut 3` the one before that. `wut` and `wut 1`
are the same thing. Anything newer than the selected command is left out of the context.

### See what is sent

```console
$ wut 2 --debug
wut | Using LLM provider: openai
wut | Captured terminal context from tmux
wut | Retrieved shell information: {Path:/bin/zsh Name:zsh Prompt:}
wut | Using output 2, counting back from the newest
wut | Inferred shell prompt from the pane: "❯❯❯ "
wut | Retrieved terminal context:
<terminal_history>
<previous_commands>

</previous_commands>

<last_command>
❯❯❯  go test ./...
--- FAIL: TestParseArgs (0.00s)
</last_command>
</terminal_history>
wut | Sending request to LLM...
```

### Arguments and flags

| Argument | Description |
| --- | --- |
| `[<output>]` | Which command output to explain, counting back from the newest: `1` (the default) is the last command, `2` the one before it. |

| Flag | Description |
| --- | --- |
| `--query <question>` | Ask a specific question about what's on your terminal. |
| `--debug` | Print the detected shell, the selected output, the captured context, and the provider. Debug lines are held until after the capture so they never pollute the pane. |
| `--help` | Print the usage text. |

All flags are long-form. `wut` never reads stdin or a file, and it never runs or fixes a command for
you.

Exit codes: `0` success, `1` runtime failure (no capture source, no configuration, failed capture,
provider error), `2` usage error.

## How it works

```text
tmux / screen / Kitty
        │
        ▼
shell + prompt ──► split commands ──► build <terminal_history>
                                              │
                                              ▼
                    provider (OpenAI / Anthropic / Ollama)
                                              │
                                              ▼
                        Markdown rendered for the terminal
```

1. **Capture.** tmux, then GNU screen, then the current Kitty window. A stale multiplexer is not
   fatal: `wut` moves on to the next available source.
2. **Detect the shell and its prompt.** `SHELL`/`TF_SHELL` first, then the process tree via `ps`.
   If the prompt cannot be obtained (zsh does not export `PS1`, for example), it is inferred from
   the pane so older outputs can still be selected.
3. **Split commands.** The pane is divided on prompt lines, newest first, and the running `wut`
   invocation is dropped.
4. **Build the context.** The selected command becomes `<last_command>`; older commands become
   `<previous_commands>`. At most 3 commands and 10,000 characters are sent.
5. **Ask the provider** and **render the Markdown** answer for the terminal.

### Project layout

```text
cmd/wut/            CLI entry point, flag parsing, wiring, usage text
internal/config/    INI + environment resolution, provider selection
internal/llm/       OpenAI, Anthropic, and Ollama clients, prompts
internal/terminal/  tmux/screen/Kitty capture, shell detection, pane parser
internal/render/    Markdown-to-terminal renderer
```

The test suite follows the same split and runs without a live multiplexer, a provider, or external
network access.

## Configuration

Two sources, one precedence rule: a non-empty value in the config file wins, then the environment
variable, then the default.

### Environment variables

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_MODEL="gpt-4o"
```

| Variable | Used for | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | OpenAI credential. Required for the OpenAI provider. | none |
| `OPENAI_MODEL` | OpenAI model. | `gpt-4o` |
| `OPENAI_BASE_URL` | Custom OpenAI-compatible endpoint (Azure OpenAI, a proxy, a local server). | `https://api.openai.com/v1` |
| `OPENAI_SESSION` | Client session id sent as the `x-opencode-session` header. Overrides the generated id. | generated per run |
| `ANTHROPIC_API_KEY` | Anthropic credential. Required for the Anthropic provider. | none |
| `ANTHROPIC_MODEL` | Anthropic model. | `claude-3-5-sonnet-20241022` |
| `OLLAMA_MODEL` | Local Ollama model. Required for the Ollama provider. | none |
| `OLLAMA_HOST` | Ollama server, e.g. `http://192.168.1.10:11434`. | `http://localhost:11434` |

The Anthropic endpoint is fixed at `https://api.anthropic.com`.

### Configuration file

```console
$ mkdir -p ~/.config/wut
$ cp config.example ~/.config/wut/config
```

`config.example` documents every supported key. A minimal file:

```ini
[general]
provider = openai

[openai]
api_key = sk-...
model = gpt-4o
```

| Section | Key | Meaning | Default |
| --- | --- | --- | --- |
| `[general]` | `provider` | Force a provider: `openai`, `anthropic`, or `ollama`. | auto-detected |
| `[openai]` | `api_key` | OpenAI credential. | none |
| `[openai]` | `model` | OpenAI model. | `gpt-4o` |
| `[openai]` | `base_url` | Custom OpenAI-compatible endpoint. | `https://api.openai.com/v1` |
| `[openai]` | `session` | Client session id sent as the `x-opencode-session` header. Overrides the generated id. | generated per run |
| `[anthropic]` | `api_key` | Anthropic credential. | none |
| `[anthropic]` | `model` | Anthropic model. | `claude-3-5-sonnet-20241022` |
| `[ollama]` | `model` | Local Ollama model. | none |

Two rules matter when editing the file:

- Section and key names are case-insensitive, and a value ends at the end of the line. `wut` does
  **not** strip inline comments, so put notes on their own `#` line.
- Keep credentials out of the repository: the file lives in your home directory.

A file that cannot be parsed never aborts the run: values read before the problem are kept and the
rest falls back to the environment. Run `wut --debug` to see the reported problem.

## Providers

`wut` auto-detects the provider from the available credentials, in this order:

1. OpenAI (if `api_key` is configured)
2. Anthropic (if `api_key` is configured)
3. Ollama (if `model` is configured)

Set `provider` in `[general]` to override the order.

### Ollama

```ini
[general]
provider = ollama

[ollama]
model = llama3.2
```

`OLLAMA_HOST` points at a non-default server; the default is `http://localhost:11434`.

### OpenCode Console and OpenCode Go

Both speak the OpenAI chat completions API, so they use the `[openai]` provider with a custom
`base_url`. `wut` appends `chat/completions`, so the base URL includes the API family and version:

```ini
[general]
provider = openai

[openai]
api_key = <your opencode key>
model = kimi-k2.6
base_url = https://opencode.ai/inference/openai/v1
```

[OpenCode Go](https://opencode.ai/docs/go/) is served from `https://opencode.ai/inference/go` and
rejects requests without an `x-opencode-session` header. `wut` always sends one: the configured
`session` when present, otherwise an id generated per run.

```ini
[openai]
api_key = <your opencode key>
model = deepseek-v4.1-flash
base_url = https://opencode.ai/inference/go/openai/v1
# Optional: pin the session id instead of generating one per run.
# session = 11111111-2222-3333-4444-555555555555
```

Pick a model whose Go endpoint is `/chat/completions` (for example `deepseek-v4.1-flash`, `glm-5.3`,
`kimi-k2.7-code`). Models served over `/responses` or `/messages` are not reachable through the
OpenAI provider.

## Capture sources

`wut` tries the sources it finds in the environment, in order, and stops at the first one that
returns text:

1. **tmux**, when `TMUX` is set: `tmux capture-pane -p -S -`.
2. **GNU screen**, when `STY` is set: `screen -X hardcopy -h <file>`.
3. **Kitty**, when `KITTY_WINDOW_ID` is set:
   `kitten @ get-text --match id:<KITTY_WINDOW_ID> --extent=all`.

A successful tmux or screen capture never invokes Kitty. With `KITTY_WINDOW_ID` unset, Kitty is never
invoked. Kitty answers with the whole scrollback, so only the newest 256 KiB are kept in memory.

### Kitty fallback (optional)

Kitty's [remote control](https://sw.kovidgoyal.net/kitty/remote-control/) is disabled by default and
`wut` never enables it. Grant the narrowest permission that makes `get-text` work; the config syntax
has changed between Kitty releases, so check the docs for your version.

Check whether `kitten` can reach the running instance:

```console
$ kitten @ get-text --match id:$KITTY_WINDOW_ID --extent=all | head
```

If that prints recent window text, `wut` reads the same text:

```console
$ echo "wut now works in plain kitty" && wut
```

If the permission is missing, `wut` says so instead of reading another window:

```text
wut: capturing the terminal from kitty: terminal: no usable capture source: kitty capture failed:
`kitten` may be missing, or kitty remote control is disabled by default because the narrow
get-text-only permission was never granted; the window is selected with KITTY_WINDOW_ID,
see https://github.com/diffficult/wut#kitty-fallback-optional
```

## Output rendering

Providers answer in Markdown. `wut` renders it for the terminal: headings and bold text become
styled text, lists keep their markers, and fenced code blocks are printed as indented commands you
can copy and re-run. Long lines are not re-wrapped, so a command is never broken in half.

Styling is only applied when stdout is a real terminal. When you pipe or redirect `wut`, or set
`NO_COLOR=1` (or `TERM=dumb`), the answer is plain text with no escape sequences.

## Development

```console
$ go build ./...          # compile every package
$ go test ./...           # run the whole suite
$ go vet ./...            # static checks
$ gofmt -l .              # list files that need formatting (no output = clean)
```

Run one package or one test:

```console
$ go test ./internal/terminal
$ go test ./internal/terminal -run TestBuildContext -v
```

Cross-compile a release binary:

```console
$ GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o wut-darwin-arm64 ./cmd/wut
$ GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o wut-linux-amd64 ./cmd/wut
```

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `wut must be run inside a tmux or screen session...` | No `TMUX`, `STY`, or `KITTY_WINDOW_ID` in the environment. Run `wut` in the pane, not from a script outside it. |
| `No valid LLM provider configuration found` | No usable `[openai] api_key`, `[anthropic] api_key`, or `[ollama] model`. Create `~/.config/wut/config` or export a key. |
| `cannot reach output N: the terminal shows only M previous command(s)` | The pane does not hold that much scrollback. Scroll further up or pick a smaller number. |
| `cannot reach output N: the shell prompt could not be determined` | `wut` could not read or infer a prompt, so it cannot split commands. Set a single-line prompt or use `wut` with the default output. |
| `openai request failed: ... 401` | The key is missing or invalid for the configured `base_url`. |

## Roadmap

1. [If possible,](https://stackoverflow.com/questions/24283097/reusing-output-from-last-command-in-bash/75629157#75629157)
   drop the requirement of being inside a tmux or screen session.
2. Add a `--fix` option to execute a command suggested by `wut`.
3. Add `wut` to Homebrew.

## Credits and license

Original concept and Python implementation: [Jonathan Shobrook](https://github.com/shobrook/wut).
Go rewrite and the configuration layer in this fork: [diffficult](https://github.com/diffficult/wut).

[MIT](LICENSE) © 2024 Jonathan Shobrook.
