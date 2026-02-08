package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTPControlManager manages services via lightweight HTTP control servers.
type HTTPControlManager struct {
	httpClient *http.Client
	registry   *Registry
}

// NewHTTPControlManager creates a manager backed by HTTP control endpoints.
func NewHTTPControlManager(registry *Registry) *HTTPControlManager {
	return &HTTPControlManager{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		registry:   registry,
	}
}

// Start launches a service via its HTTP control server.
func (h *HTTPControlManager) Start(ctx context.Context, name string) error {
	meta, ok := h.registry.Lookup(name)
	if !ok {
		return fmt.Errorf("service %q not in registry", name)
	}
	if meta.ControlURL == "" {
		return fmt.Errorf("service %q has no control URL", name)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", meta.ControlURL+"/start", nil)
	if err != nil {
		return err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	resp.Body.Close()
	return nil
}

// Stop kills a service via its HTTP control server.
func (h *HTTPControlManager) Stop(ctx context.Context, name string) error {
	meta, ok := h.registry.Lookup(name)
	if !ok {
		return fmt.Errorf("service %q not in registry", name)
	}
	if meta.ControlURL == "" {
		return fmt.Errorf("service %q has no control URL", name)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", meta.ControlURL+"/stop", nil)
	if err != nil {
		return err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stop %s: %w", name, err)
	}
	resp.Body.Close()
	return nil
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
	if h.probeHealth(ctx, meta.HealthURL) {
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

func (h *HTTPControlManager) probeHealth(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
