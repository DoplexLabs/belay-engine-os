package localapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const experienceApprovalPreviewLifetime = 15 * time.Minute

var (
	ErrExperienceApprovalInvalidRequest = errors.New(
		"experience approval request is invalid",
	)
	ErrExperienceApprovalExpired = errors.New(
		"experience approval preview has expired",
	)
)

type ExperienceApprovalServiceStore interface {
	ExperienceCandidateReviewStore
	IssueExperienceApprovalToken(
		experience.ApprovalTokenClaims,
	) (string, error)
	DecodeExperienceApprovalToken(
		string,
	) (experience.ApprovalTokenClaims, error)
	ApproveExperienceProposal(
		context.Context,
		local.ExperienceApprovalInput,
	) (local.ExperienceApprovalResult, error)
}

type ExperienceApprovalService struct {
	store ExperienceApprovalServiceStore
	now   func() time.Time
}

type ExperienceApprovalServiceOption func(*ExperienceApprovalService)

func WithExperienceApprovalClock(
	clock func() time.Time,
) ExperienceApprovalServiceOption {
	return func(service *ExperienceApprovalService) {
		if clock != nil {
			service.now = clock
		}
	}
}

type ExperienceApprovalPreview struct {
	Review      ExperienceCandidateReview `json:"review"`
	ActionToken string                    `json:"action_token"`
	ExpiresAt   time.Time                 `json:"expires_at"`
}

type ExperienceApprovalWithContentRequest struct {
	ProposalID      string                        `json:"proposal_id"`
	ActionToken     string                        `json:"action_token"`
	Actor           string                        `json:"actor"`
	Mode            experience.ApprovalMode       `json:"approval_mode"`
	ApprovedContent experience.ExperienceProposal `json:"approved_content"`
}

func NewExperienceApprovalService(
	store ExperienceApprovalServiceStore,
	options ...ExperienceApprovalServiceOption,
) (*ExperienceApprovalService, error) {
	if store == nil {
		return nil, errors.New(
			"experience approval service requires a store",
		)
	}
	service := &ExperienceApprovalService{
		store: store,
		now:   time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func (service *ExperienceApprovalService) Prepare(
	ctx context.Context,
	proposalID string,
) (ExperienceApprovalPreview, error) {
	review, err := GetExperienceCandidateReview(
		ctx,
		service.store,
		strings.TrimSpace(proposalID),
	)
	if err != nil {
		return ExperienceApprovalPreview{}, err
	}
	issuedAt := service.now().UTC().Round(0)
	expiresAt := issuedAt.Add(experienceApprovalPreviewLifetime)
	claims := experience.ApprovalTokenClaims{
		Version:             experience.ApprovalTokenVersion,
		CandidateID:         review.CandidateID,
		ProposalID:          review.ProposalID,
		ProjectIdentity:     review.ProjectIdentity,
		SemanticInputHash:   review.SemanticInputHash,
		ProposedContentHash: review.ProposedContentHash,
		EvidenceGeneration:  review.EvidenceGeneration,
		IssuedAt:            issuedAt,
		ExpiresAt:           expiresAt,
	}
	token, err := service.store.IssueExperienceApprovalToken(claims)
	if err != nil {
		return ExperienceApprovalPreview{}, mapExperienceApprovalError(err)
	}
	return ExperienceApprovalPreview{
		Review:      review,
		ActionToken: token,
		ExpiresAt:   expiresAt,
	}, nil
}

func (service *ExperienceApprovalService) ApproveAsProposed(
	ctx context.Context,
	proposalID string,
	actionToken string,
	approvedBy string,
) (local.ExperienceApprovalResult, error) {
	proposalID = strings.TrimSpace(proposalID)
	actionToken = strings.TrimSpace(actionToken)
	approvedBy = strings.TrimSpace(approvedBy)
	if proposalID == "" || actionToken == "" || approvedBy == "" {
		return local.ExperienceApprovalResult{},
			ErrExperienceApprovalInvalidRequest
	}
	claims, err := service.store.DecodeExperienceApprovalToken(actionToken)
	if err != nil {
		return local.ExperienceApprovalResult{},
			mapExperienceApprovalError(err)
	}
	if claims.ProposalID != proposalID {
		return local.ExperienceApprovalResult{},
			ErrExperienceApprovalInvalidRequest
	}
	result, err := service.store.ApproveExperienceProposal(
		ctx,
		local.ExperienceApprovalInput{
			Claims:     claims,
			ApprovedBy: approvedBy,
			ApprovedAt: service.now().UTC().Round(0),
			Mode:       experience.ApprovalAsProposed,
		},
	)
	if err != nil {
		return local.ExperienceApprovalResult{},
			mapExperienceApprovalError(err)
	}
	return result, nil
}

func (service *ExperienceApprovalService) ApproveWithContent(
	ctx context.Context,
	request ExperienceApprovalWithContentRequest,
) (local.ExperienceApprovalResult, error) {
	request.ProposalID = strings.TrimSpace(request.ProposalID)
	request.ActionToken = strings.TrimSpace(request.ActionToken)
	request.Actor = strings.TrimSpace(request.Actor)
	if request.ProposalID == "" ||
		request.ActionToken == "" ||
		request.Actor == "" ||
		(request.Mode != experience.ApprovalNarrowed &&
			request.Mode != experience.ApprovalUserEdited) {
		return local.ExperienceApprovalResult{},
			ErrExperienceApprovalInvalidRequest
	}
	claims, err := service.store.DecodeExperienceApprovalToken(
		request.ActionToken,
	)
	if err != nil {
		return local.ExperienceApprovalResult{},
			mapExperienceApprovalError(err)
	}
	if claims.ProposalID != request.ProposalID {
		return local.ExperienceApprovalResult{},
			ErrExperienceApprovalInvalidRequest
	}
	result, err := service.store.ApproveExperienceProposal(
		ctx,
		local.ExperienceApprovalInput{
			Claims:           claims,
			ApprovedBy:       request.Actor,
			ApprovedAt:       service.now().UTC().Round(0),
			Mode:             request.Mode,
			ApprovedProposal: request.ApprovedContent,
		},
	)
	if err != nil {
		return local.ExperienceApprovalResult{},
			mapExperienceApprovalError(err)
	}
	return result, nil
}

func mapExperienceApprovalError(err error) error {
	switch {
	case errors.Is(err, local.ErrExperienceApprovalTokenInvalid):
		return ErrExperienceApprovalInvalidRequest
	case errors.Is(err, local.ErrExperienceApprovalInvalidContent):
		return ErrExperienceApprovalInvalidRequest
	case errors.Is(err, local.ErrExperienceApprovalTokenExpired):
		return ErrExperienceApprovalExpired
	default:
		return err
	}
}

var _ ExperienceApprovalServiceStore = (*local.Store)(nil)
