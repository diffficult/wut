package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffficult/wut/internal/config"
)

type recordedRequest struct {
	method string
	path   string
	header http.Header
	body   map[string]any
	raw    string
}

// newServer starts a test server and returns a client pointed at it. The
// handler never reaches a real provider.
func newServer(t *testing.T, status int, response string) (*Client, *recordedRequest, func()) {
	t.Helper()

	recorded := &recordedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recorded.method = r.Method
		recorded.path = r.URL.Path
		recorded.header = r.Header.Clone()
		recorded.raw = string(body)
		if len(body) > 0 {
			_ = json.Unmarshal(body, &recorded.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))

	client := NewClient(server.Client())
	client.OpenAIBaseURL = server.URL + "/v1"
	client.AnthropicBaseURL = server.URL
	client.OllamaBaseURL = server.URL
	return client, recorded, server.Close
}

func openAIConfig() config.ProviderConfig {
	return config.ProviderConfig{APIKey: "openai-key", Model: "gpt-4o-mini"}
}

func messages(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()

	raw, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %#v, want a list", body["messages"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		message, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("message = %#v, want an object", item)
		}
		out = append(out, message)
	}
	return out
}

func TestOpenAIRequestShape(t *testing.T) {
	client, recorded, closeServer := newServer(t, http.StatusOK,
		`{"choices":[{"message":{"role":"assistant","content":"because of reasons"}}]}`)
	defer closeServer()

	got, err := client.OpenAI(context.Background(), openAIConfig(), ExplainPrompt, "<terminal_history>x</terminal_history>")
	if err != nil {
		t.Fatalf("OpenAI() error = %v", err)
	}
	if got != "because of reasons" {
		t.Fatalf("OpenAI() = %q, want the message content", got)
	}

	if recorded.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", recorded.method)
	}
	if recorded.path != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", recorded.path)
	}
	if got := recorded.header.Get("Authorization"); got != "Bearer openai-key" {
		t.Fatalf("Authorization = %q, want a bearer token", got)
	}
	if got := recorded.header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", got)
	}
	if recorded.body["model"] != "gpt-4o-mini" {
		t.Fatalf("model = %#v, want the configured model", recorded.body["model"])
	}
	if recorded.body["temperature"] != 0.7 {
		t.Fatalf("temperature = %#v, want 0.7", recorded.body["temperature"])
	}

	sent := messages(t, recorded.body)
	if len(sent) != 2 {
		t.Fatalf("messages = %#v, want a system and a user message", sent)
	}
	if sent[0]["role"] != "system" || sent[0]["content"] != ExplainPrompt {
		t.Fatalf("first message = %#v, want the system prompt", sent[0])
	}
	if sent[1]["role"] != "user" || sent[1]["content"] != "<terminal_history>x</terminal_history>" {
		t.Fatalf("second message = %#v, want the user context", sent[1])
	}
}

func TestOpenAIHonorsACustomBaseURL(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.OpenAIBaseURL = server.URL + "/proxy/openai/v1/"

	got, err := client.OpenAI(context.Background(), openAIConfig(), "system", "user")
	if err != nil {
		t.Fatalf("OpenAI() error = %v", err)
	}
	if got != "ok" {
		t.Fatalf("OpenAI() = %q, want ok", got)
	}
	if len(paths) != 1 || paths[0] != "/proxy/openai/v1/chat/completions" {
		t.Fatalf("paths = %v, want the custom base URL to be preserved", paths)
	}
}

func TestOpenAIReportsHTTPErrors(t *testing.T) {
	client, _, closeServer := newServer(t, http.StatusUnauthorized,
		`{"error":{"message":"invalid api key"}}`)
	defer closeServer()

	_, err := client.OpenAI(context.Background(), openAIConfig(), "system", "user")
	if err == nil {
		t.Fatal("OpenAI() error = nil, want the failure reported")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("OpenAI() error = %v, want the status and the provider message", err)
	}
}

