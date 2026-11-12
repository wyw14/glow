package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// probe renders a deterministic string from a Styles set so tests can detect
// whether styles actually changed between snapshots.
func probe(s Styles) string {
	return s.logoStyle.Render(" Glow ") + "|" + s.lineNumberStyle("1")
}

func mustMarshalConfig(t *testing.T, cfg StyleConfig) []byte {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal style config: %v", err)
	}
	return data
}

func modifiedConfig(t *testing.T, key string, color AdaptiveColor) StyleConfig {
	t.Helper()
	cfg := DefaultStyleConfig()
	cfg.Colors[key] = color
	return cfg
}

func TestDefaultStyleConfigValid(t *testing.T) {
	cfg := DefaultStyleConfig()
	if err := ValidateStyleConfig(cfg); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
	for _, isDark := range []bool{true, false} {
		styles, err := buildStyles(cfg, isDark)
		if err != nil {
			t.Fatalf("build styles (dark=%v): %v", isDark, err)
		}
		if err := styles.smokeRender(); err != nil {
			t.Fatalf("smoke render (dark=%v): %v", isDark, err)
		}
	}

	// Defaults must be independent copies: mutating one must not leak into
	// the next.
	cfg.Colors[colorLogoFg] = AdaptiveColor{Light: "#111111", Dark: "#222222"}
	if DefaultStyleConfig().Colors[colorLogoFg].Light == "#111111" {
		t.Fatal("DefaultStyleConfig returned a shared map")
	}
}

