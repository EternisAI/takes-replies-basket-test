package takesreply

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMessageMentionsHorizonRequiresAdjacentTimeUnit(t *testing.T) {
	tests := []struct {
		name    string
		message string
		days    int
		want    bool
	}{
		{name: "bare holdings count is not a horizon", message: "I want 20 holdings", days: 20},
		{name: "number with day unit", message: "I want 20 days", days: 20, want: true},
		{name: "one day does not round to six days", message: "Use one day", days: 6},
		{name: "one week does not round to twelve days", message: "Use one week", days: 12},
		{name: "a year", message: "I want to invest for a year", days: 365, want: true},
		{name: "one year", message: "I want one year", days: 365, want: true},
		{name: "next year", message: "I want to invest for the next year", days: 365, want: true},
		{name: "one year does not mean two years", message: "I want one year", days: 730},
		{name: "calendar rounding", message: "Plan over 12 months", days: 365, want: true},
		{name: "one week does not mean two weeks", message: "Use one week", days: 14},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := messageMentionsHorizon(test.message, test.days); got != test.want {
				t.Fatalf("messageMentionsHorizon(%q, %d) = %t, want %t", test.message, test.days, got, test.want)
			}
		})
	}
}

func TestApplyTurnOutputRecordsExplicitDefaultHorizonAndPreservesUserHorizon(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for _, message := range []string{"I want one year", "I want to invest for the next year"} {
		t.Run(message, func(t *testing.T) {
			old := newDraft(uuid.NewString(), now)
			days := old.Horizon.Days
			source := uuid.New()
			updated := applyTurnOutput(old, TurnModelOutput{
				AssistantMessage: "I have noted the horizon.", Decision: "REPLY", Stage: "DEVELOPING",
				Horizon: HorizonOutput{Days: &days, UserSpecified: true},
			}, source, nil, message, now.Add(time.Minute))
			if updated.Horizon.Origin != "USER" || updated.Horizon.SourceMessageID == nil || *updated.Horizon.SourceMessageID != source {
				t.Fatalf("explicit default horizon provenance = %+v, want USER sourced to %s", updated.Horizon, source)
			}
		})
	}

	old := newDraft(uuid.NewString(), now)
	previousSource := uuid.New()
	old.Horizon.Origin = "USER"
	old.Horizon.SourceMessageID = &previousSource
	modelDays := 730
	newSource := uuid.New()
	updated := applyTurnOutput(old, TurnModelOutput{
		AssistantMessage: "I will keep your horizon.", Decision: "REPLY", Stage: "DEVELOPING",
		Horizon: HorizonOutput{Days: &modelDays, UserSpecified: true},
	}, newSource, nil, "I want 20 holdings", now.Add(time.Minute))
	if updated.Horizon.Days != old.Horizon.Days || updated.Horizon.Origin != "USER" || updated.Horizon.SourceMessageID == nil || *updated.Horizon.SourceMessageID != previousSource {
		t.Fatalf("unrelated turn changed the saved user horizon: got %+v, want %+v", updated.Horizon, old.Horizon)
	}
}
