package orchestrator

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
