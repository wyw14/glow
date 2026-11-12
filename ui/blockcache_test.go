package ui

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const (
	testStyleDark  = "dark"
	testStyleLight = "light"
	testWidth80    = 80
	testWidth100   = 100
)

// stubRenderer records every block source it renders and returns a
// deterministic fragment, which makes cache hits and misses observable.
type stubRenderer struct {
	mu    sync.Mutex
	calls []string
	err   error
	gate  chan struct{}
}

func (s *stubRenderer) render(source string) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, source)
	gate := s.gate
	s.mu.Unlock()

	if gate != nil {
		<-gate
	}
	if s.err != nil {
		return "", s.err
	}
	return "R(" + source + ")", nil
}

func (s *stubRenderer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *stubRenderer) callCountFor(source string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, call := range s.calls {
		if call == source {
			n++
		}
	}
	return n
}

func TestSplitMarkdownBlocks(t *testing.T) {
	t.Run("empty and whitespace-only documents have no blocks", func(t *testing.T) {
		for _, doc := range []string{"", "\n\n\n", "   \n\t\n  \n"} {
			if blocks := splitMarkdownBlocks(doc); len(blocks) != 0 {
				t.Errorf("expected no blocks for %q, got %d", doc, len(blocks))
			}
		}
	})

	t.Run("blank lines separate blocks and list items stay together", func(t *testing.T) {
		blocks := splitMarkdownBlocks("Alpha\n\n- one\n- two\n\nGamma\n")
		if len(blocks) != 3 {
			t.Fatalf("expected 3 blocks, got %d: %#v", len(blocks), blocks)
		}
		if blocks[0].source != "Alpha" || blocks[1].source != "- one\n- two" || blocks[2].source != "Gamma" {
			t.Errorf("unexpected block sources: %#v", blocks)
		}
		if blocks[0].index != 0 || blocks[1].index != 1 || blocks[2].index != 2 {
			t.Errorf("expected sequential indexes, got %d %d %d", blocks[0].index, blocks[1].index, blocks[2].index)
		}
	})

	t.Run("IDs are content-addressed and duplicates share an ID", func(t *testing.T) {
		blocks := splitMarkdownBlocks("Same\n\nOther\n\nSame")
		if len(blocks) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(blocks))
		}
		if blocks[0].id != blocks[2].id {
			t.Errorf("expected duplicate blocks to share ID %q, got %q", blocks[0].id, blocks[2].id)
		}
		if blocks[0].id == blocks[1].id {
			t.Errorf("expected distinct content to produce distinct IDs")
		}
		if blocks[0].id != blockIDFor("Same") {
			t.Errorf("expected stable content-addressed ID")
		}
	})
}

func TestBlockCacheKeyImmutability(t *testing.T) {
	base := blockCacheKey{blockID: "b1", styleVersion: testStyleDark, width: testWidth80}
	same := blockCacheKey{blockID: "b1", styleVersion: testStyleDark, width: testWidth80}
	if base.String() != same.String() {
		t.Errorf("identical inputs must produce identical keys")
	}

	variants := []blockCacheKey{
		{blockID: "b2", styleVersion: testStyleDark, width: testWidth80},
		{blockID: "b1", styleVersion: testStyleLight, width: testWidth80},
		{blockID: "b1", styleVersion: testStyleDark, width: testWidth100},
	}
	for i, variant := range variants {
		if variant.String() == base.String() {
			t.Errorf("variant %d must produce a distinct immutable key", i)
		}
	}
}

func TestRenderDocumentEmpty(t *testing.T) {
	cache := newBlockRenderCache()
	renderer := &stubRenderer{}

	for _, doc := range []string{"", "\n\n  \n"} {
		out, err := cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render)
		if err != nil {
			t.Fatalf("empty document must not error: %v", err)
		}
		if out != "" {
			t.Errorf("expected empty output for %q, got %q", doc, out)
		}
	}
	if renderer.callCount() != 0 {
		t.Errorf("empty documents must not invoke the renderer, got %d calls", renderer.callCount())
	}
	if stats := cache.Stats(); stats.Entries != 0 {
		t.Errorf("expected no cache entries, got %d", stats.Entries)
	}
}

