package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/dto"
	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/model"
	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestCertificateVersionChainRequiresIndependentReviewer(t *testing.T) {
	db := newVersionTestDB(t)
	service := NewCertificateRecordService(repository.NewCertificateRecordRepository(db), nil)
	ctx := context.Background()

	created, err := service.Create(ctx, certificateInput("CERT-TEST-01"), "operator", "cert-create-1")
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if created.Version != 1 || created.PreparedBy != "operator" || len(created.Revisions) != 1 {
		t.Fatalf("unexpected prepared certificate: %#v", created)
	}
	if revision := created.Revisions[0]; revision.Actor != "operator" || revision.RequestID != "cert-create-1" {
		t.Fatalf("missing create attribution: %#v", revision)
	}

	transition := dto.TransitionRequest{Status: "valid", ExpectedVersion: created.Version, Reason: "independent airworthiness review passed"}
	if _, err := service.Transition(ctx, created.ID, transition, "operator", model.RoleOperator, "cert-operator-denied"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator publication must be forbidden, got %v", err)
	}
	if _, err := service.Transition(ctx, created.ID, transition, "operator", model.RoleReviewer, "cert-same-user-denied"); !errors.Is(err, ErrSeparationOfDuty) {
		t.Fatalf("same-user review must be rejected, got %v", err)
	}

	verified, err := service.Transition(ctx, created.ID, transition, "reviewer", model.RoleReviewer, "cert-review-2")
	if err != nil {
		t.Fatalf("verify certificate: %v", err)
	}
	if verified.Status != "valid" || verified.Version != 2 || verified.VerifiedBy != "reviewer" || len(verified.Revisions) != 2 {
		t.Fatalf("unexpected verified certificate: %#v", verified)
	}
	latest := verified.Revisions[1]
	if latest.Actor != "reviewer" || latest.RequestID != "cert-review-2" || latest.Status != "valid" {
		t.Fatalf("invalid publication revision: %#v", latest)
	}

	update := updateCertificateInput(verified)
	if _, err := service.Update(ctx, verified.ID, update, "operator", "cert-late-edit"); !errors.Is(err, ErrLocked) {
		t.Fatalf("published certificate must be immutable, got %v", err)
	}
	assertAuditChain(t, db, "CertificateRecord", created.ID, 2)
}

func TestAuthorizationVersionChainEnforcesDualControl(t *testing.T) {
	db := newVersionTestDB(t)
	repos := newAuthorizationRepositories(t, db)
	service := NewReleaseAuthorizationService(repos.authorization, repos.inspection, repos.certificate, nil)
	ctx := context.Background()

	repos.seedEvidence(t, "PART-101", "IT-PASS-01", "passed", 1, "CR-VALID-01", "valid", 1)

	created, err := service.Create(ctx, authorizationInput("AUTH-TEST-01"), "operator", "auth-create-1")
	if err != nil {
		t.Fatalf("create authorization: %v", err)
	}
	review, err := service.Transition(ctx, created.ID, dto.TransitionRequest{
		Status: "review", ExpectedVersion: created.Version, Reason: "inspection evidence complete",
	}, "operator", model.RoleOperator, "auth-submit-2")
	if err != nil {
		t.Fatalf("submit authorization: %v", err)
	}
	if review.SubmittedBy != "operator" || review.Status != "review" || len(review.Revisions) != 2 {
		t.Fatalf("unexpected review submission: %#v", review)
	}
	// 提交时冻结两端版本，并写入提交版本快照。
	if review.FrozenInspectionCode != "IT-PART-101" || review.FrozenInspectionVersion != 1 ||
		review.FrozenCertificateCode != "CR-PART-101" || review.FrozenCertificateVersion != 1 {
		t.Fatalf("frozen evidence missing on review: %#v", review)
	}
	frozenRevision := review.Revisions[1]
	if frozenRevision.FrozenInspectionCode != "IT-PART-101" || frozenRevision.FrozenCertificateVersion != 1 {
		t.Fatalf("frozen evidence missing on revision: %#v", frozenRevision)
	}
	if review.EvidenceStatus == nil || !review.EvidenceStatus.Consistent {
		t.Fatalf("freshly submitted evidence must be consistent: %#v", review.EvidenceStatus)
	}

	approval := dto.TransitionRequest{Status: "approved", ExpectedVersion: review.Version, Reason: "independent release review passed"}
	if _, err := service.Transition(ctx, review.ID, approval, "operator", model.RoleOperator, "auth-operator-denied"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator approval must be forbidden, got %v", err)
	}
	if _, err := service.Transition(ctx, review.ID, approval, "operator", model.RoleReviewer, "auth-same-user-denied"); !errors.Is(err, ErrSeparationOfDuty) {
		t.Fatalf("same-user approval must be rejected, got %v", err)
	}

	approved, err := service.Transition(ctx, review.ID, approval, "reviewer", model.RoleReviewer, "auth-approve-3")
	if err != nil {
		t.Fatalf("approve authorization: %v", err)
	}
	if approved.Status != "approved" || approved.Version != 3 || approved.ReviewedBy != "reviewer" || len(approved.Revisions) != 3 {
		t.Fatalf("unexpected approved authorization: %#v", approved)
	}
	latest := approved.Revisions[2]
	if latest.Actor != "reviewer" || latest.RequestID != "auth-approve-3" || latest.Status != "approved" {
		t.Fatalf("invalid approval revision: %#v", latest)
	}
	// 已批准记录保留当时证据，后续更新不得改写。
	if approved.FrozenInspectionCode != "IT-PART-101" || approved.FrozenInspectionVersion != 1 ||
		approved.FrozenCertificateCode != "CR-PART-101" || approved.FrozenCertificateVersion != 1 {
		t.Fatalf("approved record must keep frozen evidence: %#v", approved)
	}
	if _, err := service.Update(ctx, approved.ID, updateAuthorizationInput(approved), "operator", "auth-late-edit"); !errors.Is(err, ErrLocked) {
		t.Fatalf("reviewed authorization must be immutable, got %v", err)
	}
	assertAuditChain(t, db, "ReleaseAuthorization", created.ID, 3)
}

