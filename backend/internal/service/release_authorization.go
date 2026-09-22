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
	if len(page.Items) > 0 {
		if err := s.enrichEvidence(ctx, page.Items); err != nil {
			return repository.Page[model.ReleaseAuthorization]{}, err
		}
	}
	return page, nil
}

func (s *releaseAuthorizationService) Get(ctx context.Context, id uint) (model.ReleaseAuthorization, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ReleaseAuthorization{}, err
	}
	items := []model.ReleaseAuthorization{item}
	if err := s.enrichEvidence(ctx, items); err != nil {
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
	current.EvidenceBlockedReason = ""
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
		inspection, certificate, block, err := s.checkSubmitEvidence(ctx, current.RelatedCode)
		if err != nil {
			return model.ReleaseAuthorization{}, err
		}
		if block != nil {
			if persistErr := s.repository.ApplyEvidenceBlock(ctx, id, actor, requestID, current.Status, block.Reason); persistErr != nil {
				return model.ReleaseAuthorization{}, fmt.Errorf("persist evidence block: %w", persistErr)
			}
			return model.ReleaseAuthorization{}, block
		}
		current.FrozenInspectionCode = inspection.Code
		current.FrozenInspectionVersion = inspection.Version
		current.FrozenCertificateCode = certificate.Code
		current.FrozenCertificateVersion = certificate.Version
		current.EvidenceBlockedReason = ""
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
		if (target == "approved" || target == "restricted") && current.Status == "review" {
			block, err := s.checkFrozenEvidence(ctx, current)
			if err != nil {
				return model.ReleaseAuthorization{}, err
			}
			if block != nil {
				if persistErr := s.repository.ApplyEvidenceBlock(ctx, id, actor, requestID, current.Status, block.Reason); persistErr != nil {
					return model.ReleaseAuthorization{}, fmt.Errorf("persist evidence block: %w", persistErr)
				}
				return model.ReleaseAuthorization{}, block
			}
			current.EvidenceBlockedReason = ""
		}
		if target == "draft" {
			current.FrozenInspectionCode = ""
			current.FrozenInspectionVersion = 0
			current.FrozenCertificateCode = ""
			current.FrozenCertificateVersion = 0
			current.EvidenceBlockedReason = ""
		}
		current.ReviewedBy = actor
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

// checkSubmitEvidence locates the passed inspection task and valid certificate
// bound to relatedCode at submission time. A non-nil block means the draft must
// stay where it is and the blocker code should be reported to the operator.
func (s *releaseAuthorizationService) checkSubmitEvidence(ctx context.Context, relatedCode string) (model.InspectionTask, model.CertificateRecord, *EvidenceBlockError, error) {
	code := strings.ToUpper(strings.TrimSpace(relatedCode))
	if code == "" {
		return model.InspectionTask{}, model.CertificateRecord{}, submitEvidenceBlock(code, false, false), nil
	}
	passed, err := s.inspections.LatestPassedByRelatedCodes(ctx, []string{code})
	if err != nil {
		return model.InspectionTask{}, model.CertificateRecord{}, nil, fmt.Errorf("locate inspection evidence: %w", err)
	}
	valid, err := s.certificates.LatestValidByRelatedCodes(ctx, []string{code})
	if err != nil {
		return model.InspectionTask{}, model.CertificateRecord{}, nil, fmt.Errorf("locate certificate evidence: %w", err)
	}
	inspection, inspectionFound := passed[code]
	certificate, certificateFound := valid[code]
	if block := submitEvidenceBlock(code, inspectionFound, certificateFound); block != nil {
		return model.InspectionTask{}, model.CertificateRecord{}, block, nil
	}
	return inspection, certificate, nil, nil
}

// checkFrozenEvidence re-validates the evidence frozen at submission time. Any
// version or status change on either side forces the approval back to a
// re-submission instead of releasing drifted evidence.
func (s *releaseAuthorizationService) checkFrozenEvidence(ctx context.Context, current model.ReleaseAuthorization) (*EvidenceBlockError, error) {
	if current.FrozenInspectionCode == "" || current.FrozenCertificateCode == "" {
		return &EvidenceBlockError{
			Code:   "EVIDENCE_NOT_FROZEN",
			Reason: fmt.Sprintf("放行 %s 的提交证据未冻结，请先退回草案再重新提交（阻断编号 EVIDENCE_NOT_FROZEN）", current.Code),
		}, nil
	}
	inspections, err := s.inspections.ListByCodes(ctx, []string{current.FrozenInspectionCode})
	if err != nil {
		return nil, fmt.Errorf("verify frozen inspection: %w", err)
	}
	certificates, err := s.certificates.ListByCodes(ctx, []string{current.FrozenCertificateCode})
	if err != nil {
		return nil, fmt.Errorf("verify frozen certificate: %w", err)
	}
	inspection, inspectionFound := inspections[current.FrozenInspectionCode]
	if block := inspectionDriftBlock(current, inspection, inspectionFound); block != nil {
		return block, nil
	}
	certificate, certificateFound := certificates[current.FrozenCertificateCode]
	if block := certificateDriftBlock(current, certificate, certificateFound); block != nil {
		return block, nil
	}
	return nil, nil
}

// enrichEvidence fills read-only evidence status on loaded records: drafts show
// whether evidence is currently available, submitted records show whether the
// frozen inspection/certificate versions are still the current ones.
func (s *releaseAuthorizationService) enrichEvidence(ctx context.Context, items []model.ReleaseAuthorization) error {
	draftCodes := make(map[string]struct{})
	inspectionCodes := make(map[string]struct{})
	certificateCodes := make(map[string]struct{})
	for _, item := range items {
		if item.FrozenInspectionCode != "" || item.FrozenCertificateCode != "" {
			if item.FrozenInspectionCode != "" {
				inspectionCodes[item.FrozenInspectionCode] = struct{}{}
			}
			if item.FrozenCertificateCode != "" {
				certificateCodes[item.FrozenCertificateCode] = struct{}{}
			}
		} else if strings.TrimSpace(item.RelatedCode) != "" {
			draftCodes[item.RelatedCode] = struct{}{}
		}
	}
	passedByRelated, err := s.inspections.LatestPassedByRelatedCodes(ctx, setKeys(draftCodes))
	if err != nil {
		return fmt.Errorf("load inspection evidence: %w", err)
	}
	validByRelated, err := s.certificates.LatestValidByRelatedCodes(ctx, setKeys(draftCodes))
	if err != nil {
		return fmt.Errorf("load certificate evidence: %w", err)
	}
	inspectionsByCode, err := s.inspections.ListByCodes(ctx, setKeys(inspectionCodes))
	if err != nil {
		return fmt.Errorf("load frozen inspections: %w", err)
	}
	certificatesByCode, err := s.certificates.ListByCodes(ctx, setKeys(certificateCodes))
	if err != nil {
		return fmt.Errorf("load frozen certificates: %w", err)
	}
	for i := range items {
		item := &items[i]
		if item.FrozenInspectionCode != "" || item.FrozenCertificateCode != "" {
			inspection, inspectionFound := inspectionsByCode[item.FrozenInspectionCode]
			certificate, certificateFound := certificatesByCode[item.FrozenCertificateCode]
			if inspectionFound {
				item.CurrentInspectionVersion, item.CurrentInspectionStatus = inspection.Version, inspection.Status
			}
			if certificateFound {
				item.CurrentCertificateVersion, item.CurrentCertificateStatus = certificate.Version, certificate.Status
			}
			consistent := inspectionFound && inspection.Status == "passed" && inspection.Version == item.FrozenInspectionVersion &&
				certificateFound && certificate.Status == "valid" && certificate.Version == item.FrozenCertificateVersion
			item.EvidenceConsistent = &consistent
			if !consistent {
				if block := inspectionDriftBlock(*item, inspection, inspectionFound); block != nil {
					item.EvidenceBlockCode, item.EvidenceBlockReason = block.Code, block.Reason
				} else if block := certificateDriftBlock(*item, certificate, certificateFound); block != nil {
					item.EvidenceBlockCode, item.EvidenceBlockReason = block.Code, block.Reason
				}
			}
			continue
		}
		inspection, inspectionFound := passedByRelated[item.RelatedCode]
		certificate, certificateFound := validByRelated[item.RelatedCode]
		if inspectionFound {
			item.CurrentInspectionVersion, item.CurrentInspectionStatus = inspection.Version, inspection.Status
		}
		if certificateFound {
			item.CurrentCertificateVersion, item.CurrentCertificateStatus = certificate.Version, certificate.Status
		}
		consistent := inspectionFound && certificateFound
		item.EvidenceConsistent = &consistent
		if !consistent {
			if block := submitEvidenceBlock(item.RelatedCode, inspectionFound, certificateFound); block != nil {
				item.EvidenceBlockCode, item.EvidenceBlockReason = block.Code, block.Reason
			}
		}
	}
	return nil
}

func submitEvidenceBlock(relatedCode string, inspectionFound, certificateFound bool) *EvidenceBlockError {
	code := strings.ToUpper(strings.TrimSpace(relatedCode))
	if code == "" {
		return &EvidenceBlockError{
			Code:   "EVIDENCE_LINK_MISSING",
			Reason: "未填写关联编号，无法查找已通过检查任务与有效证书（阻断编号 EVIDENCE_LINK_MISSING）",
		}
	}
	if !inspectionFound {
		return &EvidenceBlockError{
			Code:   "INSPECTION_NOT_PASSED",
			Reason: fmt.Sprintf("关联编号 %s 缺少状态为 passed 的检查任务，放行证据不完整（阻断编号 INSPECTION_NOT_PASSED）", code),
		}
	}
	if !certificateFound {
		return &EvidenceBlockError{
			Code:   "CERTIFICATE_NOT_VALID",
			Reason: fmt.Sprintf("关联编号 %s 缺少状态为 valid 的有效证书，放行证据不完整（阻断编号 CERTIFICATE_NOT_VALID）", code),
		}
	}
	return nil
}

func inspectionDriftBlock(item model.ReleaseAuthorization, inspection model.InspectionTask, found bool) *EvidenceBlockError {
	if found && inspection.Status == "passed" && inspection.Version == item.FrozenInspectionVersion {
		return nil
	}
	if !found {
		return &EvidenceBlockError{
			Code:   "INSPECTION_VERSION_CHANGED",
			Reason: fmt.Sprintf("检查任务 %s 在提交后已删除或无法找到，请退回草案重新提交（阻断编号 INSPECTION_VERSION_CHANGED）", item.FrozenInspectionCode),
		}
	}
	return &EvidenceBlockError{
		Code: "INSPECTION_VERSION_CHANGED",
		Reason: fmt.Sprintf("检查任务 %s 已变化（冻结 v%d/passed，当前 %s v%d），批准必须拒绝，请退回草案重新提交（阻断编号 INSPECTION_VERSION_CHANGED）",
			item.FrozenInspectionCode, item.FrozenInspectionVersion, inspection.Status, inspection.Version),
	}
}

func certificateDriftBlock(item model.ReleaseAuthorization, certificate model.CertificateRecord, found bool) *EvidenceBlockError {
	if found && certificate.Status == "valid" && certificate.Version == item.FrozenCertificateVersion {
		return nil
	}
	if !found {
		return &EvidenceBlockError{
			Code:   "CERTIFICATE_VERSION_CHANGED",
			Reason: fmt.Sprintf("证书 %s 在提交后已删除或无法找到，请退回草案重新提交（阻断编号 CERTIFICATE_VERSION_CHANGED）", item.FrozenCertificateCode),
		}
	}
	return &EvidenceBlockError{
		Code: "CERTIFICATE_VERSION_CHANGED",
		Reason: fmt.Sprintf("证书 %s 已变化（冻结 v%d/valid，当前 %s v%d），批准必须拒绝，请退回草案重新提交（阻断编号 CERTIFICATE_VERSION_CHANGED）",
			item.FrozenCertificateCode, item.FrozenCertificateVersion, certificate.Status, certificate.Version),
	}
}

func setKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
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
