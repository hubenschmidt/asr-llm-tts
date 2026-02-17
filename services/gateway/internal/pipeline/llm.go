package pipeline

import (
	"context"
	"time"
)

// LLMChatClient produces streaming chat completions from a user message.
type LLMChatClient interface {
	Chat(ctx context.Context, userMessage, systemPrompt, model string, onToken TokenCallback) (*LLMResult, error)
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

type streamResult struct {
	ttft time.Time
}
