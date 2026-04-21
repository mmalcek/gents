package gentsmapdirective

//gents:map uuid.UUID=string
//gents:map decimal.Decimal=string

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

//gents:export
type Record struct {
	ID     uuid.UUID       `json:"id"`
	Maybe  *uuid.UUID      `json:"maybe"`
	Amount decimal.Decimal `json:"amount"`
}