func TestOpenAIRejectsAMalformedResponse(t *testing.T) {
	client, _, closeServer := newServer(t, http.StatusOK, `{"choices":`)
	defer closeServer()

	if _, err := client.OpenAI(context.Background(), openAIConfig(), "system", "user"); err == nil {
		t.Fatal("OpenAI() error = nil, want a decoding failure")
	}
}

func TestOpenAIRejectsAResponseWithoutChoices(t *testing.T) {
	client, _, closeServer := newServer(t, http.StatusOK, `{"choices":[]}`)
	defer closeServer()

	_, err := client.OpenAI(context.Background(), openAIConfig(), "system", "user")
	if err == nil {
		t.Fatal("OpenAI() error = nil, want an empty answer to be an error")
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Fatalf("OpenAI() error = %v, want it to name the provider", err)
	}
}

func TestAnthropicRequestShape(t *testing.T) {
	client, recorded, closeServer := newServer(t, http.StatusOK,
		`{"content":[{"type":"text","text":"stack overflow at line 3"}]}`)
	defer closeServer()

	got, err := client.Anthropic(context.Background(),
		config.ProviderConfig{APIKey: "anthropic-key", Model: "claude-3-opus-20240229"},
		ExplainPrompt, "user message")
	if err != nil {
		t.Fatalf("Anthropic() error = %v", err)
	}
	if got != "stack overflow at line 3" {
		t.Fatalf("Anthropic() = %q, want the first content block", got)
	}

	if recorded.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", recorded.method)
	}
	if recorded.path != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", recorded.path)
	}
	if got := recorded.header.Get("x-api-key"); got != "anthropic-key" {
		t.Fatalf("x-api-key = %q, want the configured key", got)
	}
	if got := recorded.header.Get("anthropic-version"); got == "" {
		t.Fatal("anthropic-version = empty, want an API version header")
	}
	if recorded.body["model"] != "claude-3-opus-20240229" {
		t.Fatalf("model = %#v, want the configured model", recorded.body["model"])
	}
	if recorded.body["max_tokens"] != float64(1024) {
		t.Fatalf("max_tokens = %#v, want 1024", recorded.body["max_tokens"])
	}
	if recorded.body["system"] != ExplainPrompt {
		t.Fatalf("system = %#v, want the system prompt as its own field", recorded.body["system"])
	}

	sent := messages(t, recorded.body)
	if len(sent) != 1 {
		t.Fatalf("messages = %#v, want a single user message", sent)
	}
	if sent[0]["role"] != "user" || sent[0]["content"] != "user message" {
		t.Fatalf("user message = %#v, want the user message", sent[0])
	}
}

func TestAnthropicReportsHTTPErrors(t *testing.T) {
	client, _, closeServer := newServer(t, http.StatusBadRequest, `{"error":{"message":"bad model"}}`)
	defer closeServer()

	_, err := client.Anthropic(context.Background(),
		config.ProviderConfig{APIKey: "k", Model: "claude-3-opus-20240229"}, "system", "user")
	if err == nil {
		t.Fatal("Anthropic() error = nil, want the failure reported")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("Anthropic() error = %v, want the status and the provider message", err)
	}
}

func TestAnthropicRejectsAnEmptyContentList(t *testing.T) {
	client, _, closeServer := newServer(t, http.StatusOK, `{"content":[]}`)
	defer closeServer()

	if _, err := client.Anthropic(context.Background(),
		config.ProviderConfig{APIKey: "k", Model: "m"}, "system", "user"); err == nil {
		t.Fatal("Anthropic() error = nil, want an empty answer to be an error")
	}
}

