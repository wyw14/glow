package styleconfig_test

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/glow/v3/styleconfig"
)

func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }
func uintPtr(v uint) *uint       { return &v }

// findField returns the provenance entry for a field, if present.
func findField(report *styleconfig.Report, path string) (styleconfig.FieldSource, bool) {
	for _, field := range report.Fields {
		if field.Field == path {
			return field, true
		}
	}
	return styleconfig.FieldSource{}, false
}

func warningKinds(report *styleconfig.Report) map[string]styleconfig.WarningKind {
	out := make(map[string]styleconfig.WarningKind, len(report.Warnings))
	for _, warning := range report.Warnings {
		out[warning.Field] = warning.Kind
	}
	return out
}

func TestMergePriorityAndProvenance(t *testing.T) {
	t.Parallel()

	builtin := ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: stringPtr("#ffffff")},
			Margin:         uintPtr(2),
		},
		Paragraph: ansi.StyleBlock{Indent: uintPtr(1)},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Bold: boolPtr(true)},
		},
	}
	fileJSON := []byte(`{
		"paragraph": {"indent": 3},
		"h1": {"color": "#111111"}
	}`)
	cli := []string{"paragraph.indent=5"}

	report, err := styleconfig.Merge(
		styleconfig.BuiltinLayer(builtin),
		styleconfig.FileLayer(fileJSON),
		styleconfig.CLILayer(cli),
	)
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}

	cases := []struct {
		field  string
		value  string
		source styleconfig.Layer
		chain  []styleconfig.Layer
	}{
		{
			field:  "paragraph.indent",
			value:  "5",
			source: styleconfig.LayerCLI,
			chain:  []styleconfig.Layer{styleconfig.LayerBuiltin, styleconfig.LayerUserFile, styleconfig.LayerCLI},
		},
		{
			field:  "h1.bold",
			value:  "true",
			source: styleconfig.LayerBuiltin,
			chain:  []styleconfig.Layer{styleconfig.LayerBuiltin},
		},
		{
			field:  "h1.color",
			value:  `"#111111"`,
			source: styleconfig.LayerUserFile,
			chain:  []styleconfig.Layer{styleconfig.LayerUserFile},
		},
		{
			field:  "document.margin",
			value:  "2",
			source: styleconfig.LayerBuiltin,
			chain:  []styleconfig.Layer{styleconfig.LayerBuiltin},
		},
		{
			field:  "document.color",
			value:  `"#ffffff"`,
			source: styleconfig.LayerBuiltin,
			chain:  []styleconfig.Layer{styleconfig.LayerBuiltin},
		},
	}
	for _, tc := range cases {
		field, ok := findField(report, tc.field)
		if !ok {
			t.Errorf("missing provenance for %q", tc.field)
			continue
		}
		if field.Value != tc.value {
			t.Errorf("field %q: value = %q, want %q", tc.field, field.Value, tc.value)
		}
		if field.Source != tc.source {
			t.Errorf("field %q: source = %q, want %q", tc.field, field.Source, tc.source)
		}
		if !reflect.DeepEqual(field.ConflictChain, tc.chain) {
			t.Errorf("field %q: conflict chain = %v, want %v", tc.field, field.ConflictChain, tc.chain)
		}
		if field.Source != field.ConflictChain[len(field.ConflictChain)-1] {
			t.Errorf("field %q: source %q is not the last chain entry %v", tc.field, field.Source, field.ConflictChain)
		}
	}

	if report.Config.Paragraph.Indent == nil || *report.Config.Paragraph.Indent != 5 {
		t.Errorf("merged paragraph.indent = %v, want 5", report.Config.Paragraph.Indent)
	}
	if report.Config.H1.Bold == nil || !*report.Config.H1.Bold {
		t.Errorf("merged h1.bold = %v, want true", report.Config.H1.Bold)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", report.Warnings)
	}
}

func TestEmptyLayers(t *testing.T) {
	t.Parallel()

	report, err := styleconfig.Merge(
		styleconfig.BuiltinLayer(ansi.StyleConfig{}),
		styleconfig.FileLayer(nil),
		styleconfig.FileLayer([]byte("{}")),
		styleconfig.FileLayer([]byte("  \n\t ")),
		styleconfig.CLILayer(nil),
		styleconfig.CLILayer([]string{}),
	)
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}
	if len(report.Fields) != 0 {
		t.Errorf("expected no resolved fields, got %d", len(report.Fields))
	}
	if len(report.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", report.Warnings)
	}
}

