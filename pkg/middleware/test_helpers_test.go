package middleware

type stubStringer string

func (s stubStringer) String() string { return string(s) }