func TestAuthorizationSubmissionFreezeBlocksDraftWithoutEvidence(t *testing.T) {
	db := newVersionTestDB(t)
	ctx := context.Background()

	cases := []struct {
		name              string
		relatedCode       string
		inspectionStatus  string
		inspectionExists  bool
		certificateStatus string
		certificateExists bool
		wantBlockCode     string
	}{
		{name: "missing related code", relatedCode: "", wantBlockCode: "EVIDENCE-RELATED-CODE"},
		{name: "inspection missing", relatedCode: "REL-BLOCK-1", certificateExists: true, certificateStatus: "valid", wantBlockCode: "EVIDENCE-INSPECTION-MISSING"},
		{name: "inspection not passed", relatedCode: "REL-BLOCK-2", inspectionExists: true, inspectionStatus: "running", certificateExists: true, certificateStatus: "valid", wantBlockCode: "EVIDENCE-INSPECTION-STATUS"},
		{name: "certificate missing", relatedCode: "REL-BLOCK-3", inspectionExists: true, inspectionStatus: "passed", wantBlockCode: "EVIDENCE-CERTIFICATE-MISSING"},
		{name: "certificate not valid", relatedCode: "REL-BLOCK-4", inspectionExists: true, inspectionStatus: "passed", certificateExists: true, certificateStatus: "expired", wantBlockCode: "EVIDENCE-CERTIFICATE-STATUS"},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repos := newAuthorizationRepositories(t, db)
			service := NewReleaseAuthorizationService(repos.authorization, repos.inspection, repos.certificate, nil)
			if tc.inspectionExists {
				repos.seedInspection(t, tc.relatedCode, "IT-"+tc.relatedCode, tc.inspectionStatus, 1)
			}
			if tc.certificateExists {
				repos.seedCertificate(t, tc.relatedCode, "CR-"+tc.relatedCode, tc.certificateStatus, 1)
			}
			input := authorizationInput("AUTH-BLOCK-" + string(rune('A'+index)))
			input.RelatedCode = tc.relatedCode
			created, err := service.Create(ctx, input, "operator", "auth-block-create")
			if err != nil {
				t.Fatalf("create draft: %v", err)
			}
			_, err = service.Transition(ctx, created.ID, dto.TransitionRequest{
				Status: "review", ExpectedVersion: created.Version, Reason: "attempt submit without valid evidence",
			}, "operator", model.RoleOperator, "auth-block-submit")
			var blocked *EvidenceValidationError
			if !errors.As(err, &blocked) || !errors.Is(err, ErrEvidenceBlocked) {
				t.Fatalf("submission must be evidence blocked, got %v", err)
			}
			if blocked.BlockCode != tc.wantBlockCode {
				t.Fatalf("block code = %q, want %q", blocked.BlockCode, tc.wantBlockCode)
			}
			// 任一缺失或状态不合就保持草案。
			refreshed, err := service.Get(ctx, created.ID)
			if err != nil {
				t.Fatalf("reload draft: %v", err)
			}
			if refreshed.Status != "draft" || refreshed.Version != 1 || refreshed.FrozenInspectionCode != "" {
				t.Fatalf("draft must stay unchanged after blocked submit: %#v", refreshed)
			}
			if refreshed.EvidenceStatus == nil || refreshed.EvidenceStatus.Consistent || refreshed.EvidenceStatus.BlockCode != tc.wantBlockCode {
				t.Fatalf("draft evidence status must expose block code: %#v", refreshed.EvidenceStatus)
			}
		})
	}
}

