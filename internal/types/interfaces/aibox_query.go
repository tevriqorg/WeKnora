package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type AIBOXQueryService interface {
	Query(ctx context.Context, input types.AIBOXQueryInput) (*types.AIBOXQueryResult, error)
}
