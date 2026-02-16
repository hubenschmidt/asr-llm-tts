package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	CallsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "pipeline_calls_active",
		Help: "Currently active call sessions",
	})

	CallsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_calls_total",
		Help: "Total calls processed",
	})

	StageDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "pipeline_stage_duration_seconds",
		Help:    "Per-stage latency",
		Buckets: []float64{0.05, 0.1, 0.2, 0.3, 0.5, 0.8, 1.0, 2.0, 5.0},
	}, []string{"stage"})

	E2EDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "pipeline_e2e_duration_seconds",
		Help:    "End-to-end latency from speech-end to first TTS audio",
		Buckets: []float64{0.1, 0.2, 0.5, 0.8, 1.0, 1.5, 2.0, 3.0, 5.0},
	})

	Errors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pipeline_errors_total",
		Help: "Error counts by stage",
	}, []string{"stage", "error_type"})

	AudioChunks = promauto.NewCounter(prometheus.CounterOpts{
		Name: "audio_chunks_processed_total",
		Help: "Total audio chunks received",
	})

	SpeechSegments = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vad_speech_segments_total",
		Help: "Speech segments detected by VAD",
	})

	EmbeddingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "pipeline_embedding_duration_seconds",
		Help:    "Embedding generation latency",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.5},
	})

	RAGDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "pipeline_rag_duration_seconds",
		Help:    "RAG retrieval latency (embed + search)",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.5},
	})

	ASRNoSpeechProb = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "asr_no_speech_prob",
		Help:    "Whisper no_speech_prob per accepted segment",
		Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	})

	ASRNoiseFiltered = promauto.NewCounter(prometheus.CounterOpts{
		Name: "asr_noise_filtered_total",
		Help: "Transcripts dropped by confidence or noise filter",
	})

	ASRWEREstimate = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "asr_wer_estimate",
		Help: "Latest WER estimate from reference transcript evaluation",
	})
)
