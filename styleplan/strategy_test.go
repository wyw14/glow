package styleplan

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func fullCapabilities() Capabilities {
	return Capabilities{
		Interactive:    true,
		Color:          colorprofile.TrueColor,
		ColorKnown:     true,
		Unicode:        UnicodeFull,
		EastAsianWidth: false,
		Width:          100,
		WidthKnown:     true,
		TextSizing:     true,
	}
}

func redirectedCapabilities() Capabilities {
	return Capabilities{
		Interactive: false,
		Color:       colorprofile.NoTTY,
		ColorKnown:  true,
		Unicode:     UnicodeASCII,
		WidthKnown:  false,
		TextSizing:  false,
	}
}

func findDegradation(t *testing.T, strategy *Strategy, reason Reason) (Degradation, bool) {
	t.Helper()
	for _, degradation := range strategy.Degradations() {
		if degradation.Reason == reason {
			return degradation, true
		}
	}
	return Degradation{}, false
}

func TestPlanRedirectedOutput(t *testing.T) {
	strategy := plan(redirectedCapabilities(), Request{Theme: AutoTheme})

	if strategy.ResolvedTheme() != NoTTYTheme {
		t.Errorf("expected implicit redirected output to resolve to %q, got %q", NoTTYTheme, strategy.ResolvedTheme())
	}
	if strategy.EmitColor() {
		t.Error("redirected output must not emit colors")
	}
	if strategy.Unicode() != UnicodeASCII {
		t.Errorf("expected ASCII level, got %s", strategy.Unicode())
	}
	if strategy.WrapWidth() != defaultWrapWidth {
		t.Errorf("expected default width %d for unknown sizes, got %d", defaultWrapWidth, strategy.WrapWidth())
	}
	for _, reason := range []Reason{
		ReasonRedirectedOutput,
		ReasonNoColor,
		ReasonASCIITerminal,
		ReasonUnknownWidth,
		ReasonTextSizingUnsupported,
	} {
		if _, ok := findDegradation(t, strategy, reason); !ok {
			t.Errorf("expected degradation reason %q", reason)
		}
	}
}

func TestPlanPreservesExplicitTheme(t *testing.T) {
	req := Request{Theme: "dark", ThemeExplicit: true}
	strategy := plan(redirectedCapabilities(), req)

	if strategy.ResolvedTheme() != "dark" {
		t.Errorf("explicit theme must be preserved, got %q", strategy.ResolvedTheme())
	}
	if _, ok := findDegradation(t, strategy, ReasonRedirectedOutput); ok {
		t.Error("explicit themes must never be switched to notty")
	}
	if _, ok := findDegradation(t, strategy, ReasonNoColor); !ok {
		t.Error("colors must still be removed on a colorless target")
	}
	if strategy.Request().Theme != "dark" {
		t.Error("the original request must remain untouched")
	}
}

func TestPlanResolvesAutoTheme(t *testing.T) {
	strategy := plan(fullCapabilities(), Request{Theme: AutoTheme})
	if strategy.ResolvedTheme() != darkTheme {
		t.Errorf("expected auto theme to resolve to %q, got %q", darkTheme, strategy.ResolvedTheme())
	}
	if _, ok := findDegradation(t, strategy, ReasonRedirectedOutput); ok {
		t.Error("auto resolution on a TTY is not a degradation")
	}
}

func TestPlanUnknownCapabilities(t *testing.T) {
	caps := fullCapabilities()
	caps.Color = colorprofile.Unknown
	caps.ColorKnown = false
	caps.Unicode = UnicodeUnknown
	caps.WidthKnown = false
	caps.Width = 0

	strategy := plan(caps, Request{Theme: AutoTheme})
	if strategy.EmitColor() {
		t.Error("unknown color profiles must fall back to colorless output")
	}
	if strategy.ColorDepth() != colorprofile.ASCII {
		t.Errorf("expected ASCII fallback, got %s", strategy.ColorDepth())
	}
	if strategy.Unicode() != UnicodeASCII {
		t.Errorf("expected ASCII Unicode fallback, got %s", strategy.Unicode())
	}
	if strategy.WrapWidth() != defaultWrapWidth {
		t.Errorf("expected default width on unknown size, got %d", strategy.WrapWidth())
	}
	for _, reason := range []Reason{
		ReasonUnknownColorProfile,
		ReasonUnknownUnicode,
		ReasonUnknownWidth,
	} {
		if _, ok := findDegradation(t, strategy, reason); !ok {
			t.Errorf("expected degradation reason %q", reason)
		}
	}
}

