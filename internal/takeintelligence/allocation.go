package takeintelligence

import (
	"math"
	"slices"
	"strings"
	"time"
)

const (
	InverseVolWindow      = 365 * 24 * time.Hour
	InverseVolMaxCloseAge = 5 * 24 * time.Hour
	InverseVolMinReturns  = 60
	InverseVolHalfLife    = 60.0
	InverseVolMaxFloor    = 0.10
	allocationEpsilon     = 1e-8
)

type AllocationMethod string

const (
	MethodUserWeights     AllocationMethod = "user_weights"
	MethodEqualRole       AllocationMethod = "equal_role"
	MethodInverseVol      AllocationMethod = "inverse_volatility"
	MethodResearchWeights AllocationMethod = "research_weights"
)

type AllocationDenominator string

// RiskStyle is a user-stated preference that may shape a research proposal.
// It is not a measured risk score and does not imply leverage or a return target.
type RiskStyle string

const (
	RiskStyleConservative RiskStyle = "CONSERVATIVE"
	RiskStyleBalanced     RiskStyle = "BALANCED"
	RiskStyleAggressive   RiskStyle = "AGGRESSIVE"
	RiskStyleCustom       RiskStyle = "CUSTOM"
	RiskStyleUnclear      RiskStyle = "UNCLEAR"
)

func (style RiskStyle) Resolved() bool {
	switch style {
	case RiskStyleConservative, RiskStyleBalanced, RiskStyleAggressive:
		return true
	default:
		return false
	}
}

const (
	DenominatorTotalBudget    AllocationDenominator = "total_budget_including_cash"
	DenominatorInvestedSleeve AllocationDenominator = "invested_sleeve_excluding_cash"
)

type PositionDirection string

const (
	DirectionLong  PositionDirection = "long"
	DirectionShort PositionDirection = "short"
)

type AllocationLeg struct {
	Key         ProviderInstrumentKey
	ExposureKey string
	// CompanyKey identifies the economic name shared by listings or contracts.
	CompanyKey            string
	Role                  string
	Direction             PositionDirection
	UserWeight            *float64
	ResearchWeightPercent *int
}

type RoleBudget struct {
	Role   string
	Weight float64
}

type DailyClose struct {
	At    time.Time
	Price float64
}

// DailyCloseSeries is keyed to the exact instrument whose returns it contains.
// An equity or index series must not be passed as a substitute for a perp.
type DailyCloseSeries struct {
	Key    ProviderInstrumentKey
	Closes []DailyClose
}

type AllocationInput struct {
	Method      AllocationMethod
	Denominator AllocationDenominator
	CashReserve float64 // share of total portfolio budget, even when weights exclude cash
	// UnallocatedWeight is a share of total budget reserved for an explicit
	// coverage or constraint gap. It is only supported with the total-budget
	// denominator; it is never normalized into selected legs.
	UnallocatedWeight float64
	AsOf              time.Time
	Legs              []AllocationLeg
	RoleBudgets       []RoleBudget
	CloseSeries       []DailyCloseSeries
}

type LegAllocation struct {
	Key               ProviderInstrumentKey
	ExposureKey       string
	Role              string
	Direction         PositionDirection
	Weight            float64 // share of the declared denominator
	TotalBudgetWeight float64 // share of invested portfolio budget
	Volatility        float64 // populated only for inverse-volatility allocations
}

type AllocationResult struct {
	Complete          bool
	Method            AllocationMethod
	Denominator       AllocationDenominator
	CashWeight        float64
	UnallocatedWeight float64
	InvestedWeight    float64
	FloorPerLeg       float64
	LookbackStart     time.Time
	LookbackEnd       time.Time
	LatestSharedClose time.Time
	SharedReturns     int
	Legs              []LegAllocation
	Findings          []Finding
}

