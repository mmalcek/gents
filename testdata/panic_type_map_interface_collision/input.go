package typemapinterfacecollision

//gents:export
type tUser struct {
	ID string `json:"id"`
}

//gents:export
type tItem struct {
	Owner Something `json:"owner"`
}
