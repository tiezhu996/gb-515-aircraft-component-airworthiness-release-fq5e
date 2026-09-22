package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/dto"
	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/model"
	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/repository"
	"gorm.io/gorm"
)

type evidenceFixture struct {
	db       *gorm.DB
	service  ReleaseAuthorizationService
	ctx      context.Context
	authID   uint
	authCode string
	related  string
	inspCode string
	certCode string
}

func newEvidenceFixture(t *testing.T, related string, inspectionStatus string, inspectionVersion uint, certificateStatus string, certificateVersion uint) evidenceFixture {
	t.Helper()
	db := newVersionTestDB(t)
	seedAuthorizationEvidence(t, db, related, inspectionVersion, inspectionStatus, certificateVersion, certificateStatus)
	service := NewReleaseAuthorizationService(
		repository.NewReleaseAuthorizationRepository(db),
		repository.NewInspectionTaskRepository(db),
		repository.NewCertificateRecordRepository(db), nil)
	ctx := context.Background()
	created, err := service.Create(ctx, authorizationInputFor("AUTH-EVID-"+related, related), "operator", "auth-evidence-create")
	if err != nil {
		t.Fatalf("create authorization: %v", err)
	}
	return evidenceFixture{
		db: db, service: service, ctx: ctx, authID: created.ID, authCode: created.Code, related: related,
		inspCode: "INS-" + related, certCode: "CERT-" + related,
	}
}

func authorizationInputFor(code, related string) dto.CreateReleaseAuthorization {
	input := authorizationInput(code)
	input.RelatedCode = related
	return input
}

func (f evidenceFixture) submit(t *testing.T, expectedVersion uint) (model.ReleaseAuthorization, error) {
	t.Helper()
	return f.service.Transition(f.ctx, f.authID, dto.TransitionRequest{
		Status: "review", ExpectedVersion: expectedVersion, Reason: "evidence frozen for review",
	}, "operator", model.RoleOperator, "auth-evidence-submit")
}

func TestSubmitReviewFreezesBothEvidenceVersions(t *testing.T) {
	fixture := newEvidenceFixture(t, "REL-GOOD", "passed", 3, "valid", 2)
	draft, err := fixture.service.Get(fixture.ctx, fixture.authID)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if draft.EvidenceConsistent == nil || !*draft.EvidenceConsistent {
		t.Fatalf("draft with complete evidence should be consistent, got %#v", draft.EvidenceConsistent)
	}

	review, err := fixture.submit(t, draft.Version)
	if err != nil {
		t.Fatalf("submit review: %v", err)
	}
	if review.Status != "review" || review.FrozenInspectionCode != fixture.inspCode || review.FrozenInspectionVersion != 3 ||
		review.FrozenCertificateCode != fixture.certCode || review.FrozenCertificateVersion != 2 {
		t.Fatalf("frozen evidence missing on submission: %#v", review)
	}
	if review.EvidenceConsistent == nil || !*review.EvidenceConsistent {
		t.Fatalf("freshly submitted evidence should be consistent")
	}
	frozen := review.Revisions[len(review.Revisions)-1]
	if frozen.FrozenInspectionCode != fixture.inspCode || frozen.FrozenInspectionVersion != 3 ||
		frozen.FrozenCertificateCode != fixture.certCode || frozen.FrozenCertificateVersion != 2 {
		t.Fatalf("revision must preserve frozen evidence: %#v", frozen)
	}
}