func TestRenderDocumentCachesUnchangedBlocks(t *testing.T) {
	cache := newBlockRenderCache()
	renderer := &stubRenderer{}
	doc := "Alpha\n\nBeta\n\nGamma"

	first, err := cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render)
	if err != nil {
		t.Fatal(err)
	}
	if renderer.callCount() != 3 {
		t.Fatalf("expected 3 renders on cold cache, got %d", renderer.callCount())
	}

	second, err := cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render)
	if err != nil {
		t.Fatal(err)
	}
	if renderer.callCount() != 3 {
		t.Errorf("expected zero re-renders on warm cache, got %d total", renderer.callCount())
	}
	if first != second {
		t.Errorf("cached output must be stable:\n first=%q\nsecond=%q", first, second)
	}

	stats := cache.Stats()
	if stats.Hits != 3 || stats.Misses != 3 {
		t.Errorf("expected 3 hits and 3 misses, got %+v", stats)
	}
	if stats.Entries != 3 {
		t.Errorf("expected 3 entries, got %d", stats.Entries)
	}
}

func TestIncrementalInvalidationOnLocalEdit(t *testing.T) {
	cache := newBlockRenderCache()
	renderer := &stubRenderer{}

	if _, err := cache.RenderDocument("Alpha\n\nBeta\n\nGamma", testStyleDark, testWidth80, renderer.render); err != nil {
		t.Fatal(err)
	}
	before := renderer.callCount()

	out, err := cache.RenderDocument("Alpha\n\nBeta changed\n\nGamma", testStyleDark, testWidth80, renderer.render)
	if err != nil {
		t.Fatal(err)
	}

	if renderer.callCount() != before+1 {
		t.Errorf("expected only the edited block to re-render, calls went from %d to %d", before, renderer.callCount())
	}
	if renderer.callCountFor("Alpha") != 1 || renderer.callCountFor("Gamma") != 1 {
		t.Errorf("unchanged blocks must render exactly once, calls=%v", renderer.calls)
	}
	if renderer.callCountFor("Beta changed") != 1 {
		t.Errorf("edited block must render once, calls=%v", renderer.calls)
	}

	// Render order is preserved even though storage is an unordered map.
	alphaPos := strings.Index(out, "R(Alpha)")
	betaPos := strings.Index(out, "R(Beta changed)")
	gammaPos := strings.Index(out, "R(Gamma)")
	if !(alphaPos < betaPos && betaPos < gammaPos) {
		t.Errorf("expected order Alpha < Beta < Gamma, got %q", out)
	}

	stats := cache.Stats()
	if stats.Hits != 2 {
		t.Errorf("expected 2 hits for unchanged sibling blocks, got %d", stats.Hits)
	}
}

func TestBlockRemovalEvictsVanishedEntries(t *testing.T) {
	cache := newBlockRenderCache()
	renderer := &stubRenderer{}

	if _, err := cache.RenderDocument("Alpha\n\nBeta\n\nGamma", testStyleDark, testWidth80, renderer.render); err != nil {
		t.Fatal(err)
	}
	betaID := blockIDFor("Beta")

	if _, err := cache.RenderDocument("Alpha\n\nGamma", testStyleDark, testWidth80, renderer.render); err != nil {
		t.Fatal(err)
	}

	stats := cache.Stats()
	if stats.Entries != 2 {
		t.Errorf("expected 2 surviving entries, got %d", stats.Entries)
	}
	if stats.Evictions == 0 {
		t.Errorf("expected the removed block entry to be evicted")
	}
	for _, entry := range cache.snapshotEntries() {
		if entry.key.blockID == betaID {
			t.Errorf("removed block %s must not remain in the cache", betaID)
		}
	}
}

