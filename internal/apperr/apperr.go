package apperr

import (
	"errors"
	"fmt"
)

type Error struct {
	Code     string
	Message  string
	ExitCode int
	Err      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func New(code, message string, exitCode int) *Error {
	return &Error{Code: code, Message: message, ExitCode: exitCode}
}

func Wrap(code, message string, exitCode int, err error) *Error {
	return &Error{Code: code, Message: message, ExitCode: exitCode, Err: err}
}

func ExitCode(err error) int {
	var app *Error
	if errors.As(err, &app) && app.ExitCode > 0 {
		return app.ExitCode
	}
	return 1
}
