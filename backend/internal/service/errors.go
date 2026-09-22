package service

import "errors"

var (
	ErrInvalidTransition = errors.New("requested status transition is not allowed")
	ErrInvalidInput      = errors.New("business input validation failed")
	ErrUnauthorized      = errors.New("invalid username or password")
	ErrInactiveUser      = errors.New("user account is inactive")
	ErrForbidden         = errors.New("role is not permitted for this operation")
	ErrLocked            = errors.New("record is locked after review begins")
	ErrSeparationOfDuty  = errors.New("preparer and reviewer must be different users")
	ErrEvidenceBlocked   = errors.New("release evidence is blocked")
)

// EvidenceBlockError reports why evidence freeze or frozen-evidence validation
// stopped the state machine. Code is a stable machine-readable blocker id and
// Reason contains the related code plus both frozen and current versions.
type EvidenceBlockError struct {
	Code   string
	Reason string
}

func (e *EvidenceBlockError) Error() string { return e.Reason }

func (e *EvidenceBlockError) Is(target error) bool { return target == ErrEvidenceBlocked }
