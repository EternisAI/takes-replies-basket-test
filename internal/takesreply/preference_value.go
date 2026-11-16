package takesreply

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/invopop/jsonschema"
)

const (
	maxPreferenceValueStringRunes = 500
	maxPreferenceValueArrayItems  = 20
	maxPreferenceValueItemRunes   = 200
)

// PreferenceValueOutput is intentionally limited to JSON primitives and
// arrays of strings. These are descriptive, non-enforced conversation notes;
// arbitrary model-produced objects must not become an open strict-schema hole.
type PreferenceValueOutput json.RawMessage

func (PreferenceValueOutput) JSONSchema() *jsonschema.Schema {
	maxString := uint64(maxPreferenceValueStringRunes)
	maxItem := uint64(maxPreferenceValueItemRunes)
	maxItems := uint64(maxPreferenceValueArrayItems)
	return &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
		{Type: "string", MaxLength: &maxString},
		{Type: "number"},
		{Type: "boolean"},
		{Type: "null"},
		{Type: "array", Items: &jsonschema.Schema{Type: "string", MaxLength: &maxItem}, MaxItems: &maxItems},
	}}
}

func (value *PreferenceValueOutput) UnmarshalJSON(data []byte) error {
	if err := validatePreferenceValue(data); err != nil {
		return err
	}
	*value = append((*value)[:0], data...)
	return nil
}

func (value PreferenceValueOutput) MarshalJSON() ([]byte, error) {
	if len(value) == 0 {
		return []byte("null"), nil
	}
	if err := validatePreferenceValue(value); err != nil {
		return nil, err
	}
	return append([]byte(nil), value...), nil
}

func validatePreferenceValue(data []byte) error {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return errors.New("preference value must be a JSON primitive or string array")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("preference value must contain one JSON value")
	}
	switch typed := decoded.(type) {
	case nil, bool, json.Number:
		return nil
	case string:
		if utf8.RuneCountInString(typed) <= maxPreferenceValueStringRunes {
			return nil
		}
	case []any:
		if len(typed) > maxPreferenceValueArrayItems {
			break
		}
		for _, item := range typed {
			text, ok := item.(string)
			if !ok || utf8.RuneCountInString(text) > maxPreferenceValueItemRunes {
				return errors.New("preference arrays may contain only bounded strings")
			}
		}
		return nil
	}
	return errors.New("preference value must be a bounded primitive or string array")
}
