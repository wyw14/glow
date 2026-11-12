package styleconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"charm.land/glamour/v2/ansi"
)

// parsedLayer is a layer decoded into a generic nested map together with the
// order it appeared in.
type parsedLayer struct {
	name   Layer
	order  int
	values map[string]any
}

// parseLayer decodes one layer input into a nested map and returns the
// non-fatal warnings found while decoding.
func parseLayer(in LayerInput) (map[string]any, []Warning, error) {
	sources := 0
	if in.Config != nil {
		sources++
	}
	if in.JSON != nil {
		sources++
	}
	if in.Values != nil {
		sources++
	}
	if sources > 1 {
		return nil, nil, &ParseError{
			Layer: in.Name,
			Err:   errors.New("layer has multiple sources; set exactly one of Config, JSON or Values"),
		}
	}

	switch {
	case in.Config != nil:
		values, err := configToMap(in.Config)
		if err != nil {
			return nil, nil, &ParseError{Layer: in.Name, Err: err}
		}
		return values, nil, nil
	case in.Values != nil:
		values, duplicates, err := parseCLIOverrides(in.Values)
		if err != nil {
			return nil, nil, wrapFieldError(in.Name, err)
		}
		return values, duplicateWarnings(in.Name, duplicates), nil
	default:
		values, duplicates, err := parseJSON(in.JSON)
		if err != nil {
			return nil, nil, &ParseError{Layer: in.Name, Err: err}
		}
		return values, duplicateWarnings(in.Name, duplicates), nil
	}
}

// wrapFieldError converts an internal fieldError into a ParseError.
func wrapFieldError(name Layer, err error) error {
	var fieldErr *fieldError
	if errors.As(err, &fieldErr) {
		return &ParseError{Layer: name, Field: fieldErr.field, Err: fieldErr.err}
	}
	return &ParseError{Layer: name, Err: err}
}

// configToMap converts a built-in style config to the generic nested map
// representation. JSON omitempty semantics ensure only explicitly set fields
// are present, which gives accurate provenance.
func configToMap(cfg *ansi.StyleConfig) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshaling built-in config: %w", err)
	}
	values := make(map[string]any)
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("decoding built-in config: %w", err)
	}
	return values, nil
}

// parseJSON decodes a JSON style file. Blank input is an empty layer.
// Duplicate object keys are reported as warnings; the last occurrence wins,
// matching encoding/json semantics.
func parseJSON(data []byte) (map[string]any, []string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil, nil
	}

	values := make(map[string]any)
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}

	duplicates, err := duplicateJSONKeys(data)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return values, duplicates, nil
}

// duplicateJSONKeys walks the JSON tokens and returns the dotted paths of
// every object key that occurs more than once within the same object.
func duplicateJSONKeys(data []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := nextToken(decoder)
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("expected a JSON object at the top level")
	}

	var duplicates []string
	if err := readJSONObject(decoder, "", &duplicates); err != nil {
		return nil, err
	}
	return duplicates, nil
}

// nextToken reads the next JSON token and wraps decoder errors with context.
func nextToken(decoder *json.Decoder) (json.Token, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("reading JSON token: %w", err)
	}
	return token, nil
}

// readJSONObject consumes an object body after its opening brace.
func readJSONObject(decoder *json.Decoder, prefix string, duplicates *[]string) error {
	seen := make(map[string]bool)
	for decoder.More() {
		keyToken, err := nextToken(decoder)
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("invalid JSON object key")
		}
		path := joinPath(prefix, key)
		if seen[key] {
			*duplicates = append(*duplicates, path)
		}
		seen[key] = true
		if err := readJSONValue(decoder, path, duplicates); err != nil {
			return err
		}
	}
	if _, err := nextToken(decoder); err != nil { // closing brace
		return err
	}
	return nil
}

// readJSONValue consumes a single JSON value, recursing into objects and
// skipping arrays.
func readJSONValue(decoder *json.Decoder, path string, duplicates *[]string) error {
	token, err := nextToken(decoder)
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil // scalar value
	}
	switch delim {
	case '{':
		return readJSONObject(decoder, path, duplicates)
	case '[':
		return skipJSONArray(decoder)
	default:
		return nil
	}
}

