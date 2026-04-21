package jsonstringflag

import "time"

//gents:export
type Ids struct {
	User    int64         `json:"user,string"`
	Maybe   *int64        `json:"maybe,string"`
	Enabled bool          `json:"enabled,string"`
	Toggle  *bool         `json:"toggle,string"`
	Price   float64       `json:"price,string"`
	Wait    time.Duration `json:"wait,string"`
	Plain   int           `json:"plain"`
}