func TestDuplicateBlocksShareEntriesAndKeepOrder(t *testing.T) {
	cache := newBlockRenderCache()
	renderer := &stubRenderer{}

	out, err := cache.RenderDocument("Same\n\nOther\n\nSame", testStyleDark, testWidth80, renderer.render)
	if err != nil {
		t.Fatal(err)
	}

	if renderer.callCountFor("Same") != 1 {
		t.Errorf("duplicate block should render once, got %d calls", renderer.callCountFor("Same"))
	}
	if renderer.callCountFor("Other") != 1 {
		t.Errorf("other block should render once, calls=%v", renderer.calls)
	}
	if cache.Stats().Entries != 2 {
		t.Errorf("expected 2 unique entries, got %d", cache.Stats().Entries)
	}

	parts := strings.Split(out, blockSeparator)
	if len(parts) != 3 || parts[0] != parts[2] || parts[0] != "R(Same)" || parts[1] != "R(Other)" {
		t.Errorf("duplicate blocks must appear twice in document order, got %q", out)
	}
}

func TestStyleVersionSwitchFlushesOldStyle(t *testing.T) {
	cache := newBlockRenderCache()
	styleRenderer := func(style string) BlockRenderer {
		return func(source string) (string, error) {
			return style + ":R(" + source + ")", nil
		}
	}
	doc := "Alpha\n\nBeta"

	dark, err := cache.RenderDocument(doc, testStyleDark, testWidth80, styleRenderer(testStyleDark))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dark, testStyleDark) {
		t.Fatalf("expected dark style output, got %q", dark)
	}

	light, err := cache.RenderDocument(doc, testStyleLight, testWidth80, styleRenderer(testStyleLight))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(light, testStyleDark) {
		t.Errorf("light render must never contain old dark style output: %q", light)
	}
	if !strings.Contains(light, testStyleLight) {
		t.Errorf("expected light style output, got %q", light)
	}

	stats := cache.Stats()
	if stats.Entries != 2 {
		t.Errorf("generation flush should replace entries, got %d", stats.Entries)
	}
	for _, entry := range cache.snapshotEntries() {
		if entry.key.styleVersion != testStyleLight {
			t.Errorf("no old-style entry may survive a theme switch")
		}
	}

	// Switching back cannot resurrect a stale entry: every block is re-rendered.
	again, err := cache.RenderDocument(doc, testStyleDark, testWidth80, styleRenderer(testStyleDark))
	if err != nil {
		t.Fatal(err)
	}
	if again != dark {
		t.Errorf("re-rendered dark output must match the original dark output")
	}
}

func TestWidthChangeFlushesOldWidth(t *testing.T) {
	cache := newBlockRenderCache()
	widthRenderer := func(width int) BlockRenderer {
		return func(source string) (string, error) {
			return fmt.Sprintf("w%d:R(%s)", width, source), nil
		}
	}
	doc := "Alpha\n\nBeta"

	narrow, err := cache.RenderDocument(doc, testStyleDark, testWidth80, widthRenderer(testWidth80))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(narrow, "w80") {
		t.Fatalf("expected width-80 output, got %q", narrow)
	}

	wide, err := cache.RenderDocument(doc, testStyleDark, testWidth100, widthRenderer(testWidth100))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wide, "w80") {
		t.Errorf("resized render must never contain old width output: %q", wide)
	}
	if !strings.Contains(wide, "w100") {
		t.Errorf("expected width-100 output, got %q", wide)
	}
	for _, entry := range cache.snapshotEntries() {
		if entry.key.width != testWidth100 {
			t.Errorf("no old-width entry may survive a resize")
		}
	}
}