func TestDuplicateKeysInJSON(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"paragraph": {"indent": 1, "indent": 2},
		"h1": {"bold": true, "bold": false}
	}`)
	report, err := styleconfig.Merge(styleconfig.FileLayer(data))
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}

	indent, ok := findField(report, "paragraph.indent")
	if !ok || indent.Value != "2" {
		t.Errorf("paragraph.indent = %v, want last-wins value 2", indent)
	}
	bold, ok := findField(report, "h1.bold")
	if !ok || bold.Value != "false" {
		t.Errorf("h1.bold = %v, want last-wins value false", bold)
	}

	kinds := warningKinds(report)
	if kinds["paragraph.indent"] != styleconfig.WarnDuplicateKey {
		t.Errorf("expected duplicate warning for paragraph.indent, got %v", report.Warnings)
	}
	if kinds["h1.bold"] != styleconfig.WarnDuplicateKey {
		t.Errorf("expected duplicate warning for h1.bold, got %v", report.Warnings)
	}
}

func TestDuplicateKeysInCLI(t *testing.T) {
	t.Parallel()

	report, err := styleconfig.Merge(styleconfig.CLILayer([]string{
		"paragraph.indent=1",
		"paragraph.indent=9",
	}))
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}
	indent, ok := findField(report, "paragraph.indent")
	if !ok || indent.Value != "9" {
		t.Errorf("paragraph.indent = %v, want last-wins value 9", indent)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Kind != styleconfig.WarnDuplicateKey {
		t.Errorf("expected one duplicate warning, got %v", report.Warnings)
	}
	if report.Warnings[0].Layer != styleconfig.LayerCLI {
		t.Errorf("duplicate warning layer = %q, want %q", report.Warnings[0].Layer, styleconfig.LayerCLI)
	}
}

func TestUnknownKeys(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"unknown_top": 1,
		"paragraph": {"mystery": true, "indent": 2}
	}`)
	report, err := styleconfig.Merge(styleconfig.FileLayer(data))
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}

	indent, ok := findField(report, "paragraph.indent")
	if !ok || indent.Value != "2" {
		t.Errorf("known key paragraph.indent should still merge, got %v", indent)
	}

	kinds := warningKinds(report)
	if kinds["unknown_top"] != styleconfig.WarnUnknownKey {
		t.Errorf("expected unknown-key warning for unknown_top, got %v", report.Warnings)
	}
	if kinds["paragraph.mystery"] != styleconfig.WarnUnknownKey {
		t.Errorf("expected unknown-key warning for paragraph.mystery, got %v", report.Warnings)
	}
	if _, exists := kinds["paragraph.indent"]; exists {
		t.Errorf("paragraph.indent must not be reported as a warning")
	}
}

func TestParseFailureIsAtomic(t *testing.T) {
	t.Parallel()

	builtin := ansi.StyleConfig{
		Paragraph: ansi.StyleBlock{Indent: uintPtr(1)},
	}
	resolver := styleconfig.NewResolver(builtin)

	fileJSON := []byte(`{"h1": {"color": "#111111"}}`)
	if err := resolver.Apply(styleconfig.FileLayer(fileJSON)); err != nil {
		t.Fatalf("initial Apply failed: %v", err)
	}
	wantConfig, wantReport := resolver.Effective()

	failures := []struct {
		name   string
		layers []styleconfig.LayerInput
	}{
		{
			name:   "malformed JSON",
			layers: []styleconfig.LayerInput{styleconfig.FileLayer([]byte("{not json"))},
		},
		{
			name:   "wrong uint type",
			layers: []styleconfig.LayerInput{styleconfig.CLILayer([]string{"document.margin=abc"})},
		},
		{
			name:   "negative uint",
			layers: []styleconfig.LayerInput{styleconfig.CLILayer([]string{"document.margin=-1"})},
		},
		{
			name:   "wrong bool type",
			layers: []styleconfig.LayerInput{styleconfig.CLILayer([]string{"h1.bold=maybe"})},
		},
		{
			name:   "missing equals sign",
			layers: []styleconfig.LayerInput{styleconfig.CLILayer([]string{"paragraph.indent"})},
		},
		{
			name:   "scalar at object path",
			layers: []styleconfig.LayerInput{styleconfig.CLILayer([]string{"paragraph=oops"})},
		},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			err := resolver.Apply(failure.layers...)
			if err == nil {
				t.Fatal("expected a parse error, got nil")
			}
			var parseErr *styleconfig.ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("error %v is not a *ParseError", err)
			}

			gotConfig, gotReport := resolver.Effective()
			if !reflect.DeepEqual(gotConfig, wantConfig) {
				t.Errorf("effective config changed after parse failure:\n got  %+v\n want %+v", gotConfig, wantConfig)
			}
			if !reflect.DeepEqual(gotReport, wantReport) {
				t.Errorf("provenance report changed after parse failure:\n got  %+v\n want %+v", gotReport, wantReport)
			}
		})
	}

	// A subsequent valid Apply must still succeed and update the config.
	if err := resolver.Apply(styleconfig.CLILayer([]string{"paragraph.indent=7"})); err != nil {
		t.Fatalf("recovery Apply failed: %v", err)
	}
	gotConfig, _ := resolver.Effective()
	if gotConfig.Paragraph.Indent == nil || *gotConfig.Paragraph.Indent != 7 {
		t.Errorf("paragraph.indent = %v, want 7 after recovery", gotConfig.Paragraph.Indent)
	}
}

