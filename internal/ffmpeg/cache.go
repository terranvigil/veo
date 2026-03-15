package ffmpeg

import (
	"context"
	"sync"
)

// ProbeCache caches probe results by file path to avoid redundant ffprobe calls.
// Thread-safe for concurrent use.
type ProbeCache struct {
	mu    sync.RWMutex
	cache map[string]*ProbeResult
}

// NewProbeCache creates a new probe cache.
func NewProbeCache() *ProbeCache {
	return &ProbeCache{
		cache: make(map[string]*ProbeResult),
	}
}

// Probe returns cached result or calls ffprobe and caches the result.
func (c *ProbeCache) Probe(ctx context.Context, path string) (*ProbeResult, error) {
	c.mu.RLock()
	if result, ok := c.cache[path]; ok {
		c.mu.RUnlock()
		return result, nil
	}
	c.mu.RUnlock()

	result, err := Probe(ctx, path)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[path] = result
	c.mu.Unlock()

	return result, nil
}