func TestCorruptedEntryFallsBackToFreshRender(t *testing.T) {
	cache := newBlockRenderCache()
	renderer := &stubRenderer{}
	doc := "Alpha\n\nBeta"

	original, err := cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate on-disk/in-memory corruption: payload no longer matches checksum.
	var tamperedKey string
	for key, entry := range cache.snapshotEntries() {
		entry.output = "TAMPERED"
		cache.replaceEntry(key, entry)
		tamperedKey = key
		break
	}
	if tamperedKey == "" {
		t.Fatal("expected at least one cached entry")
	}

	recovered, err := cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(recovered, "TAMPERED") {
		t.Errorf("corrupted output must never be served, got %q", recovered)
	}
	if recovered != original {
		t.Errorf("fallback render must reproduce the original output")
	}
	if cache.Stats().Corruptions == 0 {
		t.Errorf("expected corruption to be recorded in stats")
	}

	// A mismatched identity (for example stale width metadata) is rejected too.
	for key, entry := range cache.snapshotEntries() {
		entry.key.width = testWidth100
		cache.replaceEntry(key, entry)
	}
	recovered, err = cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != original {
		t.Errorf("stale-metadata entry must be replaced by a fresh render")
	}
}

func TestInvalidateBlocksIsTargeted(t *testing.T) {
	cache := newBlockRenderCache()
	renderer := &stubRenderer{}
	doc := "Alpha\n\nBeta\n\nGamma"

	if _, err := cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render); err != nil {
		t.Fatal(err)
	}

	removed := cache.InvalidateBlocks(blockIDFor("Beta"))
	if removed != 1 {
		t.Fatalf("expected 1 targeted eviction, got %d", removed)
	}
	if _, err := cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render); err != nil {
		t.Fatal(err)
	}
	if renderer.callCountFor("Beta") != 2 {
		t.Errorf("invalidated block must re-render, calls for Beta=%d", renderer.callCountFor("Beta"))
	}
	if renderer.callCountFor("Alpha") != 1 || renderer.callCountFor("Gamma") != 1 {
		t.Errorf("sibling blocks must stay cached, calls=%v", renderer.calls)
	}
}

func TestInvalidateAllFlushesEverything(t *testing.T) {
	cache := newBlockRenderCache()
	renderer := &stubRenderer{}

	if _, err := cache.RenderDocument("Alpha\n\nBeta", testStyleDark, testWidth80, renderer.render); err != nil {
		t.Fatal(err)
	}
	cache.InvalidateAll()
	if stats := cache.Stats(); stats.Entries != 0 {
		t.Errorf("expected empty cache after InvalidateAll, got %d entries", stats.Entries)
	}

	if _, err := cache.RenderDocument("Alpha\n\nBeta", testStyleDark, testWidth80, renderer.render); err != nil {
		t.Fatal(err)
	}
	if renderer.callCount() != 4 {
		t.Errorf("expected full re-render after flush, got %d total calls", renderer.callCount())
	}
}

func TestRenderEntryGenerationFlush(t *testing.T) {
	cache := newBlockRenderCache()
	calls := 0
	render := func() (string, error) {
		calls++
		return "out", nil
	}

	if _, err := cache.RenderEntry("doc-1", testStyleDark, testWidth80, render); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.RenderEntry("doc-1", testStyleDark, testWidth80, render); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected a cache hit on the second call, got %d renders", calls)
	}

	if _, err := cache.RenderEntry("doc-1", testStyleDark, testWidth100, render); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("width change must force a re-render, got %d renders", calls)
	}
	if stats := cache.Stats(); stats.Entries != 1 {
		t.Errorf("old-width entry must be flushed, got %d entries", stats.Entries)
	}

	if _, err := cache.RenderEntry("doc-1", testStyleLight, testWidth100, render); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("style change must force a re-render, got %d renders", calls)
	}
	if stats := cache.Stats(); stats.Entries != 1 {
		t.Errorf("old-style entry must be flushed, got %d entries", stats.Entries)
	}
}

