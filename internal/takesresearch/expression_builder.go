package takesresearch

import (
	"context"
	"errors"
	"strings"
)

// Builder assembles the default exploratory expression from one saved draft,
// its immutable start catalog, and read-only public identity metadata.
type Builder struct {
	catalog            CatalogReader
	expressionModel    ExpressionPlanner
	expressionIdentity CompanyIdentityReader
}

// NewExpressionBuilder wires only the providers used by the original default
// exploratory-expression path. The catalog and identity adapters are
// read-only; they do not establish account access or authorize execution.
func NewExpressionBuilder(catalog CatalogReader, planner ExpressionPlanner, identity CompanyIdentityReader) *Builder {
	return &Builder{catalog: catalog, expressionModel: planner, expressionIdentity: identity}
}

// Build dispatches only the default exploratory expression mode. An empty
// mode is its legacy default; the separate deep-research implementation is
// intentionally not part of this standalone package.
func (b *Builder) Build(ctx context.Context, input Input) (Output, error) {
	if b == nil {
		return Output{Status: StatusFailed}, errors.New("takes research builder is required")
	}
	mode := strings.TrimSpace(input.ConstructionMode)
	if mode == "" {
		mode = ExpressionMode
	}
	if mode == DeepResearchMode {
		return Output{Status: StatusFailed}, errors.New("deep research mode is not supported by the standalone exploratory builder")
	}
	if mode != ExpressionMode {
		return Output{Status: StatusFailed}, errors.New("unsupported Takes research construction mode")
	}
	return b.buildExpression(ctx, input)
}
