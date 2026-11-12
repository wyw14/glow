package styleplan

import (
	"fmt"
	"image/color"
	"reflect"
	"strconv"
	"strings"

	"charm.land/glamour/v2/ansi"
	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"
)

const (
	colorField           = "color"
	backgroundColorField = "background_color"
	blinkField           = "blink"
	concealField         = "conceal"
	// chromaPrefix marks the syntax highlighting block. Chroma builds its
	// own styles from hex or named colors and rejects ANSI index strings, so
	// its entries are only removed on colorless targets, never rewritten.
	chromaPrefix = "code_block.chroma"
)

// asciiGlyphs maps Unicode glyphs used by glamour themes to ASCII fallbacks
// that remain readable on dumb terminals and in redirected output.
var asciiGlyphs = map[rune]string{
	'\u00a0':     " ",   // no-break space
	'\u2022':     "*",   // bullet
	'\u2026':     "...", // horizontal ellipsis
	'\u2500':     "-",   // light horizontal
	'\u2501':     "-",   // heavy horizontal
	'\u2502':     "|",   // light vertical
	'\u2503':     "|",   // heavy vertical
	'\u2506':     "|",   // light triple dash vertical
	'\u250a':     "|",   // heavy triple dash vertical
	'\u258c':     "|",   // left half block
	'\u2192':     "->",  // rightwards arrow
	'\u2713':     "x",   // check mark
	'\u2714':     "x",   // heavy check mark
	'\U0001f836': "->",  // rightwards sans-serif arrow
	'\u201c':     "\"",  // left double quotation mark
	'\u201d':     "\"",  // right double quotation mark
	'\u2018':     "'",   // left single quotation mark
	'\u2019':     "'",   // right single quotation mark
}

// applier walks a StyleConfig through reflection and records every concrete
// field change it performs. It only mutates the caller owned copy.
type applier struct {
	strategy *Strategy
	degraded []Degradation
}

// Apply degrades the given style configuration so it matches the immutable
// strategy. The configuration is modified in place; callers must pass a copy
// they own (for example a struct copy of a default style). It returns the
// concrete field level degradation records, which are intentionally separate
// from the plan level records carried by the strategy.
func Apply(cfg *ansi.StyleConfig, strategy *Strategy) []Degradation {
	if cfg == nil || strategy == nil {
		return nil
	}
	a := &applier{
		strategy: strategy,
		degraded: make([]Degradation, 0, 16),
	}
	a.walk(reflect.ValueOf(cfg).Elem(), "")
	if strategy.Narrow() {
		a.applyNarrowGeometry(cfg)
	}
	return a.degraded
}

func (a *applier) record(field string, reason Reason, from, to string) {
	a.degraded = append(a.degraded, Degradation{
		Field:  field,
		Reason: reason,
		From:   from,
		To:     to,
	})
}

func (a *applier) walk(v reflect.Value, path string) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		a.walkPointer(v, path)
	case reflect.Struct:
		typ := v.Type()
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			next := path
			if !field.Anonymous {
				if name := jsonFieldName(field); path == "" {
					next = name
				} else {
					next = path + "." + name
				}
			}
			a.walk(v.Field(i), next)
		}
	case reflect.String:
		if a.strategy.Unicode() == UnicodeASCII {
			a.replaceGlyphs(v, path)
		}
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Uintptr,
		reflect.Float32,
		reflect.Float64,
		reflect.Complex64,
		reflect.Complex128,
		reflect.Array,
		reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Slice,
		reflect.UnsafePointer:
		// Scalars and unsupported kinds need no transformation.
	}
}

func (a *applier) walkPointer(pointer reflect.Value, path string) {
	switch pointer.Type().Elem().Kind() {
	case reflect.String:
		switch pathBase(path) {
		case colorField, backgroundColorField:
			a.downgradeColor(pointer, path)
		default:
			a.replacePointerString(pointer, path)
		}
	case reflect.Bool:
		if pathBase(path) == blinkField || pathBase(path) == concealField {
			a.dropUnsupportedDecoration(pointer, path)
		}
	case reflect.Struct:
		// Clone nested struct pointers (for example *Chroma) before descending
		// so the transformation never reaches a struct shared with a default
		// style. Scalar pointers are replaced wholesale, never edited in place.
		clone := reflect.New(pointer.Type().Elem())
		clone.Elem().Set(pointer.Elem())
		pointer.Set(clone)
		a.walk(pointer.Elem(), path)
	case reflect.Invalid,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Uintptr,
		reflect.Float32,
		reflect.Float64,
		reflect.Complex64,
		reflect.Complex128,
		reflect.Array,
		reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice,
		reflect.UnsafePointer:
		a.walk(pointer.Elem(), path)
	}
}

