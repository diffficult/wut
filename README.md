# wut

**CLI that explains the output of your last command.**

> This is a fork of the original [wut-cli](https://github.com/shobrook/wut) with enhanced configuration options.

Just type `wut` and an LLM will help you understand whatever's in your terminal. You'll be surprised how useful this can be. It can help you:

- Understand stack traces
- Decipher error codes
- Fix incorrect commands
- Summarize logs

![Demo](./demo.gif)

## Requirements

- **A terminal multiplexer: `wut` must be run inside a `tmux` or GNU `screen` session.** It reads the visible pane to recover what you just ran. Running it in a plain terminal prints `wut must be run inside a tmux or screen session.` and exits.
- **Go 1.22 or newer** to build the binary. No Python is needed at runtime or at build time.

## Installation

Build the binary from a clone of this repository:

```bash
> git clone https://github.com/diffficult/wut.git
> cd wut
> go build -o ./wut-go ./cmd/wut
> ./wut-go --help
```

The binary is named `wut-go` because the repository root still contains the legacy Python package directory `wut/`, and Go refuses to write a build output over an existing directory. Once the Python sources are gone the binary can simply be called `wut`; `go install ./cmd/wut` always installs it as `wut`.

Or install it into `$GOBIN` (which defaults to `$(go env GOPATH)/bin`) so `wut` is on your `PATH`:

```bash
> go install ./cmd/wut
```

```bash
> go build ./...     # compile every package
> go test ./...      # run the test suite
```

## Usage

`wut` must be used inside a `tmux` or `screen` session to capture the last command's output. To use it, just type `wut` after running a command:

```bash
> git create-pr
git: 'create-pr' is not a git command.
> wut
```

You'll quickly get a brief explanation of the issue:

```
This error occurs because Git doesn't have a built-in `create-pr` command.
To create a pull request, you typically need to:

1. Push your branch to the remote repository
2. Use the GitHub web interface
```

If you have a _specific question_ about your last command, pass it with `--query`:

```bash
> brew install pip
...
> wut --query "how do i add this to my PATH variable?"
```

All flags are long-form, and there are no other input modes: `wut` never reads stdin or a file, and it never runs or fixes a command for you.

| Flag | Description |
| --- | --- |
| `--query <question>` | Ask a specific question about what's on your terminal. |
| `--debug` | Print debug information: the detected shell, the captured terminal context, and the selected provider. |
| `--help` | Print the usage text. |

Exit codes: `0` on success, `1` on a runtime failure (no tmux/screen session, no configuration, failed capture, provider error), `2` on a usage error.

### How the answer is printed

Providers answer in Markdown. `wut` renders that Markdown for your terminal: headings and bold text become styled text, lists keep their markers, and fenced code blocks are printed as plain indented commands you can copy and re-run. Long lines are not re-wrapped, so a command is never broken in half.

Styling is only applied when the output is a real terminal. When you pipe or redirect `wut`, or set `NO_COLOR=1` (or `TERM=dumb`), the answer is printed as plain text with no escape sequences.

## Configuration

You can configure `wut` in two ways: using environment variables or a configuration file.

### Option 1: Environment Variables

Set the appropriate API key for your preferred LLM provider:

```bash
> export OPENAI_API_KEY="..."
> export ANTHROPIC_API_KEY="..."
```

For local models with Ollama:

```bash
> export OLLAMA_MODEL="..."
```

Every setting has an environment variable named `<SECTION>_<KEY>` in upper case:

| Variable | Used for | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | OpenAI credential. Required for the OpenAI provider. | none |
| `OPENAI_MODEL` | OpenAI model. | `gpt-4o` |
| `OPENAI_BASE_URL` | Custom OpenAI-compatible endpoint (Azure OpenAI, a proxy, a local server). | `https://api.openai.com/v1` |
| `ANTHROPIC_API_KEY` | Anthropic credential. Required for the Anthropic provider. | none |
| `ANTHROPIC_MODEL` | Anthropic model. | `claude-3-5-sonnet-20241022` |
| `OLLAMA_MODEL` | Local Ollama model. Required for the Ollama provider. | none |
| `OLLAMA_HOST` | Ollama server, e.g. `http://192.168.1.10:11434`. | `http://localhost:11434` |

The Anthropic endpoint is fixed at `https://api.anthropic.com`.

### Option 2: Configuration File (Recommended)

`wut` reads a single INI file at `~/.config/wut/config` (`$HOME/.config/wut/config`):

```bash
> mkdir -p ~/.config/wut
> cp config.example ~/.config/wut/config
```

Then edit `~/.config/wut/config` with your preferences. The file copied above is the annotated reference; it documents every supported key:

| Section | Key | Meaning | Default |
| --- | --- | --- | --- |
| `[general]` | `provider` | Force a provider: `openai`, `anthropic`, or `ollama`. | auto-detected |
| `[openai]` | `api_key` | OpenAI credential. | none |
| `[openai]` | `model` | OpenAI model. | `gpt-4o` |
| `[openai]` | `base_url` | Custom OpenAI-compatible endpoint. | `https://api.openai.com/v1` |
| `[anthropic]` | `api_key` | Anthropic credential. | none |
| `[anthropic]` | `model` | Anthropic model. | `claude-3-5-sonnet-20241022` |
| `[ollama]` | `model` | Local Ollama model. | none |

Two rules matter when editing the file:

- Section and key names are case-insensitive, and a value ends at the end of the line. `wut` does **not** strip inline comments, so put notes on their own `#` line instead of after a value.
- Keep credentials out of the repository: the file lives in your home directory and is yours alone.

The configuration file takes precedence over environment variables. A key in the file that is missing or empty falls back to the environment variable of the same name, and then to the default above, so you can keep only the keys you want to override. This allows you to:

- Store all your credentials in one place
- Specify custom OpenAI-compatible API endpoints (e.g., for Azure OpenAI, local models, or other providers)
- Easily switch between providers
- Customize model selection per provider

A configuration file that cannot be parsed never aborts the run: the values read before the problem are used and everything else falls back to the environment. Run `wut --debug` to see the reported problem.

## Provider Selection

`wut` automatically detects which LLM provider to use based on available credentials, with the following priority:

1. OpenAI (if `api_key` is configured)
2. Anthropic (if `api_key` is configured)
3. Ollama (if `model` is configured)

You can override this by setting `provider` in the `[general]` section of your config file.

## Roadmap

1. [If possible,](https://stackoverflow.com/questions/24283097/reusing-output-from-last-command-in-bash/75629157#75629157) drop the requirement of being inside a tmux or screen session.
2. Add a `--fix` option to automatically execute a command suggested by `wut`.
3. Add `wut` to Homebrew.
