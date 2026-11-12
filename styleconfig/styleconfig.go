// Package styleconfig provides a layered parser for markdown rendering
// style options.
//
// Layers are merged in a fixed priority order: built-in defaults first,
// then the user style file, and finally command-line overrides. For every
// field that ends up in the merged configuration the parser records which
// layer supplied the final value (the source) together with the complete
// conflict chain of every layer that touched the field.
//
// Parsing a layer is all-or-nothing: a malformed layer makes Resolve fail
// without changing the configuration that is already in effect.
package styleconfig

import (
	"errors"
	"fmt"

	"charm.land/glamour/v2/ansi"
)

// Layer identifies a configuration layer.
type Layer string

const (
	// LayerBuiltin is the built-in default layer with the lowest priority.
	LayerBuiltin Layer = "builtin"
	// LayerUserFile is the user supplied style file layer.
	LayerUserFile Layer = "user-file"
	// LayerCLI is the command-line override layer with the highest priority.
	LayerCLI Layer = "cli"
)

// WarningKind classifies a non-fatal configuration warning.
type WarningKind string

const (
	// WarnDuplicateKey reports a key that appears more than once inside a
	// single layer. The last occurrence wins.
	WarnDuplicateKey WarningKind = "duplicate-key"
	// WarnUnknownKey reports a key that does not map to any known style
	// field. Unknown keys are ignored.
	WarnUnknownKey WarningKind = "unknown-key"
)

// Warning describes a recoverable problem found while parsing a layer.
type Warning struct {
	// Layer is the layer the warning was found in.
	Layer Layer
	// Field is the dotted configuration path the warning refers to.
	Field string
	// Kind classifies the warning.
	Kind WarningKind
	// Message is a human readable explanation.
	Message string
}

// FieldSource records the provenance of a single merged configuration field.
type FieldSource struct {
	// Field is the dotted JSON path of the field, for example
	// "paragraph.indent" or "code_block.chroma.keyword.color".
	Field string
	// Value is the final value rendered as a string.
	Value string
	// Source is the layer that supplied the final value. It equals the
	// last entry of ConflictChain.
	Source Layer
	// ConflictChain lists, in merge order, every layer that explicitly set
	// the field.
	ConflictChain []Layer
}

// Report is the result of merging a set of configuration layers.
type Report struct {
	// Config is the fully merged style configuration.
	Config ansi.StyleConfig
	// Fields holds the provenance for every field that was set by at least
	// one layer, stably sorted by Field.
	Fields []FieldSource
	// Warnings holds non-fatal problems such as duplicate or unknown keys,
	// stably sorted by layer order and field.
	Warnings []Warning
}

// LayerInput describes one configuration layer. Exactly one of Config, JSON
// or Values must be set; when none are set the layer is treated as an empty
// JSON layer.
type LayerInput struct {
	// Name is the layer label used in provenance reports.
	Name Layer
	// Config provides an already parsed built-in style configuration.
	Config *ansi.StyleConfig
	// JSON provides the raw JSON contents of a user style file.
	JSON []byte
	// Values provides command-line overrides in "dotted.key=value" form.
	Values []string
}

// BuiltinLayer wraps a built-in style configuration as a configuration layer.
func BuiltinLayer(cfg ansi.StyleConfig) LayerInput {
	c := cfg
	return LayerInput{Name: LayerBuiltin, Config: &c}
}

// FileLayer wraps the raw JSON contents of a user style file as a layer.
func FileLayer(data []byte) LayerInput {
	return LayerInput{Name: LayerUserFile, JSON: data}
}

// CLILayer wraps command-line "dotted.key=value" overrides as a layer.
func CLILayer(overrides []string) LayerInput {
	return LayerInput{Name: LayerCLI, Values: overrides}
}

// ParseError is returned when a layer cannot be parsed or coerced.
type ParseError struct {
	// Layer is the layer that failed to parse.
	Layer Layer
	// Field is the dotted key that triggered the error, when known.
	Field string
	// Err is the underlying parse error.
	Err error
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("styleconfig: layer %q: %s", e.Layer, e.Err)
	}
	return fmt.Sprintf("styleconfig: layer %q, key %q: %s", e.Layer, e.Field, e.Err)
}

// Unwrap returns the underlying error.
func (e *ParseError) Unwrap() error {
	return e.Err
}

// fieldError associates a parse error with the dotted key that caused it.
type fieldError struct {
	field string
	err   error
}

// Error implements the error interface.
func (e *fieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.field, e.err)
}

// Unwrap returns the underlying error.
func (e *fieldError) Unwrap() error {
	return e.err
}

// Resolver keeps the effective style configuration and its provenance
// report. Apply merges a fresh layer set on top of the original built-in
// defaults and only swaps the effective configuration when every layer
// parses successfully, so a parse failure never changes what is in effect.
type Resolver struct {
	builtin ansi.StyleConfig
	current ansi.StyleConfig
	report  Report
}

// NewResolver creates a Resolver seeded with the given built-in defaults.
func NewResolver(builtin ansi.StyleConfig) *Resolver {
	return &Resolver{
		builtin: builtin,
		current: builtin,
		report:  Report{Fields: []FieldSource{}, Warnings: []Warning{}},
	}
}

// Apply merges the built-in defaults with the given layers (typically the
// user file and command-line overrides, in that order). On any parse error
// the effective configuration and the previous report are left untouched.
func (r *Resolver) Apply(layers ...LayerInput) error {
	all := make([]LayerInput, 0, len(layers)+1)
	all = append(all, BuiltinLayer(r.builtin))
	all = append(all, layers...)

	report, err := Merge(all...)
	if err != nil {
		return err
	}

	r.current = report.Config
	r.report = *report
	return nil
}

// Effective returns the current style configuration and its provenance
// report.
func (r *Resolver) Effective() (ansi.StyleConfig, Report) {
	return r.current, r.report
}

// errEmptyName is returned when a layer has no name.
var errEmptyName = errors.New("styleconfig: layer name must not be empty")

// errUnresolvedField is returned when a field could not be read back after
// being merged, which indicates an internal schema mismatch.
var errUnresolvedField = errors.New("merged field could not be resolved")
