package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	mux := http.NewServeMux()
	mux.HandleFunc("POST /start", handleStart)
	mux.HandleFunc("POST /stop", handleStop)
	mux.HandleFunc("GET /status", handleStatus)
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /gpu", handleGPU)

	slog.Info("whisper-control listening", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func handleStart(w http.ResponseWriter, r *http.Request) {
	if isRunning() {
		writeJSON(w, currentGPU("already_running"))
		return
	}
	cmd := fmt.Sprintf(
		"nohup %s -m %s --host 0.0.0.0 --port %s -t %s > /tmp/whisper-server.log 2>&1 &",
		whisperBin, whisperModel, whisperPort, whisperThreads,
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
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	slog.Warn("health check timed out", "url", url)
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
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		name := processName(pid)
		vram := pidVRAM(filepath.Join(kfdProc, entry.Name()))
		procs = append(procs, gpuProcess{PID: pid, Name: name, VRAMMB: vram / (1024 * 1024)})
	}
	return procs
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
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil || v < 0 {
			continue
		}
		total += int(v)
	}
	return total
}

func isRunning() bool {
	return exec.Command("pgrep", "-f", whisperBin).Run() == nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func envOr(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
