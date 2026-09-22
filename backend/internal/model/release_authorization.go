package model

import "time"

// ReleaseAuthorization models 放行授权 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type ReleaseAuthorization struct {
	BaseModel
	Facility     string    `json:"facility" gorm:"size:120;index"`
	Owner        string    `json:"owner" gorm:"size:120;index"`
	Category     string    `json:"category" gorm:"size:80;index"`
	RiskLevel    string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue  float64   `json:"metricValue"`
	MetricUnit   string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt  time.Time `json:"effectiveAt"`
	Evidence     string    `json:"evidence" gorm:"size:2000"`
	RelatedCode  string    `json:"relatedCode" gorm:"size:64;index"`
	SubmittedBy  string    `json:"submittedBy" gorm:"size:80;index"`
	ReviewedBy   string    `json:"reviewedBy" gorm:"size:80;index"`
	ReviewReason string    `json:"reviewReason" gorm:"size:500"`
	// Evidence freeze captured when the draft is submitted for review. The
	// versions are re-checked at approval so post-submission evidence drift
	// forces a rejection and a fresh submission.
	FrozenInspectionCode     string `json:"frozenInspectionCode" gorm:"size:64"`
	FrozenInspectionVersion  uint   `json:"frozenInspectionVersion"`
	FrozenCertificateCode    string `json:"frozenCertificateCode" gorm:"size:64"`
	FrozenCertificateVersion uint   `json:"frozenCertificateVersion"`
	// EvidenceStatus is derived on read (never persisted) and reports the
	// frozen-vs-current comparison and the blocking code/reason.
	EvidenceStatus *EvidenceFreezeStatus          `json:"evidenceStatus,omitempty" gorm:"-"`
	Revisions      []ReleaseAuthorizationRevision `json:"revisions,omitempty" gorm:"foreignKey:ReleaseAuthorizationID"`
}

func (item *ReleaseAuthorization) GetBase() *BaseModel { return &item.BaseModel }

func (item ReleaseAuthorization) TableName() string { return "release_authorizations" }

var ReleaseAuthorizationInitialStatus = "draft"

// EvidenceFreezeStatus is the read model for the evidence freeze: it exposes
// the related code, the versions frozen at submission, the current versions
// and whether they still agree.
type EvidenceFreezeStatus struct {
	RelatedCode               string `json:"relatedCode"`
	InspectionCode            string `json:"inspectionCode,omitempty"`
	InspectionStatus          string `json:"inspectionStatus,omitempty"`
	FrozenInspectionVersion   uint   `json:"frozenInspectionVersion,omitempty"`
	CurrentInspectionVersion  uint   `json:"currentInspectionVersion,omitempty"`
	CertificateCode           string `json:"certificateCode,omitempty"`
	CertificateStatus         string `json:"certificateStatus,omitempty"`
	FrozenCertificateVersion  uint   `json:"frozenCertificateVersion,omitempty"`
	CurrentCertificateVersion uint   `json:"currentCertificateVersion,omitempty"`
	Consistent                bool   `json:"consistent"`
	// BlockCode/BlockReason explain the first blocking condition (missing
	// evidence, wrong status or drifted version). Empty when nothing blocks.
	BlockCode   string `json:"blockCode,omitempty"`
	BlockReason string `json:"blockReason,omitempty"`
}

// ReleaseAuthorizationRevision preserves the complete authorization decision
// chain, including who acted and which request produced the version.
type ReleaseAuthorizationRevision struct {
	ID                       uint      `json:"id" gorm:"primaryKey"`
	ReleaseAuthorizationID   uint      `json:"releaseAuthorizationId" gorm:"not null;index;uniqueIndex:idx_authorization_revision_version,priority:1"`
	Version                  uint      `json:"version" gorm:"not null;uniqueIndex:idx_authorization_revision_version,priority:2"`
	Status                   string    `json:"status" gorm:"size:40;not null"`
	Evidence                 string    `json:"evidence" gorm:"size:2000"`
	Actor                    string    `json:"actor" gorm:"size:80;not null;index"`
	RequestID                string    `json:"requestId" gorm:"size:64;not null;index"`
	Action                   string    `json:"action" gorm:"size:40;not null"`
	Reason                   string    `json:"reason" gorm:"size:500"`
	FrozenInspectionCode     string    `json:"frozenInspectionCode" gorm:"size:64"`
	FrozenInspectionVersion  uint      `json:"frozenInspectionVersion"`
	FrozenCertificateCode    string    `json:"frozenCertificateCode" gorm:"size:64"`
	FrozenCertificateVersion uint      `json:"frozenCertificateVersion"`
	CreatedAt                time.Time `json:"createdAt" gorm:"index"`
}
