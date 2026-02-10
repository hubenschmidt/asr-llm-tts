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

// OpenAICompletionsClient streams from the /v1/completions endpoint
// for models (like codex) that don't support chat completions.
type OpenAICompletionsClient struct {
	apiKey    string
	url       string
	model     string
	maxTokens int
	client    *http.Client
}

// NewOpenAICompletionsClient creates a client for the OpenAI completions API.
func NewOpenAICompletionsClient(apiKey, url, model string, maxTokens, poolSize int) *OpenAICompletionsClient {
	return &OpenAICompletionsClient{
		apiKey:    apiKey,
		url:       url,
		model:     model,
		maxTokens: maxTokens,
		client:    NewPooledHTTPClient(poolSize, 120*time.Second),
	}
}

func (c *OpenAICompletionsClient) Chat(ctx context.Context, userMessage, ragContext, systemPrompt, model string, onToken TokenCallback) (*LLMResult, error) {
	start := time.Now()

	useModel := c.model
	if model != "" {
		useModel = model
	}

	prompt := systemPrompt + "\n"
	if ragContext != "" {
		prompt += "Relevant context from knowledge base:\n" + ragContext + "\n"
	}
	prompt += "User: " + userMessage + "\nAssistant:"

	body, err := json.Marshal(map[string]any{
		"model":      useModel,
		"prompt":     prompt,
		"max_tokens": c.maxTokens,
		"stream":     true,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal completions request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create completions request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("llm", "http").Inc()
		return nil, fmt.Errorf("completions request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		metrics.Errors.WithLabelValues("llm", "status").Inc()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("completions status %d: %s", resp.StatusCode, errBody)
	}

	sr := consumeCompletionsStream(resp.Body, onToken)

	latency := time.Since(start)
	metrics.StageDuration.WithLabelValues("llm").Observe(latency.Seconds())

	ttft := float64(0)
	if !sr.ttft.IsZero() {
		ttft = float64(sr.ttft.Sub(start).Milliseconds())
	}

	return &LLMResult{
		Text:               sr.text,
		LatencyMs:          float64(latency.Milliseconds()),
		TimeToFirstTokenMs: ttft,
	}, nil
}

func consumeCompletionsStream(body io.Reader, onToken TokenCallback) streamResult {
	var sr streamResult
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return sr
		}
		var chunk struct {
			Choices []struct {
				Text string `json:"text"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		text := chunk.Choices[0].Text
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

	return sr
}