// Allocate applies one explicitly named policy. User weights are validated as
// supplied and never normalized. Equal-role allocation divides declared role
// budgets transparently. Inverse-volatility requires matching, recent closes
// for every exact provider instrument key and never falls back to another series.
func Allocate(input AllocationInput) AllocationResult {
	result := AllocationResult{Method: input.Method, Denominator: input.Denominator}
	var findings []Finding
	if input.AsOf.IsZero() {
		findings = append(findings, allocationError("allocation_as_of_missing", "", "allocation needs an as-of time"))
	}
	if len(input.Legs) == 0 {
		findings = append(findings, allocationError("allocation_legs_missing", "", "allocation needs at least one selected instrument"))
	}
	cashValid := finiteBetween(input.CashReserve, 0, 1) && input.CashReserve < 1
	if !cashValid {
		findings = append(findings, allocationError("cash_reserve_invalid", "", "cash reserve must be finite, at least zero, and less than the total budget"))
	}
	if !finiteBetween(input.UnallocatedWeight, 0, 1) || input.CashReserve+input.UnallocatedWeight >= 1 {
		findings = append(findings, allocationError("unallocated_weight_invalid", "", "unallocated share must be finite, nonnegative, and leave room for cash and selected holdings"))
	}
	if input.Denominator != DenominatorTotalBudget && input.Denominator != DenominatorInvestedSleeve {
		findings = append(findings, allocationError("allocation_denominator_invalid", "", "allocation denominator must state whether weights include cash"))
	}
	if input.UnallocatedWeight > 0 && input.Denominator != DenominatorTotalBudget {
		findings = append(findings, allocationError("unallocated_denominator_invalid", "", "unallocated shares require the total-budget denominator"))
	}

	keys := make(map[string]bool, len(input.Legs))
	exposures := make(map[string]AllocationLeg, len(input.Legs))
	roles := make(map[string]int)
	for _, leg := range input.Legs {
		subject := leg.Key.String()
		if !leg.Key.valid() {
			findings = append(findings, allocationError("allocation_instrument_key_invalid", subject, "every allocation leg needs a full provider instrument key"))
		}
		if keys[subject] {
			findings = append(findings, allocationError("allocation_duplicate_instrument", subject, "the same provider instrument cannot appear twice"))
		}
		keys[subject] = true
		if strings.TrimSpace(leg.ExposureKey) == "" {
			findings = append(findings, allocationError("allocation_exposure_key_missing", subject, "every leg needs a reviewed economic exposure key"))
		} else if previous, exists := exposures[leg.ExposureKey]; exists {
			code, message := "allocation_duplicate_exposure", "two legs map to the same economic exposure and cannot be presented as diversified holdings"
			if previous.Direction != leg.Direction {
				code, message = "allocation_conflicting_exposure", "opposite-side legs map to the same economic exposure and must be resolved before allocation"
			}
			findings = append(findings, allocationError(code, leg.ExposureKey, message))
		} else {
			exposures[leg.ExposureKey] = leg
		}
		if strings.TrimSpace(leg.Role) == "" {
			findings = append(findings, allocationError("allocation_role_missing", subject, "each selected instrument needs its stated role in the belief"))
		} else {
			roles[leg.Role]++
		}
		if leg.Direction != DirectionLong && leg.Direction != DirectionShort {
			findings = append(findings, allocationError("allocation_direction_invalid", subject, "each instrument direction must be explicit"))
		}
		if input.Method != MethodResearchWeights && leg.ResearchWeightPercent != nil {
			findings = append(findings, allocationError("allocation_inputs_ambiguous", subject, "research proportions cannot be combined with another allocation method"))
		}
	}
	if !cashValid || hasErrors(findings) {
		result.Findings = findingsSorted(findings)
		return result
	}

	target := 1.0
	switch input.Denominator {
	case DenominatorTotalBudget:
		target = 1 - input.CashReserve - input.UnallocatedWeight
		result.CashWeight = input.CashReserve
		result.UnallocatedWeight = input.UnallocatedWeight
	case DenominatorInvestedSleeve:
		result.CashWeight = input.CashReserve
	}
	result.InvestedWeight = target

	weights := make([]float64, len(input.Legs))
	vols := make([]float64, len(input.Legs))
	methodValid := true
	switch input.Method {
	case MethodUserWeights:
		if len(input.RoleBudgets) > 0 || len(input.CloseSeries) > 0 {
			findings = append(findings, allocationError("allocation_inputs_ambiguous", "", "user weights cannot also include generated role budgets or volatility series"))
			methodValid = false
		}
		for i, leg := range input.Legs {
			if leg.UserWeight == nil {
				findings = append(findings, allocationError("user_weight_missing", leg.Key.String(), "user-weight method requires an explicit weight for every leg"))
				methodValid = false
				continue
			}
			if !finiteBetween(*leg.UserWeight, 0, 1) || *leg.UserWeight == 0 {
				findings = append(findings, allocationError("user_weight_invalid", leg.Key.String(), "user weight must be positive, finite, and no greater than one"))
				methodValid = false
				continue
			}
			weights[i] = *leg.UserWeight
		}
	case MethodEqualRole:
		if len(input.CloseSeries) > 0 {
			findings = append(findings, allocationError("allocation_inputs_ambiguous", "", "equal-role method cannot also include volatility series"))
			methodValid = false
		}
		for _, leg := range input.Legs {
			if leg.UserWeight != nil {
				findings = append(findings, allocationError("allocation_inputs_ambiguous", leg.Key.String(), "equal-role method cannot also provide per-leg user weights"))
				methodValid = false
			}
		}
		roleWeights, issues := equalRoleWeights(input.Legs, input.RoleBudgets, roles, target)
		findings = append(findings, issues...)
		if hasErrors(issues) {
			methodValid = false
		} else {
			weights = roleWeights
		}
	case MethodInverseVol:
		if len(input.RoleBudgets) > 0 {
			findings = append(findings, allocationError("allocation_inputs_ambiguous", "", "inverse-volatility method cannot also include role budgets"))
			methodValid = false
		}
		for _, leg := range input.Legs {
			if leg.UserWeight != nil {
				findings = append(findings, allocationError("allocation_inputs_ambiguous", leg.Key.String(), "inverse-volatility method cannot also provide per-leg user weights"))
				methodValid = false
			}
		}
		baseWeights, volatility, audit, issues := inverseVolWeights(input)
		findings = append(findings, issues...)
		if hasErrors(issues) {
			methodValid = false
		} else {
			result.FloorPerLeg = audit.floor
			result.LookbackStart = audit.start
			result.LookbackEnd = input.AsOf
			result.LatestSharedClose = audit.latest
			result.SharedReturns = audit.returns
			for i := range weights {
				weights[i] = baseWeights[i] * target
				vols[i] = volatility[i]
			}
		}
	case MethodResearchWeights:
		if len(input.RoleBudgets) > 0 || len(input.CloseSeries) > 0 || input.Denominator != DenominatorTotalBudget || input.UnallocatedWeight != 0 {
			findings = append(findings, allocationError("allocation_inputs_ambiguous", "", "research proportions require the total-budget denominator and cannot include role budgets, unallocated shares, or volatility series"))
			methodValid = false
		}
		proportionTotal := 0
		for i, leg := range input.Legs {
			if leg.UserWeight != nil {
				findings = append(findings, allocationError("allocation_inputs_ambiguous", leg.Key.String(), "research proportions cannot also provide user weights"))
				methodValid = false
			}
			if leg.ResearchWeightPercent == nil {
				findings = append(findings, allocationError("research_weight_missing", leg.Key.String(), "research-weight method requires a positive whole-percent proportion for every selected holding"))
				methodValid = false
				continue
			}
			if *leg.ResearchWeightPercent < 1 || *leg.ResearchWeightPercent > 100 {
				findings = append(findings, allocationError("research_weight_invalid", leg.Key.String(), "research proportions must be positive whole percentages no greater than one hundred"))
				methodValid = false
				continue
			}
			proportionTotal += *leg.ResearchWeightPercent
			weights[i] = float64(*leg.ResearchWeightPercent) / 100 * target
		}
		if proportionTotal != 100 {
			findings = append(findings, Finding{Code: "research_weight_sum_mismatch", Severity: SeverityError, Message: "research proportions must sum exactly to one hundred percent of the noncash sleeve; invalid totals are not normalized"})
			methodValid = false
		}
	default:
		findings = append(findings, allocationError("allocation_method_invalid", "", "allocation method must be user weights, equal role, inverse volatility, or research proportions"))
		methodValid = false
	}

	if methodValid && len(weights) == len(input.Legs) {
		sum := 0.0
		for _, weight := range weights {
			sum += weight
		}
		if math.Abs(sum-target) > allocationEpsilon {
			findings = append(findings, Finding{Code: "allocation_sum_mismatch", Severity: SeverityError, Message: "weights must sum to the stated invested denominator; invalid totals are not normalized"})
			methodValid = false
		}
		for i, leg := range input.Legs {
			totalWeight := weights[i]
			if input.Denominator == DenominatorInvestedSleeve {
				totalWeight *= 1 - input.CashReserve
			}
			result.Legs = append(result.Legs, LegAllocation{Key: leg.Key, ExposureKey: leg.ExposureKey, Role: leg.Role, Direction: leg.Direction, Weight: weights[i], TotalBudgetWeight: totalWeight, Volatility: vols[i]})
		}
	}
	if !methodValid {
		result.Legs = nil
	}
	result.Findings = findingsSorted(findings)
	result.Complete = !hasErrors(result.Findings) && methodValid && len(result.Legs) == len(input.Legs)
	if result.Complete {
		result.Complete = validateAllocationSums(result, len(input.Legs))
	}
	return result
}

