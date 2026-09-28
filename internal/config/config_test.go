package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

const fullConfig = `
# wut configuration file
[general]
provider = openai

[openai]
api_key = file-openai-key
model = gpt-4o-mini
base_url = https://proxy.example.com/v1

[anthropic]
api_key = file-anthropic-key
model = claude-3-opus-20240229

[ollama]
model = llama3
`

func TestLoadReadsEveryProviderFromTheConfigFile(t *testing.T) {
	path := writeConfig(t, fullConfig)

	cfg := Load(env(nil), WithPath(path))

	providers := cfg.Providers()
	if providers.OpenAI.APIKey != "file-openai-key" {
		t.Fatalf("openai api key = %q, want the config file value", providers.OpenAI.APIKey)
	}
	if providers.OpenAI.Model != "gpt-4o-mini" {
		t.Fatalf("openai model = %q, want the config file value", providers.OpenAI.Model)
	}
	if providers.OpenAI.BaseURL != "https://proxy.example.com/v1" {
		t.Fatalf("openai base url = %q, want the config file value", providers.OpenAI.BaseURL)
	}
	if providers.Anthropic.APIKey != "file-anthropic-key" || providers.Anthropic.Model != "claude-3-opus-20240229" {
		t.Fatalf("anthropic = %+v, want the config file values", providers.Anthropic)
	}
	if providers.Ollama.Model != "llama3" {
		t.Fatalf("ollama model = %q, want the config file value", providers.Ollama.Model)
	}
}

func TestFileValueWinsOverEnvironment(t *testing.T) {
	path := writeConfig(t, fullConfig)

	cfg := Load(env(map[string]string{
		"OPENAI_API_KEY": "env-openai-key",
		"OPENAI_MODEL":   "env-model",
		"OLLAMA_MODEL":   "env-llama",
	}), WithPath(path))

	providers := cfg.Providers()
	if providers.OpenAI.APIKey != "file-openai-key" || providers.OpenAI.Model != "gpt-4o-mini" {
		t.Fatalf("openai = %+v, want the file values to win", providers.OpenAI)
	}
	if providers.Ollama.Model != "llama3" {
		t.Fatalf("ollama model = %q, want the file value to win", providers.Ollama.Model)
	}
}

func TestEmptyFileValueFallsBackToEnvironment(t *testing.T) {
	path := writeConfig(t, "[openai]\napi_key =\nmodel =\n")

	cfg := Load(env(map[string]string{"OPENAI_API_KEY": "env-key", "OPENAI_MODEL": "env-model"}), WithPath(path))

	providers := cfg.Providers()
	if providers.OpenAI.APIKey != "env-key" {
		t.Fatalf("openai api key = %q, want the environment value", providers.OpenAI.APIKey)
	}
	if providers.OpenAI.Model != "env-model" {
		t.Fatalf("openai model = %q, want the environment value", providers.OpenAI.Model)
	}
}

func TestEnvironmentIsUsedWhenTheSectionIsMissing(t *testing.T) {
	path := writeConfig(t, "[openai]\napi_key = file-key\n")

	cfg := Load(env(map[string]string{"ANTHROPIC_API_KEY": "env-anthropic", "OLLAMA_MODEL": "env-llama"}), WithPath(path))

	providers := cfg.Providers()
	if providers.Anthropic.APIKey != "env-anthropic" {
		t.Fatalf("anthropic api key = %q, want the environment value", providers.Anthropic.APIKey)
	}
	if providers.Ollama.Model != "env-llama" {
		t.Fatalf("ollama model = %q, want the environment value", providers.Ollama.Model)
	}
}

func TestDefaultModelsMatchThePythonDefaults(t *testing.T) {
	cfg := Load(env(nil), WithPath(filepath.Join(t.TempDir(), "absent")))

	providers := cfg.Providers()
	if providers.OpenAI.Model != "gpt-4o" {
		t.Fatalf("openai model = %q, want gpt-4o", providers.OpenAI.Model)
	}
	if providers.Anthropic.Model != "claude-3-5-sonnet-20241022" {
		t.Fatalf("anthropic model = %q, want claude-3-5-sonnet-20241022", providers.Anthropic.Model)
	}
	if providers.Ollama.Model != "" {
		t.Fatalf("ollama model = %q, want no default", providers.Ollama.Model)
	}
}