func TestAuthorizationApprovalRejectsEvidenceDrift(t *testing.T) {
	db := newVersionTestDB(t)
	ctx := context.Background()

	t.Run("inspection version changed after submit", func(t *testing.T) {
		repos := newAuthorizationRepositories(t, db)
		service := NewReleaseAuthorizationService(repos.authorization, repos.inspection, repos.certificate, nil)
		repos.seedEvidence(t, "REL-DRIFT-I", "", "passed", 1, "", "valid", 1)
		review := submitAuthorization(t, ctx, service, "AUTH-DRIFT-I", "REL-DRIFT-I")
		if review.FrozenInspectionCode != "IT-REL-DRIFT-I" || review.FrozenCertificateCode != "CR-REL-DRIFT-I" {
			t.Fatalf("unexpected frozen codes: %s %s", review.FrozenInspectionCode, review.FrozenCertificateCode)
		}

		// 提交后检查任务产生新版本（仍为 passed），批准必须拒绝。
		repos.seedEvidence(t, "REL-DRIFT-I", "", "passed", 2, "", "valid", 1)
		_, err := service.Transition(ctx, review.ID, dto.TransitionRequest{
			Status: "approved", ExpectedVersion: review.Version, Reason: "approve after inspection drift",
		}, "reviewer", model.RoleReviewer, "auth-drift-approve")
		assertDriftBlocked(t, err, "DRIFT-INSPECTION-VERSION")

		refreshed, _ := service.Get(ctx, review.ID)
		if refreshed.Status != "review" {
			t.Fatalf("drift must not change authorization status, got %s", refreshed.Status)
		}
		if refreshed.EvidenceStatus == nil || refreshed.EvidenceStatus.Consistent ||
			refreshed.EvidenceStatus.FrozenInspectionVersion != 1 || refreshed.EvidenceStatus.CurrentInspectionVersion != 2 {
			t.Fatalf("review evidence status must show inspection drift: %#v", refreshed.EvidenceStatus)
		}
	})

	t.Run("certificate version changed after submit", func(t *testing.T) {
		repos := newAuthorizationRepositories(t, db)
		service := NewReleaseAuthorizationService(repos.authorization, repos.inspection, repos.certificate, nil)
		repos.seedEvidence(t, "REL-DRIFT-C", "", "passed", 1, "", "valid", 1)
		review := submitAuthorization(t, ctx, service, "AUTH-DRIFT-C", "REL-DRIFT-C")

		// 提交后证书产生新版本（仍为 valid），批准必须拒绝。
		repos.seedEvidence(t, "REL-DRIFT-C", "", "passed", 1, "", "valid", 2)
		_, err := service.Transition(ctx, review.ID, dto.TransitionRequest{
			Status: "restricted", ExpectedVersion: review.Version, Reason: "restricted approval after certificate drift",
		}, "reviewer", model.RoleReviewer, "auth-drift-restricted")
		assertDriftBlocked(t, err, "DRIFT-CERTIFICATE-VERSION")
	})

	t.Run("resubmission after drift freezes new versions and approves", func(t *testing.T) {
		repos := newAuthorizationRepositories(t, db)
		service := NewReleaseAuthorizationService(repos.authorization, repos.inspection, repos.certificate, nil)
		repos.seedEvidence(t, "REL-DRIFT-R", "", "passed", 1, "", "valid", 1)
		review := submitAuthorization(t, ctx, service, "AUTH-DRIFT-R", "REL-DRIFT-R")
		repos.seedEvidence(t, "REL-DRIFT-R", "", "passed", 2, "", "valid", 2)

		// 复核员退回草案。
		draft, err := service.Transition(ctx, review.ID, dto.TransitionRequest{
			Status: "draft", ExpectedVersion: review.Version, Reason: "evidence drifted, return for resubmission",
		}, "reviewer", model.RoleReviewer, "auth-drift-return")
		if err != nil {
			t.Fatalf("return to draft: %v", err)
		}
		if draft.FrozenInspectionCode != "" || draft.FrozenCertificateCode != "" {
			t.Fatalf("returned draft must clear frozen evidence: %#v", draft)
		}
		// 操作员重新提交，冻结新版本。
		resubmitted, err := service.Transition(ctx, draft.ID, dto.TransitionRequest{
			Status: "review", ExpectedVersion: draft.Version, Reason: "resubmit against refreshed evidence",
		}, "operator", model.RoleOperator, "auth-drift-resubmit")
		if err != nil {
			t.Fatalf("resubmit: %v", err)
		}
		if resubmitted.FrozenInspectionVersion != 2 || resubmitted.FrozenCertificateVersion != 2 {
			t.Fatalf("resubmission must freeze current versions: %#v", resubmitted)
		}
		// 证据未再变化时仍按双人复核批准。
		approved, err := service.Transition(ctx, resubmitted.ID, dto.TransitionRequest{
			Status: "approved", ExpectedVersion: resubmitted.Version, Reason: "independent approval after resubmission",
		}, "reviewer", model.RoleReviewer, "auth-drift-approved")
		if err != nil {
			t.Fatalf("approve after resubmission: %v", err)
		}
		if approved.Status != "approved" || approved.FrozenInspectionVersion != 2 || approved.FrozenCertificateVersion != 2 {
			t.Fatalf("approved record must keep resubmitted evidence: %#v", approved)
		}
	})
}

