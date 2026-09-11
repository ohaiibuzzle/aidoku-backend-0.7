package models

import "fmt"

// SourceErrorKind mirrors AidokuRunner's SourceError.swift.
type SourceErrorKind int

const (
	SourceErrorMissingResult SourceErrorKind = iota
	SourceErrorUnimplemented
	SourceErrorNetworkError
	SourceErrorMessage
	SourceErrorHTMLError
	SourceErrorJSError
	SourceErrorCanvasError
	SourceErrorUTF8Error
	SourceErrorJSONParseError
	SourceErrorDeserializeError
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
	case SourceErrorHTMLError:
		return "source: html error"
	case SourceErrorJSError:
		return "source: js error"
	case SourceErrorCanvasError:
		return "source: canvas error"
	case SourceErrorUTF8Error:
		return "source: utf8 error"
	case SourceErrorJSONParseError:
		return "source: json parse error"
	case SourceErrorDeserializeError:
		return "source: deserialize error"
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
func ErrHTMLError() error        { return &SourceError{Kind: SourceErrorHTMLError} }
func ErrJSError() error          { return &SourceError{Kind: SourceErrorJSError} }
func ErrCanvasError() error      { return &SourceError{Kind: SourceErrorCanvasError} }
func ErrUTF8Error() error        { return &SourceError{Kind: SourceErrorUTF8Error} }
func ErrJSONParseError() error   { return &SourceError{Kind: SourceErrorJSONParseError} }
func ErrDeserializeError() error { return &SourceError{Kind: SourceErrorDeserializeError} }
