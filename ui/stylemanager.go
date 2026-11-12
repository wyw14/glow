package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Sentinel errors reported by style transactions.
var (
	// ErrStyleManagerClosed is returned once the manager has been closed.
	ErrStyleManagerClosed = errors.New("style manager is closed")
	// ErrReloadSuperseded is returned when a reload finishes after a newer
	// transaction was started. Its result is discarded and never committed.
	ErrReloadSuperseded = errors.New("style reload superseded by a newer transaction")
	// ErrReloadCanceled is returned when a reload is canceled before commit,
	// typically because the session is closing.
	ErrReloadCanceled = errors.New("style reload canceled")
)

// StyleSnapshot is an immutable, versioned set of styles. Readers always
// observe a fully built snapshot: a hot reload never exposes partially
// constructed styles, and every reader that holds a snapshot keeps using the
// same version for as long as it retains it.
type StyleSnapshot struct {
	// Version is monotonically increased every time a snapshot is committed.
	Version uint64
	// IsDark reports the terminal background the snapshot was resolved for.
	IsDark bool
	// Config is the validated palette the snapshot was built from.
	Config StyleConfig
	// Styles holds the resolved lipgloss styles.
	Styles Styles
}

// ReloadOutcome reports the result of an asynchronous style reload.
type ReloadOutcome struct {
	// Version is the committed snapshot version, or zero on failure.
	Version uint64
	// Err is nil when the reload was committed.
	Err error
}

