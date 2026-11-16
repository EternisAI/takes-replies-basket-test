package takesreply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
)

const decisionPointTurnSchemaName = "takes_conversation_decision_point_v1"

// DecisionPointOutput is a compact product conclusion selected from the final
// enriched evidence. It is not a reasoning trace and does not replace Sources.
type DecisionPointOutput struct {
	Kind        string `json:"kind"`
	Conclusion  string `json:"conclusion"`
	FactIndices []int  `json:"factIndices"`
}

type decisionPointTurnOutput struct {
	DecisionPoint *DecisionPointOutput `json:"decisionPoint"`
	Turn          *TurnModelOutput     `json:"turn"`
}

func decisionPointTurnModelOutputSchema() (any, error) {
	turnSchema, err := turnModelOutputSchema()
	if err != nil {
		return nil, err
	}
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://github.com/axionhypothesis/backend-go/internal/takesconversation/decision-point-turn-output",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"decisionPoint": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"kind": map[string]any{
						"type": "string",
						"enum": []any{"none", "expectations", "business_condition", "alternative"},
					},
					"conclusion": map[string]any{
						"type":        "string",
						"description": "At most 25 words. Empty when kind is none. The concise decision conclusion, not a reasoning trace.",
					},
					"factIndices": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "integer"},
						"description": "At most 4 unique zero-based indices into input evidenceCheck.facts. Must be empty for none. Expectations requires at least one index. Validate bounds and source linkage in service code.",
					},
				},
				"required": []any{"kind", "conclusion", "factIndices"},
			},
			"turn": turnSchema,
		},
		"required": []any{"decisionPoint", "turn"},
	}
	return normalizeStrictTurnSchema(schema), nil
}

func (s *Service) conversationCompletionModel() string {
	if s != nil && strings.TrimSpace(s.conversationModel) != "" {
		return strings.TrimSpace(s.conversationModel)
	}
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.model)
}

func (s *Service) conversationCompletionMaxTokens() int {
	if s != nil && s.conversationMaxTokens > 0 {
		return s.conversationMaxTokens
	}
	return defaultReplyTokens
}

func (s *Service) conversationCompletionEffort() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.conversationReasoningEffort)
}

func conversationCompletionRequest(model, effort string, maxTokens int, evidence EvidenceCheckOutput, initialBrandPreference bool, userPrompt string) (llm.CompletionRequest, error) {
	schema, err := decisionPointTurnModelOutputSchema()
	if err != nil {
		return llm.CompletionRequest{}, err
	}
	if maxTokens <= 0 {
		maxTokens = defaultReplyTokens
	}
	return llm.CompletionRequest{
		Model: model, ReasoningEffort: effort,
		SystemPrompt: conversationSystemForEvidence(evidence, initialBrandPreference),
		UserPrompt:   userPrompt,
		SchemaName:   decisionPointTurnSchemaName, Schema: schema,
		MaxTokens: maxTokens, Strict: true, NoRetry: true,
	}, nil
}

func acceptedReplyCompletionRequest(model, effort string, maxTokens int, evidence EvidenceCheckOutput, initialBrandPreference bool, userPrompt string) (llm.CompletionRequest, error) {
	request, err := conversationCompletionRequest(model, effort, maxTokens, evidence, initialBrandPreference, userPrompt)
	if err != nil {
		return llm.CompletionRequest{}, err
	}
	request.Provider = requiredProvider("anthropic")
	request.CanonicalOpenRouterReasoning = true
	request.ExcludeReasoning = true
	request.ServiceTier = "default"
	request.NoRetry = true
	return request, nil
}

func acceptedReplyCompletionRequestForTurnContext(model, effort string, maxTokens int, initialBrandPreference bool, turn turnContext) (llm.CompletionRequest, error) {
	if turn.AsOf.IsZero() || turn.EvidenceCheck.AsOfUTC.IsZero() || !turn.AsOf.Equal(turn.EvidenceCheck.AsOfUTC) {
		return llm.CompletionRequest{}, errors.New("reply context and evidence check must share one as-of cutoff")
	}
	input, err := json.Marshal(turn)
	if err != nil {
		return llm.CompletionRequest{}, fmt.Errorf("encode Takes reply context: %w", err)
	}
	userPrompt := "Use the saved draft, recent message history, selected choice, and current user turn below. Return a complete updated working interpretation in the required JSON schema. Do not invent evidence.\n\n" + string(input)
	return acceptedReplyCompletionRequest(model, effort, maxTokens, turn.EvidenceCheck, initialBrandPreference, userPrompt)
}