func equalRoleWeights(legs []AllocationLeg, provided []RoleBudget, roles map[string]int, target float64) ([]float64, []Finding) {
	var findings []Finding
	roleBudgets := make(map[string]float64, len(provided))
	if len(provided) == 0 {
		if len(roles) == 0 {
			return nil, []Finding{allocationError("allocation_roles_missing", "", "equal-role method needs at least one named role")}
		}
		for role := range roles {
			roleBudgets[role] = target / float64(len(roles))
		}
	} else {
		for _, budget := range provided {
			if strings.TrimSpace(budget.Role) == "" || !finiteBetween(budget.Weight, 0, 1) || budget.Weight == 0 {
				findings = append(findings, allocationError("role_budget_invalid", budget.Role, "role budgets need a name and positive finite weight"))
				continue
			}
			if _, exists := roleBudgets[budget.Role]; exists {
				findings = append(findings, allocationError("role_budget_duplicate", budget.Role, "a role can have only one budget"))
				continue
			}
			roleBudgets[budget.Role] = budget.Weight
		}
		for role := range roles {
			if _, ok := roleBudgets[role]; !ok {
				findings = append(findings, allocationError("role_budget_missing", role, "every selected role needs an explicit role budget"))
			}
		}
		for role := range roleBudgets {
			if roles[role] == 0 {
				findings = append(findings, allocationError("role_budget_unused", role, "role budget does not match any selected instrument"))
			}
		}
		total := 0.0
		for _, weight := range roleBudgets {
			total += weight
		}
		if math.Abs(total-target) > allocationEpsilon {
			findings = append(findings, Finding{Code: "role_budget_sum_mismatch", Severity: SeverityError, Message: "role budgets must sum to the stated denominator and are never silently normalized"})
		}
	}
	if hasErrors(findings) {
		return nil, findingsSorted(findings)
	}
	weights := make([]float64, len(legs))
	for i, leg := range legs {
		weights[i] = roleBudgets[leg.Role] / float64(roles[leg.Role])
	}
	return weights, findingsSorted(findings)
}

