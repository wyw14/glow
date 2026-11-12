package styleconfig

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"charm.land/glamour/v2/ansi"
)

const pathSeparator = "."

// leafKind describes the scalar type of a configuration leaf field.
type leafKind int

const (
	kindString leafKind = iota
	kindBool
	kindUint
)

// fieldSchema describes a single leaf field of ansi.StyleConfig.
type fieldSchema struct {
	// path is the dotted JSON path of the field.
	path string
	// indices is the reflect.StructField index sequence reaching the leaf.
	indices []int
	// kind is the scalar type of the leaf.
	kind leafKind
	// isPtr reports whether the leaf itself is a pointer (*string, *bool,
	// *uint).
	isPtr bool
}

// schema is the reflection-derived description of ansi.StyleConfig.
type schema struct {
	// leaves maps every dotted leaf path to its descriptor.
	leaves map[string]fieldSchema
	// paths is the sorted list of every leaf path.
	paths []string
	// prefixes contains every intermediate object path.
	prefixes map[string]bool
}

// styleSchema is built once from the glamour StyleConfig type.
var styleSchema = buildSchema()

// buildSchema walks ansi.StyleConfig and indexes every scalar leaf using its
// JSON field names.
func buildSchema() schema {
	s := schema{
		leaves:   make(map[string]fieldSchema),
		prefixes: make(map[string]bool),
	}
	s.walk(reflect.TypeOf(ansi.StyleConfig{}), nil, "")
	s.paths = make([]string, 0, len(s.leaves))
	for path := range s.leaves {
		s.paths = append(s.paths, path)
	}
	sort.Strings(s.paths)
	return s
}

// walk recursively indexes the fields of t. Anonymous (embedded) structs are
// flattened so their leaves keep the surrounding prefix.
func (s *schema) walk(t reflect.Type, indices []int, prefix string) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		fieldIndices := append(append([]int(nil), indices...), i)
		fieldType := field.Type

		if fieldType.Kind() == reflect.Pointer {
			if fieldType.Elem().Kind() == reflect.Struct {
				path := joinPath(prefix, jsonName(field))
				s.prefixes[path] = true
				s.walk(fieldType.Elem(), fieldIndices, path)
				continue
			}
			s.addLeaf(joinPath(prefix, jsonName(field)), fieldIndices, fieldType.Elem().Kind(), true)
			continue
		}

		if fieldType.Kind() == reflect.Struct {
			if field.Anonymous {
				s.walk(fieldType, fieldIndices, prefix)
			} else {
				path := joinPath(prefix, jsonName(field))
				s.prefixes[path] = true
				s.walk(fieldType, fieldIndices, path)
			}
			continue
		}

		s.addLeaf(joinPath(prefix, jsonName(field)), fieldIndices, fieldType.Kind(), false)
	}
}

// addLeaf registers a scalar leaf and panics on unsupported field types,
// which would indicate a glamour change that needs explicit handling.
func (s *schema) addLeaf(path string, indices []int, kind reflect.Kind, isPtr bool) {
	var leafKind leafKind
	switch {
	case kind == reflect.String:
		leafKind = kindString
	case kind == reflect.Bool:
		leafKind = kindBool
	case kind >= reflect.Uint && kind <= reflect.Uintptr:
		leafKind = kindUint
	default:
		panic(fmt.Sprintf("styleconfig: unsupported field kind %s for %q", kind, path))
	}
	s.leaves[path] = fieldSchema{
		path:    path,
		indices: append([]int(nil), indices...),
		kind:    leafKind,
		isPtr:   isPtr,
	}
}

// isPrefix reports whether path is an intermediate object with leaves below
// it.
func (s *schema) isPrefix(path string) bool {
	return s.prefixes[path]
}

// set stores raw into the leaf described by fs, allocating intermediate
// pointer structs as needed and coercing raw to the target scalar type.
func (s *schema) set(cfg *ansi.StyleConfig, fs fieldSchema, raw any) error {
	parent := reflect.ValueOf(cfg).Elem()
	last := len(fs.indices) - 1
	for _, idx := range fs.indices[:last] {
		parent = parent.Field(idx)
		if parent.Kind() == reflect.Pointer && parent.Type().Elem().Kind() == reflect.Struct {
			if parent.IsNil() {
				parent.Set(reflect.New(parent.Type().Elem()))
			}
			parent = parent.Elem()
		}
	}

	leaf := parent.Field(fs.indices[last])
	value, err := convertValue(raw, fs, leaf.Type())
	if err != nil {
		return err
	}
	leaf.Set(value)
	return nil
}

// format renders the current leaf value as a string. The second result is
// false when the field or one of its parent pointers is unset.
func (s *schema) format(cfg *ansi.StyleConfig, fs fieldSchema) (string, bool) {
	value := reflect.ValueOf(cfg).Elem()
	for _, idx := range fs.indices {
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return "", false
			}
			value = value.Elem()
		}
		value = value.Field(idx)
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "", false
		}
		value = value.Elem()
	}

	switch fs.kind {
	case kindString:
		return strconv.Quote(value.String()), true
	case kindBool:
		return strconv.FormatBool(value.Bool()), true
	case kindUint:
		return strconv.FormatUint(value.Uint(), 10), true
	default:
		return "", false
	}
}

// convertValue coerces raw into a reflect.Value assignable to target,
// allocating pointer leaves when needed.
func convertValue(raw any, fs fieldSchema, target reflect.Type) (reflect.Value, error) {
	if fs.isPtr {
		elem, err := convertScalar(raw, fs.kind, target.Elem())
		if err != nil {
			return reflect.Value{}, err
		}
		ptr := reflect.New(target.Elem())
		ptr.Elem().Set(elem)
		return ptr, nil
	}
	return convertScalar(raw, fs.kind, target)
}

// convertScalar coerces raw into the given scalar type.
func convertScalar(raw any, kind leafKind, target reflect.Type) (reflect.Value, error) {
	switch kind {
	case kindString:
		str, ok := raw.(string)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected string, got %T", raw)
		}
		return reflect.ValueOf(str).Convert(target), nil
	case kindBool:
		switch value := raw.(type) {
		case bool:
			return reflect.ValueOf(value).Convert(target), nil
		case string:
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("invalid bool %q: %w", value, err)
			}
			return reflect.ValueOf(parsed).Convert(target), nil
		default:
			return reflect.Value{}, fmt.Errorf("expected bool, got %T", raw)
		}
	case kindUint:
		parsed, err := toUint(raw)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(parsed).Convert(target), nil
	default:
		return reflect.Value{}, fmt.Errorf("unknown field kind %d", kind)
	}
}

// toUint coerces a JSON number or a CLI string into an unsigned integer.
func toUint(raw any) (uint64, error) {
	switch value := raw.(type) {
	case string:
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid unsigned integer %q: %w", value, err)
		}
		return parsed, nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value != math.Trunc(value) {
			return 0, fmt.Errorf("invalid unsigned integer %v", value)
		}
		if value > float64(math.MaxUint64) {
			return 0, fmt.Errorf("unsigned integer out of range: %v", value)
		}
		return uint64(value), nil
	default:
		return 0, fmt.Errorf("expected unsigned integer, got %T", raw)
	}
}

// jsonName returns the JSON name of a struct field, falling back to the Go
// field name when no usable tag is present.
func jsonName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "" || name == "-" {
		return field.Name
	}
	return name
}

// joinPath joins a prefix and a key with the dotted path separator.
func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + pathSeparator + name
}