func TestOllamaRequestShape(t *testing.T) {
	client, recorded, closeServer := newServer(t, http.StatusOK,
		`{"model":"llama3","message":{"role":"assistant","content":"permission denied"},"done":true}`)
	defer closeServer()

	got, err := client.Ollama(context.Background(), config.ProviderConfig{Model: "llama3"}, ExplainPrompt, "user message")
	if err != nil {
		t.Fatalf("Ollama() error = %v", err)
	}
	if got != "permission denied" {
		t.Fatalf("Ollama() = %q, want the message content", got)
	}

	if recorded.path != "/api/chat" {
		t.Fatalf("path = %q, want /api/chat", recorded.path)
	}
	if recorded.body["model"] != "llama3" {
		t.Fatalf("model = %#v, want the configured model", recorded.body["model"])
	}
	if recorded.body["stream"] != false {
		t.Fatalf("stream = %#v, want false so the answer arrives in one response", recorded.body["stream"])
	}

	sent := messages(t, recorded.body)
	if len(sent) != 2 {
		t.Fatalf("messages = %#v, want a system and a user message", sent)
	}
	if sent[0]["role"] != "system" || sent[0]["content"] != ExplainPrompt {
		t.Fatalf("first message = %#v, want the system prompt", sent[0])
	}
	if sent[1]["role"] != "user" || sent[1]["content"] != "user message" {
		t.Fatalf("second message = %#v, want the user message", sent[1])
	}
}

func TestOllamaHonorsTheHostEnvironment(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, `{"message":{"content":"ok"}}`)
	}))
	defer server.Close()

	t.Setenv("OLLAMA_HOST", server.URL)
	client := NewClient(server.Client())

	if _, err := client.Ollama(context.Background(), config.ProviderConfig{Model: "llama3"}, "system", "user"); err != nil {
		t.Fatalf("Ollama() error = %v", err)
	}
	if path != "/api/chat" {
		t.Fatalf("path = %q, want /api/chat on OLLAMA_HOST", path)
	}
}

func TestOllamaReportsHTTPErrors(t *testing.T) {
	client, _, closeServer := newServer(t, http.StatusNotFound, `{"error":"model not found"}`)
	defer closeServer()

	_, err := client.Ollama(context.Background(), config.ProviderConfig{Model: "nope"}, "system", "user")
	if err == nil {
		t.Fatal("Ollama() error = nil, want the failure reported")
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("Ollama() error = %v, want the provider message", err)
	}
}

func TestOllamaRejectsAResponseWithoutAMessage(t *testing.T) {
	client, _, closeServer := newServer(t, http.StatusOK, `{"done":true}`)
	defer closeServer()

	if _, err := client.Ollama(context.Background(), config.ProviderConfig{Model: "m"}, "system", "user"); err == nil {
		t.Fatal("Ollama() error = nil, want a missing message to be an error")
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestExplainUsesTheActiveProvider(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		env      map[string]string
		wantPath string
	}{
		{
			name:     "openai by default",
			config:   "[openai]\napi_key = k\n[anthropic]\napi_key = k\n",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "anthropic when only it is configured",
			config:   "[anthropic]\napi_key = k\n",
			wantPath: "/v1/messages",
		},
		{
			name:     "ollama when it is the only provider",
			config:   "[ollama]\nmodel = llama3\n",
			wantPath: "/api/chat",
		},
		{
			name:     "explicit provider wins",
			config:   "[general]\nprovider = ollama\n[openai]\napi_key = k\n[ollama]\nmodel = llama3\n",
			wantPath: "/api/chat",
		},
		{
			name:     "environment credentials",
			env:      map[string]string{"OPENAI_API_KEY": "env-key"},
			wantPath: "/v1/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OLLAMA_HOST", "")
			client, recorded, closeServer := newServer(t, http.StatusOK, "")
			defer closeServer()

			getenv := func(key string) string { return tt.env[key] }
			cfg := config.Load(getenv, config.WithPath(writeConfig(t, tt.config)))

			body := `{"choices":[{"message":{"content":"a"}}],"content":[{"text":"b"}],"message":{"content":"c"}}`
			resetServer(t, client, body, recorded)

			if _, err := client.Explain(context.Background(), cfg, "<terminal_history>x</terminal_history>", ""); err != nil {
				t.Fatalf("Explain() error = %v", err)
			}
			if recorded.path != tt.wantPath {
				t.Fatalf("path = %q, want %q", recorded.path, tt.wantPath)
			}
		})
	}
}