func assertDriftBlocked(t *testing.T, err error, wantBlockCode string) {
	t.Helper()
	var drifted *EvidenceValidationError
	if !errors.As(err, &drifted) || !errors.Is(err, ErrEvidenceDrift) {
		t.Fatalf("approval must be rejected for evidence drift, got %v", err)
	}
	if drifted.BlockCode != wantBlockCode {
		t.Fatalf("drift block code = %q, want %q", drifted.BlockCode, wantBlockCode)
	}
}

func submitAuthorization(t *testing.T, ctx context.Context, service ReleaseAuthorizationService, code, relatedCode string) model.ReleaseAuthorization {
	t.Helper()
	input := authorizationInput(code)
	input.RelatedCode = relatedCode
	created, err := service.Create(ctx, input, "operator", "auth-drift-create")
	if err != nil {
		t.Fatalf("create authorization: %v", err)
	}
	review, err := service.Transition(ctx, created.ID, dto.TransitionRequest{
		Status: "review", ExpectedVersion: created.Version, Reason: "evidence complete",
	}, "operator", model.RoleOperator, "auth-drift-submit")
	if err != nil {
		t.Fatalf("submit authorization: %v", err)
	}
	return review
}

type authorizationRepositories struct {
	authorization repository.ReleaseAuthorizationRepository
	inspection    repository.InspectionTaskRepository
	certificate   repository.CertificateRecordRepository
	db            *gorm.DB
}

func newAuthorizationRepositories(t *testing.T, db *gorm.DB) authorizationRepositories {
	t.Helper()
	return authorizationRepositories{
		authorization: repository.NewReleaseAuthorizationRepository(db),
		inspection:    repository.NewInspectionTaskRepository(db),
		certificate:   repository.NewCertificateRecordRepository(db),
		db:            db,
	}
}

// seedEvidence upserts the latest inspection task and certificate for a
// related code at the requested versions so freeze/drift paths are testable.
// Codes are derived from the related code to avoid unique-index clashes when
// several table-driven cases share one in-memory database.
func (r authorizationRepositories) seedEvidence(t *testing.T, relatedCode, inspectionCode, inspectionStatus string, inspectionVersion uint, certificateCode, certificateStatus string, certificateVersion uint) {
	t.Helper()
	if strings.TrimSpace(inspectionStatus) != "" {
		r.seedInspection(t, relatedCode, "IT-"+relatedCode, inspectionStatus, inspectionVersion)
	}
	if strings.TrimSpace(certificateStatus) != "" {
		r.seedCertificate(t, relatedCode, "CR-"+relatedCode, certificateStatus, certificateVersion)
	}
}

