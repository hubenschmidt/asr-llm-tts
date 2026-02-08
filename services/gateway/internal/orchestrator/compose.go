package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ComposeManager manages Docker Compose services via the docker CLI.
type ComposeManager struct {
	composePath string
	envFile     string
	projectName string
	registry    *Registry
	httpClient  *http.Client
}

// NewComposeManager creates a manager that shells out to docker compose.
func NewComposeManager(composePath, envFile, projectName string, registry *Registry) *ComposeManager {
	return &ComposeManager{
		composePath: composePath,
		envFile:     envFile,
		projectName: projectName,
		registry:    registry,
		httpClient:  &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *ComposeManager) composeArgs(args ...string) []string {
	base := []string{"compose", "-f", c.composePath, "--env-file", c.envFile, "-p", c.projectName}
	return append(base, args...)
}

// PullAll pre-pulls images for all registered services without starting them.
func (c *ComposeManager) PullAll(ctx context.Context) {
	names := c.registry.Names()
	slog.Info("pre-pulling ML service images", "count", len(names))
	args := c.composeArgs(append([]string{"pull"}, names...)...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("pre-pull failed (images will be pulled on first start)", "error", err, "output", string(out))
		return
	}
	slog.Info("all ML service images pulled")
}

// Start launches a Docker Compose service.
func (c *ComposeManager) Start(ctx context.Context, name string) error {
	_, ok := c.registry.Lookup(name)
	if !ok {
		return fmt.Errorf("service %q not in registry", name)
	}

	slog.Info("starting service", "name", name)
	args := c.composeArgs("up", "-d", "--force-recreate", name)
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compose up %s: %w: %s", name, err, string(out))
	}
	slog.Info("service started", "name", name)
	return nil
}

// Stop halts a Docker Compose service.
func (c *ComposeManager) Stop(ctx context.Context, name string) error {
	_, ok := c.registry.Lookup(name)
	if !ok {
		return fmt.Errorf("service %q not in registry", name)
	}

	slog.Info("stopping service", "name", name)
	args := c.composeArgs("stop", name)
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compose stop %s: %w: %s", name, err, string(out))
	}
	slog.Info("service stopped", "name", name)
	return nil
}

// Status returns the current state of a single service.
func (c *ComposeManager) Status(ctx context.Context, name string) (*ServiceInfo, error) {
	meta, ok := c.registry.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("service %q not in registry", name)
	}

	info := &ServiceInfo{
		Name:     name,
		Category: meta.Category,
		Status:   StatusStopped,
	}

	state, err := c.containerState(ctx, name)
	if err != nil {
		return info, nil // container doesn't exist = stopped
	}

	if state != "running" {
		info.Status = StatusStarting
		return info, nil
	}

	info.Status = StatusRunning

	if meta.HealthURL == "" {
		return info, nil
	}

	if c.probeHealth(ctx, meta.HealthURL) {
		info.Status = StatusHealthy
	}

	return info, nil
}

// StatusAll returns the status of every registered service.
func (c *ComposeManager) StatusAll(ctx context.Context) ([]ServiceInfo, error) {
	names := c.registry.Names()
	results := make([]ServiceInfo, 0, len(names))
	for _, name := range names {
		info, _ := c.Status(ctx, name)
		results = append(results, *info)
	}
	return results, nil
}

type composePSEntry struct {
	State string `json:"State"`
}

func (c *ComposeManager) containerState(ctx context.Context, name string) (string, error) {
	args := c.composeArgs("ps", "--format", "json", name)
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", fmt.Errorf("no container for %s", name)
	}

	var entry composePSEntry
	if err = json.Unmarshal([]byte(trimmed), &entry); err != nil {
		return "", fmt.Errorf("parse compose ps: %w", err)
	}

	return strings.ToLower(entry.State), nil
}

func (c *ComposeManager) probeHealth(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
