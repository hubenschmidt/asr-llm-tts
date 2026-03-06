package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// AnthropicClient implements LLMChatClient using the native Anthropic Messages API.
type AnthropicClient struct {
	baseURL    string
	apiKey     string
	maxTokens  int
	httpClient *http.Client
}

// NewAnthropicClient creates an AnthropicClient backed by an HTTP/1.1 transport
// optimised for SSE streaming (HTTP/2 frame buffering adds visible latency).
func NewAnthropicClient(baseURL, apiKey string, maxTokens int) *AnthropicClient {
	return &AnthropicClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		maxTokens: maxTokens,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				ForceAttemptHTTP2:   false, // HTTP/1.1 chunked delivers SSE tokens immediately
				DisableCompression:  true,  // avoid gzip buffering on streamed responses
			},
		},
	}
}

type anthropicReq struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system,omitempty"`
	Messages  []anthropicMsg   `json:"messages"`
	Stream    bool             `json:"stream"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicSSEData struct {
	Type  string          `json:"type"`
	Delta json.RawMessage `json:"delta,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (c *AnthropicClient) Chat(ctx context.Context, userMessage, systemPrompt, model string, onToken TokenCallback) (*LLMResult, error) {
	body, err := json.Marshal(anthropicReq{
		Model:     model,
		MaxTokens: c.maxTokens,
		System:    systemPrompt,
		Messages:  []anthropicMsg{{Role: "user", Content: userMessage}},
		Stream:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	start := time.Now()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic status %d", resp.StatusCode)
	}

	var textBuf strings.Builder
	var ttft time.Time

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		payload := line[len("data: "):]

		var evt anthropicSSEData
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			continue
		}

		if evt.Error != nil {
			return nil, fmt.Errorf("anthropic stream error: %s", evt.Error.Message)
		}

		if evt.Type == "message_stop" {
			break
		}

		if evt.Type != "content_block_delta" {
			continue
		}

		var delta anthropicDelta
		if err := json.Unmarshal(evt.Delta, &delta); err != nil {
			continue
		}

		if ttft.IsZero() {
			ttft = time.Now()
		}

		textBuf.WriteString(delta.Text)
		if onToken != nil {
			onToken(delta.Text)
		}
	}

	latency := time.Since(start)
	ttftMs := float64(0)
	if !ttft.IsZero() {
		ttftMs = float64(ttft.Sub(start).Milliseconds())
	}

	return &LLMResult{
		Text:               textBuf.String(),
		LatencyMs:          float64(latency.Milliseconds()),
		TimeToFirstTokenMs: ttftMs,
	}, nil
}