func TestRendererErrorIsNotCached(t *testing.T) {
	cache := newBlockRenderCache()
	boom := errors.New("render boom")
	failing := &stubRenderer{err: boom}

	_, err := cache.RenderDocument("Alpha\n\nBeta", testStyleDark, testWidth80, failing.render)
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped renderer error, got %v", err)
	}
	if cache.Stats().Entries != 0 {
		t.Errorf("failed renders must not be cached, got %d entries", cache.Stats().Entries)
	}

	healthy := &stubRenderer{}
	out, err := cache.RenderDocument("Alpha\n\nBeta", testStyleDark, testWidth80, healthy.render)
	if err != nil {
		t.Fatalf("retry after error should succeed: %v", err)
	}
	if !strings.Contains(out, "R(Alpha)") || !strings.Contains(out, "R(Beta)") {
		t.Errorf("expected both blocks rendered, got %q", out)
	}
}

func TestConcurrentReadsAndSingleFlight(t *testing.T) {
	t.Run("concurrent documents are race-free and render each key once", func(t *testing.T) {
		cache := newBlockRenderCache()
		renderer := &stubRenderer{}
		doc := "Alpha\n\nBeta\n\nGamma"

		const goroutines = 50
		var wg sync.WaitGroup
		outputs := make([]string, goroutines)
		errs := make([]error, goroutines)
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			i := i
			go func() {
				defer wg.Done()
				outputs[i], errs[i] = cache.RenderDocument(doc, testStyleDark, testWidth80, renderer.render)
			}()
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				t.Fatalf("concurrent render errored: %v", err)
			}
		}
		for _, out := range outputs {
			if out != outputs[0] {
				t.Errorf("all concurrent readers must see identical output")
			}
		}
		if renderer.callCount() != 3 {
			t.Errorf("in-flight coalescing should render 3 blocks, got %d", renderer.callCount())
		}
	})

	t.Run("same-key misses coalesce onto one in-flight render", func(t *testing.T) {
		cache := newBlockRenderCache()
		gate := make(chan struct{})
		started := make(chan struct{}, 16)
		render := func() (string, error) {
			started <- struct{}{}
			<-gate
			return "only-once", nil
		}

		const goroutines = 16
		var wg sync.WaitGroup
		results := make([]string, goroutines)
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			i := i
			go func() {
				defer wg.Done()
				out, err := cache.RenderEntry("b-blocking", testStyleDark, testWidth80, render)
				if err == nil {
					results[i] = out
				}
			}()
		}

		<-started
		select {
		case <-started:
			t.Fatal("second render started while the first was in flight")
		default:
		}
		close(gate)
		wg.Wait()

		for _, out := range results {
			if out != "only-once" {
				t.Errorf("expected every waiter to receive the single render result, got %q", out)
			}
		}
	})

	t.Run("concurrent invalidation never serves stale output", func(t *testing.T) {
		cache := newBlockRenderCache()
		doc := "Alpha\n\nBeta"
		styleRenderer := func(style string) BlockRenderer {
			return func(source string) (string, error) {
				return style + ":R(" + source + ")", nil
			}
		}

		const goroutines = 40
		var wg sync.WaitGroup
		wg.Add(goroutines * 2)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				out, err := cache.RenderDocument(doc, testStyleDark, testWidth80, styleRenderer(testStyleDark))
				if err == nil && strings.Contains(out, testStyleLight) {
					t.Errorf("dark reader observed light output: %q", out)
				}
			}()
			go func() {
				defer wg.Done()
				cache.InvalidateAll()
			}()
		}
		wg.Wait()
	})
}

// snapshotEntries and replaceEntry are test-only helpers for inspecting and
// tampering with cache internals from within the package.
func (c *blockRenderCache) snapshotEntries() map[string]cachedBlock {
	c.mu.RLock()
	defer c.mu.RUnlock()
	copyMap := make(map[string]cachedBlock, len(c.entries))
	for key, entry := range c.entries {
		copyMap[key] = entry
	}
	return copyMap
}

func (c *blockRenderCache) replaceEntry(key string, entry cachedBlock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry
}