func TestSubmitReviewBlockedWithoutEvidenceKeepsDraft(t *testing.T) {
	cases := []struct {
		name       string
		related    string
		seedUnder  string
		inspStatus string
		certStatus string
		blockCode  string
	}{
		{name: "missing related code", related: "", seedUnder: "REL-SEED-0", inspStatus: "passed", certStatus: "valid", blockCode: "EVIDENCE_LINK_MISSING"},
		{name: "inspection not passed", related: "REL-BAD-1", seedUnder: "REL-BAD-1", inspStatus: "running", certStatus: "valid", blockCode: "INSPECTION_NOT_PASSED"},
		{name: "certificate not valid", related: "REL-BAD-2", seedUnder: "REL-BAD-2", inspStatus: "passed", certStatus: "draft", blockCode: "CERTIFICATE_NOT_VALID"},
		{name: "no evidence at all", related: "REL-BAD-3", seedUnder: "REL-OTHER", inspStatus: "passed", certStatus: "valid", blockCode: "INSPECTION_NOT_PASSED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newVersionTestDB(t)
			seedAuthorizationEvidence(t, db, tc.seedUnder, 1, tc.inspStatus, 1, tc.certStatus)
			service := NewReleaseAuthorizationService(
				repository.NewReleaseAuthorizationRepository(db),
				repository.NewInspectionTaskRepository(db),
				repository.NewCertificateRecordRepository(db), nil)
			ctx := context.Background()
			created, err := service.Create(ctx, authorizationInputFor("AUTH-EVID-"+tc.related, tc.related), "operator", "auth-evidence-blocked")
			if err != nil {
				t.Fatalf("create authorization: %v", err)
			}
			_, err = service.Transition(ctx, created.ID, dto.TransitionRequest{
				Status: "review", ExpectedVersion: created.Version, Reason: "blocked submission attempt",
			}, "operator", model.RoleOperator, "auth-evidence-blocked-submit")
			var block *EvidenceBlockError
			if !errors.As(err, &block) || block.Code != tc.blockCode {
				t.Fatalf("expected block %s, got %v", tc.blockCode, err)
			}
			stored, err := service.Get(ctx, created.ID)
			if err != nil {
				t.Fatalf("reload blocked draft: %v", err)
			}
			if stored.Status != "draft" || stored.Version != created.Version {
				t.Fatalf("blocked submission must keep draft/version, got %s v%d", stored.Status, stored.Version)
			}
			if stored.EvidenceBlockCode != tc.blockCode || stored.EvidenceBlockReason == "" {
				t.Fatalf("block reason must survive refresh, got code=%q reason=%q", stored.EvidenceBlockCode, stored.EvidenceBlockReason)
			}
			if stored.EvidenceConsistent == nil || *stored.EvidenceConsistent {
				t.Fatalf("blocked draft must report inconsistent evidence")
			}
		})
	}
}

