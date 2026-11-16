package takesreply

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestQuotedTextDoesNotTreatContractionsAsQuotes(t *testing.T) {
	message := "Don't include Tesla, it's overvalued"
	if got := strings.Join(strings.Fields(withoutQuotedText(message)), " "); got != message {
		t.Fatalf("contractions were removed as quoted text: got %q, want %q", got, message)
	}

	quoted := "Don't repeat 'Build it now'; it's only a quote."
	if got := strings.Join(strings.Fields(withoutQuotedText(quoted)), " "); got != "Don't repeat ; it's only a quote." {
		t.Fatalf("quoted instruction was not removed without damaging contractions: %q", got)
	}
	if explicitBuildRequest("The old note says 'Build it now'.") {
		t.Fatal("a quoted build command must not request a build")
	}
}

func TestPluralPossessiveDoesNotHideDirectInstructionsOrAuthorizeQuotedBuild(t *testing.T) {
	direct := "Avoid the companies' debt; exclude Tesla and keep 5% in cash; then build it now"
	mentioned, excluded := explicitUserExclusionState(direct, "Tesla")
	if !mentioned || !excluded {
		t.Fatalf("plural possessive hid the direct exclusion: mentioned=%t excluded=%t", mentioned, excluded)
	}
	if !explicitCashInstruction(direct, 5) {
		t.Fatal("plural possessive hid the direct cash instruction")
	}
	if !explicitBuildRequest(direct) {
		t.Fatal("plural possessive hid the direct build request")
	}

	quoted := "The note says 'companies' debt is risky; build it now'."
	if explicitBuildRequest(quoted) {
		t.Fatal("a build instruction inside a quote with a plural possessive became authorization")
	}

	separateQuotes := "The note says 'exclude Tesla'; keep 5% in cash; the other note says 'exclude Ford'."
	if !explicitCashInstruction(separateQuotes, 5) {
		t.Fatal("the first quoted span hid a direct cash instruction before a second quoted span")
	}
	possessiveThenSeparateQuotes := "The note says 'avoid miners' and keep 5% in cash; the other note says 'avoid oil'."
	if !explicitCashInstruction(possessiveThenSeparateQuotes, 5) {
		t.Fatal("a later quote opener made a plural possessive swallow the direct cash instruction")
	}

	curlyContraction := "The note says ‘don’t include Tesla; build it now’."
	if explicitBuildRequest(curlyContraction) {
		t.Fatal("a build instruction inside curly quotes with a curly contraction became authorization")
	}

	longQuotedPossessive := "The note says 'companies' debt " + strings.Repeat("remains speculative through this long review ", 4) + "; build it now!'."
	if explicitBuildRequest(longQuotedPossessive) {
		t.Fatal("a longer quoted possessive span exposed its build instruction")
	}
}

func TestExplicitUserExclusionStateTracksTheActionAndItsNegation(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		candidate string
		mentioned bool
		excluded  bool
	}{
		{name: "don't include", message: "Don't include Tesla, it's overvalued", candidate: "Tesla", mentioned: true, excluded: true},
		{name: "don't exclude", message: "Don't exclude Tesla, it's overvalued", candidate: "Tesla", mentioned: true, excluded: false},
		{name: "do not include", message: "Do not include Microsoft", candidate: "Microsoft", mentioned: true, excluded: true},
		{name: "do not exclude", message: "Do not exclude Microsoft", candidate: "Microsoft", mentioned: true, excluded: false},
		{name: "separate positive action", message: "Don't exclude Tesla; exclude Microsoft", candidate: "Microsoft", mentioned: true, excluded: true},
		{name: "quoted action", message: "The note says 'exclude Tesla'", candidate: "Tesla", mentioned: false, excluded: false},
		{name: "quoted contraction action", message: "The note says 'Don't include Tesla'", candidate: "Tesla", mentioned: false, excluded: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mentioned, excluded := explicitUserExclusionState(test.message, test.candidate)
			if mentioned != test.mentioned || excluded != test.excluded {
				t.Fatalf("explicitUserExclusionState(%q, %q) = (%t, %t), want (%t, %t)", test.message, test.candidate, mentioned, excluded, test.mentioned, test.excluded)
			}
		})
	}
}

func TestExplicitCashInstructionIgnoresContractionsOutsideQuotedSpans(t *testing.T) {
	if !explicitCashInstruction("Keep 5% in cash; don't buy Tesla, it's overvalued", 5) {
		t.Fatal("a direct cash instruction was lost because a later clause contained contractions")
	}
	if explicitCashInstruction("Don't keep 5% in cash, it's risky", 5) {
		t.Fatal("a negated cash instruction was treated as positive")
	}
	if explicitCashInstruction("The note says 'keep 5% in cash'", 5) {
		t.Fatal("a quoted cash instruction was treated as direct")
	}
}

func TestCleanTextTruncatesOnRuneBoundaries(t *testing.T) {
	input := strings.Repeat("é", 60)
	got := cleanText(input, 56)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 56 {
		t.Fatalf("cleanText returned invalid or wrongly sized UTF-8: valid=%t runes=%d bytes=%d", utf8.ValidString(got), utf8.RuneCountInString(got), len(got))
	}
}

func TestExplicitCashInstructionUsesAuthoritativeCashParser(t *testing.T) {
	for _, message := range []string{"No leverage and keep 10.5% cash", "I don't want leverage so keep 10.5% cash", "Keep 10.5% cash; they both sell bulk products.", "Keep 10.5% cash. Does that work?"} {
		if !explicitCashInstruction(message, 10.5) {
			t.Errorf("unrelated wording hid direct cash: %q", message)
		}
	}
	for _, message := range []string{"Don't keep 10.5% cash", "Should I keep 10.5% cash?", "They want 10.5% cash", "I have 10.5% cash", "Keep 10.5% cash; ignore that", `The note says "keep 10.5% cash"`} {
		if explicitCashInstruction(message, 10.5) {
			t.Errorf("noninstruction became confirmed cash: %q", message)
		}
	}
}