func (r authorizationRepositories) seedInspection(t *testing.T, relatedCode, code, status string, version uint) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	var existing model.InspectionTask
	err := r.db.WithContext(ctx).Where("related_code = ?", relatedCode).First(&existing).Error
	if err == nil {
		existing.Code = code
		existing.Status = status
		existing.Version = version
		existing.UpdatedAt = now
		if err := r.db.Save(&existing).Error; err != nil {
			t.Fatalf("update inspection evidence: %v", err)
		}
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("lookup inspection evidence: %v", err)
	}
	item := model.InspectionTask{
		BaseModel: model.BaseModel{
			Code: code, Name: "Inspection " + code, Status: status,
			Version: version, CreatedAt: now, UpdatedAt: now,
		},
		Facility: "Hangar 2", Owner: "Inspection desk", Category: "engine", RiskLevel: "high",
		MetricValue: 100, MetricUnit: "percent", EffectiveAt: now, Evidence: "inspection evidence",
		RelatedCode: relatedCode,
	}
	if err := r.db.WithContext(ctx).Create(&item).Error; err != nil {
		t.Fatalf("create inspection evidence: %v", err)
	}
}

func (r authorizationRepositories) seedCertificate(t *testing.T, relatedCode, code, status string, version uint) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	var existing model.CertificateRecord
	err := r.db.WithContext(ctx).Where("related_code = ?", relatedCode).First(&existing).Error
	if err == nil {
		existing.Code = code
		existing.Status = status
		existing.Version = version
		existing.UpdatedAt = now
		if err := r.db.Save(&existing).Error; err != nil {
			t.Fatalf("update certificate evidence: %v", err)
		}
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("lookup certificate evidence: %v", err)
	}
	item := model.CertificateRecord{
		BaseModel: model.BaseModel{
			Code: code, Name: "Certificate " + code, Status: status,
			Version: version, CreatedAt: now, UpdatedAt: now,
		},
		Facility: "Hangar 2", Owner: "Certificate desk", Category: "engine", RiskLevel: "high",
		MetricValue: 100, MetricUnit: "percent", EffectiveAt: now, Evidence: "certificate evidence",
		RelatedCode: relatedCode, PreparedBy: "operator", VerifiedBy: "reviewer",
	}
	if err := r.db.WithContext(ctx).Create(&item).Error; err != nil {
		t.Fatalf("create certificate evidence: %v", err)
	}
}

func newVersionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.AuditLog{}, &model.CertificateRecord{}, &model.CertificateRecordRevision{},
		&model.ReleaseAuthorization{}, &model.ReleaseAuthorizationRevision{},
		&model.InspectionTask{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	return db
}

func certificateInput(code string) dto.CreateCertificateRecord {
	return dto.CreateCertificateRecord{
		Code: code, Name: "Turbine certificate", Description: "controlled certificate",
		Facility: "Hangar 2", Owner: "Airworthiness team", Category: "engine",
		RiskLevel: "high", MetricValue: 100, MetricUnit: "percent",
		EffectiveAt: time.Now().UTC(), Evidence: "inspection report IR-101", RelatedCode: "PART-101",
	}
}

func authorizationInput(code string) dto.CreateReleaseAuthorization {
	return dto.CreateReleaseAuthorization{
		Code: code, Name: "Component release", Description: "controlled release",
		Facility: "Hangar 2", Owner: "Release desk", Category: "engine",
		RiskLevel: "high", MetricValue: 100, MetricUnit: "percent",
		EffectiveAt: time.Now().UTC(), Evidence: "certificate CERT-101", RelatedCode: "PART-101",
	}
}

func updateCertificateInput(item model.CertificateRecord) dto.UpdateCertificateRecord {
	return dto.UpdateCertificateRecord{
		ExpectedVersion: item.Version, Name: item.Name, Description: item.Description,
		Facility: item.Facility, Owner: item.Owner, Category: item.Category, RiskLevel: item.RiskLevel,
		MetricValue: item.MetricValue, MetricUnit: item.MetricUnit, EffectiveAt: item.EffectiveAt,
		Evidence: item.Evidence, RelatedCode: item.RelatedCode,
	}
}

func updateAuthorizationInput(item model.ReleaseAuthorization) dto.UpdateReleaseAuthorization {
	return dto.UpdateReleaseAuthorization{
		ExpectedVersion: item.Version, Name: item.Name, Description: item.Description,
		Facility: item.Facility, Owner: item.Owner, Category: item.Category, RiskLevel: item.RiskLevel,
		MetricValue: item.MetricValue, MetricUnit: item.MetricUnit, EffectiveAt: item.EffectiveAt,
		Evidence: item.Evidence, RelatedCode: item.RelatedCode,
	}
}

func assertAuditChain(t *testing.T, db *gorm.DB, entityType string, entityID uint, expected int64) {
	t.Helper()
	var count int64
	if err := db.Model(&model.AuditLog{}).Where("entity_type = ? AND entity_id = ?", entityType, entityID).Count(&count).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d atomic audits, got %d", expected, count)
	}
}