func TestParseStyleConfig(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		data := mustMarshalConfig(t, DefaultStyleConfig())
		cfg, err := ParseStyleConfig(context.Background(), data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(cfg.Colors) != len(paletteKeys) {
			t.Fatalf("expected %d colors, got %d", len(paletteKeys), len(cfg.Colors))
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		if _, err := ParseStyleConfig(context.Background(), []byte("{not json")); err == nil {
			t.Fatal("expected parse error")
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := ParseStyleConfig(ctx, mustMarshalConfig(t, DefaultStyleConfig()))
		if !errors.Is(err, ErrReloadCanceled) {
			t.Fatalf("expected ErrReloadCanceled, got %v", err)
		}
	})
}

func TestValidateStyleConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*StyleConfig)
		wantErr bool
	}{
		{name: "defaults", wantErr: false},
		{
			name:    "empty palette",
			mutate:  func(c *StyleConfig) { c.Colors = nil },
			wantErr: true,
		},
		{
			name: "missing key",
			mutate: func(c *StyleConfig) {
				delete(c.Colors, colorFuchsia)
			},
			wantErr: true,
		},
		{
			name: "unknown key",
			mutate: func(c *StyleConfig) {
				c.Colors["madeUp"] = AdaptiveColor{Light: "#fff", Dark: "#000"}
			},
			wantErr: true,
		},
		{
			name: "invalid hex",
			mutate: func(c *StyleConfig) {
				c.Colors[colorGreen] = AdaptiveColor{Light: "greenish", Dark: "#04B575"}
			},
			wantErr: true,
		},
		{
			name: "empty value",
			mutate: func(c *StyleConfig) {
				c.Colors[colorRed] = AdaptiveColor{Light: "", Dark: "#ED567A"}
			},
			wantErr: true,
		},
		{
			name: "ansi name accepted",
			mutate: func(c *StyleConfig) {
				c.Colors[colorRed] = AdaptiveColor{Light: "red", Dark: "201"}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultStyleConfig()
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}
			err := ValidateStyleConfig(cfg)
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestNewStyleManagerRejectsInvalid(t *testing.T) {
	cfg := DefaultStyleConfig()
	cfg.Colors[colorGreen] = AdaptiveColor{Light: "nope", Dark: "nope"}
	if _, err := NewStyleManager(cfg, true); err == nil {
		t.Fatal("expected constructor to reject invalid config")
	}
}

func TestReloadCommit(t *testing.T) {
	mgr := NewDefaultStyleManager(true)
	defer mgr.Close()

	if mgr.Version() != 1 {
		t.Fatalf("expected initial version 1, got %d", mgr.Version())
	}
	before := mgr.Snapshot()
	oldProbe := probe(before.Styles)

	cfg := modifiedConfig(t, colorLogoFg, AdaptiveColor{Light: "#123456", Dark: "#654321"})
	outcome := <-mgr.Reload(mustMarshalConfig(t, cfg))
	if outcome.Err != nil {
		t.Fatalf("reload failed: %v", outcome.Err)
	}
	if outcome.Version != 2 {
		t.Fatalf("expected committed version 2, got %d", outcome.Version)
	}

	snap := mgr.Snapshot()
	if snap.Version != 2 {
		t.Fatalf("expected snapshot version 2, got %d", snap.Version)
	}
	if got := snap.Config.Colors[colorLogoFg].Light; got != "#123456" {
		t.Fatalf("expected new palette value, got %q", got)
	}
	if probe(snap.Styles) == oldProbe {
		t.Fatal("expected rendered styles to change after commit")
	}
	// The previous snapshot stays intact for readers that retained it.
	if probe(before.Styles) != oldProbe {
		t.Fatal("old snapshot was mutated after reload")
	}
}

func TestReloadFailureKeepsOldStyles(t *testing.T) {
	mgr := NewDefaultStyleManager(true)
	defer mgr.Close()

	oldSnap := mgr.Snapshot()
	oldProbe := probe(oldSnap.Styles)

	// Malformed JSON.
	if outcome := <-mgr.Reload([]byte("@@@")); outcome.Err == nil {
		t.Fatal("expected error for malformed config")
	}
	// Well-formed JSON but invalid palette.
	bad := DefaultStyleConfig()
	delete(bad.Colors, colorGray)
	if outcome := <-mgr.Reload(mustMarshalConfig(t, bad)); outcome.Err == nil {
		t.Fatal("expected error for incomplete palette")
	}

	if mgr.Version() != oldSnap.Version {
		t.Fatalf("version changed after failed reload: %d", mgr.Version())
	}
	if probe(mgr.Snapshot().Styles) != oldProbe {
		t.Fatal("styles changed after failed reload")
	}
}

func TestConcurrentSnapshotReaders(t *testing.T) {
	mgr := NewDefaultStyleManager(true)
	defer mgr.Close()

	initial := mgr.Snapshot()

	// While no reload is in flight every reader must observe the exact same
	// version pointer.
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				snap := mgr.Snapshot()
				if snap != initial || snap.Version != 1 {
					t.Errorf("unexpected snapshot: ptr=%p version=%d", snap, snap.Version)
					return
				}
			}
		}()
	}
	wg.Wait()

	// With a reload racing readers, every observed snapshot must be a fully
	// built version, never a torn one.
	cfg := modifiedConfig(t, colorLogoFg, AdaptiveColor{Light: "#0a0a0a", Dark: "#f0f0f0"})
	data := mustMarshalConfig(t, cfg)

	var readerWg sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snap := mgr.Snapshot()
					if snap == nil {
						t.Error("nil snapshot")
						return
					}
					if len(snap.Config.Colors) != len(paletteKeys) {
						t.Errorf("torn snapshot: %d colors", len(snap.Config.Colors))
						return
					}
					if err := snap.Styles.smokeRender(); err != nil {
						t.Errorf("snapshot failed smoke render: %v", err)
						return
					}
				}
			}
		}()
	}

	outcome := <-mgr.Reload(data)
	if outcome.Err != nil {
		t.Fatalf("reload failed: %v", outcome.Err)
	}
	close(stop)
	readerWg.Wait()

	if mgr.Version() != 2 {
		t.Fatalf("expected version 2 after reload, got %d", mgr.Version())
	}
}