func TestPlanColorDepthMatrix(t *testing.T) {
	tests := []struct {
		name       string
		profile    colorprofile.Profile
		emitColor  bool
		wantReason bool
	}{
		{"true color", colorprofile.TrueColor, true, false},
		{"256 colors", colorprofile.ANSI256, true, true},
		{"16 colors", colorprofile.ANSI, true, true},
		{"ascii", colorprofile.ASCII, false, false},
		{"no tty", colorprofile.NoTTY, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := fullCapabilities()
			caps.Color = tt.profile
			strategy := plan(caps, Request{Theme: "dark", ThemeExplicit: true})

			if strategy.EmitColor() != tt.emitColor {
				t.Errorf("expected EmitColor=%v", tt.emitColor)
			}
			_, hasDepthReason := findDegradation(t, strategy, ReasonColorDepth)
			_, hasNoColorReason := findDegradation(t, strategy, ReasonNoColor)
			if tt.wantReason && !hasDepthReason {
				t.Error("expected a color depth degradation")
			}
			if !tt.emitColor && !hasNoColorReason {
				t.Error("expected a no-color degradation")
			}
		})
	}
}

func TestPlanNarrowTerminal(t *testing.T) {
	caps := fullCapabilities()
	caps.Width = 20
	strategy := plan(caps, Request{Theme: AutoTheme})

	if strategy.WrapWidth() != 20 {
		t.Errorf("expected wrap width 20, got %d", strategy.WrapWidth())
	}
	if !strategy.Narrow() {
		t.Error("expected a 20 cell terminal to be treated as narrow")
	}
	if _, ok := findDegradation(t, strategy, ReasonNarrowTerminal); !ok {
		t.Error("expected a narrow terminal degradation")
	}

	wide := fullCapabilities()
	wide.Width = 200
	wideStrategy := plan(wide, Request{Theme: AutoTheme})
	if wideStrategy.WrapWidth() != maxWrapWidth {
		t.Errorf("expected width clamped to %d, got %d", maxWrapWidth, wideStrategy.WrapWidth())
	}
	if wideStrategy.Narrow() {
		t.Error("a clamped wide terminal must not be treated as narrow")
	}
}

func TestPlanExplicitWidth(t *testing.T) {
	caps := fullCapabilities()
	strategy := plan(caps, Request{Theme: AutoTheme, WidthExplicit: true, RequestedWidth: 0})
	if strategy.WrapWidth() != 0 {
		t.Errorf("explicit width 0 disables wrapping, got %d", strategy.WrapWidth())
	}
	if strategy.Narrow() {
		t.Error("disabled wrapping must not trigger narrow geometry")
	}

	caps.Width = 30
	clamped := plan(caps, Request{
		Theme:          AutoTheme,
		WidthExplicit:  true,
		RequestedWidth: 200,
	})
	if clamped.WrapWidth() != 30 {
		t.Errorf("expected explicit width clamped to terminal 30, got %d", clamped.WrapWidth())
	}
	if _, ok := findDegradation(t, clamped, ReasonNarrowTerminal); !ok {
		t.Error("expected the clamped width to be recorded as a degradation")
	}
}

func TestPlanTextSizingGate(t *testing.T) {
	caps := fullCapabilities()
	caps.TextSizing = false
	strategy := plan(caps, Request{Theme: AutoTheme})
	if strategy.TextSizing() {
		t.Error("text sizing must be disabled when the capability is absent")
	}
	if _, ok := findDegradation(t, strategy, ReasonTextSizingUnsupported); !ok {
		t.Error("expected a text sizing degradation")
	}

	enabled := plan(fullCapabilities(), Request{Theme: AutoTheme})
	if !enabled.TextSizing() {
		t.Error("text sizing must stay enabled when the capability is present")
	}
	if _, ok := findDegradation(t, enabled, ReasonTextSizingUnsupported); ok {
		t.Error("capable terminals must not record a text sizing degradation")
	}
}

func TestStrategyIsImmutable(t *testing.T) {
	strategy := plan(redirectedCapabilities(), Request{Theme: AutoTheme})
	first := strategy.Degradations()
	if len(first) == 0 {
		t.Fatal("expected degradation records")
	}
	first[0].Field = "mutated"
	first[0].Reason = "mutated"

	again := strategy.Degradations()
	if again[0].Field == "mutated" || again[0].Reason == "mutated" {
		t.Fatal("mutating a returned degradation slice changed the strategy")
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	first := plan(fullCapabilities(), Request{Theme: "dark", ThemeExplicit: true, RequestedWidth: 90})
	second := plan(fullCapabilities(), Request{Theme: "dark", ThemeExplicit: true, RequestedWidth: 90})

	if first.Version() != second.Version() {
		t.Error("identical capabilities must share a version")
	}
	if first.ResolvedTheme() != second.ResolvedTheme() ||
		first.WrapWidth() != second.WrapWidth() ||
		first.ColorDepth() != second.ColorDepth() ||
		first.Unicode() != second.Unicode() ||
		first.EmitColor() != second.EmitColor() ||
		first.TextSizing() != second.TextSizing() ||
		len(first.Degradations()) != len(second.Degradations()) {
		t.Error("identical inputs must produce identical strategies")
	}
}
