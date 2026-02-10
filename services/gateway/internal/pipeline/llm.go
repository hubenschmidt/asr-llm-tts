package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
)

// LLMChatClient produces streaming chat completions from a user message.
type LLMChatClient interface {
	Chat(ctx context.Context, userMessage, ragContext, systemPrompt, model string, onToken TokenCallback) (*LLMResult, error)
}

// LLMResult holds the complete LLM response with timing.
type LLMResult struct {
	Text               string  `json:"text"`
	Thinking           string  `json:"thinking,omitempty"`
	LatencyMs          float64 `json:"latency_ms"`
	TimeToFirstTokenMs float64 `json:"ttft_ms"`
}

// TokenCallback is called for each streamed token.
type TokenCallback func(token string)

// LLMRouter dispatches to the correct LLM backend based on engine name.
type LLMRouter struct {
	*Router[LLMChatClient]
}

// NewLLMRouter creates a router with registered LLM backends and a fallback default.
func NewLLMRouter(backends map[string]LLMChatClient, fallback string) *LLMRouter {
	return &LLMRouter{Router: NewRouter(backends, fallback)}
}

// Chat routes to the correct backend and streams a chat completion.
func (r *LLMRouter) Chat(ctx context.Context, userMessage, ragContext, systemPrompt, model, engine string, onToken TokenCallback) (*LLMResult, error) {
	backend, err := r.Route(engine)
	if err != nil {
		return nil, err
	}
	return backend.Chat(ctx, userMessage, ragContext, systemPrompt, model, onToken)
}

// --- Ollama backend ---

// OllamaLLMClient streams chat completions from Ollama.
type OllamaLLMClient struct {
	url          string
	model        string
	systemPrompt string
	maxTokens    int
	client       *http.Client
}

// NewOllamaLLMClient creates an Ollama HTTP client.
func NewOllamaLLMClient(url, model, systemPrompt string, maxTokens, poolSize int) *OllamaLLMClient {
	return &OllamaLLMClient{
		url:          url,
		model:        model,
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
		client:       NewPooledHTTPClient(poolSize, 60*time.Second),
	}
}

// Chat sends a user message to Ollama and streams the response.
func (c *OllamaLLMClient) Chat(ctx context.Context, userMessage, ragContext, systemPrompt, model string, onToken TokenCallback) (*LLMResult, error) {
	start := time.Now()

	resp, err := c.postChatRequest(ctx, userMessage, ragContext, systemPrompt, model)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		metrics.Errors.WithLabelValues("llm", "status").Inc()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama status %d: %s", resp.StatusCode, body)
	}

	sr := c.consumeStream(resp, onToken)

	latency := time.Since(start)
	metrics.StageDuration.WithLabelValues("llm").Observe(latency.Seconds())

	ttft := float64(0)
	if !sr.ttft.IsZero() {
		ttft = float64(sr.ttft.Sub(start).Milliseconds())
	}

	return &LLMResult{
		Text:               sr.text,
		Thinking:           sr.thinking,
		LatencyMs:          float64(latency.Milliseconds()),
		TimeToFirstTokenMs: ttft,
	}, nil
}

func (c *OllamaLLMClient) postChatRequest(ctx context.Context, userMessage, ragContext, systemPrompt, model string) (*http.Response, error) {
	sysPrompt := c.systemPrompt
	if systemPrompt != "" {
		sysPrompt = systemPrompt
	}
	useModel := c.model
	if model != "" {
		useModel = model
	}
	messages := []ollamaMessage{
		{Role: "system", Content: sysPrompt},
	}
	if ragContext != "" {
		messages = append(messages, ollamaMessage{Role: "system", Content: "Relevant context from knowledge base:\n" + ragContext})
	}
	messages = append(messages, ollamaMessage{Role: "user", Content: userMessage})

	reqBody := ollamaRequest{
		Model:    useModel,
		Stream:   true,
		Options:  ollamaOptions{NumPredict: c.maxTokens},
		Messages: messages,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("llm", "http").Inc()
		return nil, fmt.Errorf("ollama request: %w", err)
	}

	return resp, nil
}

type streamResult struct {
	text     string
	thinking string
	ttft     time.Time
}

func (c *OllamaLLMClient) consumeStream(resp *http.Response, onToken TokenCallback) streamResult {
	var sr streamResult
	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		chunk := c.parseChunk(scanner.Bytes())
		if chunk == nil {
			return sr
		}
		sr = applyChunk(chunk, sr, onToken)
	}

	return sr
}

func applyChunk(chunk *parsedChunk, sr streamResult, onToken TokenCallback) streamResult {
	if chunk.Thinking != "" {
		sr.thinking += chunk.Thinking
		return sr
	}
	if chunk.Content == "" {
		return sr
	}
	if sr.ttft.IsZero() {
		sr.ttft = time.Now()
	}
	if onToken != nil {
		onToken(chunk.Content)
	}
	sr.text += chunk.Content
	return sr
}

type parsedChunk struct {
	Content  string
	Thinking string
	Done     bool
}

func (c *OllamaLLMClient) parseChunk(data []byte) *parsedChunk {
	var chunk ollamaStreamChunk
	if json.Unmarshal(data, &chunk) != nil {
		return &parsedChunk{}
	}
	if chunk.Done {
		return nil
	}
	return &parsedChunk{Content: chunk.Message.Content, Thinking: chunk.Message.Thinking}
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []ollamaMessage `json:"messages"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
}

type ollamaOptions struct {
	NumPredict int `json:"num_predict"`
}

type ollamaStreamChunk struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
}