func TestSupersededReload(t *testing.T) {
	mgr := NewDefaultStyleManager(true)
	defer mgr.Close()

	cfgA := modifiedConfig(t, colorLogoFg, AdaptiveColor{Light: "#111111", Dark: "#111111"})
	cfgB := modifiedConfig(t, colorLogoFg, AdaptiveColor{Light: "#222222", Dark: "#222222"})

	outA := mgr.Reload(mustMarshalConfig(t, cfgA))
	outB := mgr.Reload(mustMarshalConfig(t, cfgB))

	a := <-outA
	b := <-outB
	if b.Err != nil {
		t.Fatalf("newest reload must commit, got: %v", b.Err)
	}
	// The older transaction either committed before the newer one or was
	// superseded; it must never overwrite the newer snapshot.
	if a.Err == nil && a.Version >= b.Version {
		t.Fatalf("stale reload committed at newer version: a=%d b=%d", a.Version, b.Version)
	}
	if got := mgr.Snapshot().Config.Colors[colorLogoFg].Light; got != "#222222" {
		t.Fatalf("expected newest config to win, got %q", got)
	}
}

func TestSetDark(t *testing.T) {
	mgr := NewDefaultStyleManager(true)
	defer mgr.Close()

	darkProbe := probe(mgr.Snapshot().Styles)

	out := mgr.SetDark(false)
	if out.Err != nil {
		t.Fatalf("SetDark: %v", out.Err)
	}
	snap := mgr.Snapshot()
	if snap.IsDark || snap.Version != 2 {
		t.Fatalf("expected light snapshot v2, got dark=%v version=%d", snap.IsDark, snap.Version)
	}
	if probe(snap.Styles) == darkProbe {
		t.Fatal("expected light styles to differ from dark styles")
	}

	// Same background is a no-op.
	if out := mgr.SetDark(false); out.Err != nil || out.Version != 2 {
		t.Fatalf("expected no-op, got %+v", out)
	}

	if out := mgr.SetDark(true); out.Err != nil || out.Version != 3 {
		t.Fatalf("expected v3 after switching back, got %+v", out)
	}
	if !mgr.Snapshot().IsDark {
		t.Fatal("expected dark background again")
	}
}

func TestCloseCancelsAndWaits(t *testing.T) {
	mgr := NewDefaultStyleManager(true)

	valid := mustMarshalConfig(t, modifiedConfig(t, colorLogoFg,
		AdaptiveColor{Light: "#333333", Dark: "#444444"}))
	invalid := []byte("{broken")

	channels := make([]<-chan ReloadOutcome, 0, 64)
	for i := 0; i < 32; i++ {
		channels = append(channels, mgr.Reload(valid))
		channels = append(channels, mgr.Reload(invalid))
	}

	// Close cancels in-flight work and waits for every worker to exit.
	done := make(chan struct{})
	go func() {
		_ = mgr.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not wait for workers to exit")
	}

	// Every buffered outcome must be deliverable even though no transaction
	// committed after close.
	for i, ch := range channels {
		select {
		case outcome, ok := <-ch:
			if !ok {
				t.Fatalf("outcome channel %d closed without result", i)
			}
			if outcome.Version != 0 && outcome.Err != nil {
				t.Fatalf("outcome %d carries both version %d and error", i, outcome.Version)
			}
		case <-time.After(time.Second):
			t.Fatalf("outcome channel %d never delivered", i)
		}
	}

	// Idempotent close.
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	// The last committed snapshot remains readable for a final frame.
	if mgr.Snapshot() == nil {
		t.Fatal("snapshot unavailable after close")
	}
	// New transactions are rejected.
	outcome := <-mgr.Reload(valid)
	if !errors.Is(outcome.Err, ErrStyleManagerClosed) {
		t.Fatalf("expected ErrStyleManagerClosed, got %v", outcome.Err)
	}
	if err := mgr.SetDark(false).Err; !errors.Is(err, ErrStyleManagerClosed) {
		t.Fatalf("expected ErrStyleManagerClosed from SetDark, got %v", err)
	}
}

func TestModelStyleRenderBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	if err := os.WriteFile(path, []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newModel(Config{Path: path}, "").(model)
	mgr := m.common.styleMgr
	before := probe(m.common.styles)

	// A failed transaction must not move the render-boundary cache.
	failed, cmd := m.Update(stylesReloadFailedMsg{err: errors.New("boom")})
	m = failed.(model)
	if cmd != nil {
		t.Fatal("failed reload should not return commands")
	}
	if probe(m.common.styles) != before {
		t.Fatal("styles changed after failed reload")
	}

	// Run a real background transaction, then deliver the commit message:
	// the cache swaps once, at this single boundary.
	cfg := modifiedConfig(t, colorLogoFg, AdaptiveColor{Light: "#010203", Dark: "#040506"})
	outcome := <-mgr.Reload(mustMarshalConfig(t, cfg))
	if outcome.Err != nil {
		t.Fatalf("reload: %v", outcome.Err)
	}
	// Before the message arrives the cache still holds the old version.
	if probe(m.common.styles) != before {
		t.Fatal("cache changed before the commit message was handled")
	}

	committed, _ := m.Update(stylesCommittedMsg{snap: mgr.Snapshot()})
	m = committed.(model)
	if probe(m.common.styles) == before {
		t.Fatal("cache did not switch at the render boundary")
	}
	if probe(m.common.styles) != probe(mgr.Snapshot().Styles) {
		t.Fatal("cached styles diverge from committed snapshot")
	}
}

func TestStyleReloadRequestMsgEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	if err := os.WriteFile(path, []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel(Config{Path: path}, "").(model)
	before := probe(m.common.styles)

	// Invalid payload: the command delivered through the public request
	// message must report failure without changing the active styles.
	updated, cmds := m.Update(StyleReloadRequestMsg{Data: []byte("not-json")})
	m = updated.(model)
	if cmds == nil {
		t.Fatal("expected a reload command")
	}
	if msg, ok := cmds().(stylesReloadFailedMsg); !ok {
		t.Fatalf("expected stylesReloadFailedMsg, got %T", msg)
	}
	if probe(m.common.styles) != before {
		t.Fatal("active styles changed after invalid reload request")
	}

	// Valid payload: the background transaction commits and the follow-up
	// message switches styles at the render boundary.
	cfg := modifiedConfig(t, colorLogoFg, AdaptiveColor{Light: "#0a0b0c", Dark: "#0d0e0f"})
	updated, cmds = m.Update(StyleReloadRequestMsg{Data: mustMarshalConfig(t, cfg)})
	m = updated.(model)
	committed, ok := cmds().(stylesCommittedMsg)
	if !ok {
		t.Fatalf("expected stylesCommittedMsg, got %T", committed)
	}
	committedModel, _ := m.Update(committed)
	m = committedModel.(model)
	if probe(m.common.styles) == before {
		t.Fatal("styles did not switch after committed reload")
	}
}

func TestReloadStylesCmdFailure(t *testing.T) {
	// A model without a manager (defensive construction) reports failure
	// through the standard message instead of panicking.
	common := &commonModel{styles: newStyles(true)}
	msg := reloadStylesCmd(common, []byte("{}"))()
	if _, ok := msg.(stylesReloadFailedMsg); !ok {
		t.Fatalf("expected stylesReloadFailedMsg, got %T", msg)
	}
}

func TestVersionMonotonicAcrossTransactions(t *testing.T) {
	mgr := NewDefaultStyleManager(true)
	defer mgr.Close()

	last := mgr.Version()
	for i := range 5 {
		cfg := modifiedConfig(t, colorLogoFg, AdaptiveColor{
			Light: fmt.Sprintf("#%06x", i+1),
			Dark:  fmt.Sprintf("#%06x", i+100),
		})
		outcome := <-mgr.Reload(mustMarshalConfig(t, cfg))
		if outcome.Err != nil {
			t.Fatalf("reload %d: %v", i, outcome.Err)
		}
		if outcome.Version <= last {
			t.Fatalf("version not monotonic: %d <= %d", outcome.Version, last)
		}
		last = outcome.Version
	}
}
