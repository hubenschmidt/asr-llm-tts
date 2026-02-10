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

// OpenAILLMClient streams chat completions from the OpenAI API.
type OpenAILLMClient struct {
	apiKey    string
	url       string
	model     string
	maxTokens int
	client    *http.Client
}

// NewOpenAILLMClient creates an OpenAI-compatible streaming client.
func NewOpenAILLMClient(apiKey, url, model string, maxTokens, poolSize int) *OpenAILLMClient {
	return &OpenAILLMClient{
		apiKey:    apiKey,
		url:       url,
		model:     model,
		maxTokens: maxTokens,
		client:    NewPooledHTTPClient(poolSize, 120*time.Second),
	}
}

func (c *OpenAILLMClient) Chat(ctx context.Context, userMessage, ragContext, systemPrompt, model string, onToken TokenCallback) (*LLMResult, error) {
	start := time.Now()

	useModel := c.model
	if model != "" {
		useModel = model
	}

	messages := []openaiMessage{{Role: "system", Content: systemPrompt}}
	if ragContext != "" {
		messages = append(messages, openaiMessage{Role: "system", Content: "Relevant context from knowledge base:\n" + ragContext})
	}
	messages = append(messages, openaiMessage{Role: "user", Content: userMessage})

	body, err := json.Marshal(openaiRequest{
		Model:     useModel,
		Stream:    true,
		MaxTokens: c.maxTokens,
		Messages:  messages,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create openai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("llm", "http").Inc()
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		metrics.Errors.WithLabelValues("llm", "status").Inc()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("openai status %d: %s", resp.StatusCode, errBody)
	}

	sr := consumeOpenAIStream(resp.Body, onToken)

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

func consumeOpenAIStream(body io.Reader, onToken TokenCallback) streamResult {
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
		var chunk openaiStreamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		content := chunk.Choices[0].Delta.Content
		if content == "" {
			continue
		}
		if sr.ttft.IsZero() {
			sr.ttft = time.Now()
		}
		if onToken != nil {
			onToken(content)
		}
		sr.text += content
	}

	return sr
}

type openaiRequest struct {
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []openaiMessage `json:"messages"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiStreamChunk struct {
	Choices []openaiChoice `json:"choices"`
}

type openaiChoice struct {
	Delta openaiDelta `json:"delta"`
}

type openaiDelta struct {
	Content string `json:"content"`
}
