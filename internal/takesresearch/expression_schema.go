package takesresearch

import (
	"encoding/json"
	"fmt"
)

// basketResearchProviderSchema adapts a copy so canonical validation bounds
// stay intact when provider grammar limitations require a simpler schema.
func basketResearchProviderSchema(schema any, omitArrayBounds bool) (any, error) {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal basket provider schema: %w", err)
	}
	var wire any
	if err = json.Unmarshal(encoded, &wire); err != nil {
		return nil, fmt.Errorf("decode basket provider schema: %w", err)
	}
	basketResearchProviderConstraints(wire, omitArrayBounds)
	return wire, nil
}

func basketResearchProviderConstraints(node any, omitArrayBounds bool) {
	switch value := node.(type) {
	case map[string]any:
		if omitArrayBounds && value["type"] == "array" {
			delete(value, "maxItems")
		}
		pair, ok := value["oneOf"].([]any)
		if ok && len(pair) == 2 {
			first, firstOK := pair[0].(map[string]any)
			second, secondOK := pair[1].(map[string]any)
			firstType, firstTyped := first["type"].(string)
			secondType, secondTyped := second["type"].(string)
			if firstOK && secondOK && firstTyped && secondTyped && ((firstType == "null" && secondType != "null") || (secondType == "null" && firstType != "null")) {
				if _, exists := value["anyOf"]; !exists {
					value["anyOf"] = pair
					delete(value, "oneOf")
				}
			}
		}
		for _, child := range value {
			basketResearchProviderConstraints(child, omitArrayBounds)
		}
	case []any:
		for _, child := range value {
			basketResearchProviderConstraints(child, omitArrayBounds)
		}
	}
}
