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
	// Frozen evidence is captured when the draft is submitted for review and
	// never rewritten afterwards, so approved records keep their original proof.
	FrozenInspectionCode     string `json:"frozenInspectionCode" gorm:"size:64;index"`
	FrozenInspectionVersion  uint   `json:"frozenInspectionVersion"`
	FrozenCertificateCode    string `json:"frozenCertificateCode" gorm:"size:64;index"`
	FrozenCertificateVersion uint   `json:"frozenCertificateVersion"`
	EvidenceBlockedReason    string `json:"evidenceBlockedReason" gorm:"size:500"`
	// Read-only evidence status computed on load; never persisted.
	EvidenceConsistent        *bool                          `json:"evidenceConsistent,omitempty" gorm:"-"`
	EvidenceBlockCode         string                         `json:"evidenceBlockCode,omitempty" gorm:"-"`
	EvidenceBlockReason       string                         `json:"evidenceBlockReason,omitempty" gorm:"-"`
	CurrentInspectionVersion  uint                           `json:"currentInspectionVersion,omitempty" gorm:"-"`
	CurrentInspectionStatus   string                         `json:"currentInspectionStatus,omitempty" gorm:"-"`
	CurrentCertificateVersion uint                           `json:"currentCertificateVersion,omitempty" gorm:"-"`
	CurrentCertificateStatus  string                         `json:"currentCertificateStatus,omitempty" gorm:"-"`
	Revisions                 []ReleaseAuthorizationRevision `json:"revisions,omitempty" gorm:"foreignKey:ReleaseAuthorizationID"`
}

func (item *ReleaseAuthorization) GetBase() *BaseModel { return &item.BaseModel }

func (item ReleaseAuthorization) TableName() string { return "release_authorizations" }

var ReleaseAuthorizationInitialStatus = "draft"

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
