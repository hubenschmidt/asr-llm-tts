package pipeline

import "fmt"

// Router is a generic backend dispatcher that maps engine names to backend implementations.
// It provides O(1) lookup by name with a configurable fallback default.
type Router[T any] struct {
	backends map[string]T
	fallback string
}

// NewRouter creates a router with the given backends and a fallback engine name
// used when the requested engine is not found.
func NewRouter[T any](backends map[string]T, fallback string) *Router[T] {
	return &Router[T]{backends: backends, fallback: fallback}
}

// Route returns the backend for the given engine name, falling back to the default.
func (r *Router[T]) Route(engine string) (T, error) {
	if backend, ok := r.backends[engine]; ok {
		return backend, nil
	}
	if backend, ok := r.backends[r.fallback]; ok {
		return backend, nil
	}
	var zero T
	return zero, fmt.Errorf("no backend for engine %q", engine)
}

// Has reports whether the router has a backend for the given engine name.
func (r *Router[T]) Has(engine string) bool {
	_, ok := r.backends[engine]
	return ok
}

// Engines returns the names of all registered backends.
func (r *Router[T]) Engines() []string {
	names := make([]string, 0, len(r.backends))
	for k := range r.backends {
		names = append(names, k)
	}
	return names
}
