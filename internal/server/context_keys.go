package server

type ctxKey int

const (
	requestIDKey ctxKey = iota
	csrfKey
)
