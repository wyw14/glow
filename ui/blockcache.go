package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

// blockSeparator is the deterministic separator used when assembling cached
// block renders back into a complete document. The injected BlockRenderer owns
// the formatting inside each fragment; the cache only owns ordering and
// inter-block separation.
const blockSeparator = "\n\n"

// BlockRenderer renders a single markdown block. Implementations must be
// deterministic for a given (block source, style version, width) triple so
// that cache hits remain correct.
type BlockRenderer func(blockSource string) (string, error)

// markdownBlock is an ordered, content-addressed slice of a document. The ID
// is derived from the block source only, so an unchanged block keeps the same
// ID after local edits elsewhere in the document. The index exists solely to
// preserve render order; it never participates in the cache key.
type markdownBlock struct {
	index  int
	id     string
	source string
}

// blockCacheKey is the immutable cache identity. Every combination of block
// ID, style version and terminal width produces a distinct key, so an entry
// can never be reused across a theme switch or a terminal resize. Instances
// are never mutated after creation; changed inputs always yield a new key.
type blockCacheKey struct {
	blockID      string
	styleVersion string
	width        int
}

// cacheKeyVersion is bumped if the canonical key serialization ever changes,
// preventing collisions with entries written by older protocol revisions.
const cacheKeyVersion = "v1"

func (k blockCacheKey) String() string {
	h := sha256.New()
	// Hash writes never fail for the standard library's sha256 implementation.
	_, _ = fmt.Fprintf(h, "%s\nblock=%s\nstyle=%s\nwidth=%d",
		cacheKeyVersion, k.blockID, k.styleVersion, k.width)
	return hex.EncodeToString(h.Sum(nil))
}

// cachedBlock is a write-once cache entry. The checksum covers the rendered
// output and is verified on every read, allowing the cache to detect
// corrupted (or externally tampered with) entries instead of serving stale or
// broken content.
type cachedBlock struct {
	key      blockCacheKey
	output   string
	checksum string
}

func checksumOutput(output string) string {
	sum := sha256.Sum256([]byte(output))
	return hex.EncodeToString(sum[:])
}

// blockIDFor returns the content-addressed ID for a block source.
func blockIDFor(source string) string {
	sum := sha256.Sum256([]byte(source))
	return "b" + hex.EncodeToString(sum[:16])
}

// splitMarkdownBlocks splits a document into top-level blocks on runs of
// blank lines. Consecutive non-blank lines (for example list items) stay in
// the same block. Empty and whitespace-only documents yield no blocks.
func splitMarkdownBlocks(document string) []markdownBlock {
	lines := strings.Split(document, "\n")
	blocks := make([]markdownBlock, 0)
	current := make([]string, 0)

	flush := func() {
		if len(current) == 0 {
			return
		}
		source := strings.Join(current, "\n")
		blocks = append(blocks, markdownBlock{
			index:  len(blocks),
			id:     blockIDFor(source),
			source: source,
		})
		current = current[:0]
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()

	return blocks
}

// CacheStats is a point-in-time snapshot of cache counters, intended for
// tests and diagnostics.
type CacheStats struct {
	Hits        int
	Misses      int
	Corruptions int
	Evictions   int
	Entries     int
}

// blockRenderCache is a concurrency-safe incremental cache of rendered
// markdown blocks.
//
// Invalidating entries works at three scopes:
//
//   - A local document update only changes the content-addressed IDs of the
//     edited blocks. Unchanged blocks keep their IDs and stay cache hits,
//     while blocks that disappeared are evicted explicitly.
//   - A style version change (theme switch) or a terminal width change flushes
//     the whole generation up front, making it impossible to serve output
//     rendered with an old style or width.
//   - InvalidateAll forces a full flush, for example when leaving the pager.
//
// Reads scale across goroutines via an RWMutex, and concurrent misses for the
// same key collapse onto a single in-flight render.
type blockRenderCache struct {
	mu          sync.RWMutex
	entries     map[string]cachedBlock
	inflight    map[string]*sync.Mutex
	activeStyle string
	activeWidth int
	hasGen      bool

	hits        int
	misses      int
	corruptions int
	evictions   int
}

// newBlockRenderCache returns an empty render cache.
func newBlockRenderCache() *blockRenderCache {
	return &blockRenderCache{
		entries:  make(map[string]cachedBlock),
		inflight: make(map[string]*sync.Mutex),
	}
}

// Stats returns a snapshot of the cache counters.
func (c *blockRenderCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return CacheStats{
		Hits:        c.hits,
		Misses:      c.misses,
		Corruptions: c.corruptions,
		Evictions:   c.evictions,
		Entries:     len(c.entries),
	}
}

// ensureGeneration flushes every entry whenever the style version or the
// terminal width changes. After this returns, no entry from a previous
// generation remains, so old-style or old-width output can never be served.
func (c *blockRenderCache) ensureGeneration(styleVersion string, width int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasGen && c.activeStyle == styleVersion && c.activeWidth == width {
		return
	}

	c.entries = make(map[string]cachedBlock)
	c.inflight = make(map[string]*sync.Mutex)
	c.activeStyle = styleVersion
	c.activeWidth = width
	c.hasGen = true
}

// InvalidateBlocks evicts every entry belonging to the given block IDs across
// all generations. It returns the number of evicted entries. Unrelated blocks
// remain cached and keep their render order on the next assembly.
func (c *blockRenderCache) InvalidateBlocks(ids ...string) int {
	if len(ids) == 0 {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for _, id := range ids {
		for key, entry := range c.entries {
			if entry.key.blockID == id {
				delete(c.entries, key)
				removed++
			}
		}
	}
	c.evictions += removed
	return removed
}

// InvalidateAll drops every cached entry and pending in-flight render.
func (c *blockRenderCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]cachedBlock)
	c.inflight = make(map[string]*sync.Mutex)
}