func decodeDecisionPointTurnOutput(content string, evidence EvidenceCheckOutput) (TurnModelOutput, DecisionPointOutput, error) {
	if err := validateOneJSONObjectNoDuplicateKeys([]byte(content)); err != nil {
		return TurnModelOutput{}, DecisionPointOutput{}, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil || root == nil {
		if err == nil {
			err = errors.New("response must be one JSON object")
		}
		return TurnModelOutput{}, DecisionPointOutput{}, err
	}
	if len(root) != 2 || root["decisionPoint"] == nil || root["turn"] == nil {
		return TurnModelOutput{}, DecisionPointOutput{}, errors.New("response must contain only decisionPoint and turn")
	}
	if err := requireJSONFields(root["decisionPoint"], "kind", "conclusion", "factIndices"); err != nil {
		return TurnModelOutput{}, DecisionPointOutput{}, fmt.Errorf("decisionPoint: %w", err)
	}
	if err := rejectJSONNullFields(root["decisionPoint"], "kind", "conclusion", "factIndices"); err != nil {
		return TurnModelOutput{}, DecisionPointOutput{}, fmt.Errorf("decisionPoint: %w", err)
	}
	if err := requireJSONFields(root["turn"],
		"assistantMessage", "decision", "buildRequested", "stage", "belief", "horizon", "choices",
		"selectedChoiceIds", "criteria", "exclusions", "requestedHoldings", "cashConstraint", "preferences"); err != nil {
		return TurnModelOutput{}, DecisionPointOutput{}, fmt.Errorf("turn: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(content)))
	decoder.DisallowUnknownFields()
	var output decisionPointTurnOutput
	if err := decoder.Decode(&output); err != nil {
		return TurnModelOutput{}, DecisionPointOutput{}, err
	}
	if output.DecisionPoint == nil || output.Turn == nil {
		return TurnModelOutput{}, DecisionPointOutput{}, errors.New("response must contain non-null decisionPoint and turn objects")
	}
	if err := validateDecisionPoint(*output.DecisionPoint, evidence); err != nil {
		return TurnModelOutput{}, DecisionPointOutput{}, err
	}
	return *output.Turn, *output.DecisionPoint, nil
}

// decodeDecisionPointTurnOutputForContext keeps index validation bound to the
// exact final evidence object serialized into the reply input and the same
// as-of instant used to build and filter it.
func decodeDecisionPointTurnOutputForContext(content string, turn turnContext) (TurnModelOutput, DecisionPointOutput, error) {
	if turn.AsOf.IsZero() || turn.EvidenceCheck.AsOfUTC.IsZero() || !turn.AsOf.Equal(turn.EvidenceCheck.AsOfUTC) {
		return TurnModelOutput{}, DecisionPointOutput{}, errors.New("reply context and evidence check must share one as-of cutoff")
	}
	return decodeDecisionPointTurnOutput(content, turn.EvidenceCheck)
}

func validateDecisionPoint(point DecisionPointOutput, evidence EvidenceCheckOutput) error {
	if point.FactIndices == nil {
		return errors.New("decisionPoint.factIndices must be an array")
	}
	if len(point.FactIndices) > 4 {
		return errors.New("decisionPoint references more than four facts")
	}
	if len(strings.Fields(point.Conclusion)) > 25 {
		return errors.New("decisionPoint conclusion exceeds 25 words")
	}
	switch point.Kind {
	case "none":
		if strings.TrimSpace(point.Conclusion) != "" || len(point.FactIndices) != 0 {
			return errors.New("none decisionPoint must have an empty conclusion and no fact references")
		}
	case "expectations", "business_condition", "alternative":
		if strings.TrimSpace(point.Conclusion) == "" {
			return errors.New("non-none decisionPoint requires a conclusion")
		}
	default:
		return fmt.Errorf("invalid decisionPoint kind %q", point.Kind)
	}
	if point.Kind == "expectations" && len(point.FactIndices) == 0 {
		return errors.New("expectations decisionPoint requires source-linked facts")
	}

	sourceRefs := make(map[string]struct{}, len(evidence.Sources))
	for _, source := range evidence.Sources {
		if ref := strings.TrimSpace(source.SourceRef); ref != "" {
			sourceRefs[ref] = struct{}{}
		}
	}
	seen := make(map[int]struct{}, len(point.FactIndices))
	for _, index := range point.FactIndices {
		if index < 0 || index >= len(evidence.Facts) {
			return fmt.Errorf("decisionPoint fact index %d is out of range", index)
		}
		if _, duplicate := seen[index]; duplicate {
			return fmt.Errorf("decisionPoint fact index %d is duplicated", index)
		}
		seen[index] = struct{}{}
		ref := strings.TrimSpace(evidence.Facts[index].SourceRef)
		if ref == "" {
			return fmt.Errorf("decisionPoint fact index %d has no source reference", index)
		}
		if _, exists := sourceRefs[ref]; !exists {
			return fmt.Errorf("decisionPoint fact index %d has no matching source", index)
		}
	}
	return nil
}

func requireJSONFields(raw json.RawMessage, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("must be a JSON object")
		}
		return err
	}
	for _, field := range fields {
		if object[field] == nil {
			return fmt.Errorf("missing required field %q", field)
		}
	}
	return nil
}

func rejectJSONNullFields(raw json.RawMessage, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("must be a JSON object")
		}
		return err
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists || strings.TrimSpace(string(value)) == "null" {
			return fmt.Errorf("%q must not be null", field)
		}
	}
	return nil
}

func validateOneJSONObjectNoDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var scan func() error
	scan = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate JSON object key %q", key)
				}
				seen[key] = struct{}{}
				if err := scan(); err != nil {
					return err
				}
			}
			closeToken, err := decoder.Token()
			if err != nil || closeToken != json.Delim('}') {
				return errors.New("invalid JSON object")
			}
		case '[':
			for decoder.More() {
				if err := scan(); err != nil {
					return err
				}
			}
			closeToken, err := decoder.Token()
			if err != nil || closeToken != json.Delim(']') {
				return errors.New("invalid JSON array")
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		return nil
	}
	if err := scan(); err != nil {
		return fmt.Errorf("invalid decisionPoint response JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("decisionPoint response must contain one JSON value")
		}
		return fmt.Errorf("invalid trailing decisionPoint response data: %w", err)
	}
	return nil
}