// downgradeColor removes colors on colorless targets and downsamples hex and
// indexed colors to the detected depth on ANSI and ANSI256 targets.
func (a *applier) downgradeColor(pointer reflect.Value, path string) {
	original := pointer.Elem().String()
	if a.strategy.ColorDepth() >= colorprofile.TrueColor {
		return
	}
	if !a.strategy.EmitColor() {
		pointer.Set(reflect.Zero(pointer.Type()))
		a.record(path, ReasonNoColor, original, "")
		return
	}

	if strings.HasPrefix(path, chromaPrefix) {
		// Chroma's terminal256 formatter performs its own color downsampling;
		// rewriting hex entries to indices would make chroma reject the style.
		return
	}

	replacement, changed := convertColor(original, a.strategy.ColorDepth())
	if !changed || replacement == original {
		return
	}
	if replacement == "" {
		pointer.Set(reflect.Zero(pointer.Type()))
	} else {
		clone := reflect.New(pointer.Type().Elem())
		clone.Elem().SetString(replacement)
		pointer.Set(clone)
	}
	a.record(path, ReasonColorDepth, original, replacement)
}

// dropUnsupportedDecoration removes blink and conceal, which are unreliable
// on colorless and redirected targets, while keeping bold and italic.
func (a *applier) dropUnsupportedDecoration(pointer reflect.Value, path string) {
	if a.strategy.EmitColor() {
		return
	}
	original := strconv.FormatBool(pointer.Elem().Bool())
	pointer.Set(reflect.Zero(pointer.Type()))
	a.record(path, ReasonNoColor, original, "unset")
}

// replacePointerString swaps in a cloned pointer holding the ASCII folded
// value, leaving a potentially shared pointee untouched.
func (a *applier) replacePointerString(pointer reflect.Value, path string) {
	if a.strategy.Unicode() != UnicodeASCII {
		return
	}
	original := pointer.Elem().String()
	replacement, changed := asciiFold(original)
	if !changed {
		return
	}
	clone := reflect.New(pointer.Type().Elem())
	clone.Elem().SetString(replacement)
	pointer.Set(clone)
	a.record(path, ReasonASCIITerminal, original, replacement)
}

func (a *applier) replaceGlyphs(value reflect.Value, path string) {
	original := value.String()
	replacement, changed := asciiFold(original)
	if !changed {
		return
	}
	value.SetString(replacement)
	a.record(path, ReasonASCIITerminal, original, replacement)
}

// applyNarrowGeometry collapses horizontal spacing so content stays readable
// when the wrap width drops below narrowWidth.
func (a *applier) applyNarrowGeometry(cfg *ansi.StyleConfig) {
	collapseMargin := func(path string, block *ansi.StyleBlock) {
		if block.Margin == nil || *block.Margin == 0 {
			return
		}
		from := strconv.FormatUint(uint64(*block.Margin), 10)
		zero := uint(0)
		block.Margin = &zero
		a.record(path+".margin", ReasonNarrowTerminal, from, "0")
	}

	collapseMargin("document", &cfg.Document)
	collapseMargin("paragraph", &cfg.Paragraph)
	collapseMargin("heading", &cfg.Heading)
	collapseMargin("code", &cfg.Code)
	collapseMargin("code_block", &cfg.CodeBlock.StyleBlock)
	collapseMargin("table", &cfg.Table.StyleBlock)
	collapseMargin("block_quote", &cfg.BlockQuote)

	if cfg.BlockQuote.Indent != nil && *cfg.BlockQuote.Indent > 0 {
		from := strconv.FormatUint(uint64(*cfg.BlockQuote.Indent), 10)
		zero := uint(0)
		cfg.BlockQuote.Indent = &zero
		a.record("block_quote.indent", ReasonNarrowTerminal, from, "0")
	}
	if cfg.List.LevelIndent > 1 {
		from := strconv.FormatUint(uint64(cfg.List.LevelIndent), 10)
		cfg.List.LevelIndent = 1
		a.record("list.level_indent", ReasonNarrowTerminal, from, "1")
	}
}

