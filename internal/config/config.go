// Package config loads the wut configuration from ~/.config/wut/config and the
// environment, resolves the available LLM providers, and picks the provider wut
// talks to.
//
// Configuration precedence mirrors the Python implementation: a non-empty value
// from the config file wins, otherwise the SECTION_KEY environment variable is
// used, otherwise the caller supplied fallback. A config file that cannot be
// parsed never fails the run; the values parsed before the failure are kept and
// everything else falls back to the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Default provider models, matching the Python defaults.
const (
	DefaultOpenAIModel    = "gpt-4o"
	DefaultAnthropicModel = "claude-3-5-sonnet-20241022"
)

// defaultSection is the configparser DEFAULT section, whose options are visible
// to every other section.
const defaultSection = "default"

// Provider identifies an LLM backend.
type Provider string

// The supported providers.
const (
	ProviderNone      Provider = ""
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderOllama    Provider = "ollama"
)

// ProviderConfig holds the resolved settings for one provider. Fields that do
// not apply to a provider stay empty.
type ProviderConfig struct {
	APIKey  string
	Model   string
	BaseURL string
}

// Providers is the resolved configuration of every supported provider.
type Providers struct {
	OpenAI    ProviderConfig
	Anthropic ProviderConfig
	Ollama    ProviderConfig
}

// Config is a loaded wut configuration.
type Config struct {
	defaults map[string]string
	sections map[string]map[string]string
	getenv   func(string) string
	parseErr error
}

// Option customizes how a configuration is loaded.
type Option func(*loadOptions)

type loadOptions struct {
	path string
}

// WithPath loads the configuration from an explicit file instead of
// ~/.config/wut/config.
func WithPath(path string) Option {
	return func(o *loadOptions) { o.path = path }
}

// DefaultPath returns the configuration file path for the current user:
// $HOME/.config/wut/config, or the user home directory when $HOME is unset.
func DefaultPath(getenv func(string) string) string {
	home := ""
	if getenv != nil {
		home = getenv("HOME")
	}
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	return filepath.Join(home, ".config", "wut", "config")
}

// Load reads the configuration file and prepares environment lookups. A missing
// or unreadable file is not an error: the environment alone is used. A file
// that cannot be parsed is reported by Err and the values parsed before the
// failure are kept.
func Load(getenv func(string) string, opts ...Option) *Config {
	options := loadOptions{path: DefaultPath(getenv)}
	for _, opt := range opts {
		opt(&options)
	}

	cfg := &Config{
		defaults: map[string]string{},
		sections: map[string]map[string]string{},
		getenv:   getenv,
	}
	cfg.load(options.path)
	return cfg
}

func (c *Config) load(path string) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			c.parseErr = fmt.Errorf("reading %s: %w", path, err)
		}
		return
	}
	c.parseErr = parseINI(string(contents), c.defaults, c.sections)
}

// Err reports why the configuration file could not be read or parsed, if any.
// The configuration is still usable; the failure only explains which values
// were dropped in favor of the environment.
func (c *Config) Err() error { return c.parseErr }

// Get returns the value for an option, preferring the config file, then the
// SECTION_KEY environment variable, then fallback. Empty config file values are
// ignored, exactly as in the Python implementation.
//
// Like configparser, a value in a section shadows the DEFAULT section, even when
// it is empty, and the DEFAULT section is only visible to sections that exist.
func (c *Config) Get(section string, key string, fallback string) string {
	section = strings.ToLower(section)
	key = strings.ToLower(key)

	if value, ok := c.option(section, key); ok && value != "" {
		return value
	}

	return c.envValue(section, key, fallback)
}

// option returns the config file value for an option, if the section exists.
func (c *Config) option(section string, key string) (string, bool) {
	values, ok := c.sections[section]
	if !ok {
		return "", false
	}
	if value, ok := values[key]; ok {
		return value, true
	}
	value, ok := c.defaults[key]
	return value, ok
}

// hasOption reports whether the section exists and defines the option, either
// directly or through the DEFAULT section.
func (c *Config) hasOption(section string, key string) bool {
	values, ok := c.sections[section]
	if !ok {
		return false
	}
	if _, ok := values[key]; ok {
		return true
	}
	_, ok = c.defaults[key]
	return ok
}