func TestOpenAIBaseURLHasNoDefault(t *testing.T) {
	cfg := Load(env(nil), WithPath(filepath.Join(t.TempDir(), "absent")))

	if got := cfg.Providers().OpenAI.BaseURL; got != "" {
		t.Fatalf("openai base url = %q, want empty so the SDK default applies", got)
	}
}

func TestMalformedConfigDegradesToEnvironment(t *testing.T) {
	path := writeConfig(t, "this is not an ini file\n")

	cfg := Load(env(map[string]string{"OPENAI_API_KEY": "env-key"}), WithPath(path))

	if got := cfg.Providers().OpenAI.APIKey; got != "env-key" {
		t.Fatalf("openai api key = %q, want the environment value from a malformed file", got)
	}
	if cfg.Err() == nil {
		t.Fatal("cfg.Err() = nil, want the parse failure recorded")
	}
}

func TestMalformedConfigKeepsTheValidPrefixAndDoesNotPanic(t *testing.T) {
	path := writeConfig(t, "[openai]\napi_key = good-key\n[broken\n")

	cfg := Load(env(map[string]string{"ANTHROPIC_API_KEY": "env-anthropic"}), WithPath(path))

	if got := cfg.Providers().OpenAI.APIKey; got != "good-key" {
		t.Fatalf("openai api key = %q, want the value parsed before the failure", got)
	}
	if got := cfg.Providers().Anthropic.APIKey; got != "env-anthropic" {
		t.Fatalf("anthropic api key = %q, want the environment fallback", got)
	}
}

func TestMissingConfigFileUsesEnvironment(t *testing.T) {
	cfg := Load(env(map[string]string{"OPENAI_API_KEY": "env-key"}), WithPath(filepath.Join(t.TempDir(), "absent")))

	if cfg.Err() != nil {
		t.Fatalf("cfg.Err() = %v, want no error for a missing file", cfg.Err())
	}
	if got := cfg.Providers().OpenAI.APIKey; got != "env-key" {
		t.Fatalf("openai api key = %q, want the environment value", got)
	}
}

func TestGetFallsBackToTheProvidedFallback(t *testing.T) {
	cfg := Load(env(nil), WithPath(filepath.Join(t.TempDir(), "absent")))

	if got := cfg.Get("anthropic", "model", "fallback-model"); got != "fallback-model" {
		t.Fatalf("Get() = %q, want the fallback", got)
	}
	if got := cfg.Get("anthropic", "model", ""); got != "" {
		t.Fatalf("Get() = %q, want empty", got)
	}
}

func TestGetReadsTheEnvironmentForAnEmptyOption(t *testing.T) {
	cfg := Load(env(map[string]string{"GENERAL_PROVIDER": "ollama"}), WithPath(filepath.Join(t.TempDir(), "absent")))

	if got := cfg.Get("general", "provider", ""); got != "ollama" {
		t.Fatalf("Get() = %q, want the environment value", got)
	}
}

func TestSectionAndKeyNamesAreCaseInsensitive(t *testing.T) {
	path := writeConfig(t, "[OpenAI]\nAPI_KEY = file-key\n")

	cfg := Load(env(nil), WithPath(path))

	if got := cfg.Providers().OpenAI.APIKey; got != "file-key" {
		t.Fatalf("openai api key = %q, want the value from a differently cased header", got)
	}
}

func TestCommentsAndContinuationLinesAreParsed(t *testing.T) {
	path := writeConfig(t, "; a comment\n[openai]\n# another comment\napi_key: file-key\nmodel = gpt-4o\n  mini\n")

	cfg := Load(env(nil), WithPath(path))

	providers := cfg.Providers()
	if providers.OpenAI.APIKey != "file-key" {
		t.Fatalf("openai api key = %q, want the value after a colon separator", providers.OpenAI.APIKey)
	}
	if providers.OpenAI.Model != "gpt-4o\nmini" {
		t.Fatalf("openai model = %q, want the continued value", providers.OpenAI.Model)
	}
}

func TestDefaultSectionValuesAreInherited(t *testing.T) {
	path := writeConfig(t, "[DEFAULT]\nmodel = shared-model\n\n[openai]\napi_key = file-key\n")

	cfg := Load(env(nil), WithPath(path))

	if got := cfg.Providers().OpenAI.Model; got != "shared-model" {
		t.Fatalf("openai model = %q, want the DEFAULT section value", got)
	}
}

