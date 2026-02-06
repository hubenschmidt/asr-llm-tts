package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var modelDir = "/models"

type synthRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
}

func main() {
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/synthesize", handleSynthesize)

	log.Println("piper-server listening on :5100")
	log.Fatal(http.ListenAndServe(":5100", nil))
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte(`{"status":"ok"}`))
}

func handleSynthesize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req synthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	voice := resolveVoice(req.Voice)

	audioData, err := runPiper(req.Text, voice)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "audio/wav")
	w.Write(audioData)
}

func resolveVoice(voice string) string {
	if voice != "" {
		return voice
	}
	if env := os.Getenv("PIPER_VOICE"); env != "" {
		return env
	}
	return "en_US-lessac-medium"
}

func runPiper(text, voice string) ([]byte, error) {
	modelPath := filepath.Join(modelDir, voice+".onnx")
	configPath := filepath.Join(modelDir, voice+".onnx.json")

	tmpFile, err := os.CreateTemp("", "piper-*.wav")
	if err != nil {
		return nil, fmt.Errorf("temp file: %w", err)
	}
	outPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(outPath)

	cmd := exec.Command("/usr/local/bin/piper",
		"--model", modelPath,
		"--config", configPath,
		"--output_file", outPath,
	)
	cmd.Stdin = strings.NewReader(text)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("piper: %v\n%s", err, output)
	}

	return os.ReadFile(outPath)
}
