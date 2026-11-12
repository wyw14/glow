package styleplan

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/charmbracelet/colorprofile"
)

func noColorStrategy() *Strategy {
	caps := fullCapabilities()
	caps.Color = colorprofile.NoTTY
	caps.Unicode = UnicodeASCII
	caps.TextSizing = false
	return plan(caps, Request{Theme: "dark", ThemeExplicit: true})
}

func depthStrategy(profile colorprofile.Profile) *Strategy {
	caps := fullCapabilities()
	caps.Color = profile
	return plan(caps, Request{Theme: "dark", ThemeExplicit: true})
}

func narrowStrategy() *Strategy {
	caps := fullCapabilities()
	caps.Width = 20
	return plan(caps, Request{Theme: "dark", ThemeExplicit: true})
}

func TestApplyStripsAllColors(t *testing.T) {
	cfg := styles.DarkStyleConfig
	records := Apply(&cfg, noColorStrategy())

	if cfg.Document.Color != nil {
		t.Errorf("document color should be removed, got %q", *cfg.Document.Color)
	}
	if cfg.H1.BackgroundColor != nil {
		t.Error("H1 background color should be removed")
	}
	if cfg.CodeBlock.Chroma != nil && cfg.CodeBlock.Chroma.Keyword.Color != nil {
		t.Error("chroma keyword color should be removed")
	}
	if !hasReason(records, ReasonNoColor) {
		t.Error("expected no-color degradation records")
	}
	if !hasField(records, "document.color") {
		t.Error("expected a document.color record")
	}
}

func TestApplyDownsamplesHexColors(t *testing.T) {
	hexColor := "#00AAFF"
	cfg := ansi.StyleConfig{}
	cfg.Document.Color = &hexColor

	records := Apply(&cfg, depthStrategy(colorprofile.ANSI256))
	converted := cfg.Document.Color
	if converted == nil {
		t.Fatal("ANSI256 must keep a converted color")
	}
	if strings.HasPrefix(*converted, "#") {
		t.Errorf("hex color %q must be downsampled to an index, got %q", hexColor, *converted)
	}
	index, err := strconv.Atoi(*converted)
	if err != nil || index < 0 || index > 255 {
		t.Errorf("expected a 0-255 color index, got %q", *converted)
	}
	if !hasReason(records, ReasonColorDepth) {
		t.Error("expected a color depth degradation record")
	}
}

func TestApplyPreservesChromaHexOnColorTargets(t *testing.T) {
	hexColor := "#00AAFF"
	cfg := ansi.StyleConfig{}
	cfg.CodeBlock.Chroma = &ansi.Chroma{}
	cfg.CodeBlock.Chroma.Keyword.Color = &hexColor

	Apply(&cfg, depthStrategy(colorprofile.ANSI256))
	if cfg.CodeBlock.Chroma.Keyword.Color == nil ||
		*cfg.CodeBlock.Chroma.Keyword.Color != hexColor {
		t.Errorf("chroma hex colors must be preserved for chroma's own formatter, got %v",
			cfg.CodeBlock.Chroma.Keyword.Color)
	}

	noColor := ansi.StyleConfig{}
	noColor.CodeBlock.Chroma = &ansi.Chroma{}
	noColor.CodeBlock.Chroma.Keyword.Color = &hexColor
	Apply(&noColor, noColorStrategy())
	if noColor.CodeBlock.Chroma.Keyword.Color != nil {
		t.Error("chroma colors must still be removed on colorless targets")
	}
}

func TestApplyDownsamplesIndexedColorsToANSI(t *testing.T) {
	cfg := styles.DarkStyleConfig
	if cfg.Document.Color == nil || *cfg.Document.Color != "252" {
		t.Fatalf("test fixture changed, expected document color 252, got %v", cfg.Document.Color)
	}

	Apply(&cfg, depthStrategy(colorprofile.ANSI))
	index, err := strconv.Atoi(*cfg.Document.Color)
	if err != nil {
		t.Fatalf("expected a numeric ANSI index, got %q", *cfg.Document.Color)
	}
	if index > 15 {
		t.Errorf("ANSI profile must map 252 to a 0-15 index, got %d", index)
	}
}

