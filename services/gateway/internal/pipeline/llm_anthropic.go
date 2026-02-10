package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
)

// AnthropicLLMClient streams chat completions from the Anthropic Messages API.
type AnthropicLLMClient struct {
	apiKey    string
	url       string
	model     string
	maxTokens int
	client    *http.Client
}

// NewAnthropicLLMClient creates an Anthropic streaming client.
func NewAnthropicLLMClient(apiKey, url, model string, maxTokens, poolSize int) *AnthropicLLMClient {
	return &AnthropicLLMClient{
		apiKey:    apiKey,
		url:       url,
		model:     model,
		maxTokens: maxTokens,
		client:    NewPooledHTTPClient(poolSize, 120*time.Second),
	}
}

func (c *AnthropicLLMClient) Chat(ctx context.Context, userMessage, ragContext, systemPrompt, model string, onToken TokenCallback) (*LLMResult, error) {
	start := time.Now()

	useModel := c.model
	if model != "" {
		useModel = model
	}

	system := systemPrompt
	if ragContext != "" {
		system += "\n\nRelevant context from knowledge base:\n" + ragContext
	}

	body, err := json.Marshal(anthropicRequest{
		Model:     useModel,
		MaxTokens: c.maxTokens,
		Stream:    true,
		System:    system,
		Messages:  []anthropicMessage{{Role: "user", Content: userMessage}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create anthropic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("llm", "http").Inc()
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		metrics.Errors.WithLabelValues("llm", "status").Inc()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("anthropic status %d: %s", resp.StatusCode, errBody)
	}

	sr := consumeAnthropicStream(resp.Body, onToken)

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

func consumeAnthropicStream(body io.Reader, onToken TokenCallback) streamResult {
	var sr streamResult
	scanner := bufio.NewScanner(body)
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if eventType == "message_stop" {
			return sr
		}

		if eventType == "content_block_delta" {
			var delta anthropicDeltaEvent
			if json.Unmarshal([]byte(data), &delta) != nil {
				continue
			}
			if delta.Delta.Type == "thinking_delta" {
				sr.thinking += delta.Delta.Thinking
				continue
			}
			text := delta.Delta.Text
			if text == "" {
				continue
			}
			if sr.ttft.IsZero() {
				sr.ttft = time.Now()
			}
			if onToken != nil {
				onToken(text)
			}
			sr.text += text
		}
	}

	return sr
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicDeltaEvent struct {
	Delta anthropicDelta `json:"delta"`
}

type anthropicDelta struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}