type inverseVolAudit struct {
	start   time.Time
	latest  time.Time
	returns int
	floor   float64
}

func inverseVolWeights(input AllocationInput) ([]float64, []float64, inverseVolAudit, []Finding) {
	audit := inverseVolAudit{start: input.AsOf.Add(-InverseVolWindow)}
	var findings []Finding
	n := len(input.Legs)
	if n == 0 {
		return nil, nil, audit, []Finding{allocationError("allocation_legs_missing", "", "inverse-volatility needs at least one leg")}
	}
	floor := math.Min(InverseVolMaxFloor, 1/float64(n))
	audit.floor = floor
	if n >= 10 {
		return nil, nil, audit, []Finding{allocationError("inverse_vol_floor_exhausts_budget", "", "the documented minimum would force equal weights for ten or more legs; choose a transparent equal-role or user-weight policy instead")}
	}
	if input.AsOf.IsZero() {
		return nil, nil, audit, []Finding{allocationError("inverse_vol_as_of_missing", "", "inverse-volatility needs an as-of time")}
	}
	series := make(map[string]map[string]DailyClose, len(input.CloseSeries))
	for _, item := range input.CloseSeries {
		key := item.Key.String()
		if !item.Key.valid() {
			findings = append(findings, allocationError("close_series_key_invalid", key, "close history must have a full provider instrument key"))
			continue
		}
		if _, exists := series[key]; exists {
			findings = append(findings, allocationError("close_series_duplicate", key, "only one close series is allowed for an exact provider instrument key"))
			continue
		}
		byDay := make(map[string]DailyClose, len(item.Closes))
		for _, close := range item.Closes {
			if close.At.IsZero() || close.At.After(input.AsOf) || !finiteBetween(close.Price, 0, math.MaxFloat64) || close.Price == 0 {
				findings = append(findings, allocationError("close_value_invalid", key, "daily close needs a past timestamp and a positive finite price"))
				continue
			}
			day := utcDay(close.At)
			if prior, duplicate := byDay[day]; duplicate {
				findings = append(findings, allocationError("close_day_duplicate", key, "daily close history has more than one value for a UTC date"))
				if close.At.After(prior.At) {
					byDay[day] = close
				}
				continue
			}
			if close.At.Before(audit.start) {
				continue
			}
			byDay[day] = close
		}
		series[key] = byDay
	}
	legKeys := make([]string, len(input.Legs))
	for i, leg := range input.Legs {
		key := leg.Key.String()
		legKeys[i] = key
		if _, ok := series[key]; !ok {
			findings = append(findings, allocationError("exact_close_series_missing", key, "inverse-volatility needs daily closes for this exact instrument; substitute sources are not allowed"))
		}
	}
	if hasErrors(findings) {
		return nil, nil, audit, findingsSorted(findings)
	}

	shared := make(map[string]bool)
	for day := range series[legKeys[0]] {
		shared[day] = true
	}
	for _, key := range legKeys[1:] {
		for day := range shared {
			if _, ok := series[key][day]; !ok {
				delete(shared, day)
			}
		}
	}
	days := make([]string, 0, len(shared))
	for day := range shared {
		days = append(days, day)
	}
	slices.Sort(days)
	if len(days) == 0 {
		return nil, nil, audit, []Finding{allocationError("shared_close_dates_missing", "", "all exact instrument series need shared daily close dates")}
	}
	latest, err := time.Parse("2006-01-02", days[len(days)-1])
	if err != nil {
		return nil, nil, audit, []Finding{allocationError("shared_close_date_invalid", "", "shared close date could not be parsed")}
	}
	audit.latest = latest.UTC()
	if input.AsOf.Sub(audit.latest) > InverseVolMaxCloseAge {
		findings = append(findings, allocationError("inverse_vol_closes_stale", "", "latest shared daily close is more than five days old"))
	}
	returnsByLeg := make([][]float64, n)
	for index, key := range legKeys {
		for dayIndex := 1; dayIndex < len(days); dayIndex++ {
			previousDay, currentDay := days[dayIndex-1], days[dayIndex]
			previous, previousErr := time.Parse("2006-01-02", previousDay)
			current, currentErr := time.Parse("2006-01-02", currentDay)
			if previousErr != nil || currentErr != nil || current.Sub(previous) != 24*time.Hour {
				continue
			}
			before, after := series[key][previousDay].Price, series[key][currentDay].Price
			returnsByLeg[index] = append(returnsByLeg[index], math.Log(after/before))
		}
	}
	if len(returnsByLeg) == 0 || len(returnsByLeg[0]) < InverseVolMinReturns {
		findings = append(findings, allocationError("inverse_vol_history_short", "", "at least 60 shared consecutive daily returns within the 365-day window are required"))
	}
	for _, values := range returnsByLeg {
		if len(values) != len(returnsByLeg[0]) {
			findings = append(findings, allocationError("inverse_vol_returns_unaligned", "", "all legs need the same exact daily return intervals"))
			break
		}
	}
	if hasErrors(findings) {
		return nil, nil, audit, findingsSorted(findings)
	}
	audit.returns = len(returnsByLeg[0])
	if audit.returns > InverseVolMinReturns {
		for i := range returnsByLeg {
			returnsByLeg[i] = returnsByLeg[i][len(returnsByLeg[i])-InverseVolMinReturns:]
		}
		audit.returns = InverseVolMinReturns
	}
	volatility := make([]float64, n)
	for i, values := range returnsByLeg {
		volatility[i] = ewmaVolatility(values, InverseVolHalfLife)
		if !(volatility[i] > 0) || math.IsNaN(volatility[i]) || math.IsInf(volatility[i], 0) {
			findings = append(findings, allocationError("inverse_volatility_unusable", input.Legs[i].Key.String(), "exact close history must produce a positive finite volatility"))
		}
	}
	if hasErrors(findings) {
		return nil, nil, audit, findingsSorted(findings)
	}
	raw := make([]float64, n)
	for i, vol := range volatility {
		raw[i] = 1 / vol
	}
	weights := applyMinimumFloor(raw, floor)
	return weights, volatility, audit, findingsSorted(findings)
}