// StyleManager owns the active style snapshot and serializes style
// transactions. Reloads parse and fully validate a new configuration on a
// background goroutine and publish the resulting snapshot through a single
// atomic pointer swap, so concurrent readers either see the previous version
// or the new one but never a torn state. Closing the manager cancels every
// in-flight reload and waits for the worker goroutines to exit.
type StyleManager struct {
	current atomic.Pointer[StyleSnapshot]

	// mu guards epoch and closed together with the commit decision. The
	// active snapshot is published through an atomic pointer, so Snapshot
	// stays lock-free and safe for concurrent use.
	mu     sync.Mutex
	epoch  uint64
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewStyleManager fully validates cfg, builds the initial snapshot and
// returns a manager ready for hot reloads. The initial snapshot gets
// version 1.
func NewStyleManager(cfg StyleConfig, isDark bool) (*StyleManager, error) {
	if err := ValidateStyleConfig(cfg); err != nil {
		return nil, err
	}
	styles, err := buildStyles(cfg, isDark)
	if err != nil {
		return nil, fmt.Errorf("style config: build styles: %w", err)
	}
	if err := styles.smokeRender(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	sm := &StyleManager{ctx: ctx, cancel: cancel, epoch: 1}
	sm.current.Store(&StyleSnapshot{
		Version: 1,
		IsDark:  isDark,
		Config:  cfg,
		Styles:  styles,
	})
	return sm, nil
}

// NewDefaultStyleManager builds a manager from the compiled-in palette. It
// can only fail if the built-in defaults are invalid, which is guarded by
// tests.
func NewDefaultStyleManager(isDark bool) *StyleManager {
	sm, err := NewStyleManager(DefaultStyleConfig(), isDark)
	if err != nil {
		panic(fmt.Sprintf("ui: invalid default style config: %v", err))
	}
	return sm
}

// Snapshot returns the active immutable snapshot. It is safe for concurrent
// use. Work that needs a consistent set of styles must call Snapshot once
// and retain the result instead of re-reading styles mid-render.
func (sm *StyleManager) Snapshot() *StyleSnapshot {
	return sm.current.Load()
}

// Version returns the version of the active snapshot.
func (sm *StyleManager) Version() uint64 {
	return sm.current.Load().Version
}

// Reload parses and fully validates data on a background goroutine and
// commits the resulting snapshot atomically on success. On failure the
// previously active snapshot is left untouched. The returned buffered
// channel delivers exactly one outcome and is then closed, so a closing
// session can never block a worker on an unread result. A newer reload
// supersedes an in-flight one, whose outcome reports ErrReloadSuperseded.
func (sm *StyleManager) Reload(data []byte) <-chan ReloadOutcome {
	out := make(chan ReloadOutcome, 1)

	sm.mu.Lock()
	if sm.closed {
		sm.mu.Unlock()
		out <- ReloadOutcome{Err: ErrStyleManagerClosed}
		close(out)
		return out
	}
	sm.epoch++
	myEpoch := sm.epoch
	isDark := sm.current.Load().IsDark
	ctx := sm.ctx
	sm.wg.Add(1)
	sm.mu.Unlock()

	go func() {
		defer sm.wg.Done()
		out <- sm.runReload(ctx, data, isDark, myEpoch)
		close(out)
	}()

	return out
}

// runReload executes one transaction off the active snapshot and only
// commits if the transaction is still current and has not been canceled.
func (sm *StyleManager) runReload(ctx context.Context, data []byte, isDark bool, epoch uint64) ReloadOutcome {
	cfg, err := ParseStyleConfig(ctx, data)
	if err != nil {
		return ReloadOutcome{Err: err}
	}
	if err := ValidateStyleConfig(cfg); err != nil {
		return ReloadOutcome{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return ReloadOutcome{Err: fmt.Errorf("%w: %v", ErrReloadCanceled, err)}
	}

	styles, err := buildStyles(cfg, isDark)
	if err == nil {
		err = styles.smokeRender()
	}
	if err != nil {
		return ReloadOutcome{Err: err}
	}

	snap := &StyleSnapshot{
		Version: epoch,
		IsDark:  isDark,
		Config:  cfg,
		Styles:  styles,
	}

	// Single commit gate: the pointer swap is the only place the active
	// snapshot can change, and it happens while holding no locks needed by
	// readers.
	sm.mu.Lock()
	defer sm.mu.Unlock()
	switch {
	case sm.closed || ctx.Err() != nil:
		return ReloadOutcome{Err: ErrReloadCanceled}
	case sm.epoch != epoch:
		return ReloadOutcome{Err: ErrReloadSuperseded}
	default:
		sm.current.Store(snap)
		return ReloadOutcome{Version: epoch}
	}
}

// SetDark rebuilds styles from the active configuration for a light or dark
// terminal background and swaps them through the same atomic commit path as
// a hot reload. Calling it with the current background is a no-op.
func (sm *StyleManager) SetDark(isDark bool) ReloadOutcome {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.closed {
		return ReloadOutcome{Err: ErrStyleManagerClosed}
	}
	cur := sm.current.Load()
	if cur.IsDark == isDark {
		return ReloadOutcome{Version: cur.Version}
	}

	styles, err := buildStyles(cur.Config, isDark)
	if err == nil {
		err = styles.smokeRender()
	}
	if err != nil {
		return ReloadOutcome{Err: err}
	}

	sm.epoch++
	sm.current.Store(&StyleSnapshot{
		Version: sm.epoch,
		IsDark:  isDark,
		Config:  cur.Config,
		Styles:  styles,
	})
	return ReloadOutcome{Version: sm.epoch}
}

// Close cancels every in-flight reload and waits for the worker goroutines to
// exit. The active snapshot remains readable afterwards so a final frame can
// still be rendered. Close is idempotent.
func (sm *StyleManager) Close() error {
	sm.mu.Lock()
	if sm.closed {
		sm.mu.Unlock()
		return nil
	}
	sm.closed = true
	sm.cancel()
	sm.mu.Unlock()

	sm.wg.Wait()
	return nil
}

// ParseStyleConfig decodes a JSON style configuration. The context is checked
// up front so a session that is already closing does no parsing work at all.
func ParseStyleConfig(ctx context.Context, data []byte) (StyleConfig, error) {
	if err := ctx.Err(); err != nil {
		return StyleConfig{}, fmt.Errorf("%w: %v", ErrReloadCanceled, err)
	}

	var cfg StyleConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return StyleConfig{}, fmt.Errorf("style config: parse: %w", err)
	}
	return cfg, nil
}

// ValidateStyleConfig performs full validation of a decoded config: every
// required palette key must be present exactly once, both variants of each
// color must be valid, and unknown keys are rejected so typos fail loudly.
func ValidateStyleConfig(cfg StyleConfig) error {
	if len(cfg.Colors) == 0 {
		return errors.New("style config: empty color palette")
	}

	seen := make(map[string]struct{}, len(paletteKeys))
	for _, key := range paletteKeys {
		ac, ok := cfg.Colors[key]
		if !ok {
			return fmt.Errorf("style config: missing required color %q", key)
		}
		if err := validateAdaptiveColor(key, ac); err != nil {
			return err
		}
		seen[key] = struct{}{}
	}

	for key := range cfg.Colors {
		if _, ok := seen[key]; !ok {
			return fmt.Errorf("style config: unknown color %q", key)
		}
	}
	return nil
}

func validateAdaptiveColor(key string, ac AdaptiveColor) error {
	if strings.TrimSpace(ac.Light) == "" || strings.TrimSpace(ac.Dark) == "" {
		return fmt.Errorf("style config: color %q requires both light and dark values", key)
	}
	if err := validateColorString(key, "light", ac.Light); err != nil {
		return err
	}
	if err := validateColorString(key, "dark", ac.Dark); err != nil {
		return err
	}
	return nil
}

var hexColorRE = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// ANSI color names accepted in addition to hex colors and 0-255 indexes.
var ansiColorNames = map[string]struct{}{
	"default":       {},
	"black":         {},
	"red":           {},
	"green":         {},
	"yellow":        {},
	"blue":          {},
	"magenta":       {},
	"cyan":          {},
	"white":         {},
	"brightblack":   {},
	"brightred":     {},
	"brightgreen":   {},
	"brightyellow":  {},
	"brightblue":    {},
	"brightmagenta": {},
	"brightcyan":    {},
	"brightwhite":   {},
}

func validateColorString(key, variant, value string) error {
	if hexColorRE.MatchString(value) {
		return nil
	}
	if n, err := strconv.Atoi(value); err == nil && n >= 0 && n <= 255 {
		return nil
	}
	if _, ok := ansiColorNames[strings.ToLower(strings.ReplaceAll(value, " ", ""))]; ok {
		return nil
	}
	return fmt.Errorf("style config: color %q (%s) has invalid value %q", key, variant, value)
}
