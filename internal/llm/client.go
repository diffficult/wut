// Package llm talks to the configured LLM provider. Requests use the standard
// library HTTP client and preserve the request and response shapes of the
// Python implementation: OpenAI chat completions, Anthropic messages, and the
// Ollama chat endpoint.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/diffficult/wut/internal/config"
)

// Provider endpoints and request parameters.
const (
	DefaultOpenAIBaseURL    = "https://api.openai.com/v1"
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
	DefaultOllamaBaseURL    = "http://localhost:11434"
	DefaultAnthropicVersion = "2023-06-01"
	AnthropicMaxTokens      = 1024
	OpenAITemperature       = 0.7
	defaultTimeout          = 2 * time.Minute
	// maxErrorBody bounds how much of a provider error is reported back.
	maxErrorBody = 512
)

// OpenAISessionHeader is the header OpenCode Go requires on every request: it
// carries the client session id used for routing and prompt caching. wut sends
// it only when a session is configured, so other providers see the same
// requests they always did.
const OpenAISessionHeader = "x-opencode-session"

// DefaultQuery is the question used when wut runs without --query.
const DefaultQuery = "Explain the last command's output. Use the previous commands as context, if relevant, but focus on the last command."

// ExplainPrompt is the system prompt used when there is no custom question.
const ExplainPrompt = `<assistant>
You are a command-line assistant whose job is to explain the output of the most recently executed command in the terminal.
Your goal is to help users understand (and potentially fix) things like stack traces, error messages, logs, or any other confusing output from the terminal.
</assistant>

<instructions>
- Receive the last command in the terminal history and the previous commands before it as context.
- Explain the output of the last command.
- Use a clear, concise, and informative tone.
- If the output is an error or warning, e.g. a stack trace or incorrect command, identify the root cause and suggest a fix.
- Otherwise, if the output is something else, e.g. logs or a web response, summarize the key points.
</instructions>

<formatting>
- Use Markdown to format your response.
- Commands (both single and multi-line) should be placed in fenced markdown blocks.
- Code snippets should be placed in fenced markdown blocks.
- Only use bold for warnings or key takeaways.
- Break down your response into digestible parts.
- Keep your response as short as possible. No more than 5 sentences, unless the issue is complex.
</formatting>`

// AnswerPrompt is the system prompt used when --query supplies a question.
const AnswerPrompt = `<assistant>
You are a command-line assistant whose job is to answer the user's question about the most recently executed command in the terminal.
</assistant>

<instructions>
- Receive the last command in the terminal history and the previous commands before it as context.
- Use a clear, concise, and informative tone.
</instructions>

<formatting>
- Use Markdown to format your response.
- Commands (both single and multi-line) should be placed in fenced markdown blocks.
- Code snippets should be placed in fenced markdown blocks.
- Only use bold for warnings or key takeaways.
- Break down your response into digestible parts.
- Keep your response as short as possible. No more than 5 sentences, unless the issue is complex.
</formatting>`

// ErrNoProvider is returned when no provider is configured. The caller reports
// it to the user with the configuration hint.
var ErrNoProvider = errors.New("no valid LLM provider configuration found; create ~/.config/wut/config or set OPENAI_API_KEY, ANTHROPIC_API_KEY, or OLLAMA_MODEL")

// Client calls an LLM provider. The zero value is usable only after the exported
// fields are set; use NewClient.
type Client struct {
	// HTTPClient performs the requests.
	HTTPClient *http.Client
	// OpenAIBaseURL, AnthropicBaseURL and OllamaBaseURL are the provider
	// endpoints. OllamaBaseURL is ignored when OLLAMA_HOST is set.
	OpenAIBaseURL    string
	AnthropicBaseURL string
	OllamaBaseURL    string
	// AnthropicVersion is the anthropic-version request header.
	AnthropicVersion string
	// Getenv reads environment variables such as OLLAMA_HOST.
	Getenv func(string) string
}

// NewClient returns a client with the public provider endpoints. A nil
// httpClient uses a client with a sane timeout.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		HTTPClient:       httpClient,
		OpenAIBaseURL:    DefaultOpenAIBaseURL,
		AnthropicBaseURL: DefaultAnthropicBaseURL,
		OllamaBaseURL:    DefaultOllamaBaseURL,
		AnthropicVersion: DefaultAnthropicVersion,
		Getenv:           os.Getenv,
	}
}

// SystemPrompt returns the system prompt for a query: an empty query asks for
// an explanation, anything else asks for an answer. A whitespace-only query
// still selects the answer prompt, like the Python implementation.
func SystemPrompt(query string) string {
	if query == "" {
		return ExplainPrompt
	}
	return AnswerPrompt
}

