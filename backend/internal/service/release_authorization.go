package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/constants"
	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/dto"
	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/model"
	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/repository"
)

type ReleaseAuthorizationService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ReleaseAuthorization], error)
	Get(context.Context, uint) (model.ReleaseAuthorization, error)
	Create(context.Context, dto.CreateReleaseAuthorization, string, string) (model.ReleaseAuthorization, error)
	Update(context.Context, uint, dto.UpdateReleaseAuthorization, string, string) (model.ReleaseAuthorization, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.ReleaseAuthorization, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type releaseAuthorizationService struct {
	repository   repository.ReleaseAuthorizationRepository
	inspections  repository.InspectionTaskRepository
	certificates repository.CertificateRecordRepository
	security     SecurityService
}

func NewReleaseAuthorizationService(repo repository.ReleaseAuthorizationRepository, inspections repository.InspectionTaskRepository, certificates repository.CertificateRecordRepository, security SecurityService) ReleaseAuthorizationService {
	return &releaseAuthorizationService{repository: repo, inspections: inspections, certificates: certificates, security: security}
}

func (s *releaseAuthorizationService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ReleaseAuthorization], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	if err := s.attachEvidenceStatuses(ctx, page.Items); err != nil {
		return page, err
	}
	return page, nil
}

func (s *releaseAuthorizationService) Get(ctx context.Context, id uint) (model.ReleaseAuthorization, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseAuthorization{}, err
	}
	items := []model.ReleaseAuthorization{item}
	if err := s.attachEvidenceStatuses(ctx, items); err != nil {
		return model.ReleaseAuthorization{}, err
	}
	return items[0], nil
}

func (s *releaseAuthorizationService) Create(ctx context.Context, input dto.CreateReleaseAuthorization, actor, requestID string) (model.ReleaseAuthorization, error) {
	if err := validateReleaseAuthorizationBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ReleaseAuthorization{}, err
	}
	item := model.ReleaseAuthorization{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.ReleaseAuthorizationInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	if err := s.repository.CreateVersion(ctx, &item, actor, requestID); err != nil {
		return model.ReleaseAuthorization{}, fmt.Errorf("create 放行授权: %w", err)
	}
	return s.Get(ctx, item.ID)
}

