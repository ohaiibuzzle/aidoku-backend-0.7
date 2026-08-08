package models

import "fmt"

// SourceErrorKind mirrors AidokuRunner's SourceError.swift.
type SourceErrorKind int

const (
	SourceErrorMissingResult SourceErrorKind = iota
	SourceErrorUnimplemented
	SourceErrorNetworkError
	SourceErrorMessage
)

// SourceError is produced when a guest call returns a negative result code
// or an error-message buffer, per Interpreter.swift's handleResult.
type SourceError struct {
	Kind    SourceErrorKind
	Message string
}

func (e *SourceError) Error() string {
	switch e.Kind {
	case SourceErrorMissingResult:
		return "source: missing result"
	case SourceErrorUnimplemented:
		return "source: unimplemented"
	case SourceErrorNetworkError:
		return "source: network error"
	case SourceErrorMessage:
		return fmt.Sprintf("source: %s", e.Message)
	default:
		return "source: unknown error"
	}
}

func ErrMissingResult() error { return &SourceError{Kind: SourceErrorMissingResult} }
func ErrUnimplemented() error { return &SourceError{Kind: SourceErrorUnimplemented} }
func ErrNetworkError() error  { return &SourceError{Kind: SourceErrorNetworkError} }
func ErrMessage(msg string) error {
	return &SourceError{Kind: SourceErrorMessage, Message: msg}
}
