package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
)

// LLMClient streams chat completions from Ollama.
type LLMClient struct {
	url          string
	model        string
	systemPrompt string
	maxTokens    int
	client       *http.Client
}

// NewLLMClient creates an Ollama HTTP client.
func NewLLMClient(url, model, systemPrompt string, maxTokens, poolSize int) *LLMClient {
	return &LLMClient{
		url:          url,
		model:        model,
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
		client:       NewPooledHTTPClient(poolSize, 60*time.Second),
	}
}

// LLMResult holds the complete LLM response with timing.
type LLMResult struct {
	Text               string  `json:"text"`
	LatencyMs          float64 `json:"latency_ms"`
	TimeToFirstTokenMs float64 `json:"ttft_ms"`
}

// TokenCallback is called for each streamed token.
type TokenCallback func(token string)

// Chat sends a user message to Ollama and streams the response.
func (c *LLMClient) Chat(ctx context.Context, userMessage string, onToken TokenCallback) (*LLMResult, error) {
	start := time.Now()

	resp, err := c.postChatRequest(ctx, userMessage)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		metrics.Errors.WithLabelValues("llm", "status").Inc()
		return nil, fmt.Errorf("llm status %d", resp.StatusCode)
	}

	fullText, firstTokenTime := c.consumeStream(resp, onToken)

	latency := time.Since(start)
	metrics.StageDuration.WithLabelValues("llm").Observe(latency.Seconds())

	ttft := float64(0)
	if !firstTokenTime.IsZero() {
		ttft = float64(firstTokenTime.Sub(start).Milliseconds())
	}

	return &LLMResult{
		Text:               fullText,
		LatencyMs:          float64(latency.Milliseconds()),
		TimeToFirstTokenMs: ttft,
	}, nil
}

func (c *LLMClient) postChatRequest(ctx context.Context, userMessage string) (*http.Response, error) {
	reqBody := ollamaRequest{
		Model:  c.model,
		Stream: true,
		Options: ollamaOptions{
			NumPredict: c.maxTokens,
		},
		Messages: []ollamaMessage{
			{Role: "system", Content: c.systemPrompt},
			{Role: "user", Content: userMessage},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal llm request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create llm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("llm", "http").Inc()
		return nil, fmt.Errorf("llm request: %w", err)
	}

	return resp, nil
}

func (c *LLMClient) consumeStream(resp *http.Response, onToken TokenCallback) (string, time.Time) {
	var fullText string
	var firstTokenTime time.Time
	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		chunk := c.parseChunk(scanner.Bytes())
		if chunk == nil {
			return fullText, firstTokenTime
		}
		if chunk.Content == "" {
			return fullText, firstTokenTime
		}
		if firstTokenTime.IsZero() {
			firstTokenTime = time.Now()
		}
		fullText += chunk.Content
		if onToken != nil {
			onToken(chunk.Content)
		}
	}

	return fullText, firstTokenTime
}

// parsedChunk is the extracted content from an Ollama stream chunk.
type parsedChunk struct {
	Content string
	Done    bool
}

func (c *LLMClient) parseChunk(data []byte) *parsedChunk {
	var chunk ollamaStreamChunk
	if json.Unmarshal(data, &chunk) != nil {
		return &parsedChunk{}
	}
	if chunk.Done {
		return nil
	}
	return &parsedChunk{Content: chunk.Message.Content}
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []ollamaMessage `json:"messages"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	NumPredict int `json:"num_predict"`
}

type ollamaStreamChunk struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
}
