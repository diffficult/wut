# wut

**CLI that explains the output of your last command.**

> This is a fork of the original [wut-cli](https://github.com/shobrook/wut) with enhanced configuration options.

Just type `wut` and an LLM will help you understand whatever's in your terminal. You'll be surprised how useful this can be. It can help you:

- Understand stack traces
- Decipher error codes
- Fix incorrect commands
- Summarize logs

![Demo](./demo.gif)

## Installation

```bash
> pipx install wut-cli
```

<!-- On MacOS or Linux, you can install via Homebrew:

```bash
> brew install wut
```

On other systems, you can install using pip:

```bash
> pipx install wut-cli
``` -->

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

For OpenAI, you can customize the model and API URL:

```bash
> export OPENAI_MODEL="gpt-4o"           # Default: gpt-4o
> export OPENAI_BASE_URL="..."           # Default: None (uses OpenAI's API)
```

### Option 2: Configuration File (Recommended)

Create a configuration file at `~/.config/wut/config`:

```bash
> mkdir -p ~/.config/wut
> cp config.example ~/.config/wut/config
```

Then edit `~/.config/wut/config` with your preferences:

```ini
# wut configuration file

# General settings (optional)
[general]
# Explicitly set which provider to use (openai, anthropic, or ollama)
# If not set, will auto-detect based on available API keys
# provider = openai

# OpenAI configuration
[openai]
api_key = your-openai-api-key-here
model = gpt-4o
# base_url = https://api.openai.com/v1  # Optional: custom API endpoint

# Anthropic configuration
[anthropic]
api_key = your-anthropic-api-key-here
model = claude-3-5-sonnet-20241022

# Ollama configuration (for local models)
[ollama]
model = llama2
```

The configuration file takes precedence over environment variables. This allows you to:

- Store all your credentials in one place
- Specify custom OpenAI-compatible API endpoints (e.g., for Azure OpenAI, local models, or other providers)
- Easily switch between providers
- Customize model selection per provider

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

If you have a _specific question_ about your last command, you can include a query:

```bash
> brew install pip
...
> wut "how do i add this to my PATH variable?"
```

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
4. Make some unit tests.
