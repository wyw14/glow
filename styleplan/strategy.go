package styleplan

import (
	"strconv"

	"github.com/charmbracelet/colorprofile"
)

const (
	// colorsField names the plan level degradation target covering every
	// color field of the style configuration.
	colorsField = "colors"

	// AutoTheme is the theme name users pass to let glow resolve the theme.
	AutoTheme = "auto"
	// NoTTYTheme is the plain theme used for non-interactive output.
	NoTTYTheme = "notty"
	// darkTheme mirrors glamour's dark default style name.
	darkTheme = "dark"

	// maxWrapWidth mirrors the historical CLI cap on terminal width.
	maxWrapWidth = 120
	// defaultWrapWidth is used when the width cannot be determined.
	defaultWrapWidth = 80
	// narrowWidth is the width below which block geometry is degraded.
	narrowWidth = 40
)

// Reason explains why a field or capability was degraded.
type Reason string

const (
	// ReasonRedirectedOutput marks the switch to the notty theme for output
	// that is not an interactive terminal.
	ReasonRedirectedOutput Reason = "redirected-output"
	// ReasonNoColor marks fields removed because colors cannot be emitted.
	ReasonNoColor Reason = "no-color"
	// ReasonColorDepth marks a color downsampled to the detected depth.
	ReasonColorDepth Reason = "color-depth"
	// ReasonASCIITerminal marks Unicode glyphs replaced by ASCII fallbacks.
	ReasonASCIITerminal Reason = "ascii-terminal"
	// ReasonNarrowTerminal marks margins and indents reduced on narrow TTYs.
	ReasonNarrowTerminal Reason = "narrow-terminal"
	// ReasonUnknownColorProfile marks the conservative color fallback used
	// when the color profile cannot be determined.
	ReasonUnknownColorProfile Reason = "unknown-color-profile"
	// ReasonUnknownUnicode marks the conservative ASCII fallback used when
	// Unicode support cannot be determined.
	ReasonUnknownUnicode Reason = "unknown-unicode"
	// ReasonUnknownWidth marks the default width used for unknown sizes.
	ReasonUnknownWidth Reason = "unknown-width"
	// ReasonTextSizingUnsupported marks OSC 66 text sizing being disabled.
	ReasonTextSizingUnsupported Reason = "text-sizing-unsupported"
)

// Request captures the user intent the planner must honor. It is comparable
// and therefore usable as part of cache keys.
type Request struct {
	// Theme is the user or configuration selected theme name or style path.
	Theme string
	// ThemeExplicit reports whether the theme was passed as a flag instead
	// of being derived from configuration defaults.
	ThemeExplicit bool
	// IsCode reports whether the rendered document is a plain code file.
	IsCode bool
	// RequestedWidth is the wrap width requested by the caller, or zero.
	RequestedWidth uint
	// WidthExplicit reports whether the width was passed as a flag.
	WidthExplicit bool
}

// Degradation describes a single planning decision that lowers fidelity.
type Degradation struct {
	// Field is the dotted style field or capability that is affected.
	Field string
	// Reason is the machine readable cause of the degradation.
	Reason Reason
	// From describes the original value or capability.
	From string
	// To describes the effective value or capability.
	To string
}

// Strategy is the immutable rendering plan. Instances are only constructed
// through Plan; all accessors return value copies, so sharing a Strategy
// across goroutines cannot change any other reader's view.
type Strategy struct {
	version       Version
	request       Request
	resolvedTheme string
	wrapWidth     int
	color         colorprofile.Profile
	unicode       UnicodeLevel
	eastAsian     bool
	emitColor     bool
	textSizing    bool
	narrow        bool
	degraded      []Degradation
}

// Version returns the capability version this strategy was planned for.
func (s *Strategy) Version() Version { return s.version }

// Request returns the request the strategy was planned from.
func (s *Strategy) Request() Request { return s.request }

// ResolvedTheme returns the theme that rendering must use. An explicit user
// theme is always preserved; only auto and implicit redirected output are
// resolved.
func (s *Strategy) ResolvedTheme() string { return s.resolvedTheme }

// WrapWidth returns the word wrap width. Zero disables wrapping.
func (s *Strategy) WrapWidth() int { return s.wrapWidth }

// ColorDepth returns the target color profile.
func (s *Strategy) ColorDepth() colorprofile.Profile { return s.color }

// Unicode returns the target Unicode level.
func (s *Strategy) Unicode() UnicodeLevel { return s.unicode }

// EastAsianWidth reports whether ambiguous width glyphs count as two cells.
func (s *Strategy) EastAsianWidth() bool { return s.eastAsian }

// EmitColor reports whether color SGR sequences may be emitted.
func (s *Strategy) EmitColor() bool { return s.emitColor }

// TextSizing reports whether OSC 66 text sizing sequences may be emitted.
func (s *Strategy) TextSizing() bool { return s.textSizing }

// Narrow reports whether the plan applies narrow-terminal geometry changes.
func (s *Strategy) Narrow() bool { return s.narrow }

