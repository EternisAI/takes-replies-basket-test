package takesresearch

import (
	"strings"
	"testing"
)

func TestExpressionPlanTextBoundsCountUnicodeRunesLikeJSONSchema(t *testing.T) {
	for _, field := range []struct {
		name string
		max  int
		set  func(*ExpressionPlan, string)
	}{
		{"story title", 80, func(p *ExpressionPlan, s string) { p.Story.Title = s }},
		{"conditional thesis", 320, func(p *ExpressionPlan, s string) { p.Story.ConditionalThesis = s }},
		{"story mix reason", 320, func(p *ExpressionPlan, s string) { p.Story.WhyThisMix = s }},
		{"alternative description", 160, func(p *ExpressionPlan, s string) { p.Story.AlternativeOrGap.Description = s }},
		{"alternative tradeoff", 160, func(p *ExpressionPlan, s string) { p.Story.AlternativeOrGap.Tradeoff = s }},
		{"role label", 60, func(p *ExpressionPlan, s string) { p.Roles[0].Label = s }},
		{"role mechanism", 160, func(p *ExpressionPlan, s string) { p.Roles[0].ProposedMechanism = s }},
		{"role reason", 120, func(p *ExpressionPlan, s string) { p.Roles[0].WhyThisRole = s }},
		{"issuer name", 120, func(p *ExpressionPlan, s string) { p.Companies[0].IssuerName = s }},
		{"company connection", 160, func(p *ExpressionPlan, s string) { p.Companies[0].ProposedConnection = s }},
		{"share reason", 140, func(p *ExpressionPlan, s string) { p.Companies[0].WhyThisShare = s }},
		{"distinctive contribution", 140, func(p *ExpressionPlan, s string) { p.Companies[0].DistinctiveContribution = s }},
		{"extra assumption", 140, func(p *ExpressionPlan, s string) { p.Companies[0].ExtraAssumption = s }},
		{"overlap", 320, func(p *ExpressionPlan, s string) { p.OverlapRisks[0].ProposedDependency = s }},
	} {
		t.Run(field.name, func(t *testing.T) {
			for _, count := range []int{field.max, field.max + 1} {
				plan := expressionPlanFixture()
				field.set(&plan, strings.Repeat("界", count))
				err := validateExpressionPlan(plan)
				if (err == nil) != (count == field.max) {
					t.Fatalf("%d Unicode characters encoded in %d bytes: err=%v", count, len(strings.Repeat("界", count)), err)
				}
			}
		})
	}
}

func expressionPlanFixture() ExpressionPlan {
	return ExpressionPlan{
		Story:        ExpressionStoryPlan{Title: "A proposed mix", ConditionalThesis: "If the stated mechanism matters, these companies may express it.", WhyThisMix: "The companies cover related roles in the proposed mechanism.", AlternativeOrGap: &ExpressionConsideration{Kind: "ALTERNATIVE", Description: "A comparison path.", Tradeoff: "It would express a different route."}},
		Roles:        []ExpressionRolePlan{{ID: "role_core", Label: "Core role", ProposedMechanism: "The role carries the central proposed connection.", ExposureKind: "DIRECT", WhyThisRole: "It represents the central mechanism.", PlannedMixPercent: 100}},
		Companies:    []ExpressionCompanyPlan{{ID: "company_core", IssuerName: "Example Systems", RoleID: "role_core", ProposedConnection: "The company may participate through its products.", PlannedMixPercent: 100, WhyThisShare: "It is the only company in this small fixture.", PortfolioRole: "CORE", DistinctiveContribution: "It represents the central role."}},
		OverlapRisks: []ExpressionOverlap{{RoleIDs: []string{"role_core"}, ProposedDependency: "Both roles may depend on the same adoption path."}},
	}
}
