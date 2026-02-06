package pipeline

import (
	"net/http"
	"time"
)

// NewPooledHTTPClient creates an http.Client with connection pooling and tuned transport.
func NewPooledHTTPClient(poolSize int, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:          poolSize,
			MaxIdleConnsPerHost:   poolSize,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
}
