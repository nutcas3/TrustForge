package errors

import (
	"errors"
	"fmt"
)

// Common errors
var (
	ErrNotFound          = errors.New("resource not found")
	ErrAlreadyExists     = errors.New("resource already exists")
	ErrInvalidInput      = errors.New("invalid input")
	ErrTimeout           = errors.New("operation timed out")
	ErrCancelled         = errors.New("operation cancelled")
	ErrInternal          = errors.New("internal error")
	ErrUnavailable       = errors.New("service unavailable")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
	ErrConflict          = errors.New("conflict")
	ErrTooManyRequests   = errors.New("too many requests")
)

// VM-specific errors
var (
	ErrVMBootFailed      = errors.New("VM boot failed")
	ErrVMResumeFailed    = errors.New("VM resume from snapshot failed")
	ErrVMTimeout         = errors.New("VM operation timed out")
	ErrSnapshotFailed    = errors.New("snapshot creation failed")
	ErrNoWarmSnapshots   = errors.New("no warm snapshots available")
)

// Execution errors
var (
	ErrExecutionTimeout  = errors.New("execution timed out")
	ErrExecutionFailed   = errors.New("execution failed")
	ErrOutputTooLarge    = errors.New("output exceeds size limit")
	ErrResourceLimit     = errors.New("resource limit exceeded")
)

// Database errors
var (
	ErrDatabaseConnection = errors.New("database connection failed")
	ErrQueryFailed        = errors.New("database query failed")
	ErrTransactionFailed  = errors.New("database transaction failed")
)

// ValidationError represents a validation error with details
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error on field '%s': %s", e.Field, e.Message)
}

// NewValidationError creates a new validation error
func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{Field: field, Message: message}
}

// WrapError wraps an error with additional context
func WrapError(err error, message string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

// IsNotFound checks if an error is a not found error
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsTimeout checks if an error is a timeout error
func IsTimeout(err error) bool {
	return errors.Is(err, ErrTimeout) || errors.Is(err, ErrExecutionTimeout) || errors.Is(err, ErrVMTimeout)
}

// IsValidation checks if an error is a validation error
func IsValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
