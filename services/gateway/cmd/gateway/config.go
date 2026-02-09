package main

import (
	"os"
	"strconv"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/prompts"
)

type config struct {
	port               string
	ollamaURL          string
	ollamaModel        string
	llmSystemPrompt    string
	llmMaxTokens       int
	piperURL           string
	asrPoolSize        int
	llmPoolSize        int
	ttsPoolSize        int
	maxConcurrentCalls int
	vadConfig          audio.VADConfig
	qdrantURL          string
	qdrantPoolSize     int
	embeddingModel     string
	vectorSize         int
	ragTopK            int
	ragScoreThreshold  float64
	kokoroURL          string
	melottsURL         string
	whisperServerURL   string
	whisperControlURL  string
	elevenlabsAPIKey   string
	elevenlabsVoiceID  string
	elevenlabsModelID  string
}

func loadConfig() config {
	vad := audio.DefaultVADConfig()
	vad.SpeechThresholdDB = envFloat("VAD_SPEECH_THRESHOLD_DB", vad.SpeechThresholdDB)

	return config{
		port:               envStr("GATEWAY_PORT", "8000"),
		ollamaURL:          envStr("OLLAMA_URL", "http://localhost:11434"),
		ollamaModel:        envStr("OLLAMA_MODEL", "llama3.2:3b"),
		llmSystemPrompt:    envStr("LLM_SYSTEM_PROMPT", prompts.DefaultSystem),
		llmMaxTokens:       envInt("LLM_MAX_TOKENS", 150),
		piperURL:           envStr("PIPER_URL", "http://localhost:5100"),
		asrPoolSize:        envInt("ASR_POOL_SIZE", 50),
		llmPoolSize:        envInt("LLM_POOL_SIZE", 50),
		ttsPoolSize:        envInt("TTS_POOL_SIZE", 50),
		maxConcurrentCalls: envInt("MAX_CONCURRENT_CALLS", 100),
		vadConfig:          vad,
		qdrantURL:          envStr("QDRANT_URL", ""),
		qdrantPoolSize:     envInt("QDRANT_POOL_SIZE", 10),
		embeddingModel:     envStr("EMBEDDING_MODEL", "nomic-embed-text"),
		vectorSize:         envInt("VECTOR_SIZE", 768),
		ragTopK:            envInt("RAG_TOP_K", 3),
		ragScoreThreshold:  envFloat("RAG_SCORE_THRESHOLD", 0.7),
		kokoroURL:          envStr("KOKORO_URL", ""),
		melottsURL:         envStr("MELOTTS_URL", ""),
		whisperServerURL:   envStr("WHISPER_SERVER_URL", ""),
		whisperControlURL:  envStr("WHISPER_CONTROL_URL", ""),
		elevenlabsAPIKey:   envStr("ELEVENLABS_API_KEY", ""),
		elevenlabsVoiceID:  envStr("ELEVENLABS_VOICE_ID", "21m00Tcm4TlvDq8ikWAM"),
		elevenlabsModelID:  envStr("ELEVENLABS_MODEL_ID", "eleven_turbo_v2_5"),
	}
}

func envStr(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}

func envInt(key string, fallback int) int {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return fallback
	}
	return f
}
