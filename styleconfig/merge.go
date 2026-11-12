package styleconfig

import (
	"sort"

	"charm.land/glamour/v2/ansi"
)

// Merge combines the given layers in order: earlier layers provide defaults,
// later layers override them. The canonical usage is to pass the built-in
// layer first, the user file layer second and the command-line layer last.
//
// The returned report contains the merged configuration together with the
// source layer and conflict chain of every field that was set. Layers are
// parsed into temporary values first, so any parse failure aborts the merge
// without producing a partial configuration.
func Merge(layers ...LayerInput) (*Report, error) {
	report := &Report{
		Config:   ansi.StyleConfig{},
		Fields:   []FieldSource{},
		Warnings: []Warning{},
	}
	if len(layers) == 0 {
		return report, nil
	}

	parsed := make([]parsedLayer, 0, len(layers))
	var warnings []Warning
	for order, input := range layers {
		if input.Name == "" {
			return nil, errEmptyName
		}
		values, layerWarnings, err := parseLayer(input)
		if err != nil {
			return nil, err
		}
		if err := validateNodeTypes(input.Name, values, &styleSchema); err != nil {
			return nil, err
		}
		parsed = append(parsed, parsedLayer{
			name:   input.Name,
			order:  order,
			values: values,
		})
		warnings = append(warnings, layerWarnings...)
		warnings = append(warnings, unknownKeyWarnings(input.Name, values, &styleSchema)...)
	}

	merged := ansi.StyleConfig{}
	fields := make([]FieldSource, 0)
	for _, path := range styleSchema.paths {
		field := styleSchema.leaves[path]

		chain := make([]Layer, 0, len(parsed))
		var winner any
		for _, layer := range parsed {
			raw, ok := lookupPath(layer.values, path)
			if !ok {
				continue
			}
			chain = append(chain, layer.name)
			winner = raw
		}
		if len(chain) == 0 {
			continue
		}

		source := chain[len(chain)-1]
		if err := styleSchema.set(&merged, field, winner); err != nil {
			return nil, &ParseError{Layer: source, Field: path, Err: err}
		}
		value, ok := styleSchema.format(&merged, field)
		if !ok {
			return nil, &ParseError{
				Layer: source,
				Field: path,
				Err:   errUnresolvedField,
			}
		}
		fields = append(fields, FieldSource{
			Field:         path,
			Value:         value,
			Source:        source,
			ConflictChain: chain,
		})
	}

	sort.SliceStable(fields, func(i, j int) bool {
		return fields[i].Field < fields[j].Field
	})
	sortWarnings(warnings, parsed)

	report.Config = merged
	report.Fields = fields
	report.Warnings = warnings
	return report, nil
}

// sortWarnings stably sorts warnings by layer order, then field, then kind.
func sortWarnings(warnings []Warning, layers []parsedLayer) {
	rank := make(map[Layer]int, len(layers))
	for _, layer := range layers {
		rank[layer.name] = layer.order
	}
	sort.SliceStable(warnings, func(i, j int) bool {
		if rank[warnings[i].Layer] != rank[warnings[j].Layer] {
			return rank[warnings[i].Layer] < rank[warnings[j].Layer]
		}
		if warnings[i].Field != warnings[j].Field {
			return warnings[i].Field < warnings[j].Field
		}
		return warnings[i].Kind < warnings[j].Kind
	})
}