func TestTypeCoercion(t *testing.T) {
	t.Parallel()

	report, err := styleconfig.Merge(
		styleconfig.FileLayer([]byte(`{"list": {"level_indent": 7}}`)),
		styleconfig.CLILayer([]string{
			"h1.bold=true",
			"document.margin=4",
			"h1.color=#abcdef",
			"list.level_indent=6",
		}),
	)
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}

	if got := report.Config.H1.Bold; got == nil || !*got {
		t.Errorf("h1.bold = %v, want true", got)
	}
	if got := report.Config.Document.Margin; got == nil || *got != 4 {
		t.Errorf("document.margin = %v, want 4", got)
	}
	if got := report.Config.H1.Color; got == nil || *got != "#abcdef" {
		t.Errorf("h1.color = %v, want #abcdef", got)
	}
	// Non-pointer uint leaf: CLI value 6 must win over the JSON value 7.
	if report.Config.List.LevelIndent != 6 {
		t.Errorf("list.level_indent = %d, want 6", report.Config.List.LevelIndent)
	}
	level, ok := findField(report, "list.level_indent")
	if !ok || level.Source != styleconfig.LayerCLI {
		t.Errorf("list.level_indent provenance = %v, want CLI source", level)
	}

	// JSON numbers arrive as float64 and must be validated as integers.
	if _, err := styleconfig.Merge(styleconfig.FileLayer([]byte(`{"document": {"margin": 1.5}}`))); err == nil {
		t.Error("expected an error for non-integral uint value")
	}
	if _, err := styleconfig.Merge(styleconfig.FileLayer([]byte(`{"document": {"margin": true}}`))); err == nil {
		t.Error("expected an error for a bool where a uint is required")
	}
}

func TestZeroValueIsExplicit(t *testing.T) {
	t.Parallel()

	report, err := styleconfig.Merge(styleconfig.CLILayer([]string{"document.margin=0"}))
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}
	margin, ok := findField(report, "document.margin")
	if !ok {
		t.Fatal("document.margin provenance missing")
	}
	if margin.Value != "0" || margin.Source != styleconfig.LayerCLI {
		t.Errorf("document.margin provenance = %+v, want explicit CLI zero", margin)
	}
	if report.Config.Document.Margin == nil || *report.Config.Document.Margin != 0 {
		t.Errorf("document.margin = %v, want non-nil pointer to 0", report.Config.Document.Margin)
	}
}

func TestNestedPointerAllocation(t *testing.T) {
	t.Parallel()

	report, err := styleconfig.Merge(styleconfig.CLILayer([]string{
		"code_block.chroma.keyword.color=#f00",
		"code_block.chroma.keyword.bold=true",
	}))
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}
	chroma := report.Config.CodeBlock.Chroma
	if chroma == nil {
		t.Fatal("expected CodeBlock.Chroma to be allocated")
	}
	if chroma.Keyword.Color == nil || *chroma.Keyword.Color != "#f00" {
		t.Errorf("chroma keyword color = %v, want #f00", chroma.Keyword.Color)
	}
	if chroma.Keyword.Bold == nil || !*chroma.Keyword.Bold {
		t.Errorf("chroma keyword bold = %v, want true", chroma.Keyword.Bold)
	}
	for _, path := range []string{
		"code_block.chroma.keyword.color",
		"code_block.chroma.keyword.bold",
	} {
		if _, ok := findField(report, path); !ok {
			t.Errorf("missing provenance for %q", path)
		}
	}
}

