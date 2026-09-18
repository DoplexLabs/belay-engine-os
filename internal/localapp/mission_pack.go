package localapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
)

const (
	missionPackServiceRequestTimeout = 5 * time.Second
	missionPackGitTimeout            = time.Second
	missionPackGitOutputBytes        = 4 << 10
	maxMissionPackCWDBytes           = 4096
	maxMissionPackIssueBytes         = 512
	maxMissionPackTaskBytes          = 1120
	maxMissionPackTaskRunes          = 280
	missionPackPreviewLifetime       = 10 * time.Minute
)

type MissionPackRepository interface {
	ResolveMissionPackProject(
		context.Context,
		missionpack.ProjectSelector,
	) (missionpack.ResolvedProject, error)
	ReadMissionPackEvidence(
		context.Context,
		string,
		missionpack.Limits,
	) (missionpack.EvidenceSnapshot, error)
}

type MissionPackExperienceSelector interface {
	Select(
		context.Context,
		ExperienceSelectionRequest,
	) (ExperienceSelectionResult, error)
}

type MissionPackPreviewRepository interface {
	RegisterMissionPackPreview(
		context.Context,
		string,
		string,
		experience.Harness,
		int64,
		[]experience.ExperienceRef,
		string,
		time.Time,
		time.Time,
	) error
}

type MissionPackService struct {
	repository         MissionPackRepository
	experienceSelector MissionPackExperienceSelector
	previewRepository  MissionPackPreviewRepository
	now                func() time.Time
}

type MissionPackServiceOption func(*MissionPackService)

func WithMissionPackExperienceSelector(
	selector MissionPackExperienceSelector,
) MissionPackServiceOption {
	return func(service *MissionPackService) {
		service.experienceSelector = selector
	}
}

func WithMissionPackPreviewRepository(
	repository MissionPackPreviewRepository,
) MissionPackServiceOption {
	return func(service *MissionPackService) {
		service.previewRepository = repository
	}
}

