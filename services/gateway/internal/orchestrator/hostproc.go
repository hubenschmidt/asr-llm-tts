package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ServiceStatus represents the lifecycle state of a managed service.
type ServiceStatus string

const (
	StatusStopped ServiceStatus = "stopped"
	StatusRunning ServiceStatus = "running"
	StatusHealthy ServiceStatus = "healthy"
)

// ServiceInfo holds the current state of a managed service.
type ServiceInfo struct {
	Name     string        `json:"name"`
	Status   ServiceStatus `json:"status"`
	Category string        `json:"category"`
}

// ServiceMeta holds static metadata for a managed service.
type ServiceMeta struct {
	Category   string // "tts" or "stt"
	HealthURL  string // URL to probe for readiness
	ControlURL string // URL of HTTP control server for start/stop/status
}

// Registry is a whitelist of services the orchestrator may manage.
type Registry struct {
	services map[string]ServiceMeta
}

// NewRegistry creates a registry from a map of service metadata.
func NewRegistry(services map[string]ServiceMeta) *Registry {
	return &Registry{services: services}
}

// Lookup returns metadata for a service, or false if not whitelisted.
func (r *Registry) Lookup(name string) (ServiceMeta, bool) {
	m, ok := r.services[name]
	return m, ok
}

// Names returns all registered service names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.services))
	for k := range r.services {
		names = append(names, k)
	}
	return names
}

// HTTPControlManager manages ML services via lightweight HTTP control servers.
type HTTPControlManager struct {
	httpClient *http.Client
	registry   *Registry
}

// NewHTTPControlManager creates a manager backed by HTTP control endpoints.
func NewHTTPControlManager(registry *Registry) *HTTPControlManager {
	return &HTTPControlManager{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		registry:   registry,
	}
}

// Start launches a service via its HTTP control server.
// Returns the raw GPU JSON from the control server response.
// Optional params are appended as query string (e.g. "model=ggml-large-v3.bin").
func (h *HTTPControlManager) Start(ctx context.Context, name string, params ...string) (json.RawMessage, error) {
	meta, ok := h.registry.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("service %q not in registry", name)
	}
	if meta.ControlURL == "" {
		return nil, fmt.Errorf("service %q has no control URL", name)
	}
	url := meta.ControlURL + "/start"
	if len(params) > 0 {
		url += "?" + params[0]
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	defer resp.Body.Close()
	return extractGPU(resp)
}

// Stop kills a service via its HTTP control server.
// Returns the raw GPU JSON from the control server response.
func (h *HTTPControlManager) Stop(ctx context.Context, name string) (json.RawMessage, error) {
	meta, ok := h.registry.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("service %q not in registry", name)
	}
	if meta.ControlURL == "" {
		return nil, fmt.Errorf("service %q has no control URL", name)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", meta.ControlURL+"/stop", nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stop %s: %w", name, err)
	}
	defer resp.Body.Close()
	return extractGPU(resp)
}

func extractGPU(resp *http.Response) (json.RawMessage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		GPU json.RawMessage `json:"gpu"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, nil
	}
	return envelope.GPU, nil
}

// Status returns the current state of a service.
func (h *HTTPControlManager) Status(ctx context.Context, name string) (*ServiceInfo, error) {
	meta, ok := h.registry.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("service %q not in registry", name)
	}
	info := &ServiceInfo{Name: name, Category: meta.Category, Status: StatusStopped}

	if meta.ControlURL == "" {
		return info, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", meta.ControlURL+"/status", nil)
	if err != nil {
		return info, nil
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return info, nil
	}
	defer resp.Body.Close()

	var result struct {
		Running bool `json:"running"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.Running {
		return info, nil
	}

	info.Status = StatusRunning

	if meta.HealthURL == "" {
		return info, nil
	}
	if probeHealth(ctx, h.httpClient, meta.HealthURL) {
		info.Status = StatusHealthy
	}
	return info, nil
}

// StatusAll returns the status of every registered service.
func (h *HTTPControlManager) StatusAll(ctx context.Context) ([]ServiceInfo, error) {
	names := h.registry.Names()
	results := make([]ServiceInfo, 0, len(names))
	for _, name := range names {
		info, _ := h.Status(ctx, name)
		results = append(results, *info)
	}
	return results, nil
}

// probeHealth sends a GET to the given URL and returns true if the response is 200 OK.
func probeHealth(ctx context.Context, client *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