// Degradations returns a copy of the plan level degradation records.
func (s *Strategy) Degradations() []Degradation {
	if len(s.degraded) == 0 {
		return nil
	}
	out := make([]Degradation, len(s.degraded))
	copy(out, s.degraded)
	return out
}

// plan builds an immutable strategy from a capability snapshot and a request.
// It is a pure function: it performs no I/O and never mutates the request.
func plan(caps Capabilities, req Request) *Strategy {
	strategy := &Strategy{
		version:    caps.Version(),
		request:    req,
		color:      caps.Color,
		unicode:    caps.Unicode,
		eastAsian:  caps.EastAsianWidth,
		textSizing: caps.TextSizing,
	}
	degraded := make([]Degradation, 0, 8)

	theme, changed := resolveTheme(caps, req)
	strategy.resolvedTheme = theme
	if changed {
		degraded = append(degraded, Degradation{
			Field:  "theme",
			Reason: ReasonRedirectedOutput,
			From:   req.Theme,
			To:     theme,
		})
	}

	degraded = append(degraded, planColor(caps, strategy)...)
	degraded = append(degraded, planUnicode(caps, strategy)...)

	width, widthDegraded := resolveWidth(caps, req)
	strategy.wrapWidth = width
	degraded = append(degraded, widthDegraded...)
	strategy.narrow = width != 0 && width < narrowWidth
	if strategy.narrow {
		degraded = append(degraded, Degradation{
			Field:  "geometry",
			Reason: ReasonNarrowTerminal,
			From:   "default margins and indents",
			To:     "collapsed margins and indents",
		})
	}

	if !strategy.textSizing {
		degraded = append(degraded, Degradation{
			Field:  "text_sizing",
			Reason: ReasonTextSizingUnsupported,
			From:   "OSC 66 heading scaling",
			To:     "static heading text",
		})
	}

	strategy.degraded = degraded
	return strategy
}

// resolveTheme never rewrites an explicit theme. Only implicit output to a
// non-interactive target is moved to the notty theme.
func resolveTheme(caps Capabilities, req Request) (string, bool) {
	if !caps.Interactive && !req.ThemeExplicit {
		return NoTTYTheme, true
	}
	if req.Theme == "" || req.Theme == AutoTheme {
		return darkTheme, false
	}
	return req.Theme, false
}

func planColor(caps Capabilities, strategy *Strategy) []Degradation {
	if !caps.ColorKnown || strategy.color == colorprofile.Unknown {
		strategy.color = colorprofile.ASCII
		strategy.emitColor = false
		return []Degradation{{
			Field:  colorsField,
			Reason: ReasonUnknownColorProfile,
			From:   "unknown color profile",
			To:     colorprofile.ASCII.String(),
		}}
	}

	strategy.emitColor = strategy.color >= colorprofile.ANSI
	switch {
	case !strategy.emitColor:
		return []Degradation{{
			Field:  colorsField,
			Reason: ReasonNoColor,
			From:   "styled colors",
			To:     "plain text",
		}}
	case strategy.color < colorprofile.TrueColor:
		return []Degradation{{
			Field:  colorsField,
			Reason: ReasonColorDepth,
			From:   colorprofile.TrueColor.String(),
			To:     strategy.color.String(),
		}}
	default:
		return nil
	}
}

func planUnicode(caps Capabilities, strategy *Strategy) []Degradation {
	if caps.Unicode == UnicodeUnknown {
		strategy.unicode = UnicodeASCII
		return []Degradation{{
			Field:  "glyphs",
			Reason: ReasonUnknownUnicode,
			From:   "unknown Unicode support",
			To:     UnicodeASCII.String(),
		}}
	}
	if strategy.unicode == UnicodeASCII {
		return []Degradation{{
			Field:  "glyphs",
			Reason: ReasonASCIITerminal,
			From:   "Unicode glyphs",
			To:     "ASCII fallbacks",
		}}
	}
	return nil
}

// resolveWidth applies explicit user widths first, then the measured
// terminal width, and finally the conservative default for unknown sizes.
func resolveWidth(caps Capabilities, req Request) (int, []Degradation) {
	if req.WidthExplicit {
		width := int(req.RequestedWidth)
		if width != 0 && caps.WidthKnown && width > caps.Width {
			return caps.Width, []Degradation{{
				Field:  "width",
				Reason: ReasonNarrowTerminal,
				From:   strconv.FormatUint(uint64(req.RequestedWidth), 10),
				To:     strconv.Itoa(caps.Width),
			}}
		}
		return width, nil
	}

	if caps.WidthKnown {
		width := caps.Width
		if width > maxWrapWidth {
			width = maxWrapWidth
		}
		return width, nil
	}

	if req.RequestedWidth > 0 {
		return int(req.RequestedWidth), nil
	}
	return defaultWrapWidth, []Degradation{{
		Field:  "width",
		Reason: ReasonUnknownWidth,
		From:   "unknown terminal width",
		To:     strconv.Itoa(defaultWrapWidth),
	}}
}