// BuildUserMessage joins the terminal context and the question. A blank query
// falls back to DefaultQuery.
func BuildUserMessage(terminalContext string, query string) string {
	if strings.TrimSpace(query) == "" {
		query = DefaultQuery
	}
	return terminalContext + "\n\n" + query
}

// Explain asks the configured provider about the terminal context and returns
// the provider answer.
func (c *Client) Explain(ctx context.Context, cfg *config.Config, terminalContext string, query string) (string, error) {
	if cfg == nil {
		return "", ErrNoProvider
	}

	providers := cfg.Providers()
	system := SystemPrompt(query)
	user := BuildUserMessage(terminalContext, query)

	switch provider := cfg.ActiveProvider(); provider {
	case config.ProviderOpenAI:
		return c.OpenAI(ctx, providers.OpenAI, system, user)
	case config.ProviderAnthropic:
		return c.Anthropic(ctx, providers.Anthropic, system, user)
	case config.ProviderOllama:
		return c.Ollama(ctx, providers.Ollama, system, user)
	default:
		return "", ErrNoProvider
	}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAI calls the OpenAI chat completions endpoint. An empty BaseURL in the
// provider config uses the client default, so a proxy such as a custom
// base_url from the config file is honored.
func (c *Client) OpenAI(ctx context.Context, cfg config.ProviderConfig, system string, user string) (string, error) {
	baseURL := c.OpenAIBaseURL
	if cfg.BaseURL != "" {
		baseURL = cfg.BaseURL
	}

	body := map[string]any{
		"model": cfg.Model,
		"messages": []message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		"temperature": OpenAITemperature,
	}

	headers := map[string]string{}
	if cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + cfg.APIKey
	}
	if cfg.Session != "" {
		headers[OpenAISessionHeader] = cfg.Session
	}

	var response struct {
		Choices []struct {
			Message message `json:"message"`
		} `json:"choices"`
	}
	if err := c.post(ctx, joinURL(baseURL, "chat/completions"), body, headers, &response); err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}

	if len(response.Choices) == 0 {
		return "", errors.New("openai returned no choices")
	}
	return response.Choices[0].Message.Content, nil
}

// Anthropic calls the Anthropic messages endpoint.
func (c *Client) Anthropic(ctx context.Context, cfg config.ProviderConfig, system string, user string) (string, error) {
	body := map[string]any{
		"model":      cfg.Model,
		"max_tokens": AnthropicMaxTokens,
		"system":     system,
		"messages": []message{
			{Role: "user", Content: user},
		},
	}

	headers := map[string]string{"anthropic-version": c.anthropicVersion()}
	if cfg.APIKey != "" {
		headers["x-api-key"] = cfg.APIKey
	}

	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := c.post(ctx, joinURL(c.AnthropicBaseURL, "v1/messages"), body, headers, &response); err != nil {
		return "", fmt.Errorf("anthropic request failed: %w", err)
	}

	if len(response.Content) == 0 {
		return "", errors.New("anthropic returned no content")
	}
	return response.Content[0].Text, nil
}

// Ollama calls the local Ollama chat endpoint with a single non-streamed
// response, which is the shape the Python client aggregated into.
func (c *Client) Ollama(ctx context.Context, cfg config.ProviderConfig, system string, user string) (string, error) {
	body := map[string]any{
		"model": cfg.Model,
		"messages": []message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		"stream": false,
	}

	var response struct {
		Message message `json:"message"`
	}
	if err := c.post(ctx, joinURL(c.ollamaBaseURL(), "api/chat"), body, nil, &response); err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}

	if response.Message.Content == "" {
		return "", errors.New("ollama returned an empty message")
	}
	return response.Message.Content, nil
}

func (c *Client) anthropicVersion() string {
	if c.AnthropicVersion == "" {
		return DefaultAnthropicVersion
	}
	return c.AnthropicVersion
}

// ollamaBaseURL honors OLLAMA_HOST the way the Python client did.
func (c *Client) ollamaBaseURL() string {
	if c.Getenv != nil {
		if host := c.Getenv("OLLAMA_HOST"); host != "" {
			return host
		}
	}
	if c.OllamaBaseURL == "" {
		return DefaultOllamaBaseURL
	}
	return c.OllamaBaseURL
}

// post sends a JSON request and decodes the JSON response into out. A non-2xx
// response is reported with its status and body.
func (c *Client) post(ctx context.Context, url string, body any, headers map[string]string, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("unexpected status %d: %s", res.StatusCode, snippet(payload))
	}

	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// snippet bounds a provider error body to a readable amount.
func snippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > maxErrorBody {
		return text[:maxErrorBody] + "..."
	}
	return text
}

// joinURL appends a path to a base URL without doubling the separator.
func joinURL(baseURL string, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}
