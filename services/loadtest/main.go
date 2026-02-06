package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	gateway := flag.String("gateway", "ws://gateway:8080/ws/call", "gateway WebSocket URL")
	concurrency := flag.Int("concurrency", 10, "number of concurrent callers")
	duration := flag.Duration("duration", 30*time.Second, "test duration")
	audioDir := flag.String("audio-dir", "/samples", "directory with sample audio files")
	codec := flag.String("codec", "pcm", "audio codec to use")
	ttsEngine := flag.String("tts-engine", "piper", "TTS engine (piper|coqui)")
	flag.Parse()

	files, err := findAudioFiles(*audioDir)
	if err != nil || len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no audio files in %s, generating synthetic audio\n", *audioDir)
		files = nil
	}

	fmt.Printf("Load test: %d concurrent calls for %s\n", *concurrency, *duration)
	fmt.Printf("Gateway: %s | Codec: %s | TTS: %s\n\n", *gateway, *codec, *ttsEngine)

	var mu sync.Mutex
	var results []callResult
	var wg sync.WaitGroup

	deadline := time.Now().Add(*duration)

	for range *concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for time.Now().Before(deadline) {
				r := runCall(*gateway, *codec, *ttsEngine, files)
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	printSummary(results)
}

type callResult struct {
	success bool
	asrMs   float64
	llmMs   float64
	ttsMs   float64
	totalMs float64
	err     string
}

type pipelineMetrics struct {
	Type    string  `json:"type"`
	ASRMs   float64 `json:"asr_ms"`
	LLMMs   float64 `json:"llm_ms"`
	TTSMs   float64 `json:"tts_ms"`
	TotalMs float64 `json:"total_ms"`
}

func runCall(gateway, codec, ttsEngine string, files []string) callResult {
	conn, _, err := websocket.DefaultDialer.Dial(gateway, nil)
	if err != nil {
		return callResult{err: fmt.Sprintf("dial: %v", err)}
	}
	defer conn.Close()

	meta, _ := json.Marshal(map[string]string{
		"codec":      codec,
		"sample_rate": "16000",
		"tts_engine": ttsEngine,
		"mode":       "conversation",
	})
	if err = conn.WriteMessage(websocket.TextMessage, meta); err != nil {
		return callResult{err: fmt.Sprintf("send meta: %v", err)}
	}

	audio := getAudioData(files)
	chunkSize := 640 // 320 samples * 2 bytes = 20ms at 16kHz

	for i := 0; i < len(audio); i += chunkSize {
		end := i + chunkSize
		if end > len(audio) {
			end = len(audio)
		}
		if err = conn.WriteMessage(websocket.BinaryMessage, audio[i:end]); err != nil {
			return callResult{err: fmt.Sprintf("send audio: %v", err)}
		}
		time.Sleep(20 * time.Millisecond)
	}

	conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))

	// Read responses until we get metrics or timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return callResult{err: fmt.Sprintf("read: %v", err)}
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var m pipelineMetrics
		if err = json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.Type != "metrics" {
			continue
		}
		return callResult{
			success: true,
			asrMs:   m.ASRMs,
			llmMs:   m.LLMMs,
			ttsMs:   m.TTSMs,
			totalMs: m.TotalMs,
		}
	}
}

func getAudioData(files []string) []byte {
	if len(files) > 0 {
		data, err := os.ReadFile(files[rand.Intn(len(files))])
		if err == nil {
			return data
		}
	}
	return generateSyntheticAudio(3 * time.Second)
}

func generateSyntheticAudio(dur time.Duration) []byte {
	sampleRate := 16000
	numSamples := int(dur.Seconds()) * sampleRate
	buf := make([]byte, numSamples*2)

	for i := range numSamples {
		t := float64(i) / float64(sampleRate)
		// 440Hz sine wave with some noise to trigger VAD
		sample := math.Sin(2*math.Pi*440*t)*0.3 + (rand.Float64()-0.5)*0.05
		val := int16(sample * math.MaxInt16)
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(val))
	}
	return buf
}

var audioExts = map[string]bool{".wav": true, ".mp3": true, ".ogg": true, ".flac": true}

func findAudioFiles(dir string) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if audioExts[filepath.Ext(e.Name())] {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

func printSummary(results []callResult) {
	var succeeded, failed int
	var asrAll, llmAll, ttsAll, e2eAll []float64

	for _, r := range results {
		if !r.success {
			failed++
			continue
		}
		succeeded++
		asrAll = append(asrAll, r.asrMs)
		llmAll = append(llmAll, r.llmMs)
		ttsAll = append(ttsAll, r.ttsMs)
		e2eAll = append(e2eAll, r.totalMs)
	}

	fmt.Printf("\n=== Load Test Results ===\n")
	fmt.Printf("Calls completed: %d\n", succeeded)
	fmt.Printf("Calls failed:    %d\n", failed)

	if len(asrAll) == 0 {
		fmt.Println("No successful calls to report metrics")
		return
	}

	fmt.Printf("\n%-6s %8s %8s %8s\n", "Stage", "p50", "p95", "p99")
	fmt.Printf("%-6s %8.0fms %8.0fms %8.0fms\n", "ASR", percentile(asrAll, 50), percentile(asrAll, 95), percentile(asrAll, 99))
	fmt.Printf("%-6s %8.0fms %8.0fms %8.0fms\n", "LLM", percentile(llmAll, 50), percentile(llmAll, 95), percentile(llmAll, 99))
	fmt.Printf("%-6s %8.0fms %8.0fms %8.0fms\n", "TTS", percentile(ttsAll, 50), percentile(ttsAll, 95), percentile(ttsAll, 99))
	fmt.Printf("%-6s %8.0fms %8.0fms %8.0fms\n", "E2E", percentile(e2eAll, 50), percentile(e2eAll, 95), percentile(e2eAll, 99))
}

func percentile(data []float64, pct float64) float64 {
	sort.Float64s(data)
	idx := int(math.Ceil(pct/100*float64(len(data)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(data) {
		idx = len(data) - 1
	}
	return data[idx]
}
