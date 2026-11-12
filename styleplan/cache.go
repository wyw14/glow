package styleplan

import "sync"

// cacheKey identifies a strategy by the capability version it targets and
// the request it honors. All fields are comparable, so the struct is a valid
// map key.
type cacheKey struct {
	version Version
	request Request
}

// Planner caches immutable strategies and invalidates them by capability
// version. It is safe for concurrent use: concurrent reads for the same
// version and request share a single strategy, and a capability change
// evicts every entry produced for an older version.
type Planner struct {
	mu      sync.RWMutex
	current Version
	seen    bool
	entries map[cacheKey]*Strategy
	builds  int64
}

// NewPlanner returns an empty, ready to use strategy planner.
func NewPlanner() *Planner {
	return &Planner{entries: make(map[cacheKey]*Strategy)}
}

// Plan returns the cached strategy for the capabilities and request, or
// builds and caches a new one. When the capability version changes all
// entries for previous versions are discarded before the new strategy is
// built.
func (p *Planner) Plan(caps Capabilities, req Request) *Strategy {
	version := caps.Version()
	key := cacheKey{version: version, request: req}

	p.mu.RLock()
	if p.seen && version == p.current {
		if cached, ok := p.entries[key]; ok {
			p.mu.RUnlock()
			return cached
		}
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Re-check under the exclusive lock: another goroutine may have won the
	// race while we upgraded from the read lock.
	switch {
	case !p.seen || version != p.current:
		// Version-based invalidation: no entry from an older capability
		// snapshot may survive a capability change.
		p.entries = make(map[cacheKey]*Strategy)
		p.current = version
		p.seen = true
	case p.entries[key] != nil:
		return p.entries[key]
	}

	strategy := plan(caps, req)
	p.entries[key] = strategy
	p.builds++
	return strategy
}

// Invalidate drops every cached strategy and forgets the current version.
// The next Plan call starts from an empty cache.
func (p *Planner) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = make(map[cacheKey]*Strategy)
	p.seen = false
	p.builds = 0
}

// Len returns the number of currently cached strategies.
func (p *Planner) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.entries)
}

// Builds returns the number of strategies actually constructed since the
// last invalidation. It exists to verify cache behavior in tests.
func (p *Planner) Builds() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.builds
}

// DefaultPlanner is the process wide planner used by the package level Plan
// helper. Callers needing isolated caches, for example in tests, should use
// NewPlanner instead.
var DefaultPlanner = NewPlanner()

// Plan returns a strategy from the process wide planner.
func Plan(caps Capabilities, req Request) *Strategy {
	return DefaultPlanner.Plan(caps, req)
}
