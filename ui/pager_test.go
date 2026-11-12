package ui

import (
	"strings"
	"testing"
)

func TestGlamourRenderTextSizing(t *testing.T) {
	t.Setenv("GLOW_TEXT_SIZING", "on")

	common := &commonModel{
		cfg: Config{
			GlamourEnabled:  true,
			GlamourStyle:    "dark",
			GlamourMaxWidth: 80,
		},
		styles: newStyles(true),
		width:  80,
		height: 24,
	}
	m := newPagerModel(common)
	m.currentDocument = markdown{Note: "test.md"}
	m.setSize(80, 24)

	out, err := glamourRender(m, "# Hello\n\n## World\n")
	if err != nil {
		t.Fatal(err)
	}
	// The dark style's H1 has a background color, which renders broken when
	// scaled, so it is left at normal size.
	if strings.Contains(out, "\x1b]66;s=3;") {
		t.Errorf("expected H1 with background to not be scaled, got %q", out)
	}
	if !strings.Contains(out, "Hello") {
		t.Errorf("expected H1 text in pager output, got %q", out)
	}
	if !strings.Contains(out, "\x1b]66;s=2;World\x1b\\") {
		t.Errorf("expected H2 at 2x scale in pager output, got %q", out)
	}
	if strings.Contains(out, "\x1b]6666;") {
		t.Errorf("expected no leftover markers in pager output, got %q", out)
	}
}

func newTestPager(t *testing.T) pagerModel {
	t.Helper()
	common := &commonModel{
		cfg: Config{
			GlamourEnabled:  true,
			GlamourStyle:    "dark",
			GlamourMaxWidth: 80,
		},
		styles: newStyles(true),
		width:  80,
		height: 24,
	}
	m := newPagerModel(common)
	m.currentDocument = markdown{Note: "test.md"}
	m.setSize(80, 24)
	return m
}

func TestGlamourRenderIncrementalCache(t *testing.T) {
	m := newTestPager(t)
	doc := "# Hello\n\nWorld\n"

	first, err := glamourRender(m, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "Hello") || !strings.Contains(first, "World") {
		t.Fatalf("expected rendered document content, got %q", first)
	}
	if stats := m.blockCache.Stats(); stats.Misses != 1 || stats.Hits != 0 || stats.Entries != 1 {
		t.Fatalf("cold render expected 1 miss, got %+v", stats)
	}

	// An unchanged reload must be served from the cache byte-for-byte.
	second, err := glamourRender(m, doc)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("cached reload must be identical to the original render")
	}
	if stats := m.blockCache.Stats(); stats.Hits != 1 || stats.Misses != 1 {
		t.Errorf("expected one cache hit on reload, got %+v", stats)
	}

	// A local edit changes the content-addressed ID and forces a fresh render.
	edited := "# Hello\n\nWorld changed\n"
	third, err := glamourRender(m, edited)
	if err != nil {
		t.Fatal(err)
	}
	// Glamour styles each word in its own ANSI segment, so assert on the
	// individual words rather than the phrase as one contiguous string.
	if !strings.Contains(third, "World") || !strings.Contains(third, "changed") {
		t.Errorf("edited content must be rendered fresh, got %q", third)
	}
	if third == first {
		t.Errorf("edited document must not reuse the pre-edit render")
	}
	if stats := m.blockCache.Stats(); stats.Misses != 2 {
		t.Errorf("expected a miss after local edit, got %+v", stats)
	}

	// A terminal resize flushes the generation: no old-width output survives.
	m.setSize(40, 24)
	fourth, err := glamourRender(m, edited)
	if err != nil {
		t.Fatal(err)
	}
	if stats := m.blockCache.Stats(); stats.Entries != 1 || stats.Misses != 3 {
		t.Errorf("expected resize flush plus one miss, got %+v", stats)
	}
	if fourth == third {
		t.Errorf("resized render should differ due to the new word-wrap width")
	}

	// Switching the glamour style changes the style version and misses safely.
	m.common.cfg.GlamourStyle = "light"
	if _, err := glamourRender(m, edited); err != nil {
		t.Fatal(err)
	}
	if stats := m.blockCache.Stats(); stats.Entries != 1 || stats.Misses != 4 {
		t.Errorf("expected theme switch flush plus one miss, got %+v", stats)
	}

	// Leaving the pager releases all cached entries.
	m.unload()
	if stats := m.blockCache.Stats(); stats.Entries != 0 {
		t.Errorf("expected empty cache after unload, got %+v", stats)
	}
}

func TestGlamourRenderCacheCorruptionFallback(t *testing.T) {
	m := newTestPager(t)
	doc := "# Hello\n\nWorld\n"

	original, err := glamourRender(m, doc)
	if err != nil {
		t.Fatal(err)
	}

	for key, entry := range m.blockCache.snapshotEntries() {
		entry.output = "TAMPERED RENDER OUTPUT"
		m.blockCache.replaceEntry(key, entry)
	}

	recovered, err := glamourRender(m, doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(recovered, "TAMPERED") {
		t.Errorf("corrupted render output must never be displayed, got %q", recovered)
	}
	if recovered != original {
		t.Errorf("fallback render must reproduce the original output")
	}
	if m.blockCache.Stats().Corruptions == 0 {
		t.Errorf("expected the corrupted entry to be recorded and evicted")
	}
}
