package orchestrator

import (
	"context"
	"encoding/json"
)

// ServiceStatus represents the lifecycle state of a managed service.
type ServiceStatus string

const (
	StatusStopped  ServiceStatus = "stopped"
	StatusStarting ServiceStatus = "starting"
	StatusRunning  ServiceStatus = "running"
	StatusHealthy  ServiceStatus = "healthy"
	StatusUnknown  ServiceStatus = "unknown"
)

// ServiceInfo holds the current state of a managed service.
type ServiceInfo struct {
	Name     string        `json:"name"`
	Status   ServiceStatus `json:"status"`
	Category string        `json:"category"`
}

// ServiceManager controls the lifecycle of ML services.
// Implementations can target Docker Compose, Kubernetes, ECS, etc.
type ServiceManager interface {
	Start(ctx context.Context, name string) (json.RawMessage, error)
	Stop(ctx context.Context, name string) (json.RawMessage, error)
	Status(ctx context.Context, name string) (*ServiceInfo, error)
	StatusAll(ctx context.Context) ([]ServiceInfo, error)
}
