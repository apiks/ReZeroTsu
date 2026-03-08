package httpclient

import (
	"net/http"
	"sync"
	"time"

	"ReZeroTsu/internal/config"
)

const defaultTimeout = 30 * time.Second

var (
	defaultClient     *http.Client
	defaultClientOnce sync.Once

	userAgentMu sync.RWMutex
	userAgent   string
)

func GetUserAgent() string {
	userAgentMu.RLock()
	ua := userAgent
	userAgentMu.RUnlock()
	if ua != "" {
		return ua
	}
	return config.DefaultUserAgent
}

// Default returns a shared HTTP client (30s timeout). Set User-Agent on the request.
func Default() *http.Client {
	defaultClientOnce.Do(func() {
		defaultClient = &http.Client{
			Transport: http.DefaultTransport,
			Timeout:   defaultTimeout,
		}
	})
	return defaultClient
}

// SetUserAgent sets the User-Agent returned by GetUserAgent. Call at startup.
func SetUserAgent(ua string) {
	userAgentMu.Lock()
	defer userAgentMu.Unlock()
	userAgent = ua
}
