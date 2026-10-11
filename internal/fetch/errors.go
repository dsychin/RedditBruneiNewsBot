package fetch

import (
	"errors"
	"fmt"
	"time"
)

// Stage names where in the pipeline a fetch failed.
type Stage string

const (
	StageFetch     Stage = "fetch"
	StageChallenge Stage = "challenge"
	StageSidecar   Stage = "sidecar"
)

// Error is a classified fetch failure.
type Error struct {
	Stage     Stage
	Retryable bool
	// RetryAfter is a server-requested minimum delay before retrying, if any.
	RetryAfter time.Duration
	Err        error
}

func (e *Error) Error() string {
	kind := "permanent"
	if e.Retryable {
		kind = "retryable"
	}
	return fmt.Sprintf("%s failure at %s stage: %v", kind, e.Stage, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// IsRetryable reports whether err is a fetch failure worth retrying.
func IsRetryable(err error) bool {
	var fe *Error
	return errors.As(err, &fe) && fe.Retryable
}

func retryable(stage Stage, err error) *Error {
	return &Error{Stage: stage, Retryable: true, Err: err}
}

func permanent(stage Stage, err error) *Error {
	return &Error{Stage: stage, Err: err}
}
