package harness

import "fmt"

// RunError wraps a harness invocation failure with its stderr for display.
type RunError struct {
	Harness string
	Err     error
	Stderr  string
}

func (e *RunError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("%s failed: %v: %s", e.Harness, e.Err, e.Stderr)
	}
	return fmt.Sprintf("%s failed: %v", e.Harness, e.Err)
}

func (e *RunError) Unwrap() error { return e.Err }
