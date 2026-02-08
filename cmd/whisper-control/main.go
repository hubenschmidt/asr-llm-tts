package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	port           = envOr("CONTROL_PORT", "8179")
	whisperBin     = envOr("WHISPER_BIN", filepath.Join(os.Getenv("HOME"), ".local/bin/whisper-server"))
	whisperModel   = envOr("WHISPER_MODEL", filepath.Join(os.Getenv("HOME"), ".local/share/whisper/ggml-medium.bin"))
	whisperPort    = envOr("WHISPER_PORT", "8178")
	whisperThreads = envOr("WHISPER_THREADS", "4")
	gpuDevice      = envOr("GPU_DEVICE", "card0")
	modelsDir      = envOr("WHISPER_MODELS_DIR", filepath.Join(os.Getenv("HOME"), ".local/share/whisper"))
)

const modelBaseURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"

var knownModels = []string{
	"ggml-tiny.bin",
	"ggml-tiny.en.bin",
	"ggml-base.bin",
	"ggml-base.en.bin",
	"ggml-small.bin",
	"ggml-small.en.bin",
	"ggml-medium.bin",
	"ggml-medium.en.bin",
	"ggml-large-v2.bin",
	"ggml-large-v3.bin",
	"ggml-large-v3-turbo.bin",
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	mux := http.NewServeMux()
	mux.HandleFunc("POST /start", handleStart)
	mux.HandleFunc("POST /stop", handleStop)
	mux.HandleFunc("GET /status", handleStatus)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /gpu", handleGPU)
	mux.HandleFunc("GET /models", handleListModels)
	mux.HandleFunc("POST /models/download", handleDownloadModel)

	slog.Info("whisper-control listening", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func handleStart(w http.ResponseWriter, r *http.Request) {
	modelPath := whisperModel
	if name := r.URL.Query().Get("model"); name != "" {
		modelPath = filepath.Join(modelsDir, name)
	}
	if isRunning() {
		writeJSON(w, currentGPU("already_running"))
		return
	}
	whisperModel = modelPath
	cmd := fmt.Sprintf(
		"nohup %s -m %s --host 0.0.0.0 --port %s -t %s > /tmp/whisper-server.log 2>&1 &",
		whisperBin, modelPath, whisperPort, whisperThreads,
	)
	out, err := exec.Command("bash", "-c", cmd).CombinedOutput()
	if err != nil {
		slog.Error("start whisper-server", "error", err, "output", string(out))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("waiting for whisper-server health", "port", whisperPort)
	waitForHealth(fmt.Sprintf("http://localhost:%s", whisperPort), 30*time.Second)
	slog.Info("whisper-server ready", "port", whisperPort)
	writeJSON(w, currentGPU("started"))
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	exec.Command("pkill", "-f", whisperBin).Run()
	waitForExit(5 * time.Second)
	slog.Info("whisper-server stopped")
	writeJSON(w, currentGPU("stopped"))
}

// waitForHealth polls a URL until it returns 200 or timeout expires.
func waitForHealth(url string, timeout time.Duration) {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if healthOK(client, url) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	slog.Warn("health check timed out", "url", url)
}

func healthOK(client *http.Client, url string) bool {
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// waitForExit polls until process is no longer running or timeout expires.
func waitForExit(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isRunning() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// currentGPU returns status + GPU snapshot in one response.
func currentGPU(status string) map[string]any {
	gpu := getGPUInfo()
	return map[string]any{
		"status": status,
		"gpu":    gpu,
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"running": isRunning()})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

type gpuInfo struct {
	VRAMTotalMB int          `json:"vram_total_mb"`
	VRAMUsedMB  int          `json:"vram_used_mb"`
	Processes   []gpuProcess `json:"processes"`
}

type gpuProcess struct {
	PID    int    `json:"pid"`
	Name   string `json:"name"`
	VRAMMB int    `json:"vram_mb"`
}

func handleGPU(w http.ResponseWriter, r *http.Request) {
	info := getGPUInfo()
	writeJSON(w, info)
}

func getGPUInfo() gpuInfo {
	info := gpuInfo{Processes: []gpuProcess{}}
	out, err := exec.Command("rocm-smi", "--showmeminfo", "vram", "--json").Output()
	if err != nil {
		slog.Error("rocm-smi failed", "error", err)
		return info
	}
	info.VRAMTotalMB, info.VRAMUsedMB = parseVRAM(out)
	info.Processes = scanGPUProcesses()

	// Add "system" entry for unaccounted VRAM (driver, display server, framebuffers)
	accounted := 0
	for _, p := range info.Processes {
		accounted += p.VRAMMB
	}
	if gap := info.VRAMUsedMB - accounted; gap > 0 {
		info.Processes = append(info.Processes, gpuProcess{PID: 0, Name: "system", VRAMMB: gap})
	}

	slog.Info("gpu response", "vram_total_mb", info.VRAMTotalMB, "vram_used_mb", info.VRAMUsedMB, "processes", len(info.Processes))
	return info
}

func parseVRAM(raw []byte) (totalMB, usedMB int) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	jsonLine := lines[len(lines)-1]
	var data map[string]map[string]string
	if json.Unmarshal([]byte(jsonLine), &data) != nil {
		return 0, 0
	}
	card, ok := data[gpuDevice]
	if !ok {
		return 0, 0
	}
	total, _ := strconv.ParseInt(card["VRAM Total Memory (B)"], 10, 64)
	used, _ := strconv.ParseInt(card["VRAM Total Used Memory (B)"], 10, 64)
	return int(total / (1024 * 1024)), int(used / (1024 * 1024))
}

func scanGPUProcesses() []gpuProcess {
	kfdProc := "/sys/class/kfd/kfd/proc"
	entries, err := os.ReadDir(kfdProc)
	if err != nil {
		return []gpuProcess{}
	}
	procs := []gpuProcess{}
	for _, entry := range entries {
		p := parseGPUProc(kfdProc, entry.Name())
		if p != nil {
			procs = append(procs, *p)
		}
	}
	return procs
}

func parseGPUProc(kfdProc, name string) *gpuProcess {
	pid, err := strconv.Atoi(name)
	if err != nil {
		return nil
	}
	vram := pidVRAM(filepath.Join(kfdProc, name))
	return &gpuProcess{PID: pid, Name: processName(pid), VRAMMB: vram / (1024 * 1024)}
}

func processName(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return strconv.Itoa(pid)
	}
	exe := strings.Split(string(data), "\x00")[0]
	return filepath.Base(exe)
}

func pidVRAM(dir string) int {
	entries, err := filepath.Glob(filepath.Join(dir, "vram_*"))
	if err != nil {
		return 0
	}
	total := 0
	for _, f := range entries {
		total += readVRAMFile(f)
	}
	return total
}

func readVRAMFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return int(v)
}

func isRunning() bool {
	return exec.Command("pgrep", "-f", whisperBin).Run() == nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func handleListModels(w http.ResponseWriter, r *http.Request) {
	type modelInfo struct {
		Name       string `json:"name"`
		Downloaded bool   `json:"downloaded"`
		SizeMB     int    `json:"size_mb"`
	}
	models := make([]modelInfo, 0, len(knownModels))
	for _, name := range knownModels {
		downloaded, sizeMB := modelStatus(name)
		models = append(models, modelInfo{Name: name, Downloaded: downloaded, SizeMB: sizeMB})
	}
	writeJSON(w, map[string]any{
		"models": models,
		"active": filepath.Base(whisperModel),
		"dir":    modelsDir,
	})
}

// progressWriter wraps a file and streams NDJSON progress to the HTTP response.
type progressWriter struct {
	out        *os.File
	w          http.ResponseWriter
	flushFn    func()
	total      int64
	downloaded int64
	lastReport time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.out.Write(p)
	if err != nil {
		return n, err
	}
	pw.downloaded += int64(n)
	if time.Since(pw.lastReport) <= 500*time.Millisecond {
		return n, nil
	}
	json.NewEncoder(pw.w).Encode(map[string]int64{"bytes": pw.downloaded, "total": pw.total})
	pw.flushFn()
	pw.lastReport = time.Now()
	return n, nil
}

func noopFlush() {}

func handleDownloadModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	if !slices.Contains(knownModels, req.Name) {
		http.Error(w, "unknown model", http.StatusBadRequest)
		return
	}
	dest := filepath.Join(modelsDir, req.Name)
	if _, err := os.Stat(dest); err == nil {
		writeJSON(w, map[string]string{"status": "already_downloaded"})
		return
	}

	os.MkdirAll(modelsDir, 0755)
	url := modelBaseURL + req.Name
	slog.Info("downloading whisper model", "name", req.Name, "url", url)

	resp, err := http.Get(url)
	if err != nil {
		http.Error(w, "download request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "download returned "+resp.Status, http.StatusBadGateway)
		return
	}

	out, err := os.Create(dest + ".tmp")
	if err != nil {
		http.Error(w, "create file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	flushFn := noopFlush
	flusher, ok := w.(http.Flusher)
	if ok {
		flushFn = flusher.Flush
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)

	pw := &progressWriter{out: out, w: w, flushFn: flushFn, total: resp.ContentLength, lastReport: time.Now()}
	_, copyErr := io.Copy(pw, resp.Body)
	out.Close()

	if copyErr != nil {
		os.Remove(dest + ".tmp")
		json.NewEncoder(w).Encode(map[string]string{"error": copyErr.Error()})
		return
	}
	os.Rename(dest+".tmp", dest)
	slog.Info("model downloaded", "name", req.Name, "bytes", pw.downloaded)
	json.NewEncoder(w).Encode(map[string]string{"status": "done"})
	flushFn()
}

func modelStatus(name string) (bool, int) {
	info, err := os.Stat(filepath.Join(modelsDir, name))
	if err != nil {
		return false, 0
	}
	return true, int(info.Size() / (1024 * 1024))
}

func envOr(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
