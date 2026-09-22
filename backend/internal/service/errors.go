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
	// ErrEvidenceBlocked keeps a draft in place when the evidence freeze check
	// at submission cannot find a passed inspection task and a valid
	// certificate for the 关联编号.
	ErrEvidenceBlocked = errors.New("evidence freeze check blocked the submission")
	// ErrEvidenceDrift forces the reviewer to reject an approval when the
	// inspection task or certificate version changed after submission.
	ErrEvidenceDrift = errors.New("frozen evidence changed and the release must be resubmitted")
)

// EvidenceValidationError carries the stable 阻断编号 (BlockCode) and a human
// readable reason alongside the blocked/drift sentinel.
type EvidenceValidationError struct {
	Err       error
	BlockCode string
	Reason    string
}

func (e *EvidenceValidationError) Error() string {
	if e.Reason == "" {
		return e.Err.Error()
	}
	return e.Reason
}

func (e *EvidenceValidationError) Unwrap() error { return e.Err }

func evidenceBlocked(code, reason string) error {
	return &EvidenceValidationError{Err: ErrEvidenceBlocked, BlockCode: code, Reason: reason}
}

func evidenceDrifted(code, reason string) error {
	return &EvidenceValidationError{Err: ErrEvidenceDrift, BlockCode: code, Reason: reason}
}
