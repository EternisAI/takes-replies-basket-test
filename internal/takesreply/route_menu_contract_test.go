package takesreply

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestOriginalAcceptedReplyIsExactWithIsolatedContractCompatibility(t *testing.T) {
	sum := sha256.Sum256([]byte(originalAcceptedReplyPrompt))
	if got := hex.EncodeToString(sum[:]); got != "909634799a31551e6bf3912898ec5289278b97ad40fc1616f500b4c0cb0bf6d4" {
		t.Fatalf("original accepted reply bytes changed: %s", got)
	}
	if conversationSystem != originalAcceptedReplyPrompt+"\n\n[Current reply contract compatibility]\n"+replyContractCompatibility {
		t.Fatal("effective reply contains changes outside the isolated compatibility appendix")
	}
	for _, instruction := range []string{
		"Start with the idea in their terms",
		"Add one useful insight if it changes the choice",
		"do not appraise the user's reason for asking",
		"or explain to the user why their preference is insufficient evidence",
		"Offer one to three routes that fit the idea",
	} {
		if !strings.Contains(originalAcceptedReplyPrompt, instruction) {
			t.Fatalf("original reply guidance missing %q", instruction)
		}
	}
	for _, removed := range []string{"Then make one useful investment judgment", "at least one concrete typed non-build route", "first response to an ordinary investment belief"} {
		if strings.Contains(conversationSystem, removed) {
			t.Fatalf("newer reply rule retained: %q", removed)
		}
	}
	for _, required := range []string{"return READY_TO_BUILD instead", "server adds its saved canonical Build it now action", "companyScope", "NAMED_COMPANIES", "OPEN_THEME uses an empty array", "inferred-unconfirmed thesis"} {
		if !strings.Contains(replyContractCompatibility, required) {
			t.Fatalf("current contract compatibility missing %q", required)
		}
	}
	// The original base contains legacy model-owned BUILD_QUEUED wording; only
	// the explicit compatibility appendix may adapt it to the current service.
	if !strings.Contains(originalAcceptedReplyPrompt, "decision BUILD_QUEUED") {
		t.Fatal("original lifecycle wording was silently edited")
	}
}

func TestReplyAnswerOnlyNormalizationNeverInventsRouteOrBuildAction(t *testing.T) {
	// Historical/method answers can retain a saved belief. They must never gain
	// invented route IDs or a Build action merely because that belief exists.
	statement := "An existing conditional investment idea."
	answer := TurnModelOutput{Decision: "REPLY", Stage: "DEVELOPING"}
	got := normalizeConversationChoices(answer, Belief{UserStatement: &statement})
	if len(got.Choices) != 0 || got.Stage != "DEVELOPING" {
		t.Fatalf("answer-only output gained a route or Build action: %+v", got)
	}
}