// Like configparser, the DEFAULT section is invisible to a section that does not
// exist.
func TestDefaultSectionIsInvisibleToAMissingSection(t *testing.T) {
	path := writeConfig(t, "[DEFAULT]\nmodel = shared-model\n\n[openai]\napi_key = file-key\n")

	cfg := Load(env(map[string]string{"OLLAMA_MODEL": "env-llama"}), WithPath(path))

	if got := cfg.Providers().Ollama.Model; got != "env-llama" {
		t.Fatalf("ollama model = %q, want the environment value", got)
	}
}

// A value in a section shadows the DEFAULT section even when it is empty.
func TestEmptySectionValueShadowsTheDefaultSection(t *testing.T) {
	path := writeConfig(t, "[DEFAULT]\nmodel = shared-model\n\n[openai]\nmodel =\n")

	cfg := Load(env(map[string]string{"OPENAI_MODEL": "env-model"}), WithPath(path))

	if got := cfg.Providers().OpenAI.Model; got != "env-model" {
		t.Fatalf("openai model = %q, want the environment value", got)
	}
}

func TestOpenAISessionComesFromTheConfigFile(t *testing.T) {
	path := writeConfig(t, "[openai]\napi_key = file-key\nsession = file-session\n")

	cfg := Load(env(map[string]string{"OPENAI_SESSION": "env-session"}), WithPath(path))

	if got := cfg.Providers().OpenAI.Session; got != "file-session" {
		t.Fatalf("openai session = %q, want the file value", got)
	}
}

func TestOpenAISessionFallsBackToTheEnvironment(t *testing.T) {
	path := writeConfig(t, "[openai]\napi_key = file-key\n")

	cfg := Load(env(map[string]string{"OPENAI_SESSION": "env-session"}), WithPath(path))

	if got := cfg.Providers().OpenAI.Session; got != "env-session" {
		t.Fatalf("openai session = %q, want the environment value", got)
	}
}

func TestOpenAISessionIsEmptyWhenUnset(t *testing.T) {
	path := writeConfig(t, "[openai]\napi_key = file-key\n")

	cfg := Load(env(nil), WithPath(path))

	if got := cfg.Providers().OpenAI.Session; got != "" {
		t.Fatalf("openai session = %q, want no session so the header is not sent", got)
	}
}

func TestInlineCommentsAreNotStripped(t *testing.T) {
	path := writeConfig(t, "[openai]\napi_key = key # not a comment here\n")

	cfg := Load(env(nil), WithPath(path))

	if got := cfg.Providers().OpenAI.APIKey; got != "key # not a comment here" {
		t.Fatalf("openai api key = %q, want the raw value", got)
	}
}

func TestActiveProviderPriority(t *testing.T) {
	tests := []struct {
		name   string
		config string
		env    map[string]string
		want   Provider
	}{
		{name: "nothing configured", config: "", want: ProviderNone},
		{name: "ollama only", config: "[ollama]\nmodel = llama3\n", want: ProviderOllama},
		{name: "anthropic beats ollama", config: "[anthropic]\napi_key = k\n[ollama]\nmodel = llama3\n", want: ProviderAnthropic},
		{name: "openai beats anthropic", config: "[openai]\napi_key = k\n[anthropic]\napi_key = k\n", want: ProviderOpenAI},
		{
			name: "openai key from the environment",
			env:  map[string]string{"OPENAI_API_KEY": "env-key"},
			want: ProviderOpenAI,
		},
		{
			name: "anthropic key from the environment beats ollama from the environment",
			env:  map[string]string{"ANTHROPIC_API_KEY": "env-key", "OLLAMA_MODEL": "llama3"},
			want: ProviderAnthropic,
		},
		{
			name: "ollama model from the environment",
			env:  map[string]string{"OLLAMA_MODEL": "llama3"},
			want: ProviderOllama,
		},
		{name: "openai key alone without a model still selects openai", config: "[openai]\napi_key = k\n", want: ProviderOpenAI},
		{name: "anthropic key without a model still selects anthropic", config: "[anthropic]\napi_key = k\n", want: ProviderAnthropic},
		{name: "ollama model without a key selects ollama", config: "[ollama]\nmodel = llama3\n", want: ProviderOllama},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Load(env(tt.env), WithPath(writeConfig(t, tt.config)))

			if got := cfg.ActiveProvider(); got != tt.want {
				t.Fatalf("ActiveProvider() = %q, want %q", got, tt.want)
			}
			if want := tt.want != ProviderNone; cfg.HasValidConfig() != want {
				t.Fatalf("HasValidConfig() = %v, want %v", cfg.HasValidConfig(), want)
			}
		})
	}
}