// resetServer points an existing client at a server that answers with the given
// JSON for every provider shape.
func resetServer(t *testing.T, client *Client, body string, recorded *recordedRequest) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		recorded.method = r.Method
		recorded.path = r.URL.Path
		recorded.header = r.Header.Clone()
		recorded.raw = string(payload)
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &recorded.body)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	client.OpenAIBaseURL = server.URL + "/v1"
	client.AnthropicBaseURL = server.URL
	client.OllamaBaseURL = server.URL
}

func TestExplainFailsWithoutAConfiguredProvider(t *testing.T) {
	client, recorded, closeServer := newServer(t, http.StatusOK, `{"choices":[{"message":{"content":"a"}}]}`)
	defer closeServer()

	cfg := config.Load(func(string) string { return "" }, config.WithPath(writeConfig(t, "")))

	_, err := client.Explain(context.Background(), cfg, "<terminal_history>x</terminal_history>", "")
	if err == nil {
		t.Fatal("Explain() error = nil, want a configuration failure")
	}
	if !strings.Contains(err.Error(), "~/.config/wut/config") {
		t.Fatalf("Explain() error = %v, want it to point at the config file", err)
	}
	if recorded.path != "" {
		t.Fatalf("Explain() called %q without a configured provider, want no request", recorded.path)
	}
}

// openAIEnv answers only the OpenAI key, so provider endpoints keep their
// client defaults and no test can reach a real host.
func openAIEnv(key string) string {
	if key == "OPENAI_API_KEY" {
		return "k"
	}
	return ""
}

func TestExplainSendsTheExplainPromptByDefault(t *testing.T) {
	client, recorded, closeServer := newServer(t, http.StatusOK, `{"choices":[{"message":{"content":"a"}}]}`)
	defer closeServer()

	cfg := config.Load(openAIEnv, config.WithPath(writeConfig(t, "[openai]\napi_key = k\n")))

	if _, err := client.Explain(context.Background(), cfg, "<terminal_history>x</terminal_history>", ""); err != nil {
		t.Fatalf("Explain() error = %v", err)
	}

	sent := messages(t, recorded.body)
	if sent[0]["content"] != ExplainPrompt {
		t.Fatalf("system message = %#v, want the explain prompt", sent[0]["content"])
	}
	want := "<terminal_history>x</terminal_history>\n\n" + DefaultQuery
	if sent[1]["content"] != want {
		t.Fatalf("user message = %#v, want the context plus the default query", sent[1]["content"])
	}
}

func TestExplainSendsTheAnswerPromptForACustomQuery(t *testing.T) {
	client, recorded, closeServer := newServer(t, http.StatusOK, `{"choices":[{"message":{"content":"a"}}]}`)
	defer closeServer()

	cfg := config.Load(openAIEnv, config.WithPath(writeConfig(t, "[openai]\napi_key = k\n")))

	if _, err := client.Explain(context.Background(), cfg, "<terminal_history>x</terminal_history>", "why did that fail?"); err != nil {
		t.Fatalf("Explain() error = %v", err)
	}

	sent := messages(t, recorded.body)
	if sent[0]["content"] != AnswerPrompt {
		t.Fatalf("system message = %#v, want the answer prompt", sent[0]["content"])
	}
	want := "<terminal_history>x</terminal_history>\n\nwhy did that fail?"
	if sent[1]["content"] != want {
		t.Fatalf("user message = %#v, want the context plus the query", sent[1]["content"])
	}
}