func TestApprovalRejectedWhenEvidenceVersionChanged(t *testing.T) {
	fixture := newEvidenceFixture(t, "REL-DRIFT-I", "passed", 1, "valid", 1)
	draft, _ := fixture.service.Get(fixture.ctx, fixture.authID)
	review, err := fixture.submit(t, draft.Version)
	if err != nil {
		t.Fatalf("submit review: %v", err)
	}

	if err := fixture.db.Model(&model.InspectionTask{}).Where("code = ?", fixture.inspCode).
		Updates(map[string]any{"version": 2, "status": "passed", "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatalf("drift inspection: %v", err)
	}
	approval := dto.TransitionRequest{Status: "approved", ExpectedVersion: review.Version, Reason: "approval after drift must fail"}
	_, err = fixture.service.Transition(fixture.ctx, fixture.authID, approval, "reviewer", model.RoleReviewer, "auth-evidence-stale-approve")
	var block *EvidenceBlockError
	if !errors.As(err, &block) || block.Code != "INSPECTION_VERSION_CHANGED" {
		t.Fatalf("expected INSPECTION_VERSION_CHANGED block, got %v", err)
	}

	stored, err := fixture.service.Get(fixture.ctx, fixture.authID)
	if err != nil {
		t.Fatalf("reload review: %v", err)
	}
	if stored.Status != "review" || stored.Version != review.Version {
		t.Fatalf("rejected approval must keep review/version, got %s v%d", stored.Status, stored.Version)
	}
	if stored.EvidenceConsistent == nil || *stored.EvidenceConsistent {
		t.Fatalf("drifted evidence must be reported inconsistent")
	}
	if stored.CurrentInspectionVersion != 2 || stored.FrozenInspectionVersion != 1 {
		t.Fatalf("current/frozen versions must both be readable: %#v", stored)
	}
	if stored.EvidenceBlockCode != "INSPECTION_VERSION_CHANGED" {
		t.Fatalf("block code missing after refresh: %q", stored.EvidenceBlockCode)
	}

	// Reviewer sends it back; operator resubmits and freezes the new version; dual-control approval then works.
	returned, err := fixture.service.Transition(fixture.ctx, fixture.authID, dto.TransitionRequest{
		Status: "draft", ExpectedVersion: stored.Version, Reason: "evidence drifted, resubmit required",
	}, "reviewer", model.RoleReviewer, "auth-evidence-return")
	if err != nil {
		t.Fatalf("return to draft: %v", err)
	}
	if returned.FrozenInspectionCode != "" || returned.FrozenCertificateCode != "" {
		t.Fatalf("returned draft must clear frozen evidence: %#v", returned)
	}
	resubmitted, err := fixture.submit(t, returned.Version)
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if resubmitted.FrozenInspectionVersion != 2 {
		t.Fatalf("resubmission must freeze current inspection v2, got %d", resubmitted.FrozenInspectionVersion)
	}
	approved, err := fixture.service.Transition(fixture.ctx, fixture.authID, dto.TransitionRequest{
		Status: "approved", ExpectedVersion: resubmitted.Version, Reason: "independent review after resubmit",
	}, "reviewer", model.RoleReviewer, "auth-evidence-approve")
	if err != nil {
		t.Fatalf("approval after resubmit: %v", err)
	}
	if approved.Status != "approved" || approved.SubmittedBy != "operator" || approved.ReviewedBy != "reviewer" {
		t.Fatalf("unexpected approval: %#v", approved)
	}
}

func TestApprovalRejectedWhenCertificateVersionChanged(t *testing.T) {
	fixture := newEvidenceFixture(t, "REL-DRIFT-C", "passed", 1, "valid", 1)
	draft, _ := fixture.service.Get(fixture.ctx, fixture.authID)
	review, err := fixture.submit(t, draft.Version)
	if err != nil {
		t.Fatalf("submit review: %v", err)
	}
	if err := fixture.db.Model(&model.CertificateRecord{}).Where("code = ?", fixture.certCode).
		Updates(map[string]any{"version": 4, "status": "valid", "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatalf("drift certificate: %v", err)
	}
	_, err = fixture.service.Transition(fixture.ctx, fixture.authID, dto.TransitionRequest{
		Status: "approved", ExpectedVersion: review.Version, Reason: "approval after cert drift must fail",
	}, "reviewer", model.RoleReviewer, "auth-evidence-stale-cert")
	var block *EvidenceBlockError
	if !errors.As(err, &block) || block.Code != "CERTIFICATE_VERSION_CHANGED" {
		t.Fatalf("expected CERTIFICATE_VERSION_CHANGED block, got %v", err)
	}
}

func TestApprovalKeepsFrozenEvidenceWhenUnchanged(t *testing.T) {
	fixture := newEvidenceFixture(t, "REL-FROZEN", "passed", 5, "valid", 7)
	draft, _ := fixture.service.Get(fixture.ctx, fixture.authID)
	review, err := fixture.submit(t, draft.Version)
	if err != nil {
		t.Fatalf("submit review: %v", err)
	}
	approved, err := fixture.service.Transition(fixture.ctx, fixture.authID, dto.TransitionRequest{
		Status: "approved", ExpectedVersion: review.Version, Reason: "independent review passed",
	}, "reviewer", model.RoleReviewer, "auth-evidence-final")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected approved, got %s", approved.Status)
	}

	// Underlying evidence changes after approval; the authorization must retain its frozen proof.
	if err := fixture.db.Model(&model.InspectionTask{}).Where("code = ?", fixture.inspCode).
		Updates(map[string]any{"version": 6, "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatalf("post-approval inspection change: %v", err)
	}
	reloaded, err := fixture.service.Get(fixture.ctx, fixture.authID)
	if err != nil {
		t.Fatalf("reload approved: %v", err)
	}
	if reloaded.FrozenInspectionVersion != 5 || reloaded.FrozenCertificateVersion != 7 {
		t.Fatalf("approved record must keep frozen versions, got insp=%d cert=%d", reloaded.FrozenInspectionVersion, reloaded.FrozenCertificateVersion)
	}
	if reloaded.CurrentInspectionVersion != 6 {
		t.Fatalf("current inspection version should still be reported, got %d", reloaded.CurrentInspectionVersion)
	}
	if _, err := fixture.service.Update(fixture.ctx, fixture.authID, updateAuthorizationInput(reloaded), "operator", "auth-evidence-rewrite"); !errors.Is(err, ErrLocked) {
		t.Fatalf("approved evidence must not be rewritten, got %v", err)
	}
	final, _ := fixture.service.Get(fixture.ctx, fixture.authID)
	if final.FrozenInspectionVersion != 5 || final.FrozenCertificateVersion != 7 {
		t.Fatalf("frozen evidence changed after failed rewrite: %#v", final)
	}
}
