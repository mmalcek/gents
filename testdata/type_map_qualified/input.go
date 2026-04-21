package typemapqualified

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

//gents:export
type Record struct {
	ID     uuid.UUID       `json:"id"`
	Maybe  *uuid.UUID      `json:"maybe"`
	Tags   []uuid.UUID     `json:"tags"`
	Amount decimal.Decimal `json:"amount"`
}
