package compair

import "errors"

type codedCLIError struct {
	code int
	err  error
}

func (e *codedCLIError) Error() string { return e.err.Error() }
func (e *codedCLIError) Unwrap() error { return e.err }
func (e *codedCLIError) ExitCode() int { return e.code }

func withCLIExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &codedCLIError{code: code, err: err}
}

func exitCodeForError(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() > 0 {
		return coded.ExitCode()
	}
	return 1
}