func TestExplicitProviderInTheConfigFileWins(t *testing.T) {
	path := writeConfig(t, "[general]\nprovider = ollama\n[openai]\napi_key = k\n[ollama]\nmodel = llama3\n")

	cfg := Load(env(nil), WithPath(path))

	if got := cfg.ActiveProvider(); got != ProviderOllama {
		t.Fatalf("ActiveProvider() = %q, want ollama", got)
	}
}

func TestUnknownExplicitProviderFallsBackToAutoDetection(t *testing.T) {
	path := writeConfig(t, "[general]\nprovider = gemini\n[openai]\napi_key = k\n")

	cfg := Load(env(nil), WithPath(path))

	if got := cfg.ActiveProvider(); got != ProviderOpenAI {
		t.Fatalf("ActiveProvider() = %q, want openai", got)
	}
}

func TestExplicitProviderIsReadOnlyFromTheConfigFile(t *testing.T) {
	path := writeConfig(t, "[ollama]\nmodel = llama3\n")

	cfg := Load(env(map[string]string{"GENERAL_PROVIDER": "openai"}), WithPath(path))

	if got := cfg.ActiveProvider(); got != ProviderOllama {
		t.Fatalf("ActiveProvider() = %q, want ollama; GENERAL_PROVIDER must not select a provider", got)
	}
}

func TestExplicitProviderFromTheDefaultSectionNeedsTheGeneralSection(t *testing.T) {
	present := writeConfig(t, "[DEFAULT]\nprovider = anthropic\n[general]\ndebug = true\n[openai]\napi_key = k\n[anthropic]\napi_key = k\n")
	if got := Load(env(nil), WithPath(present)).ActiveProvider(); got != ProviderAnthropic {
		t.Fatalf("ActiveProvider() = %q, want anthropic", got)
	}

	// Without a [general] section, configparser reports the option as absent and
	// wut falls back to auto-detection.
	absent := writeConfig(t, "[DEFAULT]\nprovider = anthropic\n[openai]\napi_key = k\n[anthropic]\napi_key = k\n")
	if got := Load(env(nil), WithPath(absent)).ActiveProvider(); got != ProviderOpenAI {
		t.Fatalf("ActiveProvider() = %q, want openai", got)
	}
}

func TestDefaultPathUsesTheConfigDirectoryUnderHome(t *testing.T) {
	got := DefaultPath(env(map[string]string{"HOME": "/home/tester"}))

	want := filepath.Join("/home/tester", ".config", "wut", "config")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPathFallsBackToTheUserHomeDirectory(t *testing.T) {
	if _, err := os.UserHomeDir(); err != nil {
		t.Skipf("no user home directory: %v", err)
	}

	got := DefaultPath(env(nil))
	if !filepath.IsAbs(got) {
		t.Fatalf("DefaultPath() = %q, want an absolute path", got)
	}
	if !strings.HasSuffix(got, filepath.Join(".config", "wut", "config")) {
		t.Fatalf("DefaultPath() = %q, want the wut config path", got)
	}
}

func TestLoadWithoutOptionsUsesTheDefaultPath(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "wut"), 0o700); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "wut", "config"), []byte("[openai]\napi_key = home-key\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg := Load(env(map[string]string{"HOME": home}))

	if got := cfg.Providers().OpenAI.APIKey; got != "home-key" {
		t.Fatalf("openai api key = %q, want the value from $HOME/.config/wut/config", got)
	}
}

func TestNilEnvironmentIsTolerated(t *testing.T) {
	cfg := Load(nil, WithPath(writeConfig(t, "[openai]\napi_key = k\n")))

	if got := cfg.Providers().OpenAI.APIKey; got != "k" {
		t.Fatalf("openai api key = %q, want the file value", got)
	}
	if got := cfg.ActiveProvider(); got != ProviderOpenAI {
		t.Fatalf("ActiveProvider() = %q, want openai", got)
	}
}

func TestDuplicateSectionIsReportedAndKeepsParsedValues(t *testing.T) {
	path := writeConfig(t, "[openai]\napi_key = first\n[openai]\napi_key = second\n")

	cfg := Load(env(nil), WithPath(path))

	if cfg.Err() == nil {
		t.Fatal("cfg.Err() = nil, want the duplicate section reported")
	}
	if got := cfg.Providers().OpenAI.APIKey; got != "first" {
		t.Fatalf("openai api key = %q, want the first value", got)
	}
}