// RenderDocument splits the document into ordered blocks, renders each block
// through the cache and assembles the fragments in document order. Blocks
// absent from the new document are evicted; unchanged blocks are served from
// the cache without invoking the renderer. An empty document renders to the
// empty string without calling the renderer.
func (c *blockRenderCache) RenderDocument(
	document, styleVersion string,
	width int,
	render BlockRenderer,
) (string, error) {
	blocks := splitMarkdownBlocks(document)
	if len(blocks) == 0 {
		return "", nil
	}

	fragments := make([]string, len(blocks))
	for i, block := range blocks {
		output, err := c.RenderEntry(
			block.id,
			styleVersion,
			width,
			func() (string, error) {
				return render(block.source)
			},
		)
		if err != nil {
			return "", fmt.Errorf("render block %s: %w", block.id, err)
		}
		fragments[i] = output
	}

	c.evictVanishedBlocks(blocks)
	return strings.Join(fragments, blockSeparator), nil
}

// RenderEntry returns the cached output for the given identity, rendering it
// via the supplied function on a miss. The key binds the block ID, style
// version and width, so a resize or theme change always misses safely.
func (c *blockRenderCache) RenderEntry(
	id, styleVersion string,
	width int,
	render func() (string, error),
) (string, error) {
	// A width or style change flushes the previous generation before any
	// lookup, so an entry rendered with old dimensions or styles can neither
	// be served nor accumulate across resizes.
	c.ensureGeneration(styleVersion, width)

	key := blockCacheKey{
		blockID:      id,
		styleVersion: styleVersion,
		width:        width,
	}
	keyString := key.String()

	if output, ok := c.lookup(keyString, key); ok {
		c.countHit()
		return output, nil
	}
	c.countMiss()

	// Collapse concurrent misses for the same key onto one render. The render
	// itself runs without holding the cache lock.
	gate := c.acquireInflight(keyString)
	defer c.releaseInflight(keyString, gate)

	// Another goroutine may have completed the render while we waited.
	if output, ok := c.lookup(keyString, key); ok {
		c.countHit()
		return output, nil
	}

	output, err := render()
	if err != nil {
		return "", fmt.Errorf("render cache miss for %s: %w", id, err)
	}

	c.store(cachedBlock{
		key:      key,
		output:   output,
		checksum: checksumOutput(output),
	})
	return output, nil
}

// lookup validates the entry fully before returning its output. A checksum or
// identity mismatch is treated as corruption: the entry is evicted and the
// caller falls back to a fresh render, so damaged content is never displayed.
// It is a pure read with respect to the hit/miss counters; callers count the
// outcome once per render attempt.
func (c *blockRenderCache) lookup(keyString string, key blockCacheKey) (string, bool) {
	c.mu.RLock()
	entry, ok := c.entries[keyString]
	c.mu.RUnlock()
	if !ok {
		return "", false
	}

	corrupt := entry.key != key ||
		entry.checksum != checksumOutput(entry.output)

	if corrupt {
		c.evictCorrupt(keyString)
		return "", false
	}

	return entry.output, true
}

func (c *blockRenderCache) countHit() {
	c.mu.Lock()
	c.hits++
	c.mu.Unlock()
}

func (c *blockRenderCache) countMiss() {
	c.mu.Lock()
	c.misses++
	c.mu.Unlock()
}

func (c *blockRenderCache) store(entry cachedBlock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Entries are write-once: only insert when the key is absent, which keeps
	// the immutable-key contract even under racing producers.
	if _, exists := c.entries[entry.key.String()]; !exists {
		c.entries[entry.key.String()] = entry
	}
}

func (c *blockRenderCache) evictCorrupt(keyString string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[keyString]; ok {
		delete(c.entries, keyString)
		c.corruptions++
		c.evictions++
	}
}

func (c *blockRenderCache) acquireInflight(keyString string) *sync.Mutex {
	c.mu.Lock()
	gate, ok := c.inflight[keyString]
	if !ok {
		gate = &sync.Mutex{}
		c.inflight[keyString] = gate
	}
	c.mu.Unlock()
	gate.Lock()
	return gate
}

func (c *blockRenderCache) releaseInflight(keyString string, gate *sync.Mutex) {
	c.mu.Lock()
	delete(c.inflight, keyString)
	c.mu.Unlock()
	gate.Unlock()
}

// evictVanishedBlocks removes entries whose block IDs are no longer part of
// the current document, bounding cache growth after local edits. Entries for
// surviving blocks are untouched and remain valid in document order.
func (c *blockRenderCache) evictVanishedBlocks(blocks []markdownBlock) {
	live := make(map[string]struct{}, len(blocks))
	for _, block := range blocks {
		live[block.id] = struct{}{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for key, entry := range c.entries {
		if _, ok := live[entry.key.blockID]; ok {
			continue
		}
		delete(c.entries, key)
		removed++
	}
	c.evictions += removed
}