func (c *Config) envValue(section string, key string, fallback string) string {
	if c.getenv == nil {
		return fallback
	}
	if value := c.getenv(strings.ToUpper(section) + "_" + strings.ToUpper(key)); value != "" {
		return value
	}
	return fallback
}

// Providers returns the resolved configuration for every supported provider.
// OpenAI gets a default model, Anthropic gets a default model, and Ollama
// intentionally has none: it is only selected when a model is configured.
func (c *Config) Providers() Providers {
	return Providers{
		OpenAI: ProviderConfig{
			APIKey:  c.Get("openai", "api_key", ""),
			Model:   c.Get("openai", "model", DefaultOpenAIModel),
			BaseURL: c.Get("openai", "base_url", ""),
		},
		Anthropic: ProviderConfig{
			APIKey: c.Get("anthropic", "api_key", ""),
			Model:  c.Get("anthropic", "model", DefaultAnthropicModel),
		},
		Ollama: ProviderConfig{
			Model: c.Get("ollama", "model", ""),
		},
	}
}

// ActiveProvider returns the provider wut should use: the explicit
// general.provider from the config file when it names a supported provider, and
// otherwise the first provider with usable settings, in the order OpenAI,
// Anthropic, Ollama. It returns ProviderNone when nothing is configured.
func (c *Config) ActiveProvider() Provider {
	providers := c.Providers()

	if c.hasOption("general", "provider") {
		provider, _ := c.option("general", "provider")
		if selected := Provider(strings.ToLower(strings.TrimSpace(provider))); isSupported(selected) {
			return selected
		}
	}

	switch {
	case providers.OpenAI.APIKey != "":
		return ProviderOpenAI
	case providers.Anthropic.APIKey != "":
		return ProviderAnthropic
	case providers.Ollama.Model != "":
		return ProviderOllama
	default:
		return ProviderNone
	}
}

// HasValidConfig reports whether at least one provider is usable.
func (c *Config) HasValidConfig() bool { return c.ActiveProvider() != ProviderNone }

func isSupported(provider Provider) bool {
	switch provider {
	case ProviderOpenAI, ProviderAnthropic, ProviderOllama:
		return true
	default:
		return false
	}
}

// parseINI reads an INI file into the defaults and sections maps, keeping every
// value read before a failure and returning that failure. It follows the
// configparser rules the Python implementation relied on: keys and section names
// are lowercased, "#" and ";" start a full-line comment, a value is separated
// by the first "=" or ":", indented lines continue the previous value, and no
// interpolation or inline-comment handling is performed.
func parseINI(contents string, defaults map[string]string, sections map[string]map[string]string) error {
	section := defaultSection
	var key string
	var hasKey bool

	for number, raw := range strings.Split(contents, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}

		// An indented line continues the previous value.
		if hasKey && (line != trimmed) {
			sections[section][key] += "\n" + trimmed
			continue
		}

		if strings.HasPrefix(trimmed, "[") {
			if !strings.HasSuffix(trimmed, "]") {
				return fmt.Errorf("line %d: missing closing ] in section header %q", number+1, trimmed)
			}
			name := strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			if name == "" {
				return fmt.Errorf("line %d: empty section name", number+1)
			}
			if _, exists := sections[name]; exists && name != defaultSection {
				return fmt.Errorf("line %d: duplicate section %q", number+1, name)
			}
			section = name
			if _, exists := sections[section]; !exists {
				sections[section] = map[string]string{}
			}
			key, hasKey = "", false
			continue
		}

		name, value, ok := splitOption(trimmed)
		if !ok {
			return fmt.Errorf("line %d: missing option delimiter in %q", number+1, trimmed)
		}

		name = strings.ToLower(name)
		if name == "" {
			return fmt.Errorf("line %d: empty option name", number+1)
		}
		if section == defaultSection {
			defaults[name] = value
		} else {
			sections[section][name] = value
		}
		key, hasKey = name, true
	}

	return nil
}

// splitOption splits "key = value" or "key: value" on the first delimiter.
func splitOption(line string) (string, string, bool) {
	index := strings.IndexAny(line, "=:")
	if index < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:index]), strings.TrimSpace(line[index+1:]), true
}
