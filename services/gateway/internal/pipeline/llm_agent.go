package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nlpodyssey/openai-agents-go/agents"
	"github.com/nlpodyssey/openai-agents-go/modelsettings"
	"github.com/openai/openai-go/v2/packages/param"
)

// AgentLLM routes LLM requests to the correct provider using the openai-agents-go SDK.
// Engines registered via RegisterRaw bypass the SDK and use a direct HTTP client.
type AgentLLM struct {
	providers  map[string]agents.ModelProvider
	rawClients map[string]LLMChatClient
	models     map[string]string // engine → default model
	fallback   string
	maxTokens  int
}

// NewAgentLLM creates a new AgentLLM with the given fallback engine and max tokens.
func NewAgentLLM(fallback string, maxTokens int) *AgentLLM {
	return &AgentLLM{
		providers:  make(map[string]agents.ModelProvider),
		rawClients: make(map[string]LLMChatClient),
		models:     make(map[string]string),
		fallback:   fallback,
		maxTokens:  maxTokens,
	}
}

// Register adds an SDK provider and default model for the given engine name.
func (a *AgentLLM) Register(engine string, provider agents.ModelProvider, defaultModel string) {
	a.providers[engine] = provider
	a.models[engine] = defaultModel
}

// RegisterRaw adds a direct HTTP client for engines that bypass the SDK (e.g. completions-only models).
func (a *AgentLLM) RegisterRaw(engine string, client LLMChatClient, defaultModel string) {
	a.rawClients[engine] = client
	a.models[engine] = defaultModel
}

// Engines returns the names of all registered backends.
func (a *AgentLLM) Engines() []string {
	seen := make(map[string]bool, len(a.providers)+len(a.rawClients))
	names := make([]string, 0, len(a.providers)+len(a.rawClients))
	for k := range a.providers {
		seen[k] = true
		names = append(names, k)
	}
	for k := range a.rawClients {
		if !seen[k] {
			names = append(names, k)
		}
	}
	return names
}

// Has reports whether a backend is registered for the given engine name.
func (a *AgentLLM) Has(engine string) bool {
	_, ok := a.providers[engine]
	if ok {
		return true
	}
	_, ok = a.rawClients[engine]
	return ok
}

// Chat streams a completion from the resolved provider.
// Raw clients (registered via RegisterRaw) bypass the SDK entirely.
func (a *AgentLLM) Chat(ctx context.Context, userMessage, systemPrompt, model, engine string, onToken TokenCallback) (*LLMResult, error) {
	if raw, ok := a.rawClients[engine]; ok {
		useModel := model
		if useModel == "" {
			useModel = a.models[engine]
		}
		return raw.Chat(ctx, userMessage, systemPrompt, useModel, onToken)
	}

	provider, useModel, err := a.resolve(engine, model)
	if err != nil {
		return nil, err
	}

	agent := agents.New("assistant").
		WithInstructions(systemPrompt).
		WithModel(useModel).
		WithModelSettings(modelsettings.ModelSettings{
			MaxTokens: param.NewOpt(int64(a.maxTokens)),
		})

	runner := agents.Runner{Config: agents.RunConfig{
		ModelProvider:   provider,
		MaxTurns:        1,
		TracingDisabled: true,
	}}

	start := time.Now()

	events, errCh, err := runner.RunStreamedChan(ctx, agent, userMessage)
	if err != nil {
		return nil, fmt.Errorf("llm stream start: %w", err)
	}

	var textBuf strings.Builder
	var sr streamResult
	for ev := range events {
		handleStreamEvent(ev, &sr, onToken, &textBuf)
	}

	if streamErr := <-errCh; streamErr != nil {
		return nil, fmt.Errorf("llm stream: %w", streamErr)
	}

	latency := time.Since(start)

	ttft := float64(0)
	if !sr.ttft.IsZero() {
		ttft = float64(sr.ttft.Sub(start).Milliseconds())
	}

	return &LLMResult{
		Text:               textBuf.String(),
		LatencyMs:          float64(latency.Milliseconds()),
		TimeToFirstTokenMs: ttft,
	}, nil
}

func handleStreamEvent(ev agents.StreamEvent, sr *streamResult, onToken TokenCallback, textBuf *strings.Builder) {
	raw, ok := ev.(agents.RawResponsesStreamEvent)
	if !ok {
		return
	}
	if raw.Data.Type != "response.output_text.delta" {
		return
	}
	if sr.ttft.IsZero() {
		sr.ttft = time.Now()
	}
	if onToken != nil {
		onToken(raw.Data.Delta)
	}
	textBuf.WriteString(raw.Data.Delta)
}

func (a *AgentLLM) resolve(engine, model string) (agents.ModelProvider, string, error) {
	provider, ok := a.providers[engine]
	if !ok {
		provider, ok = a.providers[a.fallback]
	}
	if !ok {
		return nil, "", fmt.Errorf("no llm provider for engine %q", engine)
	}

	useModel := model
	if useModel != "" {
		return provider, useModel, nil
	}

	useModel = a.models[engine]
	if useModel == "" {
		useModel = a.models[a.fallback]
	}
	return provider, useModel, nil
}
