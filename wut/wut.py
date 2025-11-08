# Standard library
import argparse
import os

# Third party
from rich.console import Console

from wut.config import get_config

# Local
from wut.utils import (
    explain,
    get_shell,
    get_terminal_context,
)

# from utils import (
#     get_shell,
#     get_terminal_context,
#     explain,
# )


def main():
    parser = argparse.ArgumentParser(
        description="Understand the output of your latest terminal command."
    )
    parser.add_argument(
        "--query",
        type=str,
        required=False,
        default="",
        help="A specific question about what's on your terminal.",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Print debug information.",
    )
    args = parser.parse_args()
    console = Console()
    debug = lambda text: console.print(f"wut | {text}") if args.debug else None

    with console.status("[bold green]Trying my best..."):
        # Ensure environment is set up correctly
        if not os.environ.get("TMUX") and not os.environ.get("STY"):
            console.print(
                "[bold red]wut must be run inside a tmux or screen session.[/bold red]"
            )
            return

        # Check for valid configuration
        config = get_config()
        if not config.has_valid_config():
            console.print(
                "[bold red]No valid LLM provider configuration found.[/bold red]\n"
                "Please either:\n"
                "  1. Create ~/.config/wut/config with your API keys and models, or\n"
                "  2. Set environment variables (OPENAI_API_KEY, ANTHROPIC_API_KEY, or OLLAMA_MODEL)"
            )
            return

        # Gather context
        shell = get_shell()
        terminal_context = get_terminal_context(shell)

        debug(f"Retrieved shell information:\n{shell}")
        debug(f"Retrieved terminal context:\n{terminal_context}")
        debug("Sending request to LLM...")

        # Get response
        response = explain(terminal_context, args.query)

    console.print(response)
