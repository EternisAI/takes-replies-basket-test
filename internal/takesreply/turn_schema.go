package takesreply

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
)

// turnModelOutputSchema makes optional values nullable and moves constraints
// unsupported by Claude's structured output grammar into descriptions. The
// service validators retain those bounds, and the model does not echo the
// server-owned evidence check.
func turnModelOutputSchema() (any, error) {
	encoded, err := json.Marshal(llm.GenerateSchema[TurnModelOutput]())
	if err != nil {
		return nil, fmt.Errorf("marshal Takes turn schema: %w", err)
	}
	var schema any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, fmt.Errorf("decode Takes turn schema: %w", err)
	}
	if object, ok := schema.(map[string]any); ok {
		// Keep the accepted response contract stable across the internal package
		// extraction. The schema ID is a wire identifier, not a Go package name.
		object["$id"] = "https://github.com/axionhypothesis/backend-go/internal/takesconversation/turn-model-output"
	}
	schema, err = addPointerNullability(schema, reflect.TypeOf(TurnModelOutput{}))
	if err != nil {
		return nil, err
	}
	return normalizeStrictTurnSchema(schema), nil
}

func normalizeStrictTurnSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeStrictTurnSchema(child)
		}
		var constraints []string
		if maxLength, ok := typed["maxLength"]; ok {
			constraints = append(constraints, fmt.Sprintf("Maximum length: %v characters.", maxLength))
			delete(typed, "maxLength")
		}
		if maxItems, ok := typed["maxItems"]; ok {
			constraints = append(constraints, fmt.Sprintf("Maximum items: %v.", maxItems))
			delete(typed, "maxItems")
		}
		if len(constraints) > 0 {
			note := strings.Join(constraints, " ") + " The application validates these limits."
			if description, ok := typed["description"].(string); ok && description != "" {
				typed["description"] = description + " " + note
			} else {
				typed["description"] = note
			}
		}
		if properties, ok := typed["properties"].(map[string]any); ok {
			typed["additionalProperties"] = false
			required, _ := typed["required"].([]any)
			seen := make(map[string]bool, len(required))
			for _, name := range required {
				if field, ok := name.(string); ok {
					seen[field] = true
				}
			}
			missing := make([]string, 0)
			for name := range properties {
				if !seen[name] {
					missing = append(missing, name)
				}
			}
			sort.Strings(missing)
			for _, name := range missing {
				required = append(required, name)
			}
			typed["required"] = required
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = normalizeStrictTurnSchema(child)
		}
		return typed
	default:
		return value
	}
}

func addPointerNullability(schema any, typ reflect.Type) (any, error) {
	if typ.Kind() == reflect.Pointer {
		base, err := addPointerNullability(schema, typ.Elem())
		if err != nil {
			return nil, err
		}
		return nullableSchema(base), nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		object, ok := schema.(map[string]any)
		if !ok {
			return schema, nil
		}
		properties, ok := object["properties"].(map[string]any)
		if !ok {
			return schema, nil // e.g. time.Time, which is reflected as a string.
		}
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			child, exists := properties[name]
			if !exists {
				continue
			}
			transformed, err := addPointerNullability(child, field.Type)
			if err != nil {
				return nil, fmt.Errorf("nullable Takes schema field %s: %w", name, err)
			}
			properties[name] = transformed
		}
	case reflect.Slice, reflect.Array:
		object, ok := schema.(map[string]any)
		if !ok {
			return schema, nil
		}
		items, exists := object["items"]
		if !exists {
			return schema, nil
		}
		transformed, err := addPointerNullability(items, typ.Elem())
		if err != nil {
			return nil, err
		}
		object["items"] = transformed
	}
	return schema, nil
}

func nullableSchema(schema any) any {
	object, ok := schema.(map[string]any)
	if !ok {
		return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
	}
	if object["type"] == "null" || hasNullBranch(object["anyOf"]) {
		return object
	}
	if branches, exists := object["oneOf"]; exists {
		if hasNullBranch(branches) {
			delete(object, "oneOf")
			object["anyOf"] = branches
			return object
		}
	}
	return map[string]any{"anyOf": []any{object, map[string]any{"type": "null"}}}
}

func hasNullBranch(value any) bool {
	branches, ok := value.([]any)
	if !ok {
		return false
	}
	for _, branch := range branches {
		if object, ok := branch.(map[string]any); ok && object["type"] == "null" {
			return true
		}
	}
	return false
}