func NewMissionPackService(
	store MissionPackRepository,
	options ...MissionPackServiceOption,
) (*MissionPackService, error) {
	if store == nil {
		return nil, errors.New("Mission Pack service requires a store")
	}
	service := &MissionPackService{
		repository: store,
		now:        time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func (s *MissionPackService) Generate(
	ctx context.Context,
	request missionpack.Request,
) (missionpack.Pack, error) {
	if ctx == nil {
		return missionpack.Pack{}, errors.New(
			"Mission Pack request requires context",
		)
	}
	request, err := validateMissionPackRequest(request)
	if err != nil {
		return missionpack.Pack{}, err
	}
	runCtx, cancel := context.WithTimeout(
		ctx,
		missionPackServiceRequestTimeout,
	)
	defer cancel()

	var issueProject *missionpack.ResolvedProject
	if request.IssueID != "" {
		resolved, err := s.repository.ResolveMissionPackProject(
			runCtx,
			missionpack.ProjectSelector{IssueID: request.IssueID},
		)
		if err != nil {
			return missionpack.Pack{}, err
		}
		issueProject = &resolved
	}

	var workspace missionPackWorkspace
	var cwdProject *missionpack.ResolvedProject
	if request.CWD != "" {
		workspace = observeMissionPackWorkspace(runCtx, request.CWD)
		resolved, err := s.repository.ResolveMissionPackProject(
			runCtx,
			missionpack.ProjectSelector{
				RemoteIdentity: workspace.remote,
				ProjectRoot:    workspace.root,
				ProjectPath:    workspace.path,
			},
		)
		if err != nil {
			return missionpack.Pack{}, err
		}
		cwdProject = &resolved
	}
	if issueProject != nil && cwdProject != nil &&
		issueProject.Identity != cwdProject.Identity {
		return missionpack.Pack{}, missionpack.ErrProjectMismatch
	}
	if err := runCtx.Err(); err != nil {
		return missionpack.Pack{}, err
	}

	resolved := issueProject
	if cwdProject != nil {
		resolved = cwdProject
	}
	if resolved == nil {
		return missionpack.Pack{}, errors.New(
			"Mission Pack request requires cwd or issue",
		)
	}
	if request.CWD == "" {
		if !filepath.IsAbs(resolved.Path) {
			return missionpack.Pack{}, missionpack.ErrProjectNotFound
		}
		workspace = observeMissionPackWorkspace(runCtx, resolved.Path)
		if workspace.remote != "" &&
			(resolved.IdentityKind != "remote" ||
				workspace.remote != resolved.Identity) {
			return missionpack.Pack{}, missionpack.ErrProjectNotFound
		}
	}
	if workspace.root == "" {
		workspace.root = workspace.path
	}
	configPath := workspace.root
	if !filepath.IsAbs(configPath) {
		configPath = resolved.Path
	}

	experienceGeneration, experiences, err :=
		s.selectMissionPackExperiences(runCtx, *resolved, request)
	if err != nil {
		return missionpack.Pack{}, err
	}

	evidence, err := s.repository.ReadMissionPackEvidence(
		runCtx,
		resolved.Identity,
		missionpack.Limits{
			IssueID:      request.IssueID,
			Issues:       10,
			Sessions:     20,
			CommandTurns: 500,
			Events:       500,
		},
	)
	if err != nil {
		return missionpack.Pack{}, err
	}
	if err := runCtx.Err(); err != nil {
		return missionpack.Pack{}, err
	}
	projectFiles := discoverVerificationCommands(configPath)
	config := issueintel.ProjectConfig{
		VerificationCommands: make(
			[]string,
			0,
			len(projectFiles),
		),
	}
	for _, command := range projectFiles {
		config.VerificationCommands = append(
			config.VerificationCommands,
			command.Command,
		)
	}
	commands := observedMissionPackCommands(
		evidence.SuccessfulCommands,
		config,
	)
	resolved.Label = missionPackProjectLabel(
		resolved.Identity,
		configPath,
	)
	resolved.Path = configPath
	request.GeneratedAt = s.now().UTC()
	pack, err := missionpack.Build(missionpack.BuildInput{
		Request: request,
		Project: *resolved,
		Workspace: missionpack.WorkspaceSnapshot{
			Branch:    workspace.branch,
			Worktree:  missionPackWorktreeLabel(configPath),
			Harnesses: missionPackHarnesses(evidence.Sessions),
		},
		SourceState:          evidence.SourceState,
		Issues:               evidence.Issues,
		Insight:              evidence.Insight,
		Candidates:           evidence.Candidates,
		Commands:             commands,
		ProjectFiles:         projectFiles,
		Facts:                evidence.Facts,
		Coverage:             evidence.Coverage,
		InsightStale:         evidence.InsightStale,
		ExperienceGeneration: experienceGeneration,
		Experiences:          experiences,
	})
	if err != nil {
		return missionpack.Pack{}, err
	}
	if len(pack.Experiences) > 0 && s.previewRepository != nil {
		if err := s.registerMissionPackPreview(
			runCtx,
			*resolved,
			request,
			pack,
		); err != nil {
			return missionpack.Pack{}, err
		}
	}
	return pack, nil
}

func (s *MissionPackService) registerMissionPackPreview(
	ctx context.Context,
	project missionpack.ResolvedProject,
	request missionpack.Request,
	pack missionpack.Pack,
) error {
	refs := make(
		[]experience.ExperienceRef,
		0,
		len(pack.Experiences),
	)
	for _, item := range pack.Experiences {
		refs = append(refs, experience.ExperienceRef{
			ExperienceID: item.ExperienceID,
			Version:      item.Version,
		})
	}
	taskHintSum := sha256.Sum256([]byte(request.TaskHint))
	err := s.previewRepository.RegisterMissionPackPreview(
		ctx,
		pack.PackID,
		project.Identity,
		experience.Harness(pack.Harness),
		pack.ExperienceGeneration,
		refs,
		"sha256:"+hex.EncodeToString(taskHintSum[:]),
		pack.GeneratedAt,
		pack.GeneratedAt.Add(missionPackPreviewLifetime),
	)
	if err != nil {
		return fmt.Errorf("register Mission Pack acceptance preview: %w", err)
	}
	return nil
}

func (s *MissionPackService) selectMissionPackExperiences(
	ctx context.Context,
	project missionpack.ResolvedProject,
	request missionpack.Request,
) (int64, []missionpack.ExperienceItem, error) {
	if s.experienceSelector == nil {
		return 0, nil, nil
	}
	selection, err := s.experienceSelector.Select(
		ctx,
		ExperienceSelectionRequest{
			ProjectIdentity: strings.TrimSpace(project.Identity),
			Harness: experience.Harness(
				strings.TrimSpace(string(request.Harness)),
			),
			TaskFamily: string(request.Intent),
			TaskHint: strings.Join(
				strings.Fields(request.TaskHint),
				" ",
			),
			RepositoryPaths: make([]string, 0),
		},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	if len(selection.Selected) == 0 {
		return 0, nil, nil
	}
	items := make(
		[]missionpack.ExperienceItem,
		0,
		len(selection.Selected),
	)
	for _, selected := range selection.Selected {
		item, err := missionPackExperienceItem(selected.Experience)
		if err != nil {
			return 0, nil, err
		}
		items = append(items, item)
	}
	return selection.Generation.Generation, items, nil
}

func missionPackExperienceItem(
	value experience.Experience,
) (missionpack.ExperienceItem, error) {
	verifier, err := missionPackVerifierSummary(value.Verifier)
	if err != nil {
		return missionpack.ExperienceItem{}, err
	}
	return missionpack.ExperienceItem{
		ExperienceID:  value.ExperienceID,
		Version:       value.Version,
		Type:          string(value.Type),
		Guidance:      value.Guidance.Instruction,
		Applicability: value.Applicability.SemanticDescription,
		Exceptions:    append([]string(nil), value.Guidance.Exceptions...),
		Rationale:     value.Guidance.Rationale,
		Verifier:      verifier,
		Authority:     "user_approved",
		Sources: missionPackExperienceSources(
			value.Provenance.SourceCandidateID,
			value.Evidence.Refs,
		),
	}, nil
}

func missionPackVerifierSummary(
	value experience.Verifier,
) (missionpack.VerifierSummary, error) {
	if err := value.Validate(); err != nil {
		return missionpack.VerifierSummary{}, fmt.Errorf(
			"map Mission Pack experience verifier: %w",
			err,
		)
	}
	var summary string
	switch value.Kind {
	case experience.VerifierCommandObserved:
		summary = fmt.Sprintf(
			"Run %s.",
			boundedMissionPackSummaryText(value.Command.Command, 280),
		)
	case experience.VerifierCommandSucceeded:
		summary = fmt.Sprintf(
			"Run %s successfully.",
			boundedMissionPackSummaryText(value.Command.Command, 260),
		)
	case experience.VerifierFileNotModified:
		summary = fmt.Sprintf(
			"Confirm %s was not modified.",
			boundedMissionPackSummaryText(value.File.Path, 260),
		)
	case experience.VerifierFileModified:
		summary = fmt.Sprintf(
			"Confirm %s was modified.",
			boundedMissionPackSummaryText(value.File.Path, 260),
		)
	case experience.VerifierPathPatternNotModified:
		summary = fmt.Sprintf(
			"No files matching %s changed.",
			boundedMissionPackSummaryText(
				strings.Join(value.PathPattern.Patterns, ", "),
				260,
			),
		)
	case experience.VerifierVerificationAfterLastEdit:
		if value.VerificationAfterLastEdit.RequireSuccess {
			summary = "Run verification successfully after the final edit."
		} else {
			summary = "Run verification after the final edit."
		}
	case experience.VerifierNoRepeatFailure:
		summary = fmt.Sprintf(
			"Do not repeat this failure: %s.",
			boundedMissionPackSummaryText(
				value.NoRepeatFailure.NormalizedPattern,
				250,
			),
		)
	case experience.VerifierUserCorrectionAbsent:
		summary = "Complete the task without needing another user correction."
	case experience.VerifierObservationOnly:
		summary = boundedMissionPackSummaryText(
			value.ObservationOnly.Explanation,
			300,
		)
	default:
		return missionpack.VerifierSummary{}, errors.New(
			"map Mission Pack experience verifier: unsupported kind",
		)
	}
	return missionpack.VerifierSummary{
		Kind:    string(value.Kind),
		Summary: boundedMissionPackSummaryText(summary, 300),
	}, nil
}

func boundedMissionPackSummaryText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

func missionPackExperienceSources(
	candidateID string,
	values []experience.EvidenceRef,
) []missionpack.SourceRef {
	sources := make([]missionpack.SourceRef, 0, len(values))
	for _, value := range values {
		source := missionpack.SourceRef{
			Kind:         string(value.Kind),
			CandidateID:  candidateID,
			SessionKey:   value.SessionKey,
			EventID:      value.EventID,
			ProjectFile:  value.Path,
			SourceSHA256: value.SHA256,
		}
		if value.TurnIndex != nil {
			turnIndex := *value.TurnIndex
			source.TurnIndex = &turnIndex
		}
		if value.OccurredAt != nil {
			observedAt := value.OccurredAt.UTC()
			source.ObservedAt = &observedAt
		}
		sources = append(sources, source)
	}
	sort.Slice(sources, func(first, second int) bool {
		return missionPackExperienceSourceKey(sources[first]) <
			missionPackExperienceSourceKey(sources[second])
	})
	if len(sources) > missionpack.MaxSourcesPerItem {
		sources = sources[:missionpack.MaxSourcesPerItem]
	}
	if sources == nil {
		return make([]missionpack.SourceRef, 0)
	}
	return sources
}

func missionPackExperienceSourceKey(value missionpack.SourceRef) string {
	turnIndex := int64(-1)
	if value.TurnIndex != nil {
		turnIndex = *value.TurnIndex
	}
	observedAt := ""
	if value.ObservedAt != nil {
		observedAt = value.ObservedAt.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join([]string{
		value.Kind,
		value.CandidateID,
		value.SessionKey,
		fmt.Sprintf("%d", turnIndex),
		value.EventID,
		value.ProjectFile,
		value.SourceSHA256,
		observedAt,
	}, "\x00")
}

func validateMissionPackRequest(
	request missionpack.Request,
) (missionpack.Request, error) {
	request.CWD = strings.TrimSpace(request.CWD)
	request.IssueID = strings.TrimSpace(request.IssueID)
	request.Harness = missionpack.Harness(
		strings.TrimSpace(string(request.Harness)),
	)
	request.TaskHint = strings.TrimSpace(request.TaskHint)
	if request.CWD == "" && request.IssueID == "" {
		return missionpack.Request{}, errors.New(
			"Mission Pack request requires cwd or issue",
		)
	}
	if request.CWD != "" {
		if len(request.CWD) > maxMissionPackCWDBytes ||
			!filepath.IsAbs(request.CWD) {
			return missionpack.Request{}, errors.New(
				"Mission Pack cwd must be an absolute path",
			)
		}
		request.CWD = filepath.Clean(request.CWD)
	}
	if len(request.IssueID) > maxMissionPackIssueBytes {
		return missionpack.Request{}, errors.New(
			"Mission Pack issue ID is too long",
		)
	}
	if !utf8.ValidString(request.TaskHint) ||
		len(request.TaskHint) > maxMissionPackTaskBytes ||
		utf8.RuneCountInString(request.TaskHint) > maxMissionPackTaskRunes {
		return missionpack.Request{}, errors.New(
			"Mission Pack task hint is too long",
		)
	}
	if request.Intent == "" {
		request.Intent = missionpack.IntentGeneral
	}
	switch request.Intent {
	case missionpack.IntentGeneral,
		missionpack.IntentDebug,
		missionpack.IntentImplement,
		missionpack.IntentRefactor,
		missionpack.IntentReview,
		missionpack.IntentRelease:
	default:
		return missionpack.Request{}, errors.New(
			"invalid Mission Pack intent",
		)
	}
	switch request.Harness {
	case "", missionpack.HarnessClaude, missionpack.HarnessCodex:
	default:
		return missionpack.Request{}, errors.New(
			"invalid Mission Pack harness",
		)
	}
	return request, nil
}

type missionPackWorkspace struct {
	path      string
	root      string
	branch    string
	remote    string
	commonDir string
}

func observeMissionPackWorkspace(
	ctx context.Context,
	cwd string,
) missionPackWorkspace {
	result := missionPackWorkspace{path: filepath.Clean(cwd)}
	if resolved, err := filepath.EvalSymlinks(result.path); err == nil {
		result.path = filepath.Clean(resolved)
	}
	result.root, _ = runMissionPackGit(
		ctx,
		result.path,
		"rev-parse",
		"--show-toplevel",
	)
	if result.root == "" {
		return result
	}
	result.root = filepath.Clean(result.root)
	result.branch, _ = runMissionPackGit(
		ctx,
		result.root,
		"branch",
		"--show-current",
	)
	remote, _ := runMissionPackGit(
		ctx,
		result.root,
		"config",
		"--get",
		"remote.origin.url",
	)
	result.remote = sanitizeGitRemote(remote)
	result.commonDir, _ = runMissionPackGit(
		ctx,
		result.root,
		"rev-parse",
		"--git-common-dir",
	)
	return result
}

func runMissionPackGit(
	ctx context.Context,
	cwd string,
	args ...string,
) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, missionPackGitTimeout)
	defer cancel()
	var stdout limitedBuffer
	stdout.limit = missionPackGitOutputBytes
	commandArgs := append([]string{"-C", cwd}, args...)
	command := exec.CommandContext(commandCtx, "git", commandArgs...)
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return "", err
	}
	if stdout.truncated {
		return "", errors.New("Git observation output exceeded limit")
	}
	return strings.TrimSpace(
		strings.ToValidUTF8(stdout.String(), "\uFFFD"),
	), nil
}

func observedMissionPackCommands(
	values []missionpack.SuccessfulCommand,
	config issueintel.ProjectConfig,
) []missionpack.ObservedCommand {
	type aggregate struct {
		command string
		class   string
		count   int
		last    time.Time
		sources []missionpack.SourceRef
	}
	merged := make(map[string]*aggregate)
	for _, value := range values {
		command := strings.TrimSpace(value.Command)
		if command == "" || utf8.RuneCountInString(command) > 256 ||
			strings.ContainsAny(command, "\r\n") ||
			!isCrispMissionPackObservedCommand(command) {
			continue
		}
		class, ok := transcriptissues.ClassifyVerificationCommand(
			command,
			config,
		)
		if !ok {
			continue
		}
		current := merged[command]
		if current == nil {
			current = &aggregate{command: command, class: class}
			merged[command] = current
		}
		current.count++
		if value.SucceededAt.After(current.last) {
			current.last = value.SucceededAt
		}
		if len(current.sources) < missionpack.MaxSourcesPerItem {
			current.sources = append(current.sources, value.Source)
		}
	}
	result := make([]missionpack.ObservedCommand, 0, len(merged))
	for _, value := range merged {
		result = append(result, missionpack.ObservedCommand{
			Command:      value.command,
			Class:        value.class,
			SuccessCount: value.count,
			LastSuccess:  value.last,
			Sources:      value.sources,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SuccessCount != result[j].SuccessCount {
			return result[i].SuccessCount > result[j].SuccessCount
		}
		if !result[i].LastSuccess.Equal(result[j].LastSuccess) {
			return result[i].LastSuccess.After(result[j].LastSuccess)
		}
		return result[i].Command < result[j].Command
	})
	return result
}

func isCrispMissionPackObservedCommand(command string) bool {
	return !strings.ContainsAny(command, ";|&<>`") &&
		!strings.Contains(command, "$(")
}

func missionPackHarnesses(
	sessions []missionpack.EvidenceSession,
) []string {
	set := make(map[string]bool)
	for _, session := range sessions {
		if harness := strings.TrimSpace(session.Harness); harness != "" {
			set[harness] = true
		}
	}
	result := make([]string, 0, len(set))
	for harness := range set {
		result = append(result, harness)
	}
	sort.Strings(result)
	return result
}

func missionPackProjectLabel(identity, projectPath string) string {
	value := strings.TrimSpace(identity)
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.Path
	} else if colon := strings.LastIndex(value, ":"); colon >= 0 &&
		!strings.Contains(value[colon+1:], "/") {
		value = value[colon+1:]
	}
	value = strings.TrimSuffix(strings.TrimRight(value, "/"), ".git")
	if base := filepath.Base(value); base != "." && base != "/" &&
		base != "" {
		return base
	}
	if base := filepath.Base(filepath.Clean(projectPath)); base != "." &&
		base != "/" && base != "" {
		return base
	}
	return "project"
}

func missionPackWorktreeLabel(projectPath string) string {
	if !filepath.IsAbs(projectPath) {
		return ""
	}
	value := filepath.Base(filepath.Clean(projectPath))
	if value == "." || value == "/" {
		return ""
	}
	return value
}