func TestResultsAreStablySorted(t *testing.T) {
	t.Parallel()

	report, err := styleconfig.Merge(
		styleconfig.BuiltinLayer(ansi.StyleConfig{
			H1:        ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: stringPtr("#1")}},
			Paragraph: ansi.StyleBlock{Indent: uintPtr(2)},
			Document:  ansi.StyleBlock{Margin: uintPtr(3)},
		}),
		styleconfig.FileLayer([]byte(`{"h2": {"italic": true}}`)),
		styleconfig.CLILayer([]string{"h3.bold=false"}),
	)
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}

	fieldPaths := make([]string, len(report.Fields))
	for i, field := range report.Fields {
		fieldPaths[i] = field.Field
	}
	sorted := append([]string(nil), fieldPaths...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(fieldPaths, sorted) {
		t.Errorf("fields are not sorted by path:\n got  %v\n want %v", fieldPaths, sorted)
	}
}

func TestWarningsAreStablySorted(t *testing.T) {
	t.Parallel()

	report, err := styleconfig.Merge(
		styleconfig.FileLayer([]byte(`{"zebra": 1, "alpha": 2}`)),
		styleconfig.CLILayer([]string{"mango=1", "apple=2"}),
	)
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}
	if len(report.Warnings) != 4 {
		t.Fatalf("expected 4 unknown-key warnings, got %v", report.Warnings)
	}

	// User-file warnings come before CLI warnings (layer order), and each
	// layer's warnings are sorted by field path.
	got := make([]string, len(report.Warnings))
	for i, warning := range report.Warnings {
		got[i] = string(warning.Layer) + ":" + warning.Field
	}
	want := []string{
		"user-file:alpha",
		"user-file:zebra",
		"cli:apple",
		"cli:mango",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warning order = %v, want %v", got, want)
	}
}

func TestRealDefaultStyleOverlay(t *testing.T) {
	t.Parallel()

	dark := styles.DefaultStyles[styles.DarkStyle]
	if dark == nil {
		t.Fatal("dark default style not available")
	}
	resolver := styleconfig.NewResolver(*dark)

	report, err := styleconfig.Merge(
		styleconfig.BuiltinLayer(*dark),
		styleconfig.FileLayer([]byte(`{"h1": {"color": "#ff0000"}}`)),
	)
	if err != nil {
		t.Fatalf("Merge returned an error: %v", err)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("expected no warnings for a default style, got %v", report.Warnings)
	}
	h1, ok := findField(report, "h1.color")
	if !ok {
		t.Fatal("missing provenance for h1.color")
	}
	if h1.Source != styleconfig.LayerUserFile || h1.Value != `"#ff0000"` {
		t.Errorf("h1.color provenance = %+v, want user-file override", h1)
	}
	prefix, ok := findField(report, "document.block_prefix")
	if !ok {
		t.Fatal("missing provenance for built-in document.block_prefix")
	}
	if prefix.Source != styleconfig.LayerBuiltin {
		t.Errorf("document.block_prefix source = %q, want builtin", prefix.Source)
	}

	if err := resolver.Apply(styleconfig.FileLayer([]byte(`{"h1": {"color": "#00ff00"}}`))); err != nil {
		t.Fatalf("Apply returned an error: %v", err)
	}
	cfg, _ := resolver.Effective()
	if cfg.H1.Color == nil || *cfg.H1.Color != "#00ff00" {
		t.Errorf("effective h1.color = %v, want #00ff00", cfg.H1.Color)
	}
}

func TestLayerValidation(t *testing.T) {
	t.Parallel()

	cfg := ansi.StyleConfig{}
	if _, err := styleconfig.Merge(styleconfig.LayerInput{
		Name:   "bad",
		Config: &cfg,
		JSON:   []byte("{}"),
	}); err == nil {
		t.Error("expected an error when multiple sources are set")
	}

	if _, err := styleconfig.Merge(styleconfig.LayerInput{Name: ""}); err == nil {
		t.Error("expected an error for an empty layer name")
	}

	report, err := styleconfig.Merge()
	if err != nil {
		t.Fatalf("Merge with no layers failed: %v", err)
	}
	if report == nil || len(report.Fields) != 0 || len(report.Warnings) != 0 {
		t.Errorf("empty Merge should return an empty report, got %+v", report)
	}
}

func TestParseErrorContext(t *testing.T) {
	t.Parallel()

	_, err := styleconfig.Merge(styleconfig.CLILayer([]string{"document.margin=nope"}))
	var parseErr *styleconfig.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected *ParseError, got %v", err)
	}
	if parseErr.Layer != styleconfig.LayerCLI {
		t.Errorf("error layer = %q, want %q", parseErr.Layer, styleconfig.LayerCLI)
	}
	if parseErr.Field != "document.margin" {
		t.Errorf("error field = %q, want document.margin", parseErr.Field)
	}
	if !strings.Contains(parseErr.Error(), "document.margin") {
		t.Errorf("error message %q should mention the field", parseErr.Error())
	}
}
