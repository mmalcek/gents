package panicmarkeronalias

type Base struct {
	X int `json:"x"`
}

//gents:export
type Alias = Base