func TestApplyReplacesUnicodeGlyphs(t *testing.T) {
	cfg := styles.PinkStyleConfig
	records := Apply(&cfg, noColorStrategy())

	checks := map[string]struct {
		got  string
		want string
	}{
		"H2 prefix":         {cfg.H2.Prefix, "| "},
		"H3 prefix":         {cfg.H3.Prefix, "| "},
		"H5 prefix":         {cfg.H5.Prefix, "| "},
		"item prefix":       {cfg.Item.BlockPrefix, "* "},
		"task ticked":       {cfg.Task.Ticked, "[x] "},
		"definition prefix": {cfg.DefinitionDescription.BlockPrefix, "\n-> "},
		"horizontal rule":   {cfg.HorizontalRule.Format, "\n------\n"},
	}
	for name, check := range checks {
		if check.got != check.want {
			t.Errorf("%s: expected %q, got %q", name, check.want, check.got)
		}
	}
	if cfg.Code.Prefix != " " {
		t.Errorf("expected non-breaking space to become a regular space, got %q", cfg.Code.Prefix)
	}
	if !hasReason(records, ReasonASCIITerminal) {
		t.Error("expected ASCII terminal degradation records")
	}
}

func TestApplyUnknownUnicodeFallsBackToQuestionMark(t *testing.T) {
	cfg := ansi.StyleConfig{}
	cfg.Document.Prefix = "★star"

	Apply(&cfg, noColorStrategy())
	if cfg.Document.Prefix != "?star" {
		t.Errorf("expected unknown glyph to become ?, got %q", cfg.Document.Prefix)
	}
}

func TestApplyNarrowGeometry(t *testing.T) {
	cfg := styles.DarkStyleConfig
	records := Apply(&cfg, narrowStrategy())

	if cfg.Document.Margin == nil || *cfg.Document.Margin != 0 {
		t.Error("document margin must collapse on a narrow terminal")
	}
	if cfg.CodeBlock.Margin == nil || *cfg.CodeBlock.Margin != 0 {
		t.Error("code block margin must collapse on a narrow terminal")
	}
	if cfg.BlockQuote.Indent == nil || *cfg.BlockQuote.Indent != 0 {
		t.Error("block quote indent must collapse on a narrow terminal")
	}
	if cfg.List.LevelIndent > 1 {
		t.Errorf("list level indent must reduce to at most 1, got %d", cfg.List.LevelIndent)
	}
	if !hasReason(records, ReasonNarrowTerminal) {
		t.Error("expected narrow terminal degradation records")
	}
}

func TestApplyKeepsCapableOutputUntouched(t *testing.T) {
	original := styles.DarkStyleConfig
	cfg := styles.DarkStyleConfig

	records := Apply(&cfg, plan(fullCapabilities(), Request{Theme: "dark", ThemeExplicit: true}))
	if len(records) != 0 {
		t.Errorf("capable terminals must not degrade fields, got %v", records)
	}
	if !reflect.DeepEqual(original, cfg) {
		t.Error("a full capability strategy must leave the style config unchanged")
	}
}

func TestApplyDoesNotMutateSharedDefaultStyles(t *testing.T) {
	before := *styles.DefaultStyles[styles.DarkStyle]
	cfg := *styles.DefaultStyles[styles.DarkStyle]

	Apply(&cfg, noColorStrategy())
	Apply(&cfg, narrowStrategy())

	if !reflect.DeepEqual(before, *styles.DefaultStyles[styles.DarkStyle]) {
		t.Fatal("degradation mutated the shared default style")
	}
}

func TestApplyNilSafety(t *testing.T) {
	if Apply(nil, noColorStrategy()) != nil {
		t.Error("nil config should return nil")
	}
	cfg := ansi.StyleConfig{}
	if Apply(&cfg, nil) != nil {
		t.Error("nil strategy should return nil")
	}
}

func hasReason(records []Degradation, reason Reason) bool {
	for _, record := range records {
		if record.Reason == reason {
			return true
		}
	}
	return false
}

func hasField(records []Degradation, field string) bool {
	for _, record := range records {
		if record.Field == field {
			return true
		}
	}
	return false
}