// skipJSONArray consumes an array body after its opening bracket.
func skipJSONArray(decoder *json.Decoder) error {
	depth := 1
	for depth > 0 {
		token, err := nextToken(decoder)
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delim {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
	}
	return nil
}

// parseCLIOverrides decodes "dotted.key=value" command-line overrides.
// Repeating a key is reported as a duplicate warning and the last value
// wins.
func parseCLIOverrides(overrides []string) (map[string]any, []string, error) {
	values := make(map[string]any)
	seen := make(map[string]bool)
	var duplicates []string

	for _, item := range overrides {
		idx := strings.IndexByte(item, '=')
		if idx <= 0 {
			return nil, nil, &fieldError{
				field: item,
				err:   errors.New(`expected an override in "dotted.key=value" form`),
			}
		}
		path := item[:idx]
		value := item[idx+1:]
		if seen[path] {
			duplicates = append(duplicates, path)
		}
		seen[path] = true

		keys := strings.Split(path, pathSeparator)
		if err := setNestedValue(values, keys, value); err != nil {
			return nil, nil, &fieldError{field: path, err: err}
		}
	}
	return values, duplicates, nil
}

// setNestedValue stores value in the nested map at keys, creating intermediate
// objects as needed.
func setNestedValue(root map[string]any, keys []string, value string) error {
	current := root
	for i, key := range keys {
		if key == "" {
			return errors.New("empty key segment")
		}
		if i == len(keys)-1 {
			current[key] = value
			return nil
		}
		next, ok := current[key]
		if !ok {
			child := make(map[string]any)
			current[key] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("conflicting path: %q is already set to a scalar value", key)
		}
		current = child
	}
	return nil
}

// unknownKeyWarnings walks decoded values and reports every key that is
// neither a known leaf nor an intermediate object path.
func unknownKeyWarnings(name Layer, values map[string]any, sch *schema) []Warning {
	var warnings []Warning
	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		for key, value := range node {
			path := joinPath(prefix, key)
			if _, ok := sch.leaves[path]; ok {
				continue
			}
			if sch.isPrefix(path) {
				if child, ok := value.(map[string]any); ok {
					walk(path, child)
				}
				// A scalar at an object path is reported as a type error
				// during the merge step.
				continue
			}
			warnings = append(warnings, Warning{
				Layer:   name,
				Field:   path,
				Kind:    WarnUnknownKey,
				Message: fmt.Sprintf("unknown configuration key %q; ignoring it", path),
			})
		}
	}
	walk("", values)
	return warnings
}

// duplicateWarnings turns duplicate key paths into warnings.
func duplicateWarnings(name Layer, paths []string) []Warning {
	warnings := make([]Warning, 0, len(paths))
	for _, path := range paths {
		warnings = append(warnings, Warning{
			Layer:   name,
			Field:   path,
			Kind:    WarnDuplicateKey,
			Message: fmt.Sprintf("duplicate key %q; keeping the last value", path),
		})
	}
	return warnings
}

// validateNodeTypes makes sure every path that addresses a nested object in
// the schema actually holds an object in the decoded layer.
func validateNodeTypes(name Layer, values map[string]any, sch *schema) error {
	var validate func(prefix string, node map[string]any) error
	validate = func(prefix string, node map[string]any) error {
		for key, value := range node {
			path := joinPath(prefix, key)
			if !sch.isPrefix(path) {
				continue
			}
			if _, ok := value.(map[string]any); !ok {
				return &ParseError{
					Layer: name,
					Field: path,
					Err:   fmt.Errorf("expected a nested object at %q, got %T", path, value),
				}
			}
			if err := validate(path, value.(map[string]any)); err != nil {
				return err
			}
		}
		return nil
	}
	return validate("", values)
}

// lookupPath resolves a dotted path inside a decoded layer.
func lookupPath(values map[string]any, path string) (any, bool) {
	var current any = values
	for _, key := range strings.Split(path, pathSeparator) {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = obj[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