func applyMinimumFloor(raw []float64, floor float64) []float64 {
	weights := make([]float64, len(raw))
	active := make([]bool, len(raw))
	for i := range active {
		active[i] = true
	}
	remaining := 1.0
	for {
		total := 0.0
		for i, value := range raw {
			if active[i] {
				total += value
			}
		}
		if total <= 0 {
			return weights
		}
		belowFloor := make([]int, 0)
		for i, value := range raw {
			if active[i] && remaining*value/total < floor-allocationEpsilon {
				belowFloor = append(belowFloor, i)
			}
		}
		if len(belowFloor) == 0 {
			for i, value := range raw {
				if active[i] {
					weights[i] = remaining * value / total
				}
			}
			break
		}
		for _, i := range belowFloor {
			weights[i] = floor
			active[i] = false
			remaining -= floor
		}
	}
	return weights
}

func ewmaVolatility(returns []float64, halfLife float64) float64 {
	if len(returns) == 0 || halfLife <= 0 {
		return 0
	}
	decay := math.Exp(math.Log(0.5) / halfLife)
	weights := make([]float64, len(returns))
	totalWeight, mean := 0.0, 0.0
	for i := range returns {
		weight := math.Pow(decay, float64(len(returns)-1-i))
		weights[i] = weight
		totalWeight += weight
		mean += weight * returns[i]
	}
	mean /= totalWeight
	variance := 0.0
	for i, value := range returns {
		difference := value - mean
		variance += weights[i] * difference * difference
	}
	return math.Sqrt(variance / totalWeight)
}

func validateAllocationSums(result AllocationResult, expectedLegs int) bool {
	if len(result.Legs) != expectedLegs {
		return false
	}
	weightSum, totalBudgetSum := 0.0, result.CashWeight+result.UnallocatedWeight
	for _, leg := range result.Legs {
		weightSum += leg.Weight
		totalBudgetSum += leg.TotalBudgetWeight
	}
	wantWeightSum := 1.0
	if result.Denominator == DenominatorTotalBudget {
		wantWeightSum = 1 - result.CashWeight - result.UnallocatedWeight
	}
	return math.Abs(weightSum-wantWeightSum) <= allocationEpsilon && math.Abs(totalBudgetSum-1) <= allocationEpsilon
}

func allocationError(code, subject, message string) Finding {
	return Finding{Code: code, Subject: subject, Severity: SeverityError, Message: message}
}

func finiteBetween(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

func utcDay(value time.Time) string { return value.UTC().Format("2006-01-02") }