// A whitespace-only query keeps the answer prompt, like the Python
// implementation, but falls back to the default question.
func TestExplainWhitespaceQueryKeepsTheAnswerPrompt(t *testing.T) {
	client, recorded, closeServer := newServer(t, http.StatusOK, `{"choices":[{"message":{"content":"a"}}]}`)
	defer closeServer()

	cfg := config.Load(openAIEnv, config.WithPath(writeConfig(t, "[openai]\napi_key = k\n")))

	if _, err := client.Explain(context.Background(), cfg, "<terminal_history>x</terminal_history>", "   "); err != nil {
		t.Fatalf("Explain() error = %v", err)
	}

	sent := messages(t, recorded.body)
	if sent[0]["content"] != AnswerPrompt {
		t.Fatalf("system message = %#v, want the answer prompt", sent[0]["content"])
	}
	want := "<terminal_history>x</terminal_history>\n\n" + DefaultQuery
	if sent[1]["content"] != want {
		t.Fatalf("user message = %#v, want the default question text", sent[1]["content"])
	}
}

func TestSystemPromptSelection(t *testing.T) {
	if got := SystemPrompt(""); got != ExplainPrompt {
		t.Fatalf("SystemPrompt(\"\") = %q, want the explain prompt", got)
	}
	if got := SystemPrompt("why?"); got != AnswerPrompt {
		t.Fatalf("SystemPrompt(\"why?\") = %q, want the answer prompt", got)
	}
	if got := SystemPrompt("  "); got != AnswerPrompt {
		t.Fatalf("SystemPrompt(\"  \") = %q, want the answer prompt", got)
	}
}

func TestBuildUserMessage(t *testing.T) {
	if got := BuildUserMessage("ctx", ""); got != "ctx\n\n"+DefaultQuery {
		t.Fatalf("BuildUserMessage() = %q, want the default question", got)
	}
	if got := BuildUserMessage("ctx", "why?"); got != "ctx\n\nwhy?" {
		t.Fatalf("BuildUserMessage() = %q, want the query", got)
	}
}

func TestPromptsKeepThePythonInstructions(t *testing.T) {
	for _, want := range []string{
		"command-line assistant",
		"Explain the output of the last command.",
		"Use Markdown to format your response.",
	} {
		if !strings.Contains(ExplainPrompt, want) {
			t.Fatalf("ExplainPrompt is missing %q", want)
		}
	}
	for _, want := range []string{
		"answer the user's question about the most recently executed command",
		"Use Markdown to format your response.",
	} {
		if !strings.Contains(AnswerPrompt, want) {
			t.Fatalf("AnswerPrompt is missing %q", want)
		}
	}
}

func TestDefaultBaseURLs(t *testing.T) {
	client := NewClient(nil)

	if client.OpenAIBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("openai base url = %q, want the public API", client.OpenAIBaseURL)
	}
	if client.AnthropicBaseURL != "https://api.anthropic.com" {
		t.Fatalf("anthropic base url = %q, want the public API", client.AnthropicBaseURL)
	}
	if client.OllamaBaseURL != "http://localhost:11434" {
		t.Fatalf("ollama base url = %q, want the local daemon", client.OllamaBaseURL)
	}
}

func TestNewClientDefaultsToTheStandardHTTPClient(t *testing.T) {
	client := NewClient(nil)

	if client.HTTPClient == nil {
		t.Fatal("HTTPClient = nil, want a usable default")
	}
	if client.Getenv == nil {
		t.Fatal("Getenv = nil, want a usable default")
	}
}

func TestOpenAIConfigBaseURLOverridesTheDefault(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	cfg := openAIConfig()
	cfg.BaseURL = server.URL + "/custom/v1"

	if _, err := client.OpenAI(context.Background(), cfg, "system", "user"); err != nil {
		t.Fatalf("OpenAI() error = %v", err)
	}
	if path != "/custom/v1/chat/completions" {
		t.Fatalf("path = %q, want the configured base URL", path)
	}
}
