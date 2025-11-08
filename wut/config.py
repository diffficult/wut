# Standard library
import configparser
import os
from pathlib import Path
from typing import Any, Dict, Optional

# Config file location
CONFIG_DIR = Path.home() / ".config" / "wut"
CONFIG_FILE = CONFIG_DIR / "config"


class WutConfig:
    """Configuration manager for wut CLI."""

    def __init__(self):
        self.config = configparser.ConfigParser()
        self._load_config()

    def _load_config(self):
        """Load configuration from file if it exists."""
        if CONFIG_FILE.exists():
            try:
                self.config.read(CONFIG_FILE)
            except Exception as e:
                # If config file is malformed, we'll fall back to env vars
                pass

    def get(
        self, section: str, key: str, fallback: Optional[str] = None
    ) -> Optional[str]:
        """Get a config value, falling back to environment variable."""
        # First check config file
        if self.config.has_option(section, key):
            value = self.config.get(section, key)
            if value:  # Don't return empty strings
                return value

        # Fall back to environment variable (uppercase section_key)
        env_var = f"{section.upper()}_{key.upper()}"
        return os.environ.get(env_var, fallback)

    def get_provider_config(self) -> Dict[str, Any]:
        """Get configuration for available LLM providers."""
        return {
            "openai": {
                "api_key": self.get("openai", "api_key")
                or os.environ.get("OPENAI_API_KEY"),
                "model": self.get("openai", "model")
                or os.environ.get("OPENAI_MODEL", "gpt-4o"),
                "base_url": self.get("openai", "base_url")
                or os.environ.get("OPENAI_BASE_URL"),
            },
            "anthropic": {
                "api_key": self.get("anthropic", "api_key")
                or os.environ.get("ANTHROPIC_API_KEY"),
                "model": self.get("anthropic", "model", "claude-3-5-sonnet-20241022"),
            },
            "ollama": {
                "model": self.get("ollama", "model") or os.environ.get("OLLAMA_MODEL"),
            },
        }

    def get_active_provider(self) -> Optional[str]:
        """Determine which provider to use based on available credentials."""
        providers = self.get_provider_config()

        # Check if provider is explicitly set in config
        if self.config.has_option("general", "provider"):
            provider = self.config.get("general", "provider")
            if provider in providers:
                return provider

        # Otherwise, auto-detect based on available credentials
        # Priority: OpenAI > Anthropic > Ollama
        if providers["openai"]["api_key"]:
            return "openai"

        if providers["anthropic"]["api_key"]:
            return "anthropic"

        if providers["ollama"]["model"]:
            return "ollama"

        return None

    def has_valid_config(self) -> bool:
        """Check if we have valid configuration for at least one provider."""
        return self.get_active_provider() is not None


# Global config instance
_config = None


def get_config() -> WutConfig:
    """Get or create the global config instance."""
    global _config
    if _config is None:
        _config = WutConfig()
    return _config
