package quotedfieldnames

//gents:export
type Header struct {
	ContentType string `json:"content-type"`
	StatusCode  int    `json:"123status"`
	Dotted      string `json:"x.foo"`
}
