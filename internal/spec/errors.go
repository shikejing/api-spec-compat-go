package spec

import "fmt"

// ResolveError describes a loading/parsing/resolution failure with a position.
type ResolveError struct {
	Location Location
	Message  string
	Cause    error
}

func (e *ResolveError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Location.String(), e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Location.String(), e.Message)
}

func (e *ResolveError) Unwrap() error { return e.Cause }
