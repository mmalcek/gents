package tstagoverride

//gents:export
type Session struct {
	ID        string `json:"id"`
	EntryType string `json:"entry_type" ts:"'manual' | 'automatic'"`
	Level     int    `json:"level" ts:"0 | 1 | 2"`
	Meta      any    `json:"meta" ts:"Record<string, string>"`
	Expire    string `json:"expire,omitempty" ts:"string | null"`
}