func (s *releaseAuthorizationService) Update(ctx context.Context, id uint, input dto.UpdateReleaseAuthorization, actor, requestID string) (model.ReleaseAuthorization, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseAuthorization{}, err
	}
	if current.Status != model.ReleaseAuthorizationInitialStatus {
		return model.ReleaseAuthorization{}, ErrLocked
	}
	if err := validateReleaseAuthorizationBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ReleaseAuthorization{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersion(ctx, id, input.ExpectedVersion, &current, actor, requestID, "update", current.Status, "draft authorization fields updated"); err != nil {
		return model.ReleaseAuthorization{}, fmt.Errorf("update 放行授权: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *releaseAuthorizationService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.ReleaseAuthorization, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseAuthorization{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.ReleaseAuthorizationTransitions, current.Status, target) {
		return model.ReleaseAuthorization{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	if !isOperatorRole(role) {
		return model.ReleaseAuthorization{}, ErrForbidden
	}
	if target == "review" {
		// 提交复核：按关联编号冻结已通过的检查任务和有效证书；任一缺失或
		// 状态不合则保持草案并返回阻断编号。
		inspection, certificate, validationErr := s.freezeEvidence(ctx, current.RelatedCode)
		if validationErr != nil {
			return model.ReleaseAuthorization{}, validationErr
		}
		current.FrozenInspectionCode = inspection.Code
		current.FrozenInspectionVersion = inspection.Version
		current.FrozenCertificateCode = certificate.Code
		current.FrozenCertificateVersion = certificate.Version
		current.SubmittedBy = actor
		current.ReviewedBy = ""
		current.ReviewReason = ""
	}
	if target == "approved" || target == "restricted" || target == "revoked" || target == "draft" {
		if !isReviewerRole(role) {
			return model.ReleaseAuthorization{}, ErrForbidden
		}
		if current.SubmittedBy != "" && actor == current.SubmittedBy {
			return model.ReleaseAuthorization{}, ErrSeparationOfDuty
		}
		// 批准（或受限批准）前复核证据冻结：提交后检查任务或证书版本变化
		// 必须拒绝并要求重新提交；未变化则保持双人复核批准。
		if target == "approved" || target == "restricted" {
			if driftErr := s.checkEvidenceDrift(ctx, current); driftErr != nil {
				return model.ReleaseAuthorization{}, driftErr
			}
		}
		if target == "draft" {
			// 退回草案后旧冻结失效，重新提交时会重新冻结证据。
			current.FrozenInspectionCode = ""
			current.FrozenInspectionVersion = 0
			current.FrozenCertificateCode = ""
			current.FrozenCertificateVersion = 0
			current.ReviewedBy = ""
		} else {
			current.ReviewedBy = actor
		}
		current.ReviewReason = strings.TrimSpace(input.Reason)
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersion(ctx, id, input.ExpectedVersion, &current, actor, requestID, "transition", before, strings.TrimSpace(input.Reason)); err != nil {
		return model.ReleaseAuthorization{}, fmt.Errorf("transition 放行授权: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *releaseAuthorizationService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != model.ReleaseAuthorizationInitialStatus {
		return ErrLocked
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "ReleaseAuthorization", id, current.Status, "deleted", "soft deleted 放行授权")
}

func (s *releaseAuthorizationService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

// freezeEvidence resolves the latest inspection task and certificate for the
// 关联编号 and requires the task to be passed and the certificate to be valid.
// Any failure returns a blocked error carrying a stable 阻断编号 and leaves
// the draft untouched.
func (s *releaseAuthorizationService) freezeEvidence(ctx context.Context, relatedCode string) (model.InspectionTask, model.CertificateRecord, error) {
	relatedCode = strings.ToUpper(strings.TrimSpace(relatedCode))
	if relatedCode == "" {
		return model.InspectionTask{}, model.CertificateRecord{}, evidenceBlocked("EVIDENCE-RELATED-CODE", "关联编号为空，无法冻结检查任务与证书证据")
	}
	inspections, err := s.inspections.LatestByRelatedCodes(ctx, []string{relatedCode})
	if err != nil {
		return model.InspectionTask{}, model.CertificateRecord{}, fmt.Errorf("lookup inspection evidence: %w", err)
	}
	inspection, hasInspection := inspections[relatedCode]
	if !hasInspection {
		return model.InspectionTask{}, model.CertificateRecord{}, evidenceBlocked("EVIDENCE-INSPECTION-MISSING", fmt.Sprintf("关联编号 %s 未找到检查任务（%s）", relatedCode, "EVIDENCE-INSPECTION-MISSING"))
	}
	if inspection.Status != "passed" {
		return model.InspectionTask{}, model.CertificateRecord{}, evidenceBlocked("EVIDENCE-INSPECTION-STATUS", fmt.Sprintf("关联编号 %s 的检查任务 %s 状态为 %s，要求 passed（%s）", relatedCode, inspection.Code, inspection.Status, "EVIDENCE-INSPECTION-STATUS"))
	}
	certificates, err := s.certificates.LatestByRelatedCodes(ctx, []string{relatedCode})
	if err != nil {
		return model.InspectionTask{}, model.CertificateRecord{}, fmt.Errorf("lookup certificate evidence: %w", err)
	}
	certificate, hasCertificate := certificates[relatedCode]
	if !hasCertificate {
		return model.InspectionTask{}, model.CertificateRecord{}, evidenceBlocked("EVIDENCE-CERTIFICATE-MISSING", fmt.Sprintf("关联编号 %s 未找到有效证书（%s）", relatedCode, "EVIDENCE-CERTIFICATE-MISSING"))
	}
	if certificate.Status != "valid" {
		return model.InspectionTask{}, model.CertificateRecord{}, evidenceBlocked("EVIDENCE-CERTIFICATE-STATUS", fmt.Sprintf("关联编号 %s 的证书 %s 状态为 %s，要求 valid（%s）", relatedCode, certificate.Code, certificate.Status, "EVIDENCE-CERTIFICATE-STATUS"))
	}
	return inspection, certificate, nil
}

// checkEvidenceDrift re-resolves both evidence ends at approval time and
// compares them with the frozen versions. Any missing, re-pointed or changed
// version rejects the approval and asks for a fresh submission.
func (s *releaseAuthorizationService) checkEvidenceDrift(ctx context.Context, current model.ReleaseAuthorization) error {
	relatedCode := current.RelatedCode
	inspections, err := s.inspections.LatestByRelatedCodes(ctx, []string{relatedCode})
	if err != nil {
		return fmt.Errorf("lookup inspection evidence: %w", err)
	}
	inspection, hasInspection := inspections[relatedCode]
	if !hasInspection {
		return evidenceDrifted("DRIFT-INSPECTION-MISSING", fmt.Sprintf("提交时冻结的检查任务 %s 已不存在，证据版本变化，需重新提交（%s）", current.FrozenInspectionCode, "DRIFT-INSPECTION-MISSING"))
	}
	if inspection.Code != current.FrozenInspectionCode || inspection.Version != current.FrozenInspectionVersion {
		return evidenceDrifted("DRIFT-INSPECTION-VERSION", fmt.Sprintf("检查任务版本已变化：冻结 %s v%d，当前 %s v%d，需重新提交（%s）", current.FrozenInspectionCode, current.FrozenInspectionVersion, inspection.Code, inspection.Version, "DRIFT-INSPECTION-VERSION"))
	}
	certificates, err := s.certificates.LatestByRelatedCodes(ctx, []string{relatedCode})
	if err != nil {
		return fmt.Errorf("lookup certificate evidence: %w", err)
	}
	certificate, hasCertificate := certificates[relatedCode]
	if !hasCertificate {
		return evidenceDrifted("DRIFT-CERTIFICATE-MISSING", fmt.Sprintf("提交时冻结的证书 %s 已不存在，证据版本变化，需重新提交（%s）", current.FrozenCertificateCode, "DRIFT-CERTIFICATE-MISSING"))
	}
	if certificate.Code != current.FrozenCertificateCode || certificate.Version != current.FrozenCertificateVersion {
		return evidenceDrifted("DRIFT-CERTIFICATE-VERSION", fmt.Sprintf("证书版本已变化：冻结 %s v%d，当前 %s v%d，需重新提交（%s）", current.FrozenCertificateCode, current.FrozenCertificateVersion, certificate.Code, certificate.Version, "DRIFT-CERTIFICATE-VERSION"))
	}
	return nil
}

// attachEvidenceStatuses batch-resolves current evidence for a page of
// authorizations so the release page can show related versions, consistency
// and the blocking reason without an N+1 query.
func (s *releaseAuthorizationService) attachEvidenceStatuses(ctx context.Context, items []model.ReleaseAuthorization) error {
	if len(items) == 0 {
		return nil
	}
	codes := make([]string, 0, len(items))
	for _, item := range items {
		if code := strings.ToUpper(strings.TrimSpace(item.RelatedCode)); code != "" {
			codes = append(codes, code)
		}
	}
	inspections := map[string]model.InspectionTask{}
	certificates := map[string]model.CertificateRecord{}
	if len(codes) > 0 {
		var err error
		if inspections, err = s.inspections.LatestByRelatedCodes(ctx, codes); err != nil {
			return err
		}
		if certificates, err = s.certificates.LatestByRelatedCodes(ctx, codes); err != nil {
			return err
		}
	}
	for index := range items {
		items[index].EvidenceStatus = buildEvidenceStatus(&items[index], inspections, certificates)
	}
	return nil
}

// buildEvidenceStatus evaluates one authorization against the resolved
// evidence maps. Drafts run the submission gate; frozen records run the
// version comparison.
func buildEvidenceStatus(item *model.ReleaseAuthorization, inspections map[string]model.InspectionTask, certificates map[string]model.CertificateRecord) *model.EvidenceFreezeStatus {
	relatedCode := strings.ToUpper(strings.TrimSpace(item.RelatedCode))
	status := &model.EvidenceFreezeStatus{RelatedCode: relatedCode, Consistent: true}
	if relatedCode == "" {
		status.Consistent = false
		status.BlockCode = "EVIDENCE-RELATED-CODE"
		status.BlockReason = "关联编号为空，无法冻结检查任务与证书证据"
		return status
	}
	inspection, hasInspection := inspections[relatedCode]
	if hasInspection {
		status.InspectionCode = inspection.Code
		status.InspectionStatus = inspection.Status
		status.CurrentInspectionVersion = inspection.Version
	}
	certificate, hasCertificate := certificates[relatedCode]
	if hasCertificate {
		status.CertificateCode = certificate.Code
		status.CertificateStatus = certificate.Status
		status.CurrentCertificateVersion = certificate.Version
	}

	if item.Status == model.ReleaseAuthorizationInitialStatus {
		status.FrozenInspectionVersion = 0
		status.FrozenCertificateVersion = 0
		switch {
		case !hasInspection:
			status.Consistent = false
			status.BlockCode = "EVIDENCE-INSPECTION-MISSING"
			status.BlockReason = "关联编号 " + relatedCode + " 未找到检查任务，提交将保持草案"
		case inspection.Status != "passed":
			status.Consistent = false
			status.BlockCode = "EVIDENCE-INSPECTION-STATUS"
			status.BlockReason = "检查任务 " + inspection.Code + " 状态为 " + inspection.Status + "，要求 passed"
		case !hasCertificate:
			status.Consistent = false
			status.BlockCode = "EVIDENCE-CERTIFICATE-MISSING"
			status.BlockReason = "关联编号 " + relatedCode + " 未找到有效证书，提交将保持草案"
		case certificate.Status != "valid":
			status.Consistent = false
			status.BlockCode = "EVIDENCE-CERTIFICATE-STATUS"
			status.BlockReason = "证书 " + certificate.Code + " 状态为 " + certificate.Status + "，要求 valid"
		}
		return status
	}

	status.FrozenInspectionVersion = item.FrozenInspectionVersion
	status.FrozenCertificateVersion = item.FrozenCertificateVersion
	switch {
	case !hasInspection:
		status.Consistent = false
		status.BlockCode = "DRIFT-INSPECTION-MISSING"
		status.BlockReason = "冻结的检查任务 " + item.FrozenInspectionCode + " 已不存在，需重新提交"
	case inspection.Code != item.FrozenInspectionCode || inspection.Version != item.FrozenInspectionVersion:
		status.Consistent = false
		status.BlockCode = "DRIFT-INSPECTION-VERSION"
		status.BlockReason = fmt.Sprintf("检查任务版本不一致：冻结 %s v%d，当前 %s v%d", item.FrozenInspectionCode, item.FrozenInspectionVersion, inspection.Code, inspection.Version)
	case !hasCertificate:
		status.Consistent = false
		status.BlockCode = "DRIFT-CERTIFICATE-MISSING"
		status.BlockReason = "冻结的证书 " + item.FrozenCertificateCode + " 已不存在，需重新提交"
	case certificate.Code != item.FrozenCertificateCode || certificate.Version != item.FrozenCertificateVersion:
		status.Consistent = false
		status.BlockCode = "DRIFT-CERTIFICATE-VERSION"
		status.BlockReason = fmt.Sprintf("证书版本不一致：冻结 %s v%d，当前 %s v%d", item.FrozenCertificateCode, item.FrozenCertificateVersion, certificate.Code, certificate.Version)
	}
	return status
}

func validateReleaseAuthorizationBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}

func isOperatorRole(role string) bool {
	return role == model.RoleOperator || role == model.RoleReviewer || role == model.RoleAdmin
}

func isReviewerRole(role string) bool {
	return role == model.RoleReviewer || role == model.RoleAdmin
}