// convertColor downsampled a glamour color string. The changed flag is false
// when the input format should be left untouched; an empty replacement means
// the color must be removed.
func convertColor(original string, profile colorprofile.Profile) (replacement string, changed bool) {
	if strings.HasPrefix(original, "#") {
		parsed, ok := parseHexColor(original)
		if !ok {
			return original, false
		}
		return colorString(profile.Convert(parsed)), true
	}

	index, err := strconv.Atoi(original)
	if err != nil || index < 0 || index > 255 {
		// Named ANSI colors and unknown formats already work at ANSI depth.
		return original, false
	}
	if profile != colorprofile.ANSI {
		// ANSI256 keeps indexed colors unchanged.
		return original, false
	}
	return colorString(profile.Convert(xansi.IndexedColor(index))), true
}

func parseHexColor(value string) (xansi.RGBColor, bool) {
	hex := strings.TrimPrefix(value, "#")
	switch len(hex) {
	case 3:
		red, okR := hexNibble(hex[0])
		green, okG := hexNibble(hex[1])
		blue, okB := hexNibble(hex[2])
		if !okR || !okG || !okB {
			return xansi.RGBColor{}, false
		}
		return xansi.RGBColor{R: red * 17, G: green * 17, B: blue * 17}, true
	case 6:
		red, okR := hexByte(hex[0:2])
		green, okG := hexByte(hex[2:4])
		blue, okB := hexByte(hex[4:6])
		if !okR || !okG || !okB {
			return xansi.RGBColor{}, false
		}
		return xansi.RGBColor{R: red, G: green, B: blue}, true
	default:
		return xansi.RGBColor{}, false
	}
}

func hexByte(value string) (uint8, bool) {
	high, okHigh := hexNibble(value[0])
	low, okLow := hexNibble(value[1])
	if !okHigh || !okLow {
		return 0, false
	}
	return high*16 + low, true
}

func hexNibble(char byte) (uint8, bool) {
	switch {
	case char >= '0' && char <= '9':
		return char - '0', true
	case char >= 'a' && char <= 'f':
		return char - 'a' + 10, true
	case char >= 'A' && char <= 'F':
		return char - 'A' + 10, true
	default:
		return 0, false
	}
}

// colorString renders a converted color back into a glamour color string:
// an ANSI index for basic or indexed colors, hex for true color, and empty
// when the profile drops the color entirely.
func colorString(converted color.Color) string {
	switch c := converted.(type) {
	case nil:
		return ""
	case xansi.BasicColor:
		return strconv.Itoa(int(c))
	case xansi.IndexedColor:
		return strconv.Itoa(int(c))
	case xansi.RGBColor:
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	default:
		// Any remaining color type, including deprecated TrueColor values,
		// exposes its components through the generic color.Color contract.
		red, green, blue, _ := converted.RGBA()
		return fmt.Sprintf("#%02x%02x%02x", red>>8, green>>8, blue>>8)
	}
}

// asciiFold replaces every non-ASCII rune with its ASCII fallback. Unknown
// runes degrade to "?" so output never contains undisplayable characters.
func asciiFold(input string) (string, bool) {
	if !strings.ContainsFunc(input, func(r rune) bool { return r > 127 }) {
		return input, false
	}

	var b strings.Builder
	b.Grow(len(input))
	changed := false
	for _, r := range input {
		if r <= 127 {
			b.WriteRune(r)
			continue
		}
		if replacement, ok := asciiGlyphs[r]; ok {
			b.WriteString(replacement)
		} else {
			b.WriteString("?")
		}
		changed = true
	}
	return b.String(), changed
}

func jsonFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" || tag == "-" {
		return field.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return field.Name
	}
	return name
}

func pathBase(path string) string {
	index := strings.LastIndexByte(path, '.')
	if index < 0 {
		return path
	}
	return path[index+1:]
}
