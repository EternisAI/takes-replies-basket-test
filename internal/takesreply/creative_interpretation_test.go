package takesreply

import (
	"encoding/json"
	"strings"
	"testing"
)

// These assertions protect the instructions, not the creative quality of a
// future model response. Scope propagation is exercised separately below.
func TestCreativeInterpretationContractDistinguishesExamplesFromRestrictions(t *testing.T) {
	for _, phrase := range []string{
		"explicitly restricted company-only route",
		"even when its question, label or description includes illustrative company names",
		"An observation about a company is a starting point",
		"distinct conditional economic lenses",
		"Creative extrapolation is welcome",
		"does not need fresh sourced evidence for each conditional bridge",
		"not an instruction to hold it or make it an anchor",
		"Do not infer anchors from mentions or illustrative examples",
	} {
		if !strings.Contains(replyContractCompatibility, phrase) {
			t.Fatalf("missing isolated routing instruction %q", phrase)
		}
	}
	schema, err := decisionPointTurnModelOutputSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(schema)
	for _, phrase := range []string{"explicitly restricted company-only route", "example companies in the wording do not close", "examples are not automatic holdings or anchors", "person explicitly requests retention"} {
		if !strings.Contains(string(raw), phrase) {
			t.Fatalf("schema lacks scope semantics %q", phrase)
		}
	}
}
