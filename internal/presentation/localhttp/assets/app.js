(() => {
  "use strict";

  const pageLimits = Object.freeze({
    sessions: { initial: 40, step: 40, maximum: 100 },
    events: { initial: 100, step: 100, maximum: 500 },
    findings: { page: 20 },
    issues: { page: 20 },
    familyMembers: { page: 20 },
    occurrences: { page: 20 },
    fixMonitoring: { page: 20 },
    fixMonitoringDetail: { page: 20 },
    fixRecurrences: { page: 20 },
  });
  const mutationRequestDeadlineMilliseconds = 15_000;
  const initializationPollMilliseconds = 2_000;
  const transcriptPollMilliseconds = 2_000;
  const runtimeSchemaVersion = "belay.local-runtime.v1";
  const updateSchemaVersion = "belay.update-check.v1";
  const updateInstallCommand =
    "curl -fsSL https://getbelay.vercel.app/install | bash";
  const experienceCopy = Object.freeze({
    current: Object.freeze({
      navBrief: "Report",
      navAttention: "Patterns",
      navSessions: "Sessions",
      navHabits: "Habits",
      briefEyebrow: "Belay Report",
      briefLoading: "Preparing your report…",
      briefErrorTitle: "Report unavailable",
      briefErrorDetail:
        "Belay could not prepare the report. Patterns and Sessions remain available.",
      briefOpenAttention: "View all patterns",
      briefOpenSessions: "View sessions",
      briefRecentEmptyDetail:
        "Use /belay in Claude Code or $belay in Codex to review the top issue.",
      attentionAriaLabel: "Recurring patterns",
      attentionEyebrow: "Across your agent sessions",
      attentionHeading: "Patterns",
      attentionStatus: "Pattern status",
      attentionBackLabel: "Back to patterns",
      attentionEmptyDetail:
        "Refresh Patterns before relying on this result.",
      attentionFilterSummary: "Filter patterns",
      sessionsHeading: "Sessions",
    }),
    "value-first": Object.freeze({
      navBrief: "Report",
      navAttention: "Patterns",
      navSessions: "Sessions",
      navHabits: "Habits",
      briefEyebrow: "Belay Report",
      briefLoading: "Preparing your report…",
      briefErrorTitle: "Report unavailable",
      briefErrorDetail:
        "Belay could not prepare the report. Patterns and Sessions remain available.",
      briefOpenAttention: "View all patterns",
      briefOpenSessions: "View sessions",
      briefRecentEmptyDetail:
        "Use /belay in Claude Code or $belay in Codex to review the top issue.",
      attentionAriaLabel: "Recurring patterns",
      attentionEyebrow: "Across your agent sessions",
      attentionHeading: "Patterns",
      attentionStatus: "Pattern status",
      attentionBackLabel: "Back to patterns",
      attentionEmptyDetail:
        "Refresh Patterns before relying on this result.",
      attentionFilterSummary: "Filter patterns",
      sessionsHeading: "Sessions",
    }),
  });
  const explicitOutcomes = new Set(["succeeded", "failed", "interrupted"]);
  const issueCatalog = Object.freeze({
    "issue.explicit_command_failure": Object.freeze({
      title: "Command failed",
      explanation: "The agent reported that a command failed.",
      action: "Inspect failed command evidence",
    }),
    "issue.explicit_tool_failure": Object.freeze({
      title: "Tool call failed",
      explanation: "The agent reported that a tool call failed.",
      caveat:
        "Belay does not know why it failed or whether a later attempt succeeded.",
      action: "Inspect cited events",
    }),
    "issue.repeated_command_attempts": Object.freeze({
      title: "Command repeatedly attempted",
      explanation:
        "Belay recorded the same minimized command pattern several times close together.",
      action: "Inspect matching sessions",
      experimental: true,
    }),
    "issue.explicit_permission_denial": Object.freeze({
      title: "Permission denied",
      explanation: "The agent reported that a permission request was denied.",
      action: "Inspect permission evidence",
    }),
    "issue.verification_not_observed": Object.freeze({
      title: "No recognized verification command observed",
      explanation:
        "After a recorded file change, Belay did not see a test or verification command it recognizes before the session ended.",
      action: "Inspect verification evidence",
      evidenceGap: true,
    }),
    "issue.retained_verification_gap_after_changes": Object.freeze({
      title: "No recognized verification retained after changes",
      explanation:
        "Belay's retained evidence contains no recognized verification command after the final recorded file change and before the session ended.",
      caveat:
        "This does not show that verification did not occur; Belay only checks supported commands in retained activity.",
      action: "Inspect cited events",
      evidenceGap: true,
    }),
    "issue.unresolved_verification_failure_at_completion": Object.freeze({
      title: "Verification still failed at session end",
      explanation:
        "A verification command explicitly failed and no later successful verification was observed before session end.",
      action: "Inspect verification evidence",
    }),
    "issue.numbat_finding": Object.freeze({
      title: "Imported finding without an explanation",
      explanation:
        "Belay stored an imported finding, but no reviewed Belay explanation is available for it.",
      caveat:
        "Belay does not infer its meaning, impact, or a recommended action.",
      action: "Review the cited events",
    }),
  });
  const sourceSignalCatalog = Object.freeze({
    "numbat/tamper.guardrails_off": Object.freeze({
      title: "Fewer approval prompts enabled",
      explanation:
        "Belay recorded a setting that lets actions already permitted by the agent run without asking for approval each time.",
      caveat:
        "This setting may be intentional. The record does not show whether an action bypassed a prompt or caused harm.",
      action:
        "Review the current agent permission mode. If this was intentional, no change may be needed.",
    }),
  });
  const analysisQualifiers = Object.freeze({
    pending:
      "This result may be out of date while Belay analyzes the session again.",
    failed: "Belay could not refresh this result; it may be out of date.",
    truncated:
      "Only part of the session was analyzed; other findings may be missing.",
    unknown:
      "Belay cannot confirm whether this result is current or complete.",
  });
  const fixMonitoringCatalog = Object.freeze({
    matching_evidence_observed: Object.freeze({
      title: "Same finding observed later",
      detail:
        "This does not establish causality or whether the attempted change worked.",
      tone: "attention",
    }),
    monitoring_incomplete: Object.freeze({
      title: "Monitoring is incomplete.",
      detail:
        "Some later Local activity is pending, failed, truncated, or only partially analyzed.",
      tone: "pending",
    }),
    awaiting_later_evidence: Object.freeze({
      title: "Waiting for later sessions",
      detail: "No comparable completed Local activity is available yet.",
      tone: "neutral",
    }),
    no_later_match_observed: Object.freeze({
      title:
        "No later matching evidence was observed in completed Local analysis.",
      detail: "This does not verify resolution.",
      tone: "neutral",
    }),
    comparison_unavailable: Object.freeze({
      title: "Later sessions cannot be compared",
      detail:
        "Belay cannot compare this attempt with later activity under the current rules.",
      tone: "neutral",
    }),
    retracted: Object.freeze({
      title: "Attempt record retracted — no longer monitored.",
      detail: "",
      tone: "neutral",
    }),
    unknown: Object.freeze({
      title: "Monitoring status unavailable.",
      detail: "",
      tone: "neutral",
    }),
  });
  const canonicalUUIDv7Pattern =
    /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
  const fixChangeCatalog = Object.freeze([
    Object.freeze({
      value: "code_change",
      label: "Code change",
      description: "Source or test code changed.",
    }),
    Object.freeze({
      value: "configuration_change",
      label: "Configuration change",
      description: "Project/application configuration changed.",
    }),
    Object.freeze({
      value: "dependency_change",
      label: "Dependency change",
      description: "Dependency version or lock state changed.",
    }),
    Object.freeze({
      value: "permission_change",
      label: "Permission change",
      description: "Access or permission configuration changed.",
    }),
    Object.freeze({
      value: "environment_change",
      label: "Environment change",
      description: "Local runtime, toolchain, or environment changed.",
    }),
    Object.freeze({
      value: "agent_instruction",
      label: "Agent instruction",
      description: "User-level agent instruction changed.",
    }),
    Object.freeze({
      value: "project_rule",
      label: "Project rule",
      description: "Repository/project agent rule changed.",
    }),
    Object.freeze({
      value: "monitor_hook",
      label: "Agent monitoring setup",
      description: "Agent monitoring hook/configuration changed.",
    }),
    Object.freeze({
      value: "other",
      label: "Other",
      description: "A deliberate category outside the listed choices.",
    }),
  ]);
  const fixRetractionCatalog = Object.freeze([
    Object.freeze({
      value: "recorded_by_mistake",
      label: "Recorded by mistake",
    }),
    Object.freeze({
      value: "superseded",
      label: "Replaced by another attempt record",
    }),
    Object.freeze({
      value: "other",
      label: "Other",
    }),
  ]);
  const fixEligibilityMessages = Object.freeze({
    analysis_not_current:
      "Recording is unavailable until Belay finishes updating this finding.",
    experimental_signal:
      "An attempt cannot be recorded for an experimental finding.",
    evidence_gap:
      "An attempt cannot be recorded for an evidence gap.",
    scope_unavailable:
      "Belay does not have enough project information to compare this finding with later sessions.",
  });
  const config = globalThis.BELAY_LOCAL_CONFIG || {};
  const state = {
    token: resolveToken(config),
    apiBase: normalizeApiBase(config.apiBase),
    experience: "current",
    activeView: "brief",
    initialization: null,
    initializationRequestInFlight: false,
    initializationPollGeneration: 0,
    initializationPollTimer: 0,
    transcriptStatus: null,
    transcriptStatusStale: false,
    transcriptRequestInFlight: false,
    transcriptPollGeneration: 0,
    transcriptPollTimer: 0,
    updateStatus: null,
    updatePollTimer: 0,
    developerBrief: null,
    briefStatus: "idle",
    briefError: "",
    briefRequestGeneration: 0,
    briefSelectionID: "",
    userInsights: null,
    reportHabits: null,
    reportHabitsStatus: "idle",
    reportHabitsRequestGeneration: 0,
    habitsStatus: "idle",
    habitsSelectedKey: "",
    habitsTab: "change",
    habitsOpenInsight: 0,
    habitsError: "",
    habitsRequestGeneration: 0,
    missionPackRequestGeneration: 0,
    missionPackCacheGeneration: 0,
    missionPackRequestController: null,
    missionPackIssue: null,
    missionPack: null,
    reportEvidenceIssueID: "",
    attentionMode: "issues",
    costIssues: [],
    costIssueStatus: "idle",
    costIssueError: "",
    costIssueRequestGeneration: 0,
    issues: createAttentionFamilyBucket(),
    evidenceGaps: createIssueBucket("evidence_gap"),
    issueFilters: {
      severity: "",
      harness: "",
      origin: "",
      analysisStatus: "",
      experimental: false,
    },
    selectedFamily: null,
    selectedFamilyViewCursor: "",
    familyMembers: [],
    familyMemberNextCursor: "",
    familyMemberHasMore: false,
    familyMemberStatus: "idle",
    familyMemberError: "",
    familyMemberRequestGeneration: 0,
    familyReturnFocus: null,
    fixMonitoring: createFixMonitoringBucket(),
    fixMonitoringFilters: {
      state: "",
      changeKind: "",
      severity: "",
      harness: "",
      issueID: "",
      recordedAfter: "",
      includeRetracted: false,
    },
    selectedIssueID: "",
    selectedIssueKind: "",
    selectedIssueSource: "",
    selectedIssueFamilyID: "",
    selectedIssueEvidenceContext: "",
    selectedDrivingAnnotationID: "",
    selectedIssue: null,
    selectedIssueCatalog: null,
    selectedGlobalAnalysisCoverage: null,
    selectedIssueViewCursor: "",
    occurrences: [],
    occurrenceNextCursor: "",
    occurrenceHasMore: false,
    occurrenceStatus: "idle",
    occurrenceRequestGeneration: 0,
    issueEvidencePreview: createIssueEvidencePreview(),
    issueEvidencePreviewRequestGeneration: 0,
    issueEvidencePreviewController: null,
    fixEligibility: null,
    fixEligibilityStatus: "idle",
    fixEligibilityRequestGeneration: 0,
    fixHistory: [],
    fixHistoryNextCursor: "",
    fixHistoryHasMore: false,
    fixHistoryStatus: "idle",
    fixHistoryRequestGeneration: 0,
    fixHistoryViewCursor: "",
    fixHistoryCurrentIssueAvailable: null,
    fixHistoryStale: false,
    fixHistoryError: null,
    fixEvidenceEvaluatedAt: "",
    fixActionMessage: "",
    fixActionTone: "status",
    activeModal: "",
    modalSubmitting: false,
    dialogReturnFocus: null,
    activeRetractionAnnotationID: "",
    attentionRefreshGeneration: 0,
    directSessionRequestGeneration: 0,
    issueReturnFocus: null,
    sessionReturnFocus: null,
    sessionReturnView: "",
    attentionExpiryRefresh: false,
    refreshNoticeTimer: 0,
    sessions: [],
    sessionsRequestGeneration: 0,
    sessionLimit: pageLimits.sessions.initial,
    sessionNextCursor: "",
    sessionHasMore: false,
    sessionServerScope: false,
    selectedSessionID: "",
    selectedEventTotal: 0,
    selectedSessionDetail: null,
    selectedOverview: null,
    selectedDiagnosis: null,
    events: [],
    eventLimit: pageLimits.events.initial,
    eventNextCursor: "",
    eventHasMore: false,
    findings: [],
    findingNextCursor: "",
    findingHasMore: false,
    findingsMayHaveMore: false,
    findingsStatus: "idle",
    overviewStatus: "idle",
    sessionOccurredAfter: "",
    stats: null,
    filters: { query: "", harness: "", capture: "", outcome: "", days: "" },
    showAllEvents: false,
    lastAction: "sessions",
    sessionTab: "summary",
  };

  const elements = {
    appShell: document.querySelector("#app-shell"),
    initializationBanner: document.querySelector("#initialization-banner"),
    updateNotice: document.querySelector("#update-notice"),
    updateNoticeLabel: document.querySelector("#update-notice-label"),
    updateModalLayer: document.querySelector("#update-modal-layer"),
    updateDialog: document.querySelector("#update-dialog"),
    updateClose: document.querySelector("#update-close"),
    updateCurrentVersion: document.querySelector("#update-current-version"),
    updateLatestVersion: document.querySelector("#update-latest-version"),
    updateReleaseNote: document.querySelector("#update-release-note"),
    updateCopyCommand: document.querySelector("#update-copy-command"),
    updateAlert: document.querySelector("#update-alert"),
    updateSkip: document.querySelector("#update-skip"),
    updateRemind: document.querySelector("#update-remind"),
    navBrief: document.querySelector("#nav-brief"),
    navBriefLabel: document.querySelector("#nav-brief-label"),
    navAttention: document.querySelector("#nav-attention"),
    navAttentionLabel: document.querySelector("#nav-attention-label"),
    navSessions: document.querySelector("#nav-sessions"),
    navSessionsLabel: document.querySelector("#nav-sessions-label"),
    briefView: document.querySelector("#brief-view"),
    briefEyebrow: document.querySelector("#brief-eyebrow"),
    briefHeading: document.querySelector("#brief-heading"),
    briefWindow: document.querySelector("#brief-window"),
    briefStatus: document.querySelector("#brief-status"),
    briefSessionCount: document.querySelector("#brief-session-count"),
    briefAgentCount: document.querySelector("#brief-agent-count"),
    briefOutcomeCount: document.querySelector("#brief-outcome-count"),
    briefLatestActivity: document.querySelector("#brief-latest-activity"),
    reportSparkline: document.querySelector("#report-sparkline"),
    transcriptRefreshStatus: document.querySelector(
      "#transcript-refresh-status",
    ),
    transcriptWithCount: document.querySelector("#transcript-with-count"),
    transcriptPartialCount: document.querySelector(
      "#transcript-partial-count",
    ),
    transcriptWithoutCount: document.querySelector(
      "#transcript-without-count",
    ),
    transcriptOnlyCount: document.querySelector("#transcript-only-count"),
    transcriptSessionList: document.querySelector(
      "#transcript-session-list",
    ),
    transcriptEmpty: document.querySelector("#transcript-empty"),
    briefLoading: document.querySelector("#brief-loading"),
    briefLoadingText: document.querySelector("#brief-loading-text"),
    briefError: document.querySelector("#brief-error"),
    briefErrorTitle: document.querySelector("#brief-error-title"),
    briefErrorDetail: document.querySelector("#brief-error-detail"),
    briefRetry: document.querySelector("#brief-retry"),
    briefContent: document.querySelector("#brief-content"),
    briefActionList: document.querySelector("#brief-action-list"),
    briefActionsEmpty: document.querySelector("#brief-actions-empty"),
    briefActionsEmptyTitle: document.querySelector(
      "#brief-actions-empty-title",
    ),
    briefActionsEmptyDetail: document.querySelector(
      "#brief-actions-empty-detail",
    ),
    briefRecentList: document.querySelector("#brief-recent-list"),
    briefRecentEmpty: document.querySelector("#brief-recent-empty"),
    briefRecentEmptyTitle: document.querySelector(
      "#brief-recent-empty-title",
    ),
    briefRecentEmptyDetail: document.querySelector(
      "#brief-recent-empty-detail",
    ),
    briefAgentSummary: document.querySelector("#brief-agent-summary"),
    briefCoverage: document.querySelector("#brief-coverage"),
    briefCoverageSummary: document.querySelector("#brief-coverage-summary"),
    briefLimitations: document.querySelector("#brief-limitations"),
    briefSources: document.querySelector("#brief-sources"),
    briefOpenAttention: document.querySelector("#brief-open-attention"),
    briefOpenSessions: document.querySelector("#brief-open-sessions"),
    missionPackModalLayer: document.querySelector(
      "#mission-pack-modal-layer",
    ),
    missionPackDialog: document.querySelector("#mission-pack-dialog"),
    missionPackTitle: document.querySelector("#mission-pack-dialog-title"),
    missionPackClose: document.querySelector("#mission-pack-close"),
    missionPackDone: document.querySelector("#mission-pack-done"),
    missionPackLoading: document.querySelector("#mission-pack-loading"),
    missionPackError: document.querySelector("#mission-pack-error"),
    missionPackErrorDetail: document.querySelector(
      "#mission-pack-error-detail",
    ),
    missionPackRetry: document.querySelector("#mission-pack-retry"),
    missionPackContent: document.querySelector("#mission-pack-content"),
    missionPackMetadata: document.querySelector("#mission-pack-metadata"),
    missionPackSections: document.querySelector("#mission-pack-sections"),
    missionPackSize: document.querySelector("#mission-pack-size"),
    missionPackCopyClaude: document.querySelector(
      "#mission-pack-copy-claude",
    ),
    missionPackCopyCodex: document.querySelector("#mission-pack-copy-codex"),
    missionPackShowEvidence: document.querySelector(
      "#mission-pack-show-evidence",
    ),
    reportEvidenceModalLayer: document.querySelector(
      "#report-evidence-modal-layer",
    ),
    reportEvidenceDialog: document.querySelector("#report-evidence-dialog"),
    reportEvidenceTitle: document.querySelector(
      "#report-evidence-dialog-title",
    ),
    reportEvidenceClose: document.querySelector("#report-evidence-close"),
    reportEvidenceDone: document.querySelector("#report-evidence-done"),
    reportEvidenceCopyClaude: document.querySelector(
      "#report-evidence-copy-claude",
    ),
    reportEvidenceCopyCodex: document.querySelector(
      "#report-evidence-copy-codex",
    ),
    reportEvidenceExcerpts: document.querySelector(
      "#report-evidence-excerpts",
    ),
    reportEvidenceFix: document.querySelector("#report-evidence-fix"),
    attentionNavCount: document.querySelector("#attention-nav-count"),
    attentionView: document.querySelector("#attention-view"),
    sessionsView: document.querySelector("#sessions-view"),
    navHabits: document.querySelector("#nav-habits"),
    navHabitsLabel: document.querySelector("#nav-habits-label"),
    habitsView: document.querySelector("#habits-view"),
    habitsHeading: document.querySelector("#habits-heading"),
    habitsStatus: document.querySelector("#habits-status"),
    habitsLoading: document.querySelector("#habits-loading"),
    habitsError: document.querySelector("#habits-error"),
    habitsErrorDetail: document.querySelector("#habits-error-detail"),
    habitsRetry: document.querySelector("#habits-retry"),
    habitsContent: document.querySelector("#habits-content"),
    habitsEmpty: document.querySelector("#habits-empty"),
    habitsList: document.querySelector("#habits-list"),
    habitsAbout: document.querySelector("#habits-about"),
    habitsLimitations: document.querySelector("#habits-limitations"),
    habitsWindow: document.querySelector("#habits-window"),
    reportHabitsList: document.querySelector("#report-habits-list"),
    reportHabitsEmpty: document.querySelector("#report-habits-empty"),
    reportHabitsStatus: document.querySelector("#report-habits-status"),
    reportHabitsOpen: document.querySelector("#report-habits-open"),
    reportHabitSummary: document.querySelector("#report-habit-summary"),
    habitsDebriefAll: document.querySelector("#habits-debrief-all"),
    habitsDetail: document.querySelector("#habits-detail"),
    habitsOlder: document.querySelector("#habits-older"),
    attentionListPane: document.querySelector("#attention-list-pane"),
    attentionModeIssues: document.querySelector("#attention-mode-issues"),
    attentionModeSafety: document.querySelector("#attention-mode-safety"),
    attentionEyebrow: document.querySelector("#attention-eyebrow"),
    attentionHeading: document.querySelector("#attention-heading"),
    attentionStatusLabel: document.querySelector("#attention-status-label"),
    attentionDetailPane: document.querySelector("#attention-detail-pane"),
    attentionScroll: document.querySelector(".attention-scroll"),
    costIssuesSection: document.querySelector("#cost-issues-section"),
    costIssueCount: document.querySelector("#cost-issue-count"),
    costIssueList: document.querySelector("#cost-issue-list"),
    costIssuesLoading: document.querySelector("#cost-issues-loading"),
    costIssuesEmpty: document.querySelector("#cost-issues-empty"),
    costIssuesError: document.querySelector("#cost-issues-error"),
    costIssuesRetry: document.querySelector("#cost-issues-retry"),
    safetyContent: null,
    stableIssuesSection: document.querySelector("#stable-issues-section"),
    evidenceGapsSection: document.querySelector("#evidence-gaps-section"),
    fixMonitoringSection: document.querySelector("#fix-monitoring-section"),
    coveragePanel: document.querySelector("#coverage-panel"),
    attentionCount: document.querySelector("#attention-count"),
    attentionRefreshNotice: document.querySelector("#attention-refresh-notice"),
    coverageCurrent: document.querySelector("#coverage-current"),
    coveragePending: document.querySelector("#coverage-pending"),
    coverageFailed: document.querySelector("#coverage-failed"),
    coverageTruncated: document.querySelector("#coverage-truncated"),
    coverageUnscoped: document.querySelector("#coverage-unscoped"),
    coverageThrough: document.querySelector("#coverage-through"),
    coverageCompleteness: document.querySelector("#coverage-completeness"),
    attentionFilters: document.querySelector("#attention-filters"),
    issueFilterSeverity: document.querySelector("#issue-filter-severity"),
    issueFilterHarness: document.querySelector("#issue-filter-harness"),
    issueFilterOrigin: document.querySelector("#issue-filter-origin"),
    issueFilterStatus: document.querySelector("#issue-filter-status"),
    issueFilterExperimental: document.querySelector(
      "#issue-filter-experimental",
    ),
    clearAttentionFilters: document.querySelector("#clear-attention-filters"),
    attentionFilterNote: document.querySelector("#attention-filter-note"),
    issueFilterDisclosure: document.querySelector("#issue-filter-disclosure"),
    fixMonitoringFilters: document.querySelector("#fix-monitoring-filters"),
    fixMonitoringFilterState: document.querySelector(
      "#fix-monitoring-filter-state",
    ),
    fixMonitoringFilterChangeKind: document.querySelector(
      "#fix-monitoring-filter-change-kind",
    ),
    fixMonitoringFilterSeverity: document.querySelector(
      "#fix-monitoring-filter-severity",
    ),
    fixMonitoringFilterHarness: document.querySelector(
      "#fix-monitoring-filter-harness",
    ),
    fixMonitoringFilterIssueID: document.querySelector(
      "#fix-monitoring-filter-issue-id",
    ),
    fixMonitoringFilterRecordedAfter: document.querySelector(
      "#fix-monitoring-filter-recorded-after",
    ),
    fixMonitoringFilterRetracted: document.querySelector(
      "#fix-monitoring-filter-retracted",
    ),
    clearFixMonitoringFilters: document.querySelector(
      "#clear-fix-monitoring-filters",
    ),
    fixMonitoringFilterNote: document.querySelector(
      "#fix-monitoring-filter-note",
    ),
    fixMonitoringStatus: document.querySelector("#fix-monitoring-status"),
    fixMonitoringCount: document.querySelector("#fix-monitoring-count"),
    fixMonitoringList: document.querySelector("#fix-monitoring-list"),
    fixMonitoringLoading: document.querySelector("#fix-monitoring-loading"),
    fixMonitoringEmpty: document.querySelector("#fix-monitoring-empty"),
    fixMonitoringEmptyTitle: document.querySelector(
      "#fix-monitoring-empty-title",
    ),
    fixMonitoringEmptyDetail: document.querySelector(
      "#fix-monitoring-empty-detail",
    ),
    fixMonitoringPagination: document.querySelector(
      "#fix-monitoring-pagination",
    ),
    fixMonitoringPageStatus: document.querySelector(
      "#fix-monitoring-page-status",
    ),
    fixMonitoringLoadMore: document.querySelector(
      "#fix-monitoring-load-more",
    ),
    issueCount: document.querySelector("#issue-count"),
    issueList: document.querySelector("#issue-list"),
    issuesLoading: document.querySelector("#issues-loading"),
    issuesEmpty: document.querySelector("#issues-empty"),
    issuesEmptyTitle: document.querySelector("#issues-empty-title"),
    issuesEmptyDetail: document.querySelector("#issues-empty-detail"),
    issuesPagination: document.querySelector("#issues-pagination"),
    issuesPageStatus: document.querySelector("#issues-page-status"),
    issuesLoadMore: document.querySelector("#issues-load-more"),
    evidenceGapCount: document.querySelector("#evidence-gap-count"),
    evidenceGapList: document.querySelector("#evidence-gap-list"),
    evidenceGapsLoading: document.querySelector("#evidence-gaps-loading"),
    evidenceGapsEmpty: document.querySelector("#evidence-gaps-empty"),
    evidenceGapsEmptyTitle: document.querySelector(
      "#evidence-gaps-empty-title",
    ),
    evidenceGapsEmptyDetail: document.querySelector(
      "#evidence-gaps-empty-detail",
    ),
    evidenceGapsPagination: document.querySelector(
      "#evidence-gaps-pagination",
    ),
    evidenceGapsPageStatus: document.querySelector(
      "#evidence-gaps-page-status",
    ),
    evidenceGapsLoadMore: document.querySelector(
      "#evidence-gaps-load-more",
    ),
    attentionWelcome: document.querySelector("#attention-welcome"),
    familyDetail: document.querySelector("#family-detail"),
    familyBackButton: document.querySelector("#family-back-button"),
    familyDetailHeading: document.querySelector("#family-detail-heading"),
    familyDetailBadges: document.querySelector("#family-detail-badges"),
    familyAnalysisQualifier: document.querySelector(
      "#family-analysis-qualifier",
    ),
    familyObservation: document.querySelector("#family-observation"),
    familyCaveat: document.querySelector("#family-caveat"),
    familyNextAction: document.querySelector("#family-next-action"),
    familySummary: document.querySelector("#family-summary"),
    familyMemberCount: document.querySelector("#family-member-count"),
    familyMemberList: document.querySelector("#family-member-list"),
    familyMembersLoading: document.querySelector("#family-members-loading"),
    familyMembersEmpty: document.querySelector("#family-members-empty"),
    familyMembersEmptyDetail: document.querySelector(
      "#family-members-empty-detail",
    ),
    familyMembersPagination: document.querySelector(
      "#family-members-pagination",
    ),
    familyMembersPageStatus: document.querySelector(
      "#family-members-page-status",
    ),
    familyMembersLoadMore: document.querySelector(
      "#family-members-load-more",
    ),
    issueDetail: document.querySelector("#issue-detail"),
    issueBackButton: document.querySelector("#issue-back-button"),
    issueDetailKind: document.querySelector("#issue-detail-kind"),
    issueDetailHeading: document.querySelector("#issue-detail-heading"),
    issueDetailBadges: document.querySelector("#issue-detail-badges"),
    issueAnalysisQualifier: document.querySelector(
      "#issue-analysis-qualifier",
    ),
    issueExplanation: document.querySelector("#issue-explanation"),
    issueScopeDisclosure: document.querySelector("#issue-scope-disclosure"),
    issueNextAction: document.querySelector("#issue-next-action"),
    issueExplanationSection: document.querySelector(
      "#issue-explanation-section",
    ),
    issueEvidencePreview: document.querySelector("#issue-evidence-preview"),
    issueEvidencePreviewCount: document.querySelector(
      "#issue-evidence-preview-count",
    ),
    issueEvidencePreviewDisclosure: document.querySelector(
      "#issue-evidence-preview-disclosure",
    ),
    issueEvidencePreviewList: document.querySelector(
      "#issue-evidence-preview-list",
    ),
    issueEvidencePreviewAll: document.querySelector(
      "#issue-evidence-preview-all",
    ),
    issueTechnicalDetails: document.querySelector("#issue-technical-details"),
    issueMetadata: document.querySelector("#issue-metadata"),
    fingerprintPanel: document.querySelector("#fingerprint-panel"),
    issueFingerprint: document.querySelector("#issue-fingerprint"),
    copyIssueFingerprint: document.querySelector("#copy-issue-fingerprint"),
    recordFixAttempt: document.querySelector("#record-fix-attempt"),
    fixAttemptsSection: document.querySelector("#fix-attempts-section"),
    fixEligibilityStatus: document.querySelector("#fix-eligibility-status"),
    fixMonitoringDetailStatus: document.querySelector(
      "#fix-monitoring-detail-status",
    ),
    fixActionStatus: document.querySelector("#fix-action-status"),
    fixHistoryList: document.querySelector("#fix-history-list"),
    fixHistoryLoading: document.querySelector("#fix-history-loading"),
    fixHistoryEmpty: document.querySelector("#fix-history-empty"),
    fixEvidenceEvaluated: document.querySelector("#fix-evidence-evaluated"),
    fixHistoryPagination: document.querySelector("#fix-history-pagination"),
    fixHistoryPageStatus: document.querySelector("#fix-history-page-status"),
    fixHistoryLoadMore: document.querySelector("#fix-history-load-more"),
    occurrenceCount: document.querySelector("#occurrence-count"),
    occurrenceList: document.querySelector("#occurrence-list"),
    occurrencesLoading: document.querySelector("#occurrences-loading"),
    occurrencesPagination: document.querySelector("#occurrences-pagination"),
    occurrencesPageStatus: document.querySelector("#occurrences-page-status"),
    occurrencesLoadMore: document.querySelector("#occurrences-load-more"),
    matchingSection: document.querySelector("#matching-section"),
    refreshButton: document.querySelector("#refresh-button"),
    sessionPanel: document.querySelector(".session-panel"),
    timelinePanel: document.querySelector(".timeline-panel"),
    sessionCount: document.querySelector("#session-count"),
    overviewSessionCount: document.querySelector("#overview-session-count"),
    overviewEventCount: document.querySelector("#overview-event-count"),
    overviewFindingCount: document.querySelector("#overview-finding-count"),
    overviewHarnesses: document.querySelector("#overview-harnesses"),
    overviewFreshness: document.querySelector("#overview-freshness"),
    sessionSearch: document.querySelector("#session-search"),
    filterHarness: document.querySelector("#filter-harness"),
    filterCapture: document.querySelector("#filter-capture"),
    filterOutcome: document.querySelector("#filter-outcome"),
    filterDate: document.querySelector("#filter-date"),
    clearFilters: document.querySelector("#clear-filters"),
    sessionScopeNote: document.querySelector("#session-scope-note"),
    sessionList: document.querySelector("#session-list"),
    sessionsPagination: document.querySelector("#sessions-pagination"),
    sessionsPageStatus: document.querySelector("#sessions-page-status"),
    sessionsLoadMore: document.querySelector("#sessions-load-more"),
    sessionsHeading: document.querySelector("#sessions-heading"),
    sessionsLoading: document.querySelector("#sessions-loading"),
    sessionsEmpty: document.querySelector("#sessions-empty"),
    welcomeState: document.querySelector("#welcome-state"),
    selectedMeta: document.querySelector("#selected-meta"),
    sessionTabSummary: document.querySelector("#session-tab-summary"),
    sessionTabTimeline: document.querySelector("#session-tab-timeline"),
    sessionTabTimelineCount: document.querySelector("#session-tab-timeline-count"),
    sessionSummaryTab: document.querySelector("#session-summary-tab"),
    sessionTimelineTab: document.querySelector("#session-timeline-tab"),
    sessionVisual: document.querySelector("#session-visual"),
    timelineView: document.querySelector("#timeline-view"),
    backButton: document.querySelector("#back-button"),
    selectedAvatar: document.querySelector("#selected-avatar"),
    selectedHarness: document.querySelector("#selected-harness"),
    selectedOutcome: document.querySelector("#selected-outcome"),
    selectedHistorical: document.querySelector("#selected-historical"),
    selectedSessionID: document.querySelector("#selected-session-id"),
    copySelectedSession: document.querySelector("#copy-selected-session"),
    selectedEventCount: document.querySelector("#selected-event-count"),
    selectedDuration: document.querySelector("#selected-duration"),
    selectedEndedAt: document.querySelector("#selected-ended-at"),
    sessionDiagnosis: document.querySelector("#session-diagnosis"),
    sessionDiagnosisHeading: document.querySelector(
      "#session-diagnosis-heading",
    ),
    sessionDiagnosisStatus: document.querySelector(
      "#session-diagnosis-status",
    ),
    sessionDiagnosisTitle: document.querySelector(
      "#session-diagnosis-title",
    ),
    sessionDiagnosisDetail: document.querySelector(
      "#session-diagnosis-detail",
    ),
    sessionDiagnosisActions: document.querySelector(
      "#session-diagnosis-actions",
    ),
    sessionDiagnosisLimitations: document.querySelector(
      "#session-diagnosis-limitations",
    ),
    overviewState: document.querySelector("#overview-state"),
    overviewQuality: document.querySelector("#overview-quality"),
    activityGrid: document.querySelector("#activity-grid"),
    needsAttentionSummary: document.querySelector("#needs-attention-summary"),
    observedWorkSummary: document.querySelector("#observed-work-summary"),
    sessionHighlights: document.querySelector("#session-highlights"),
    resourceList: document.querySelector("#resource-list"),
    resourceDisclosure: document.querySelector("#resource-disclosure"),
    findingList: document.querySelector("#finding-list"),
    findingsPagination: document.querySelector("#findings-pagination"),
    findingsPageStatus: document.querySelector("#findings-page-status"),
    findingsLoadMore: document.querySelector("#findings-load-more"),
    timelineFreshness: document.querySelector("#timeline-freshness"),
    timelineScope: document.querySelector("#timeline-scope"),
    allEventsToggle: document.querySelector("#all-events-toggle"),
    eventList: document.querySelector("#event-list"),
    eventsPagination: document.querySelector("#events-pagination"),
    eventsPageStatus: document.querySelector("#events-page-status"),
    eventsLoadMore: document.querySelector("#events-load-more"),
    timelineLoading: document.querySelector("#timeline-loading"),
    eventsEmpty: document.querySelector("#events-empty"),
    errorBanner: document.querySelector("#error-banner"),
    errorTitle: document.querySelector("#error-title"),
    errorDetail: document.querySelector("#error-detail"),
    errorRetry: document.querySelector("#error-retry"),
    fixAttemptModalLayer: document.querySelector("#fix-attempt-modal-layer"),
    fixAttemptDialog: document.querySelector("#fix-attempt-dialog"),
    fixAttemptClose: document.querySelector("#fix-attempt-close"),
    fixAttemptCancel: document.querySelector("#fix-attempt-cancel"),
    fixAttemptConfirm: document.querySelector("#fix-attempt-confirm"),
    fixAttemptAlert: document.querySelector("#fix-attempt-alert"),
    fixCategoryFieldset: document.querySelector("#fix-category-fieldset"),
    fixCategoryOptions: document.querySelector("#fix-category-options"),
    fixDraftRecovery: document.querySelector("#fix-draft-recovery"),
    abandonFixDraft: document.querySelector("#abandon-fix-draft"),
    fixRetractionModalLayer: document.querySelector(
      "#fix-retraction-modal-layer",
    ),
    fixRetractionDialog: document.querySelector("#fix-retraction-dialog"),
    fixRetractionClose: document.querySelector("#fix-retraction-close"),
    fixRetractionCancel: document.querySelector("#fix-retraction-cancel"),
    fixRetractionConfirm: document.querySelector("#fix-retraction-confirm"),
    fixRetractionAlert: document.querySelector("#fix-retraction-alert"),
    fixRetractionFieldset: document.querySelector(
      "#fix-retraction-fieldset",
    ),
    fixRetractionOptions: document.querySelector(
      "#fix-retraction-options",
    ),
    fixRetractionRecovery: document.querySelector(
      "#fix-retraction-recovery",
    ),
    abandonFixRetraction: document.querySelector(
      "#abandon-fix-retraction",
    ),
  };

  const focusRegistry = {
    briefActions: new Map(),
    missionPackTriggers: new Map(),
    reportEvidenceTriggers: new Map(),
    briefSessions: new Map(),
    diagnosisActions: new Map(),
    monitoringCards: new Map(),
    issueCards: new Map(),
    familyCards: new Map(),
    familyMembers: new Map(),
    occurrenceActions: new Map(),
    sessionCards: new Map(),
    fixTriggers: new Map(),
    fixHistoryRows: new Map(),
    fixRetractionTriggers: new Map(),
    fixObservationRows: new Map(),
  };
  const fixDrafts = new Map();
  const fixRetractionDrafts = new Map();
  const fixObservationPages = new Map();
  const missionPackCacheByID = new Map();
  const missionPackIDByIssue = new Map();
  let searchTimer = 0;
  let issueFilterTimer = 0;
  const mobileQuery = globalThis.matchMedia("(max-width: 680px)");
  const runtimeReady = loadRuntimeExperience();
  prepareValueFirstAttentionLayout();
  renderFixDialogChoices();
  renderFixMonitoringFilters();
  bindEvents();
  setActiveView("brief", false);
  void startProgressiveInitialization();

  function createIssueBucket(kind) {
    return {
      kind,
      data: [],
      nextCursor: "",
      hasMore: false,
      viewCursor: "",
      analysis: null,
      selection: null,
      status: "idle",
      error: null,
      requestGeneration: 0,
    };
  }

  function createAttentionFamilyBucket() {
    return {
      kind: "family",
      data: [],
      nextCursor: "",
      hasMore: false,
      analysis: null,
      selection: null,
      status: "idle",
      error: null,
      requestGeneration: 0,
    };
  }

  function createFixMonitoringBucket() {
    return {
      data: [],
      nextCursor: "",
      hasMore: false,
      viewCursor: "",
      evidenceEvaluatedAt: "",
      status: "idle",
      error: null,
      requestGeneration: 0,
    };
  }

  function createIssueEvidencePreview() {
    return {
      issueID: "",
      occurrenceID: "",
      sessionID: "",
      status: "idle",
      events: [],
      requestedCount: 0,
      foundCount: 0,
      missingCount: 0,
      missingEventIDs: [],
      availableEventIDs: [],
      expanded: false,
      error: "",
    };
  }

  async function startProgressiveInitialization() {
    await runtimeReady;
    void loadUpdateStatus();
    await requestInitializationStatus();
    await refreshActiveViewForInitialization(false);
    scheduleInitializationPoll();
    void startTranscriptPolling();
  }

  async function startTranscriptPolling() {
    await requestTranscriptStatus();
    scheduleTranscriptPoll();
  }

  async function loadRuntimeExperience() {
    let experience = "current";
    try {
      const response = await apiGet("/v1/runtime");
      experience = requireRuntimeExperience(response);
    } catch {
      experience = "current";
    }
    applyExperience(experience);
    return experience;
  }

  function requireRuntimeExperience(response) {
    const experience = readText(response && response.experience);
    if (
      !isRecord(response) ||
      readText(response.schema_version) !== runtimeSchemaVersion ||
      !["current", "value-first"].includes(experience)
    ) {
      throw new Error("Local API returned an invalid runtime experience.");
    }
    return experience;
  }

  function applyExperience(experience) {
    const selected = experience === "value-first" ? experience : "current";
    const copy = experienceCopy[selected];
    state.experience = selected;
    document.body.dataset.experience = selected;
    elements.appShell.dataset.experience = selected;
    elements.navBriefLabel.textContent = copy.navBrief;
    elements.navAttentionLabel.textContent = copy.navAttention;
    elements.navSessionsLabel.textContent = copy.navSessions;
    elements.navHabitsLabel.textContent = copy.navHabits;
    elements.briefEyebrow.textContent = copy.briefEyebrow;
    elements.briefLoadingText.textContent = copy.briefLoading;
    elements.briefErrorTitle.textContent = copy.briefErrorTitle;
    elements.briefErrorDetail.textContent = copy.briefErrorDetail;
    elements.briefOpenAttention.textContent = copy.briefOpenAttention;
    elements.briefOpenSessions.textContent = copy.briefOpenSessions;
    elements.briefRecentEmptyDetail.textContent = copy.briefRecentEmptyDetail;
    elements.attentionListPane.setAttribute(
      "aria-label",
      copy.attentionAriaLabel,
    );
    elements.attentionEyebrow.textContent = copy.attentionEyebrow;
    elements.attentionHeading.textContent = copy.attentionHeading;
    elements.attentionStatusLabel.textContent = copy.attentionStatus;
    elements.familyBackButton.setAttribute(
      "aria-label",
      copy.attentionBackLabel,
    );
    elements.issueBackButton.setAttribute(
      "aria-label",
      copy.attentionBackLabel,
    );
    elements.familyMembersEmptyDetail.textContent =
      copy.attentionEmptyDetail;
    elements.sessionsHeading.textContent = copy.sessionsHeading;
    const filterSummary = document.querySelector(
      "#attention-filter-disclosure > summary",
    );
    if (filterSummary) {
      filterSummary.textContent = copy.attentionFilterSummary;
    }
  }

  // Only pass fixed, locally authored UI copy here. This rewrites nav terms
  // unconditionally, so event-, catalog-, API-error-, and provider-derived text
  // must render verbatim and never flow through this function.
  function experiencePageText(value) {
    const text = readText(value);
    if (state.experience !== "value-first") return text;
    return text
      .replaceAll("Developer Brief", "Home")
      .replaceAll("Brief", "Home")
      .replaceAll("The brief", "Home")
      .replaceAll("the brief", "Home")
      .replaceAll("local brief", "Home")
      .replaceAll("Attention", "Review")
      .replaceAll("Sessions", "History");
  }

  async function requestInitializationStatus() {
    if (state.initializationRequestInFlight) {
      return { ok: false, skipped: true };
    }
    state.initializationRequestInFlight = true;
    const generation = ++state.initializationPollGeneration;
    const previous = readText(state.initialization && state.initialization.state);
    try {
      const response = await apiGet("/v1/initialization");
      if (generation !== state.initializationPollGeneration) {
        return { ok: false, stale: true, previous, current: previous };
      }
      const initialization = requireInitializationStatus(response);
      state.initialization = initialization;
      renderInitializationStatus();
      return {
        ok: true,
        previous,
        current: initialization.state,
      };
    } catch {
      if (generation !== state.initializationPollGeneration) {
        return { ok: false, stale: true, previous, current: previous };
      }
      renderInitializationStatus();
      return { ok: false, previous, current: previous };
    } finally {
      state.initializationRequestInFlight = false;
    }
  }

  async function requestTranscriptStatus() {
    if (state.transcriptRequestInFlight) return false;
    state.transcriptRequestInFlight = true;
    const generation = ++state.transcriptPollGeneration;
    try {
      const response = await apiGet("/v1/transcript-status");
      if (generation !== state.transcriptPollGeneration) return false;
      state.transcriptStatus = requireTranscriptStatus(response);
      state.transcriptStatusStale = false;
      renderTranscriptStatus();
      return true;
    } catch {
      if (generation !== state.transcriptPollGeneration) return false;
      state.transcriptStatusStale = true;
      renderTranscriptStatus();
      return false;
    } finally {
      state.transcriptRequestInFlight = false;
    }
  }

  function requireTranscriptStatus(response) {
    const coverage = isRecord(response) ? response.coverage : null;
    const sessions = isRecord(response) && Array.isArray(response.sessions)
      ? response.sessions
      : null;
    if (
      !isRecord(response) ||
      readText(response.schema_version) !== "belay.transcript-status.v1" ||
      !isRecord(coverage) ||
      sessions === null ||
      sessions.length > 10
    ) {
      throw new Error("Local API returned an invalid transcript status.");
    }
    const counts = {
      with_transcript: toOptionalCount(coverage.with_transcript),
      partial: toOptionalCount(coverage.partial),
      without_transcript: toOptionalCount(coverage.without_transcript),
      transcript_only: toOptionalCount(coverage.transcript_only),
    };
    if (Object.values(counts).some((value) => value === null)) {
      throw new Error("Local API returned invalid transcript coverage.");
    }
    return {
      coverage: counts,
      sessions: sessions.map((session) => ({
        session_key: readText(session && session.session_key),
        agent: readText(session && session.agent),
        project: readText(session && session.project),
        last_activity_at: readText(session && session.last_activity_at),
        coverage: readText(session && session.coverage),
        turn_count: toOptionalCount(session && session.turn_count),
        active: session && session.active === true,
      })),
    };
  }

  function renderTranscriptStatus() {
    const status = state.transcriptStatus;
    if (!status) {
      elements.transcriptRefreshStatus.textContent =
        "Transcript status unavailable · retrying every 2 seconds";
      elements.transcriptRefreshStatus.dataset.state = "unavailable";
      return;
    }
    const coverage = status.coverage;
    elements.transcriptWithCount.textContent = formatNumber(
      coverage.with_transcript,
    );
    elements.transcriptPartialCount.textContent = formatNumber(
      coverage.partial,
    );
    elements.transcriptWithoutCount.textContent = formatNumber(
      coverage.without_transcript,
    );
    elements.transcriptOnlyCount.textContent = formatNumber(
      coverage.transcript_only,
    );
    elements.transcriptRefreshStatus.textContent = state.transcriptStatusStale
      ? "Last transcript status is stale · retrying every 2 seconds"
      : "Updates every 2 seconds";
    elements.transcriptRefreshStatus.dataset.state =
      state.transcriptStatusStale ? "stale" : "current";
    elements.transcriptSessionList.replaceChildren();
    status.sessions.forEach((session) => {
      const row = document.createElement("div");
      row.className = "transcript-session-row";
      row.dataset.active = session.active ? "true" : "false";
      const project = document.createElement("strong");
      project.textContent =
        session.project ||
        displayHarness(session.agent) ||
        "Local agent session";
      const detail = document.createElement("span");
      const activity = formatRelativeTime(session.last_activity_at);
      const turns = session.turn_count === null
        ? "turn count unavailable"
        : `${formatNumber(session.turn_count)} ${
            session.turn_count === 1 ? "turn" : "turns"
          }`;
      detail.textContent = `${
        session.active ? "Live" : readableLabel(session.coverage, "Recent")
      } · ${turns} · ${activity}`;
      row.append(project, detail);
      elements.transcriptSessionList.append(row);
    });
    elements.transcriptEmpty.hidden = status.sessions.length !== 0;
  }

  function scheduleTranscriptPoll() {
    if (state.transcriptRequestInFlight || state.transcriptPollTimer) return;
    state.transcriptPollTimer = globalThis.setTimeout(() => {
      state.transcriptPollTimer = 0;
      void pollTranscriptStatus();
    }, transcriptPollMilliseconds);
  }

  async function pollTranscriptStatus() {
    await requestTranscriptStatus();
    scheduleTranscriptPoll();
  }

  function requireInitializationStatus(response) {
    const value = isRecord(response) ? response.initialization : null;
    const initializationState = readText(value && value.state);
    if (
      !isRecord(response) ||
      readText(response.schema_version) !== "belay.initialization.v1" ||
      !isRecord(value) ||
      !["initializing", "ready", "degraded"].includes(initializationState)
    ) {
      throw new Error("Local API returned an invalid initialization status.");
    }
    return {
      schema_version: "belay.initialization.v1",
      state: initializationState,
      started_at: readText(value.started_at),
      completed_at: readText(value.completed_at),
      error_code: readText(value.error_code),
    };
  }

  function renderInitializationStatus() {
    const initializationState = readText(
      state.initialization && state.initialization.state,
    );
    let message = "";
    if (initializationState === "initializing") {
      message =
        "Belay is importing local agent history. Results are partial and will update automatically.";
    } else if (initializationState === "degraded") {
      message =
        "Belay imported available data, but part of the initial scan could not complete. Results may be partial.";
    }
    elements.initializationBanner.hidden = !message;
    elements.initializationBanner.dataset.state = initializationState;
    elements.initializationBanner.textContent = message;
  }

  function scheduleInitializationPoll() {
    if (
      readText(state.initialization && state.initialization.state) !==
        "initializing" ||
      state.initializationRequestInFlight ||
      state.initializationPollTimer
    ) {
      return;
    }
    state.initializationPollTimer = globalThis.setTimeout(() => {
      state.initializationPollTimer = 0;
      void pollInitialization();
    }, initializationPollMilliseconds);
  }

  function stopInitializationPolling() {
    globalThis.clearTimeout(state.initializationPollTimer);
    state.initializationPollTimer = 0;
  }

  async function pollInitialization() {
    if (state.initializationRequestInFlight) return;
    const result = await requestInitializationStatus();
    const transitionedToTerminal =
      result.ok &&
      result.previous === "initializing" &&
      ["ready", "degraded"].includes(result.current);
    if (
      result.ok &&
      (result.current === "initializing" || transitionedToTerminal)
    ) {
      await refreshActiveViewForInitialization(true);
    }
    if (
      readText(state.initialization && state.initialization.state) ===
      "initializing"
    ) {
      scheduleInitializationPoll();
    } else {
      stopInitializationPolling();
    }
  }

  async function refreshActiveViewForInitialization(preserveSelection) {
    if (state.activeView === "brief") {
      await loadDeveloperBrief(preserveSelection);
      return true;
    }
    if (state.activeView === "attention") {
      if (state.selectedFamily || state.selectedIssueID) return false;
      await refreshAttention(false, true);
      return true;
    }
    if (state.selectedSessionID) return false;
    await refreshSessions(false);
    return true;
  }

  async function loadDeveloperBrief(preserveCurrent = false) {
    clearMissionPackCache();
    void loadReportHabits();
    const generation = ++state.briefRequestGeneration;
    const priorBrief = state.developerBrief;
    const priorStatus = state.briefStatus;
    const priorError = state.briefError;
    if (!preserveCurrent) {
      state.briefStatus = "loading";
      state.briefError = "";
      state.developerBrief = null;
      renderDeveloperBrief();
    }
    try {
      const response = await apiGet("/v1/report");
      if (generation !== state.briefRequestGeneration) return false;
      state.developerBrief = requireDeveloperBrief(response);
      state.briefStatus = "ready";
      renderDeveloperBrief();
      return true;
    } catch (error) {
      if (generation !== state.briefRequestGeneration) return false;
      if (preserveCurrent && priorBrief) {
        state.developerBrief = priorBrief;
        state.briefStatus = priorStatus;
        state.briefError = priorError;
        renderDeveloperBrief();
        return false;
      }
      state.developerBrief = null;
      state.briefStatus = "error";
      state.briefError = customerErrorMessage(
        error,
        experienceCopy[state.experience].briefErrorDetail,
      );
      renderDeveloperBrief();
      return false;
    }
  }

  function requireDeveloperBrief(response) {
    const brief = isRecord(response && response.data)
      ? response.data
      : response;
    if (
      !isRecord(brief) ||
      readText(brief.projection_version) !== "belay.report.v1" ||
      !isRecord(brief.totals) ||
      !Array.isArray(brief.weeks) ||
      !Array.isArray(brief.top_issues) ||
      !Array.isArray(brief.fixes) ||
      !isRecord(brief.waste) ||
      !isRecord(brief.about) ||
      !Array.isArray(brief.about.notes)
    ) {
      throw new Error("Local API returned an invalid report.");
    }
    brief.top_issues = brief.top_issues.slice(0, 5).map(requireCostIssue);
    return brief;
  }

  function renderDeveloperBrief() {
    const loading = state.briefStatus === "loading";
    const failed = state.briefStatus === "error";
    const brief = state.developerBrief;
    elements.briefLoading.hidden = !loading;
    elements.briefError.hidden = !failed;
    elements.briefContent.hidden = !brief || loading || failed;
    elements.briefErrorDetail.textContent =
      state.briefError || experienceCopy[state.experience].briefErrorDetail;
    if (!brief) {
      if (loading) {
        elements.briefWindow.textContent =
          "Loading the latest recorded activity…";
        elements.briefStatus.textContent = "";
      }
      elements.reportSparkline.replaceChildren();
      elements.reportSparkline.hidden = true;
      renderBriefSummary(null);
      return;
    }
    const harnesses = Array.isArray(brief.totals.harnesses)
      ? brief.totals.harnesses.map(displayHarness).filter(Boolean)
      : [];
    const sessionCount = toFiniteNumber(brief.totals.sessions);
    elements.briefHeading.textContent =
      sessionCount === 0
        ? "Waiting for your first session"
        : "Your recent sessions at a glance";
    const harnessCopy = harnesses.length
      ? ` from ${harnesses.join(" and ")}`
      : "";
    elements.briefWindow.textContent =
      `Across ${formatNumber(sessionCount)} retained ${sessionCount === 1 ? "session" : "sessions"}${harnessCopy}`;
    elements.briefStatus.textContent = initializationInProgress()
      ? "Importing earlier sessions…"
      : `Updated ${formatRelativeTime(brief.generated_at) || "just now"}`;
    renderReportTotals(brief);
    renderReportIssues(brief.top_issues);
    renderReportFixes(brief.fixes);
    renderReportWaste(brief.waste);
    renderReportAbout(brief.about);
  }

  function renderReportTotals(report) {
    const totals = report.totals;
    elements.briefSessionCount.textContent = formatNumber(totals.sessions);
    elements.briefAgentCount.textContent =
      `${totals.hours_lower_bound === true ? "≥" : ""}${formatNumber(totals.hours)} h`;
    elements.briefOutcomeCount.textContent =
      `${totals.tokens_lower_bound === true ? "≥" : ""}${formatNumber(totals.tokens)}`;
    elements.briefLatestActivity.textContent =
      `${totals.dollars_lower_bound === true ? "≥" : ""}${formatReportDollars(totals.dollars)}`;
    const weeks = report.weeks.slice(-12);
    const maximum = Math.max(
      1,
      ...weeks.map((week) => toFiniteNumber(week && week.session_count)),
    );
    const fragment = document.createDocumentFragment();
    weeks.forEach((week) => {
      const sessions = toFiniteNumber(week && week.session_count);
      const bar = createElement("span", "report-spark-bar");
      bar.style.height =
        sessions === 0
          ? "0"
          : `${Math.max(8, (sessions / maximum) * 100)}%`;
      bar.title = `${formatFullDate(parseDate(week && week.week_start)) || "Week"} · ${formatNumber(sessions)} ${sessions === 1 ? "session" : "sessions"} · ${week && week.cost_lower_bound === true ? "at least " : ""}${formatReportDollars(week && week.total_cost_usd)}`;
      fragment.append(bar);
    });
    elements.reportSparkline.replaceChildren(fragment);
    elements.reportSparkline.hidden = weeks.length === 0;
  }

  function renderReportIssues(issues) {
    focusRegistry.missionPackTriggers.clear();
    focusRegistry.reportEvidenceTriggers.clear();
    const fragment = document.createDocumentFragment();
    issues.forEach((issue, index) => {
      fragment.append(createReportIssueCard(issue, index));
    });
    elements.briefActionList.replaceChildren(fragment);
    elements.briefActionsEmpty.hidden = issues.length !== 0;
  }

  function createReportIssueCard(issue, index) {
    const card = createElement("article", "brief-action-card report-issue-card");
    card.dataset.priority = index === 0 ? "primary" : "secondary";
    card.append(
      createElement(
        "h3",
        "report-issue-headline",
        humanizeReportHeadline(issue.headline),
      ),
      createElement(
        "p",
        "brief-evidence report-issue-metrics",
        [
          formatIssueDollarCost(issue.cost),
          formatIssueMinutes(issue.cost),
          `${formatNumber(issue.session_count)} ${toFiniteNumber(issue.session_count) === 1 ? "session" : "sessions"}`,
          reportTrendSummary(issue.trend),
        ]
          .filter(Boolean)
          .join(" · "),
      ),
    );
    if (issue.excerpts.length) {
      card.append(createReportIssuePreview(issue.excerpts[0]));
    }
    const fix = createElement("div", "report-issue-fix");
    fix.append(
      createElement(
        "strong",
        "report-issue-fix-target",
        `Suggested fix · ${compactDisplayPath(issue.suggested_fix.target_file) || "Agent instructions"}`,
      ),
      createElement(
        "p",
        "report-issue-fix-rationale",
        readText(issue.suggested_fix.rationale) ||
          "Add a durable project instruction for this pattern.",
      ),
    );
    card.append(fix);
    const actions = createElement("div", "report-card-actions");
    const prepareButton = createElement(
      "button",
      "primary-button",
      "Use in next session",
    );
    prepareButton.type = "button";
    prepareButton.setAttribute("aria-haspopup", "dialog");
    prepareButton.setAttribute("aria-controls", "mission-pack-dialog");
    prepareButton.addEventListener("click", () => {
      openMissionPackDrawer(issue);
    });
    const evidenceButton = createElement(
      "button",
      "secondary-button",
      "Show evidence",
    );
    evidenceButton.type = "button";
    evidenceButton.setAttribute("aria-haspopup", "dialog");
    evidenceButton.setAttribute("aria-controls", "report-evidence-dialog");
    evidenceButton.addEventListener("click", () => {
      openReportEvidenceDrawer(issue);
    });
    const issueID = readText(issue.issue_id);
    if (issueID) {
      focusRegistry.missionPackTriggers.set(issueID, prepareButton);
      focusRegistry.reportEvidenceTriggers.set(issueID, evidenceButton);
    }
    actions.append(prepareButton, evidenceButton);
    card.append(actions);
    return card;
  }

  function createReportIssuePreview(excerpt) {
    const wrapper = createElement("div", "report-issue-preview");
    const citation = isRecord(excerpt && excerpt.citation)
      ? excerpt.citation
      : {};
    const role = readableLabel(excerpt && excerpt.role, "Transcript");
    const tool = readText(excerpt && excerpt.tool_name);
    const turn = Number.isFinite(Number(citation.turn_index))
      ? `turn ${formatNumber(citation.turn_index)}`
      : "";
    const occurredAt = formatFullDate(parseDate(citation.occurred_at));
    wrapper.append(
      createElement(
        "p",
        "report-issue-preview-citation",
        [role, tool, turn, occurredAt].filter(Boolean).join(" · "),
      ),
      createElement(
        "blockquote",
        "",
        truncateReportPreview(
          readText(excerpt && excerpt.text) || "Excerpt unavailable",
        ),
      ),
    );
    return wrapper;
  }

  function truncateReportPreview(value) {
    const characters = Array.from(readText(value));
    if (characters.length <= 220) return characters.join("");
    const candidate = characters.slice(0, 220).join("");
    const boundary = candidate.search(/\s+\S*$/);
    const clipped = boundary >= 160 ? candidate.slice(0, boundary) : candidate;
    return `${clipped.trimEnd()}…`;
  }

  function compactDisplayPath(value) {
    const path = readText(value).trim().replace(/[\\/]+$/, "");
    if (!path) return "";
    const segments = path.split(/[\\/]/).filter(Boolean);
    return segments[segments.length - 1] || path;
  }

  function humanizeReportHeadline(value) {
    let headline = readText(value).trim();
    if (!headline) return "Recurring agent pattern";
    headline = headline.replace(/`([^`]*[\\/][^`]*)`/g, (_, path) => {
      return `\`${compactDisplayPath(path)}\``;
    });
    headline = headline.replace(
      /(?:\/Users\/|\/home\/|\/private\/|[A-Za-z]:\\)[^\s,;:()[\]{}]+/g,
      (path) => compactDisplayPath(path),
    );
    headline = headline.replace(/\b1 sessions\b/gi, "1 session");
    return headline;
  }

  function clearMissionPackCache() {
    state.missionPackCacheGeneration += 1;
    missionPackCacheByID.clear();
    missionPackIDByIssue.clear();
  }

  function openMissionPackDrawer(issue) {
    const issueID = readText(issue && issue.issue_id);
    if (!issueID) return;
    state.dialogReturnFocus = { type: "mission-pack", issueID };
    state.activeModal = "mission-pack";
    state.missionPackIssue = issue;
    state.missionPack = null;
    elements.missionPackTitle.textContent = "Agent guidance";
    resetMissionPackDrawer();
    openModalLayer(
      elements.missionPackModalLayer,
      elements.missionPackDialog,
      elements.missionPackClose,
    );
    void loadMissionPack(issueID);
  }

  function resetMissionPackDrawer() {
    elements.missionPackLoading.hidden = false;
    elements.missionPackError.hidden = true;
    elements.missionPackErrorDetail.textContent = "";
    elements.missionPackContent.hidden = true;
    elements.missionPackMetadata.replaceChildren();
    elements.missionPackSections.replaceChildren();
    elements.missionPackSize.textContent = "";
    elements.missionPackCopyClaude.disabled = true;
    elements.missionPackCopyCodex.disabled = true;
    elements.missionPackShowEvidence.disabled = !readText(
      state.missionPackIssue && state.missionPackIssue.issue_id,
    );
  }

  async function loadMissionPack(issueID) {
    const cachedPackID = missionPackIDByIssue.get(issueID);
    const cachedPack = cachedPackID
      ? missionPackCacheByID.get(cachedPackID)
      : null;
    if (cachedPack) {
      state.missionPack = cachedPack;
      renderMissionPack(cachedPack);
      return;
    }
    if (
      state.missionPackRequestController &&
      typeof state.missionPackRequestController.abort === "function"
    ) {
      state.missionPackRequestController.abort();
    }
    const controller =
      typeof globalThis.AbortController === "function"
        ? new globalThis.AbortController()
        : null;
    state.missionPackRequestController = controller;
    const requestGeneration = ++state.missionPackRequestGeneration;
    const cacheGeneration = state.missionPackCacheGeneration;
    try {
      const response = await apiGet(
        `/v1/mission-pack?issue_id=${encodeURIComponent(issueID)}&intent=general`,
        controller ? controller.signal : undefined,
      );
      if (
        requestGeneration !== state.missionPackRequestGeneration ||
        state.activeModal !== "mission-pack" ||
        readText(state.missionPackIssue && state.missionPackIssue.issue_id) !==
          issueID
      ) {
        return;
      }
      const pack = requireMissionPack(response);
      state.missionPack = pack;
      if (cacheGeneration === state.missionPackCacheGeneration) {
        missionPackCacheByID.set(pack.pack_id, pack);
        missionPackIDByIssue.set(issueID, pack.pack_id);
      }
      renderMissionPack(pack);
    } catch (error) {
      const aborted = Boolean(controller && controller.signal.aborted);
      if (
        aborted ||
        requestGeneration !== state.missionPackRequestGeneration ||
        state.activeModal !== "mission-pack"
      ) {
        return;
      }
      elements.missionPackLoading.hidden = true;
      elements.missionPackContent.hidden = true;
      elements.missionPackError.hidden = false;
      elements.missionPackErrorDetail.textContent = customerErrorMessage(
        error,
        "Belay could not prepare this Mission Pack.",
      );
    } finally {
      if (state.missionPackRequestController === controller) {
        state.missionPackRequestController = null;
      }
    }
  }

  function requireMissionPack(response) {
    if (
      !isRecord(response) ||
      readText(response.schema_version) !== "belay.mission-pack.v1" ||
      !readText(response.pack_id) ||
      !isRecord(response.project) ||
      !isRecord(response.source_state) ||
      !Array.isArray(response.known_traps) ||
      !Array.isArray(response.operating_rules) ||
      !Array.isArray(response.verification) ||
      !Array.isArray(response.completion_checklist) ||
      !Array.isArray(response.warnings)
    ) {
      throw new Error("Local API returned an invalid Mission Pack.");
    }
    return response;
  }

  function renderMissionPack(pack) {
    const isEmpty = readText(pack.status).toLowerCase() === "empty";
    elements.missionPackLoading.hidden = true;
    elements.missionPackError.hidden = true;
    elements.missionPackContent.hidden = false;
    elements.missionPackCopyClaude.disabled = isEmpty;
    elements.missionPackCopyCodex.disabled = isEmpty;
    const fragment = document.createDocumentFragment();
    if (isEmpty) {
      elements.missionPackTitle.textContent = "Agent guidance";
      elements.missionPackMetadata.replaceChildren();
      fragment.append(
        createElement(
          "p",
          "mission-pack-empty",
          "Belay found no useful guidance for this session.",
        ),
      );
    } else {
      const projectLabel = readText(pack.project.label);
      elements.missionPackTitle.textContent = projectLabel
        ? `Guidance for ${projectLabel}`
        : "Agent guidance";
      renderMissionPackMetadata(pack);
      if (pack.known_traps.length) {
        appendMissionPackGuidanceSection(
          fragment,
          "Known traps",
          pack.known_traps,
          createMissionPackTrap,
          "",
        );
      }
      if (pack.operating_rules.length) {
        appendMissionPackGuidanceSection(
          fragment,
          "Operating rules",
          pack.operating_rules,
          createMissionPackRule,
          "",
        );
      }
      if (pack.verification.length) {
        appendMissionPackGuidanceSection(
          fragment,
          "Verification",
          pack.verification,
          createMissionPackCommand,
          "",
        );
      }
      if (pack.completion_checklist.length) {
        appendMissionPackGuidanceSection(
          fragment,
          "Completion checklist",
          pack.completion_checklist,
          createMissionPackChecklistItem,
          "",
        );
      }
    }
    elements.missionPackSections.replaceChildren(fragment);
    elements.missionPackSize.textContent = "";
  }

  function renderMissionPackMetadata(pack) {
    const rows = [];
    const branch = readText(pack.project.branch);
    const intent = readText(pack.intent);
    if (branch) rows.push(["Branch", branch]);
    if (intent) rows.push(["Intent", readableLabel(intent, "General")]);
    const fragment = document.createDocumentFragment();
    rows.forEach(([label, value]) => {
      const row = createElement("div");
      row.append(
        createElement("dt", "", label),
        createElement("dd", "", value),
      );
      fragment.append(row);
    });
    elements.missionPackMetadata.replaceChildren(fragment);
  }

  function appendMissionPackGuidanceSection(
    parent,
    title,
    values,
    itemBuilder,
    emptyMessage,
  ) {
    const section = createElement("section", "mission-pack-section");
    section.append(createElement("h3", "", title));
    const list = createElement("div", "mission-pack-list");
    if (Array.isArray(values) && values.length) {
      values.forEach((value) => list.append(itemBuilder(value)));
    } else {
      list.append(createElement("p", "mission-pack-empty", emptyMessage));
    }
    section.append(list);
    parent.append(section);
  }

  function createMissionPackTrap(value) {
    const item = createElement("article", "mission-pack-item");
    const cost = missionPackCost(value);
    item.append(
      createElement("h4", "mission-pack-clamp", readText(value.title)),
    );
    if (cost) {
      item.append(createElement("p", "mission-pack-item-meta", cost));
    }
    return item;
  }

  function missionPackCost(value) {
    if (typeof value.wasted_usd === "number") {
      const amount = formatReportDollars(value.wasted_usd);
      return value.cost_lower_bound === true ? `At least ${amount}` : amount;
    }
    if (toFiniteNumber(value.wasted_tokens) > 0) {
      return `${formatNumber(value.wasted_tokens)} attributed tokens`;
    }
    return "";
  }

  function createMissionPackRule(value) {
    const item = createElement("article", "mission-pack-item");
    item.append(createMissionPackExpandableText(readText(value.guidance)));
    const badges = createElement("div", "mission-pack-badges");
    if (readText(value.target_file)) {
      badges.append(
        createElement(
          "span",
          "mission-pack-badge",
          `Target: ${compactDisplayPath(value.target_file)}`,
        ),
      );
    }
    if (badges.childNodes.length) item.append(badges);
    return item;
  }

  function createMissionPackExpandableText(value) {
    const text = value || "Rule text unavailable";
    if (Array.from(text).length <= 220) {
      return createElement("p", "mission-pack-guidance", text);
    }
    const details = createElement("details", "mission-pack-expandable");
    details.append(
      createElement("summary", "mission-pack-clamp", text),
      createElement("p", "mission-pack-guidance", text),
    );
    return details;
  }

  function createMissionPackCommand(value) {
    const item = createElement("article", "mission-pack-item");
    item.append(
      createElement("code", "mission-pack-command", readText(value.command)),
    );
    return item;
  }

  function createMissionPackChecklistItem(value) {
    const item = createElement("p", "mission-pack-checklist");
    item.append(
      createElement("span", "mission-pack-checkbox", "□"),
      document.createTextNode(readText(value.text) || "Checklist item unavailable"),
    );
    return item;
  }

  function closeMissionPackDrawer(restoreFocus) {
    if (
      state.missionPackRequestController &&
      typeof state.missionPackRequestController.abort === "function"
    ) {
      state.missionPackRequestController.abort();
    }
    state.missionPackRequestController = null;
    state.missionPackRequestGeneration += 1;
    closeModalLayer(
      "mission-pack",
      elements.missionPackModalLayer,
      restoreFocus,
    );
    state.missionPackIssue = null;
    state.missionPack = null;
    elements.missionPackMetadata.replaceChildren();
    elements.missionPackSections.replaceChildren();
  }

  function openReportEvidenceDrawer(issue) {
    const issueID = readText(issue && issue.issue_id);
    state.reportEvidenceIssueID = issueID;
    state.dialogReturnFocus = { type: "report-evidence", issueID };
    state.activeModal = "report-evidence";
    elements.reportEvidenceTitle.textContent =
      humanizeReportHeadline(issue && issue.headline);
    const excerpts = Array.isArray(issue && issue.excerpts)
      ? issue.excerpts
      : [];
    const excerptFragment = document.createDocumentFragment();
    excerpts.forEach((excerpt) => {
      excerptFragment.append(createReportEvidenceExcerpt(excerpt));
    });
    if (!excerpts.length) {
      excerptFragment.append(
        createElement("p", "overview-empty", "No transcript excerpts available."),
      );
    }
    elements.reportEvidenceExcerpts.replaceChildren(excerptFragment);
    renderReportEvidenceFix(issue && issue.suggested_fix);
    elements.reportEvidenceCopyClaude.disabled = !issueID;
    elements.reportEvidenceCopyCodex.disabled = !issueID;
    openModalLayer(
      elements.reportEvidenceModalLayer,
      elements.reportEvidenceDialog,
      elements.reportEvidenceClose,
    );
  }

  function createReportEvidenceExcerpt(excerpt) {
    const wrapper = createElement("article", "cost-issue-excerpt");
    const citation = isRecord(excerpt && excerpt.citation)
      ? excerpt.citation
      : {};
    const role = readableLabel(excerpt && excerpt.role, "Transcript");
    const tool = readText(excerpt && excerpt.tool_name);
    const session = readText(citation.session_key);
    const turn = Number.isFinite(Number(citation.turn_index))
      ? `turn ${formatNumber(citation.turn_index)}`
      : "turn unavailable";
    const occurredAt =
      formatFullDate(parseDate(citation.occurred_at)) || "time unavailable";
    const source = readText(citation.source_file_id);
    const offset = Number.isFinite(Number(citation.jsonl_byte_offset))
      ? `byte ${formatNumber(citation.jsonl_byte_offset)}`
      : "byte offset unavailable";
    wrapper.append(
      createElement(
        "p",
        "cost-issue-citation report-evidence-citation",
        [role, tool, turn, occurredAt]
          .filter(Boolean)
          .join(" · "),
      ),
      createElement(
        "blockquote",
        "",
        readText(excerpt && excerpt.text) || "Excerpt unavailable",
      ),
    );
    const technicalCitation = createElement(
      "details",
      "report-evidence-technical",
    );
    technicalCitation.append(
      createElement("summary", "", "Technical citation"),
      createElement(
        "code",
        "",
        [session, source, offset].filter(Boolean).join(" · ") ||
          "No additional citation details",
      ),
    );
    wrapper.append(technicalCitation);
    return wrapper;
  }

  function renderReportEvidenceFix(value) {
    const fix = isRecord(value) ? value : {};
    const fragment = document.createDocumentFragment();
    [
      ["Kind", readableLabel(fix.kind, "Project instruction")],
      [
        "Target file",
        compactDisplayPath(fix.target_file) || "Agent instructions",
      ],
      [
        "Rationale",
        readText(fix.rationale) ||
          "Add a durable project instruction for this pattern.",
      ],
    ].forEach(([label, detail]) => {
      const row = createElement("div");
      row.append(
        createElement("dt", "", label),
        createElement("dd", "", detail),
      );
      fragment.append(row);
    });
    elements.reportEvidenceFix.replaceChildren(fragment);
  }

  function closeReportEvidenceDrawer(restoreFocus) {
    closeModalLayer(
      "report-evidence",
      elements.reportEvidenceModalLayer,
      restoreFocus,
    );
    elements.reportEvidenceExcerpts.replaceChildren();
    elements.reportEvidenceFix.replaceChildren();
    state.reportEvidenceIssueID = "";
  }

  function reportTrendSummary(trend) {
    if (!Array.isArray(trend) || trend.length < 2) return "";
    const values = trend.slice(-8).map((week) => toFiniteNumber(week && week.count));
    const latest = values[values.length - 1];
    const prior = values.slice(0, -1).reduce((sum, value) => sum + value, 0);
    const average = prior / Math.max(1, values.length - 1);
    if (latest > average) return "trending up";
    if (latest < average) return "trending down";
    return "steady";
  }

  function renderReportFixes(fixes) {
    const values = Array.isArray(fixes) ? fixes.slice(0, 20) : [];
    const fragment = document.createDocumentFragment();
    values.forEach((status) => {
      const fix = isRecord(status && status.fix) ? status.fix : {};
      const card = createElement("article", "brief-session-card report-fix-card");
      const stateLabel =
        readText(fix.state) === "applied" ? "Applied" : "Proposed";
      card.append(
        createElement(
          "strong",
          "",
          `${stateLabel}: ${compactDisplayPath(fix.target_file) || "configuration"}`,
        ),
        createElement(
          "p",
          "",
          readText(fix.rule_text) || "Fix rule unavailable",
        ),
        createElement(
          "small",
          "",
          status && status.verification_state === "deferred"
            ? "Recurrence and cost verification deferred"
            : "Verification pending",
        ),
      );
      fragment.append(card);
    });
    elements.briefRecentList.replaceChildren(fragment);
    elements.briefRecentEmpty.hidden = values.length !== 0;
  }

  function renderReportWaste(waste) {
    const hasShare = waste && typeof waste.share_percent === "number";
    const share = hasShare
      ? `${formatNumber(waste.share_percent)}%`
      : formatReportDollars(waste && waste.attributed_usd);
    const prefix = waste && waste.lower_bound === true ? "At least " : "";
    elements.briefAgentSummary.replaceChildren(
      createElement("strong", "report-waste-number", `${prefix}${share}`),
      createElement(
        "p",
        "",
        hasShare
          ? `${formatReportDollars(waste && waste.attributed_usd)} attributed to detected issues`
          : waste && waste.total_incomplete === true
            ? "Attributed waste; total spend is incomplete, so no percentage is shown."
            : waste && waste.overlap_capped === true
              ? "Attributed spend exceeds currently known priced spend, so no percentage is shown."
              : "Attributed to detected issues.",
      ),
    );
  }

  function renderReportAbout(about) {
    const notes = Array.isArray(about && about.notes) ? about.notes : [];
    elements.briefCoverage.open = false;
    elements.briefCoverageSummary.textContent =
      "Coverage, lower bounds, and calculation notes.";
    const noteFragment = document.createDocumentFragment();
    notes.forEach((note) => {
      noteFragment.append(createElement("li", "", readText(note)));
    });
    elements.briefLimitations.replaceChildren(noteFragment);
    const coverage = isRecord(about && about.transcript_coverage)
      ? about.transcript_coverage
      : {};
    const sourceFragment = document.createDocumentFragment();
    [
      ["Sessions with transcript", coverage.with_transcript],
      ["Partial transcript", coverage.partial],
      ["Without transcript", coverage.without_transcript],
      ["Transcript only", coverage.transcript_only],
    ].forEach(([label, value]) => {
      const row = createElement("div");
      row.append(
        createElement("dt", "", label),
        createElement("dd", "", formatNumber(value)),
      );
      sourceFragment.append(row);
    });
    elements.briefSources.replaceChildren(sourceFragment);
  }

  function formatReportDollars(value) {
    const amount = Number(value);
    if (!Number.isFinite(amount)) return "$0.00";
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency: "USD",
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(amount);
  }

  function renderBriefSummary(summary) {
    if (!isRecord(summary)) {
      elements.briefSessionCount.textContent = "—";
      elements.briefAgentCount.textContent = "—";
      elements.briefOutcomeCount.textContent = "—";
      elements.briefLatestActivity.textContent = "—";
      return;
    }
    const sessions = toFiniteNumber(summary.evaluated_session_count);
    const harnesses = Array.isArray(summary.harnesses)
      ? summary.harnesses.map(readText).filter(Boolean)
      : [];
    const outcomes = isRecord(summary.outcomes) ? summary.outcomes : {};
    const reported =
      toFiniteNumber(outcomes.succeeded) +
      toFiniteNumber(outcomes.failed) +
      toFiniteNumber(outcomes.interrupted);
    elements.briefSessionCount.textContent =
      `${formatNumber(sessions)}${summary.session_count_is_lower_bound === true ? "+" : ""}`;
    elements.briefAgentCount.textContent = formatNumber(harnesses.length);
    elements.briefOutcomeCount.textContent = formatNumber(reported);
    elements.briefLatestActivity.textContent =
      formatRelativeTime(summary.latest_observed_at) || "Unavailable";
  }

  function renderBriefActions(brief) {
    focusRegistry.briefActions.clear();
    const cards = brief.action_cards.slice(0, 5);
    const fragment = document.createDocumentFragment();
    cards.forEach((card, index) => {
      fragment.append(createBriefActionCard(card, index));
    });
    elements.briefActionList.replaceChildren(fragment);
    const summary = brief.recent_summary;
    const sessionCount = toFiniteNumber(summary.evaluated_session_count);
    const complete = brief.coverage.complete === true;
    const sessionsAvailable =
      briefSourceStatus(brief, "sessions") !== "unavailable";
    const initializing = initializationInProgress();
    elements.briefActionsEmpty.hidden = cards.length !== 0;
    if (cards.length === 0) {
      elements.briefActionsEmptyTitle.textContent =
        !sessionsAvailable
          ? "Recent sessions could not be evaluated"
          : initializing && sessionCount === 0
            ? "No recent activity has been imported yet"
          : sessionCount === 0
          ? "No recorded agent activity in the last 24 hours"
          : complete
            ? "No reviewed action was identified in the evaluated activity"
            : "No reviewed action is available from the activity evaluated so far";
      elements.briefActionsEmptyDetail.textContent = experiencePageText(
        !sessionsAvailable
          ? "Attention may still contain reviewed findings, and stored activity remains available in Sessions."
          : initializing && sessionCount === 0
            ? "Initial import is still in progress; this result is partial and will update automatically."
            : sessionCount === 0
              ? "Older stored sessions remain available in Sessions."
              : complete
                ? "This is not a claim that all activity was successful or problem-free."
                : "The brief is limited; review Coverage and limitations for what was not fully evaluated.",
      );
    }
  }

  function createBriefActionCard(card, index) {
    const cardID = readText(card && card.card_id) || `brief-action-${index}`;
    const article = createElement("article", "brief-action-card");
    const heading = createElement(
      "h3",
      "",
      readText(card && card.title) || "Review recorded activity",
    );
    article.append(
      heading,
      createElement(
        "p",
        "brief-observation",
        readText(card && card.observation) ||
          "Belay recorded activity that may deserve review.",
      ),
    );
    const evidence = briefEvidenceSummary(card && card.evidence);
    if (evidence) {
      article.append(createElement("p", "brief-evidence", evidence));
    }
    const limitation = readText(card && card.limitation);
    if (limitation) {
      article.append(createElement("p", "brief-limitation", limitation));
    }
    const nextStep = isRecord(card && card.next_step) ? card.next_step : {};
    const button = createElement(
      "button",
      "primary-button",
      readText(nextStep.label) || "Review evidence",
    );
    button.type = "button";
    const reference = { type: "brief-action", key: cardID };
    button.addEventListener("click", () => {
      state.briefSelectionID = cardID;
      void navigateSupportedNextStep(card, nextStep, reference);
    });
    if (!supportedNextStep(nextStep)) {
      button.disabled = true;
      button.textContent = "Next step unavailable";
    }
    focusRegistry.briefActions.set(cardID, button);
    article.append(button);
    return article;
  }

  function briefEvidenceSummary(value) {
    if (!isRecord(value)) return "";
    const parts = [];
    const sessionCount = toFiniteNumber(value.session_count);
    const occurrenceCount = toFiniteNumber(value.occurrence_count);
    const harnesses = Array.isArray(value.harnesses)
      ? value.harnesses.map(displayHarness).filter(Boolean)
      : [];
    if (sessionCount) {
      parts.push(
        `${formatNumber(sessionCount)} ${
          sessionCount === 1 ? "session" : "sessions"
        }`,
      );
    }
    if (occurrenceCount) {
      parts.push(
        `${formatNumber(occurrenceCount)} ${
          occurrenceCount === 1 ? "observation" : "observations"
        }`,
      );
    }
    if (harnesses.length) parts.push(harnesses.join(", "));
    if (parseDate(value.last_observed_at)) {
      parts.push(`latest ${formatRelativeTime(value.last_observed_at)}`);
    }
    const outcome = normalizeOutcome(value.session_outcome);
    if (explicitOutcomes.has(outcome)) {
      parts.push(`agent reported ${explicitOutcomeLabel(outcome).toLowerCase()}`);
    }
    const eventCount = Number(value.event_count);
    if (Number.isFinite(eventCount) && eventCount >= 0) {
      parts.push(`${formatNumber(eventCount)} recorded events`);
    }
    return parts.join(" · ");
  }

  function renderBriefRecentWork(brief) {
    focusRegistry.briefSessions.clear();
    const sessions = brief.recent_work.slice(0, 8);
    const fragment = document.createDocumentFragment();
    sessions.forEach((session, index) => {
      fragment.append(createBriefSessionCard(session, index));
    });
    elements.briefRecentList.replaceChildren(fragment);
    elements.briefRecentEmpty.hidden = sessions.length !== 0;
    if (sessions.length === 0) {
      const sessionsAvailable =
        briefSourceStatus(brief, "sessions") !== "unavailable";
      const initializing = initializationInProgress();
      elements.briefRecentEmptyTitle.textContent = !sessionsAvailable
        ? "Recent work is unavailable"
        : initializing
          ? "No recent activity has been imported yet"
          : "No recorded agent activity in the last 24 hours";
      elements.briefRecentEmptyDetail.textContent = experiencePageText(
        !sessionsAvailable
          ? "Open Sessions to inspect stored activity directly."
          : initializing
            ? "Initial import is still in progress; this result is partial and will update automatically."
            : "Older stored sessions remain available in Sessions.",
      );
    }
  }

  function initializationInProgress() {
    return (
      readText(state.initialization && state.initialization.state) ===
      "initializing"
    );
  }

  function briefSourceStatus(brief, sourceName) {
    const sources =
      brief && brief.coverage && Array.isArray(brief.coverage.sources)
        ? brief.coverage.sources
        : [];
    const source = sources.find(
      (candidate) => readText(candidate && candidate.source) === sourceName,
    );
    return readText(source && source.status);
  }

  function createBriefSessionCard(session, index) {
    const sessionID = readText(session && session.session_id);
    const card = createElement("article", "brief-session-card");
    const button = createElement("button", "brief-session-button");
    button.type = "button";
    const harness = displayHarness(session && session.harness);
    const outcome = normalizeOutcome(session && session.outcome);
    const top = createElement("span", "brief-session-top");
    top.append(
      createElement("strong", "", `${harness} session`),
      sessionOutcomeBadge(outcome),
    );
    button.append(
      top,
      createElement(
        "span",
        "brief-session-time",
        formatFullDate(parseDate(session && session.started_at)) ||
          "Session start unavailable",
      ),
      createElement(
        "span",
        "brief-session-meta",
        [
          formatEventCount(session && session.event_count),
          briefHistoryLabel(session && session.history),
          readText(session && session.outcome_explanation),
        ]
          .filter(Boolean)
          .join(" · "),
      ),
    );
    const key = sessionID || `brief-session-${index}`;
    const reference = { type: "brief-session", key };
    button.addEventListener("click", () => {
      const nextStep = isRecord(session && session.next_step)
        ? session.next_step
        : { kind: "open_session", session_id: sessionID };
      void navigateSupportedNextStep(session, nextStep, reference);
    });
    button.disabled = !sessionID;
    focusRegistry.briefSessions.set(key, button);
    card.append(button);
    return card;
  }

  function briefHistoryLabel(value) {
    switch (readText(value)) {
      case "historical":
        return "Imported history";
      case "mixed":
        return "Live + imported history";
      case "live":
        return "Live";
      default:
        return "Collection unavailable";
    }
  }

  function renderBriefAcrossAgents(summary) {
    const container = elements.briefAgentSummary;
    const harnesses =
      isRecord(summary) && Array.isArray(summary.harnesses)
        ? summary.harnesses.map(displayHarness).filter(Boolean)
        : [];
    const outcomes =
      isRecord(summary) && isRecord(summary.outcomes) ? summary.outcomes : {};
    const history =
      isRecord(summary) && isRecord(summary.history) ? summary.history : {};
    const fragment = document.createDocumentFragment();
    fragment.append(
      createBriefSummaryRow(
        "Agents",
        harnesses.length ? harnesses.join(", ") : "No agent activity evaluated",
      ),
      createBriefSummaryRow(
        "Reported outcomes",
        [
          `${formatNumber(outcomes.succeeded)} succeeded`,
          `${formatNumber(outcomes.failed)} failed`,
          `${formatNumber(outcomes.interrupted)} interrupted`,
          `${formatNumber(
            toFiniteNumber(outcomes.incomplete) +
              toFiniteNumber(outcomes.unknown),
          )} not reported`,
        ].join(" · "),
      ),
      createBriefSummaryRow(
        "Collection",
        [
          `${formatNumber(history.live)} live`,
          `${formatNumber(history.historical)} imported`,
          `${formatNumber(history.mixed)} mixed`,
        ].join(" · "),
      ),
    );
    container.replaceChildren(fragment);
  }

  function createBriefSummaryRow(label, value) {
    const row = createElement("div", "brief-agent-row");
    row.append(
      createElement("strong", "", label),
      createElement("span", "", value),
    );
    return row;
  }

  function renderBriefCoverage(brief) {
    const coverage = brief.coverage;
    const limitations = coverage.limitations.slice(0, 20);
    const sources = coverage.sources.slice(0, 10);
    elements.briefCoverage.open =
      readText(brief.status) === "limited" || coverage.complete !== true;
    elements.briefCoverageSummary.textContent =
      coverage.complete === true
        ? state.experience === "value-first"
          ? "All bounded Home sources completed for this view."
          : "All bounded brief sources completed for this view."
        : "Some activity or analysis could not be fully evaluated.";
    const limitationFragment = document.createDocumentFragment();
    limitations.forEach((limitation) => {
      const message = readText(limitation && limitation.message);
      if (message) limitationFragment.append(createElement("li", "", message));
    });
    if (!limitations.length && coverage.complete !== true) {
      limitationFragment.append(
        createElement(
          "li",
          "",
          experiencePageText(
            "Some sources were limited; use Attention or Sessions for the available detail.",
          ),
        ),
      );
    }
    elements.briefLimitations.replaceChildren(limitationFragment);
    const sourceFragment = document.createDocumentFragment();
    sources.forEach((source) => {
      const row = createElement("div");
      row.append(
        createElement("dt", "", briefSourceLabel(source && source.source)),
        createElement(
          "dd",
          "",
          briefSourceSummary(source),
        ),
      );
      sourceFragment.append(row);
    });
    elements.briefSources.replaceChildren(sourceFragment);
  }

  function briefSourceLabel(value) {
    const labels = {
      sessions:
        state.experience === "value-first" ? "History" : "Sessions",
      attention_families: "Reviewed findings",
      evidence_gaps: "Evidence gaps",
    };
    return labels[readText(value)] || "Local activity";
  }

  function briefSourceSummary(source) {
    if (!isRecord(source)) return "Unavailable";
    const status = readText(source.status);
    const labels = {
      complete: "Available",
      truncated: "Limited",
      ready: "Available",
      limited: "Limited",
      unavailable: "Unavailable",
      failed: "Unavailable",
    };
    const parts = [labels[status] || "Status unavailable"];
    const count = Number(source.evaluated_count);
    if (Number.isFinite(count) && count >= 0) {
      parts.push(`${formatNumber(count)} evaluated`);
    }
    if (source.has_more === true) parts.push("more records exist");
    if (parseDate(source.as_of)) {
      parts.push(`through ${formatFullDate(parseDate(source.as_of))}`);
    }
    return parts.join(" · ");
  }

  function supportedNextStep(nextStep) {
    if (!isRecord(nextStep)) return false;
    const kind = readText(nextStep.kind);
    if (kind === "open_session") return Boolean(readText(nextStep.session_id));
    if (kind === "open_issue") {
      return Boolean(readText(nextStep.issue_id) && readCursor(nextStep.view_cursor));
    }
    if (kind === "open_attention_family") {
      return Boolean(
        readText(nextStep.family_id) &&
          readText(nextStep.issue_id) &&
          readCursor(nextStep.view_cursor),
      );
    }
    return false;
  }

  async function navigateSupportedNextStep(card, nextStep, returnFocus) {
    if (!supportedNextStep(nextStep)) return false;
    const kind = readText(nextStep.kind);
    if (kind === "open_session") {
      state.sessionReturnFocus = returnFocus;
      state.sessionReturnView =
        returnFocus && returnFocus.type.startsWith("brief-")
          ? "brief"
          : "sessions";
      openSession(readText(nextStep.session_id), null);
      return true;
    }
    const catalog = briefNavigationCatalog(card);
    if (kind === "open_attention_family") {
      const family = {
        family_id: readText(nextStep.family_id),
        kind: "mapped_upstream",
        representative_issue_id: readText(nextStep.issue_id),
        view_cursor: readCursor(nextStep.view_cursor),
        catalog,
        severity: readText(card && card.severity) || "info",
        confidence: "",
        session_count: toFiniteNumber(card && card.evidence && card.evidence.session_count),
        occurrence_count: toFiniteNumber(
          card && card.evidence && card.evidence.occurrence_count,
        ),
        harnesses:
          card && card.evidence && Array.isArray(card.evidence.harnesses)
            ? card.evidence.harnesses
            : [],
        last_observed_at:
          card && card.evidence && card.evidence.last_observed_at,
        analysis_status: "current",
      };
      setActiveView("attention", false);
      const result = selectAttentionFamily(family, true);
      state.familyReturnFocus = returnFocus;
      return result;
    }
    const issue = {
      issue_id: readText(nextStep.issue_id),
      title_code: readText(card && card.title_code),
      severity: readText(card && card.severity) || "info",
      confidence: "",
      session_count: toFiniteNumber(card && card.evidence && card.evidence.session_count),
      occurrence_count: toFiniteNumber(
        card && card.evidence && card.evidence.occurrence_count,
      ),
      harnesses:
        card && card.evidence && Array.isArray(card.evidence.harnesses)
          ? card.evidence.harnesses
          : [],
      last_observed_at:
        card && card.evidence && card.evidence.last_observed_at,
      analysis_status: "current",
    };
    setActiveView("attention", false);
    return selectIssue(
      issue,
      readText(card && card.kind) === "evidence_gap"
        ? "evidence_gap"
        : "issue",
      true,
      {
      viewCursor: readCursor(nextStep.view_cursor),
      catalog,
      returnFocus,
      },
    );
  }

  function briefNavigationCatalog(card) {
    return {
      display_title: readText(card && card.title) || "Finding",
      observation_statement:
        readText(card && card.observation) ||
        "Belay recorded activity that may deserve review.",
      caveat:
        readText(card && card.limitation) ||
        "Review the supporting evidence before deciding what to do.",
      next_evidence_action: "inspect_cited_events",
    };
  }

  function prepareValueFirstAttentionLayout() {
    const analysisDisclosure = createElement(
      "details",
      "attention-disclosure",
    );
    analysisDisclosure.id = "analysis-disclosure";
    analysisDisclosure.append(
      createElement("summary", "", "Analysis status and coverage"),
      elements.coveragePanel,
    );
    const filterDisclosure = createElement(
      "details",
      "attention-disclosure",
    );
    filterDisclosure.id = "attention-filter-disclosure";
    filterDisclosure.append(
      createElement(
        "summary",
        "",
        experienceCopy[state.experience].attentionFilterSummary,
      ),
      elements.attentionFilters,
    );
    elements.safetyContent = createElement("div", "safety-content");
    elements.safetyContent.id = "safety-content";
    elements.safetyContent.append(
      elements.stableIssuesSection,
      elements.evidenceGapsSection,
      elements.fixMonitoringSection,
      analysisDisclosure,
      filterDisclosure,
    );
    elements.attentionScroll.replaceChildren(
      elements.costIssuesSection,
      elements.safetyContent,
    );
    setAttentionMode("issues");
  }

  function bindEvents() {
    elements.updateNotice.addEventListener("click", openUpdateDialog);
    elements.updateClose.addEventListener("click", () => {
      closeUpdateDialog(true);
    });
    elements.updateCopyCommand.addEventListener("click", () => {
      void copyText(updateInstallCommand, elements.updateCopyCommand);
    });
    elements.updateRemind.addEventListener("click", () => {
      void submitUpdateAction("remind");
    });
    elements.updateSkip.addEventListener("click", () => {
      void submitUpdateAction("dismiss");
    });
    elements.updateModalLayer.addEventListener("keydown", handleModalKeydown);
    elements.attentionFilters.addEventListener("submit", (event) => {
      event.preventDefault();
      state.issueFilters.harness = elements.issueFilterHarness.value.trim();
      resetAndLoadAttention();
    });
    elements.navBrief.addEventListener("click", () => {
      setActiveView("brief", true);
      if (state.briefStatus === "idle") void loadDeveloperBrief();
    });
    elements.navAttention.addEventListener("click", () => {
      setActiveView("attention", true);
      if (state.costIssueStatus === "idle") void loadCostIssues();
    });
    elements.navSessions.addEventListener("click", () => {
      setActiveView("sessions", true);
      if (!state.sessions.length) refreshSessions(false);
    });
    elements.sessionTabSummary.addEventListener("click", () => {
      setSessionTab("summary");
    });
    elements.sessionTabTimeline.addEventListener("click", () => {
      setSessionTab("timeline");
    });
    elements.navHabits.addEventListener("click", () => {
      setActiveView("habits", true);
      if (state.habitsStatus === "idle") void loadUserInsights();
    });
    elements.habitsRetry.addEventListener("click", () => {
      void loadUserInsights();
    });
    elements.habitsDebriefAll.addEventListener("click", () => {
      void generateAllHabitsDebriefs();
    });
    elements.reportHabitsOpen.addEventListener("click", () => {
      setActiveView("habits", true);
      if (state.habitsStatus === "idle") void loadUserInsights();
    });
    elements.briefRetry.addEventListener("click", () => {
      void loadDeveloperBrief();
    });
    elements.briefOpenAttention.addEventListener("click", () => {
      setActiveView("attention", true);
      if (state.costIssueStatus === "idle") void loadCostIssues();
    });
    elements.attentionModeIssues.addEventListener("click", () => {
      setAttentionMode("issues");
      if (state.costIssueStatus === "idle") void loadCostIssues();
    });
    elements.attentionModeSafety.addEventListener("click", () => {
      setAttentionMode("safety");
      if (state.issues.status === "idle") void refreshAttention(false);
    });
    elements.costIssuesRetry.addEventListener("click", () => {
      void loadCostIssues();
    });
    elements.briefOpenSessions.addEventListener("click", () => {
      setActiveView("sessions", true);
      if (!state.sessions.length) void refreshSessions(false);
    });
    elements.missionPackClose.addEventListener("click", () => {
      closeMissionPackDrawer(true);
    });
    elements.missionPackDone.addEventListener("click", () => {
      closeMissionPackDrawer(true);
    });
    elements.missionPackRetry.addEventListener("click", () => {
      const issueID = readText(
        state.missionPackIssue && state.missionPackIssue.issue_id,
      );
      if (!issueID) return;
      resetMissionPackDrawer();
      void loadMissionPack(issueID);
    });
    elements.missionPackCopyClaude.addEventListener("click", () => {
      const issueID = readText(
        state.missionPackIssue && state.missionPackIssue.issue_id,
      );
      void copyText(
        agentIssueCommand("claude", issueID),
        elements.missionPackCopyClaude,
      );
    });
    elements.missionPackCopyCodex.addEventListener("click", () => {
      const issueID = readText(
        state.missionPackIssue && state.missionPackIssue.issue_id,
      );
      void copyText(
        agentIssueCommand("codex", issueID),
        elements.missionPackCopyCodex,
      );
    });
    elements.missionPackShowEvidence.addEventListener("click", () => {
      const issue = state.missionPackIssue;
      if (!issue) return;
      closeMissionPackDrawer(false);
      openReportEvidenceDrawer(issue);
    });
    elements.missionPackModalLayer.addEventListener(
      "keydown",
      handleModalKeydown,
    );
    elements.reportEvidenceClose.addEventListener("click", () => {
      closeReportEvidenceDrawer(true);
    });
    elements.reportEvidenceDone.addEventListener("click", () => {
      closeReportEvidenceDrawer(true);
    });
    elements.reportEvidenceCopyClaude.addEventListener("click", () => {
      const issueID = state.reportEvidenceIssueID;
      void copyText(
        agentIssueCommand("claude", issueID),
        elements.reportEvidenceCopyClaude,
      );
    });
    elements.reportEvidenceCopyCodex.addEventListener("click", () => {
      const issueID = state.reportEvidenceIssueID;
      void copyText(
        agentIssueCommand("codex", issueID),
        elements.reportEvidenceCopyCodex,
      );
    });
    elements.reportEvidenceModalLayer.addEventListener(
      "keydown",
      handleModalKeydown,
    );
    elements.refreshButton.addEventListener("click", () => refreshAll(true));
    elements.issuesLoadMore.addEventListener("click", () => {
      loadIssueBucket(state.issues, true);
    });
    elements.familyMembersLoadMore.addEventListener("click", () => {
      void loadAttentionFamilyDetail(true);
    });
    elements.evidenceGapsLoadMore.addEventListener("click", () => {
      loadIssueBucket(state.evidenceGaps, true);
    });
    elements.fixMonitoringLoadMore.addEventListener("click", () => {
      void loadFixMonitoring(true);
    });
    elements.fixMonitoringFilters.addEventListener("submit", (event) => {
      event.preventDefault();
      syncFixMonitoringFilters();
      resetAndLoadFixMonitoring();
    });
    [
      [elements.fixMonitoringFilterState, "state"],
      [elements.fixMonitoringFilterChangeKind, "changeKind"],
      [elements.fixMonitoringFilterSeverity, "severity"],
    ].forEach(([element, key]) => {
      element.addEventListener("change", () => {
        state.fixMonitoringFilters[key] = element.value;
        if (key === "state" && element.value === "retracted") {
          state.fixMonitoringFilters.includeRetracted = true;
          elements.fixMonitoringFilterRetracted.checked = true;
        }
        resetAndLoadFixMonitoring();
      });
    });
    elements.fixMonitoringFilterRetracted.addEventListener("change", () => {
      state.fixMonitoringFilters.includeRetracted =
        elements.fixMonitoringFilterRetracted.checked;
      resetAndLoadFixMonitoring();
    });
    [
      elements.fixMonitoringFilterHarness,
      elements.fixMonitoringFilterIssueID,
      elements.fixMonitoringFilterRecordedAfter,
    ].forEach((element) => {
      element.addEventListener("change", () => {
        syncFixMonitoringFilters();
        resetAndLoadFixMonitoring();
      });
    });
    elements.clearFixMonitoringFilters.addEventListener("click", () => {
      state.fixMonitoringFilters = {
        state: "",
        changeKind: "",
        severity: "",
        harness: "",
        issueID: "",
        recordedAfter: "",
        includeRetracted: false,
      };
      renderFixMonitoringFilters();
      resetAndLoadFixMonitoring();
    });
    elements.occurrencesLoadMore.addEventListener("click", loadMoreOccurrences);
    elements.issueEvidencePreviewAll.addEventListener("click", () => {
      const preview = state.issueEvidencePreview;
      const occurrence = state.occurrences.find(
        (value) =>
          readText(value.occurrence_id) === readText(preview.occurrenceID),
      );
      if (occurrence) void loadIssueEvidencePreview(occurrence, true);
    });
    elements.recordFixAttempt.addEventListener("click", openFixAttemptDialog);
    elements.fixHistoryLoadMore.addEventListener(
      "click",
      loadMoreFixMonitoringDetail,
    );
    elements.issueBackButton.addEventListener("click", closeIssueDetail);
    elements.familyBackButton.addEventListener("click", closeFamilyDetail);
    elements.copyIssueFingerprint.addEventListener("click", () => {
      copyText(
        readText(state.selectedIssue && state.selectedIssue.fingerprint_id),
        elements.copyIssueFingerprint,
      );
    });
    elements.sessionsLoadMore.addEventListener("click", loadMoreSessions);
    elements.eventsLoadMore.addEventListener("click", loadMoreEvents);
    elements.findingsLoadMore.addEventListener("click", loadMoreFindings);
    elements.backButton.addEventListener("click", closeTimeline);
    elements.errorRetry.addEventListener("click", retryLastAction);
    elements.copySelectedSession.addEventListener("click", () => {
      copyText(state.selectedSessionID, elements.copySelectedSession);
    });
    elements.allEventsToggle.addEventListener("click", () => {
      state.showAllEvents = !state.showAllEvents;
      elements.allEventsToggle.setAttribute(
        "aria-pressed",
        String(state.showAllEvents),
      );
      elements.allEventsToggle.classList.toggle("is-active", state.showAllEvents);
      renderEvents();
    });
    elements.fixAttemptClose.addEventListener("click", () => {
      closeFixAttemptDialog(true);
    });
    elements.fixAttemptCancel.addEventListener("click", () => {
      closeFixAttemptDialog(true);
    });
    elements.fixAttemptConfirm.addEventListener("click", () => {
      void submitFixAttempt();
    });
    elements.abandonFixDraft.addEventListener("click", abandonFixDraft);
    elements.fixCategoryOptions.addEventListener("change", (event) => {
      updateFixDraftChoice(event.target);
    });
    elements.fixRetractionClose.addEventListener("click", () => {
      closeFixRetractionDialog(true);
    });
    elements.fixRetractionCancel.addEventListener("click", () => {
      closeFixRetractionDialog(true);
    });
    elements.fixRetractionConfirm.addEventListener("click", () => {
      void submitFixRetraction();
    });
    elements.abandonFixRetraction.addEventListener(
      "click",
      abandonFixRetraction,
    );
    elements.fixRetractionOptions.addEventListener("change", (event) => {
      updateFixRetractionChoice(event.target);
    });
    elements.fixAttemptModalLayer.addEventListener(
      "keydown",
      handleModalKeydown,
    );
    elements.fixRetractionModalLayer.addEventListener(
      "keydown",
      handleModalKeydown,
    );

    [
      [elements.issueFilterSeverity, "severity"],
      [elements.issueFilterOrigin, "origin"],
      [elements.issueFilterStatus, "analysisStatus"],
    ].forEach(([element, key]) => {
      element.addEventListener("change", () => {
        state.issueFilters[key] = element.value;
        resetAndLoadAttention();
      });
    });
    elements.issueFilterHarness.addEventListener("input", () => {
      globalThis.clearTimeout(issueFilterTimer);
      issueFilterTimer = globalThis.setTimeout(() => {
        state.issueFilters.harness = elements.issueFilterHarness.value.trim();
        resetAndLoadAttention();
      }, 250);
    });
    elements.issueFilterExperimental.addEventListener("change", () => {
      state.issueFilters.experimental =
        elements.issueFilterExperimental.checked;
      resetAndLoadAttention();
    });
    elements.clearAttentionFilters.addEventListener("click", () => {
      state.issueFilters = {
        severity: "",
        harness: "",
        origin: "",
        analysisStatus: "",
        experimental: false,
      };
      elements.issueFilterSeverity.value = "";
      elements.issueFilterHarness.value = "";
      elements.issueFilterOrigin.value = "";
      elements.issueFilterStatus.value = "";
      elements.issueFilterExperimental.checked = false;
      resetAndLoadAttention();
    });

    elements.sessionSearch.addEventListener("input", (event) => {
      state.filters.query = event.target.value.trim();
      globalThis.clearTimeout(searchTimer);
      searchTimer = globalThis.setTimeout(resetAndLoadSessions, 250);
    });

    [
      [elements.filterHarness, "harness"],
      [elements.filterCapture, "capture"],
      [elements.filterOutcome, "outcome"],
    ].forEach(([element, key]) => {
      element.addEventListener("change", () => {
        state.filters[key] = element.value;
        resetAndLoadSessions();
      });
    });

    elements.filterDate.addEventListener("change", () => {
      state.filters.days = elements.filterDate.value;
      state.sessionOccurredAfter = dateLowerBound(state.filters.days);
      resetAndLoadSessions();
    });

    elements.clearFilters.addEventListener("click", () => {
      state.filters = {
        query: "",
        harness: "",
        capture: "",
        outcome: "",
        days: "",
      };
      state.sessionOccurredAfter = "";
      elements.sessionSearch.value = "";
      elements.filterHarness.value = "";
      elements.filterCapture.value = "";
      elements.filterOutcome.value = "";
      elements.filterDate.value = "";
      resetAndLoadSessions();
    });

    globalThis.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      if (state.activeModal) return;
      if (state.activeView === "sessions" && state.selectedSessionID) {
        closeTimeline();
      } else if (state.activeView === "attention" && state.selectedIssueID) {
        closeIssueDetail();
      } else if (state.activeView === "attention" && state.selectedFamily) {
        closeFamilyDetail();
      }
    });
    const handleMobileChange = () => applyPaneAccessibility();
    if (typeof mobileQuery.addEventListener === "function") {
      mobileQuery.addEventListener("change", handleMobileChange);
    } else if (typeof mobileQuery.addListener === "function") {
      mobileQuery.addListener(handleMobileChange);
    }
  }

  async function refreshAll(preserveSelection) {
    hideError();
    elements.refreshButton.disabled = true;
    try {
      if (state.activeView === "brief") {
        await loadDeveloperBrief();
        return;
      }
      if (state.activeView === "habits") {
        await loadUserInsights();
        return;
      }
      if (state.activeView === "attention") {
        if (state.attentionMode === "safety") {
          await refreshAttention(preserveSelection);
        } else {
          await loadCostIssues();
        }
        return;
      }
      await refreshSessions(preserveSelection);
    } finally {
      elements.refreshButton.disabled = false;
    }
  }

  function setAttentionMode(mode) {
    const selected = mode === "safety" ? "safety" : "issues";
    state.attentionMode = selected;
    const issuesActive = selected === "issues";
    elements.attentionModeIssues.setAttribute(
      "aria-selected",
      issuesActive ? "true" : "false",
    );
    elements.attentionModeSafety.setAttribute(
      "aria-selected",
      issuesActive ? "false" : "true",
    );
    elements.costIssuesSection.hidden = !issuesActive;
    if (elements.safetyContent) {
      elements.safetyContent.hidden = issuesActive;
    }
    elements.attentionDetailPane.hidden = issuesActive;
    elements.attentionView.classList.toggle("cost-mode", issuesActive);
    renderAttentionTotals();
  }

  async function loadCostIssues() {
    const generation = ++state.costIssueRequestGeneration;
    state.costIssueStatus = "loading";
    state.costIssueError = "";
    renderCostIssues();
    try {
      const response = await apiGet("/v1/cost-issues?limit=5");
      if (generation !== state.costIssueRequestGeneration) return false;
      if (
        !isRecord(response) ||
        readText(response.schema_version) !== "belay.cost-issues.v1" ||
        !Array.isArray(response.data)
      ) {
        throw new Error("Local API returned invalid cost-ranked issues.");
      }
      state.costIssues = response.data
        .slice(0, 5)
        .map(requireCostIssue);
      state.costIssueStatus = "ready";
      renderCostIssues();
      return true;
    } catch (error) {
      if (generation !== state.costIssueRequestGeneration) return false;
      state.costIssueStatus = "error";
      state.costIssueError =
        error instanceof Error ? error.message : "Cost issue read failed.";
      renderCostIssues();
      return false;
    }
  }

  function requireCostIssue(issue) {
    if (
      !isRecord(issue) ||
      !readText(issue.issue_id) ||
      !readText(issue.detector_id) ||
      !readText(issue.headline) ||
      !isRecord(issue.cost) ||
      !Array.isArray(issue.sessions) ||
      !Array.isArray(issue.trend) ||
      !Array.isArray(issue.excerpts) ||
      !isRecord(issue.project) ||
      !isRecord(issue.suggested_fix)
    ) {
      throw new Error("Local API returned an invalid cost issue.");
    }
    return issue;
  }

  function renderCostIssues() {
    const loading = state.costIssueStatus === "loading";
    const failed = state.costIssueStatus === "error";
    const empty =
      state.costIssueStatus === "ready" && state.costIssues.length === 0;
    elements.costIssuesLoading.hidden = !loading;
    elements.costIssuesError.hidden = !failed;
    elements.costIssuesEmpty.hidden = !empty;
    elements.costIssueList.hidden = loading || failed || empty;
    elements.costIssueCount.textContent =
      state.costIssueStatus === "ready"
        ? String(state.costIssues.length)
        : "—";
    if (loading || failed || empty) {
      elements.costIssueList.replaceChildren();
      renderAttentionTotals();
      return;
    }
    const fragment = document.createDocumentFragment();
    state.costIssues.forEach((issue, index) => {
      fragment.append(createCostIssueCard(issue, index));
    });
    elements.costIssueList.replaceChildren(fragment);
    renderAttentionTotals();
  }

  function createCostIssueCard(issue, index) {
    const card = createElement("article", "cost-issue-card");
    const header = createElement("header", "cost-issue-card-header");
    const heading = createElement("div");
    heading.append(
      createElement(
        "p",
        "eyebrow",
        `${projectDisplayName(issue.project)} · ${readableLabel(issue.detector_id)}`,
      ),
      createElement("h3", "", readText(issue.headline)),
    );
    header.append(
      heading,
      createElement("span", "cost-issue-rank", `#${index + 1}`),
    );
    card.append(header);

    const metrics = createElement("dl", "cost-issue-metrics");
    appendCostMetric(
      metrics,
      "Attributed cost",
      formatIssueDollarCost(issue.cost),
    );
    appendCostMetric(
      metrics,
      "Time",
      formatIssueMinutes(issue.cost),
    );
    appendCostMetric(
      metrics,
      "Tokens",
      formatNumber(issue.cost.wasted_tokens),
    );
    const sessions = Math.max(
      toFiniteNumber(issue.session_count),
      issue.sessions.length,
    );
    appendCostMetric(
      metrics,
      "Sessions",
      `${formatNumber(sessions)} ${sessions === 1 ? "session" : "sessions"}`,
    );
    card.append(metrics);

    if (issue.excerpts.length) {
      card.append(createCostIssueExcerpt(issue.excerpts[0], true));
    }
    const trend = issue.trend
      .slice(-8)
      .map((week) => formatNumber(week && week.count))
      .join(" · ");
    if (trend) {
      card.append(
        createElement(
          "p",
          "cost-issue-trend",
          `Weekly occurrences, oldest to newest: ${trend}`,
        ),
      );
    }

    const fix = createElement("section", "cost-issue-fix");
    fix.append(
      createElement("p", "eyebrow", "Suggested fix"),
      createElement(
        "h4",
        "",
        readText(issue.suggested_fix.target_file) || "Agent instructions",
      ),
      createElement(
        "p",
        "",
        readText(issue.suggested_fix.rationale) ||
          "Add a concrete project rule that prevents this pattern.",
      ),
    );
    const fixActions = createElement("div", "cost-issue-fix-actions");
    const prepareButton = createElement(
      "button",
      "secondary-button",
      "Prepare fix",
    );
    prepareButton.type = "button";
    const fixStatus = createElement("p", "cost-issue-fix-status");
    fixStatus.setAttribute("aria-live", "polite");
    const diff = createElement("pre", "cost-issue-fix-diff");
    diff.hidden = true;
    prepareButton.addEventListener("click", async () => {
      const issueID = readText(issue.issue_id);
      const kind = readText(issue.suggested_fix.kind);
      const targetFile = readText(issue.suggested_fix.target_file);
      const idempotencyKey = createUUIDv4();
      if (!issueID || !kind || !targetFile || !idempotencyKey) {
        fixStatus.textContent = "This fix could not be prepared.";
        return;
      }
      prepareButton.disabled = true;
      prepareButton.textContent = "Preparing…";
      fixStatus.textContent = "";
      try {
        const response = await apiMutation(
          `/v1/cost-issues/${encodeURIComponent(issueID)}/fixes`,
          { kind, target_file: targetFile },
          idempotencyKey,
          "propose-cost-issue-fix.v1",
        );
        if (
          readText(response && response.schema_version) !==
            "belay.cost-issue-fix.v1" ||
          !isRecord(response && response.data) ||
          !readText(response.data.fix_id) ||
          !readText(response.data.unified_diff)
        ) {
          throw new Error("Local API returned an invalid fix proposal.");
        }
        diff.textContent = readText(response.data.unified_diff);
        diff.hidden = false;
        fixStatus.textContent =
          "Proposal ready. Review the diff, then apply it with /belay in Claude Code or $belay in Codex.";
        prepareButton.textContent = "Prepared";
      } catch (error) {
        fixStatus.textContent =
          error instanceof Error
            ? error.message
            : "This fix could not be prepared.";
        prepareButton.disabled = false;
        prepareButton.textContent = "Try preparing again";
      }
    });
    fixActions.append(prepareButton);
    fix.append(fixActions, fixStatus, diff);
    card.append(fix);

    const evidence = createElement("details", "cost-issue-evidence");
    evidence.append(
      createElement(
        "summary",
        "",
        `Show evidence (${formatNumber(issue.excerpts.length)})`,
      ),
    );
    const evidenceList = createElement("div", "cost-issue-evidence-list");
    issue.excerpts.forEach((excerpt) => {
      evidenceList.append(createCostIssueExcerpt(excerpt, false));
    });
    evidence.append(evidenceList);
    card.append(evidence);
    return card;
  }

  function appendCostMetric(list, label, value) {
    const item = createElement("div");
    item.append(
      createElement("dt", "", label),
      createElement("dd", "", value),
    );
    list.append(item);
  }

  function createCostIssueExcerpt(excerpt, preview) {
    const wrapper = createElement(
      "div",
      preview ? "cost-issue-excerpt preview" : "cost-issue-excerpt",
    );
    const citation = isRecord(excerpt && excerpt.citation)
      ? excerpt.citation
      : {};
    const role = readableLabel(excerpt && excerpt.role, "Transcript");
    const tool = readText(excerpt && excerpt.tool_name);
    const session = compactID(citation.session_key);
    const turn = Number.isFinite(Number(citation.turn_index))
      ? `turn ${formatNumber(citation.turn_index)}`
      : "turn unavailable";
    wrapper.append(
      createElement(
        "p",
        "cost-issue-citation",
        [role, tool, session, turn].filter(Boolean).join(" · "),
      ),
      createElement(
        "blockquote",
        "",
        readText(excerpt && excerpt.text) || "Excerpt unavailable",
      ),
    );
    return wrapper;
  }

  function formatIssueDollarCost(cost) {
    const value = cost && cost.wasted_usd;
    if (typeof value !== "number" || !Number.isFinite(value)) {
      return cost && cost.lower_bound
        ? "At least the known token cost"
        : "Unknown model price";
    }
    const formatted = new Intl.NumberFormat(undefined, {
      style: "currency",
      currency: "USD",
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(Math.max(0, value));
    return cost.lower_bound ? `At least ${formatted}` : formatted;
  }

  function formatIssueMinutes(cost) {
    const minutes = Math.max(0, toFiniteNumber(cost && cost.wasted_minutes));
    const formatted =
      minutes >= 10 ? formatNumber(Math.round(minutes)) : minutes.toFixed(1);
    return `${cost && cost.lower_bound ? "At least " : ""}${formatted} active min`;
  }

  function formatIssueTokens(cost) {
    const tokens = Math.max(0, toFiniteNumber(cost && cost.wasted_tokens));
    return `${cost && cost.lower_bound ? "At least " : ""}${formatNumber(tokens)} tokens`;
  }

  function projectDisplayName(project) {
    const identity = readText(project && project.identity);
    const path = readText(project && project.path);
    const localIdentity = /^(?:\/|[A-Za-z]:[\\/])/.test(identity);
    const candidate =
      localIdentity && path && path !== identity ? path : identity || path;
    if (!candidate) return "Project unavailable";
    const parts = candidate.split(/[/:\\]/).filter(Boolean);
    return (parts[parts.length - 1] || candidate).replace(/\.git$/i, "");
  }

  async function refreshSessions(preserveSelection) {
    const selectedID = preserveSelection ? state.selectedSessionID : "";
    state.sessionLimit = pageLimits.sessions.initial;
    state.sessionNextCursor = "";
    const [, sessionsResult] = await Promise.allSettled([
      loadStats(),
      loadSessions(false),
    ]);
    const sessionsReady =
      sessionsResult.status === "fulfilled" && sessionsResult.value === true;
    if (!sessionsReady) return false;
    if (
      selectedID &&
      state.sessions.some((session) => readText(session.session_id) === selectedID)
    ) {
      openSession(selectedID, state.selectedSessionDetail);
    }
    return true;
  }

  function setActiveView(view, moveFocus) {
    const next = ["brief", "attention", "sessions", "habits"].includes(view)
      ? view
      : "brief";
    if (next !== "attention" && state.activeView === "attention") {
      state.attentionRefreshGeneration += 1;
    }
    if (next !== "sessions" && state.activeView === "sessions") {
      state.directSessionRequestGeneration += 1;
    }
    state.activeView = next;
    const briefActive = next === "brief";
    const attentionActive = next === "attention";
    const sessionsActive = next === "sessions";
    const habitsActive = next === "habits";
    setViewVisibility(elements.briefView, briefActive);
    setViewVisibility(elements.attentionView, attentionActive);
    setViewVisibility(elements.sessionsView, sessionsActive);
    setViewVisibility(elements.habitsView, habitsActive);
    setCurrentNavigation(elements.navBrief, briefActive);
    setCurrentNavigation(elements.navAttention, attentionActive);
    setCurrentNavigation(elements.navSessions, sessionsActive);
    setCurrentNavigation(elements.navHabits, habitsActive);
    applyPaneAccessibility();
    if (moveFocus) {
      focusCurrentElement(
        briefActive
          ? elements.navBrief
          : attentionActive
            ? elements.navAttention
            : sessionsActive
              ? elements.navSessions
              : elements.navHabits,
      );
    }
  }

  function setViewVisibility(element, visible) {
    element.hidden = !visible;
    element.inert = !visible;
    if (visible) {
      element.removeAttribute("aria-hidden");
    } else {
      element.setAttribute("aria-hidden", "true");
    }
  }

  function setCurrentNavigation(button, current) {
    if (current) {
      button.setAttribute("aria-current", "page");
    } else {
      button.removeAttribute("aria-current");
    }
  }

  function applyPaneAccessibility() {
    const mobile = mobileQuery.matches;
    const issueOpen = Boolean(state.selectedIssueID || state.selectedFamily);
    const sessionOpen = Boolean(state.selectedSessionID);
    setObscuredPane(
      elements.attentionListPane,
      state.activeView === "attention" && mobile && issueOpen,
    );
    setObscuredPane(
      elements.attentionDetailPane,
      state.activeView === "attention" && mobile && !issueOpen,
    );
    setObscuredPane(
      elements.sessionPanel,
      state.activeView === "sessions" && mobile && sessionOpen,
    );
    setObscuredPane(
      elements.timelinePanel,
      state.activeView === "sessions" && mobile && !sessionOpen,
    );
  }

  function setObscuredPane(element, obscured) {
    element.inert = obscured;
    if (obscured) {
      element.setAttribute("aria-hidden", "true");
    } else {
      element.removeAttribute("aria-hidden");
    }
  }

  function issueFocusKey(kind, issueID) {
    return `${kind === "evidence_gap" ? "evidence_gap" : "issue"}\u0000${issueID}`;
  }

  function familyFocusKey(familyID) {
    return readText(familyID);
  }

  function familyMemberFocusKey(familyID, issueID) {
    return `${readText(familyID)}\u0000${readText(issueID)}`;
  }

  function occurrenceFocusKey(issueID, occurrence) {
    const occurrenceID = readText(occurrence && occurrence.occurrence_id);
    const sessionID = readText(occurrence && occurrence.session_id);
    return `${issueID}\u0000${occurrenceID || sessionID}`;
  }

  function clearIssueFocusRegistry(kind) {
    const prefix = `${kind === "evidence_gap" ? "evidence_gap" : "issue"}\u0000`;
    Array.from(focusRegistry.issueCards.keys()).forEach((key) => {
      if (key.startsWith(prefix)) focusRegistry.issueCards.delete(key);
    });
  }

  function resolveFocusReference(reference) {
    if (!reference) return null;
    if (reference.type === "mission-pack") {
      return focusRegistry.missionPackTriggers.get(reference.issueID) || null;
    }
    if (reference.type === "report-evidence") {
      return (
        focusRegistry.reportEvidenceTriggers.get(reference.issueID) || null
      );
    }
    if (reference.type === "brief-action") {
      return focusRegistry.briefActions.get(reference.key) || null;
    }
    if (reference.type === "brief-session") {
      return focusRegistry.briefSessions.get(reference.key) || null;
    }
    if (reference.type === "diagnosis-action") {
      return focusRegistry.diagnosisActions.get(reference.key) || null;
    }
    if (reference.type === "monitoring") {
      return focusRegistry.monitoringCards.get(reference.key) || null;
    }
    if (reference.type === "issue") {
      return focusRegistry.issueCards.get(reference.key) || null;
    }
    if (reference.type === "family") {
      return focusRegistry.familyCards.get(reference.familyID) || null;
    }
    if (reference.type === "family-member") {
      return focusRegistry.familyMembers.get(reference.key) || null;
    }
    if (reference.type === "occurrence") {
      return focusRegistry.occurrenceActions.get(reference.key) || null;
    }
    if (reference.type === "session") {
      return focusRegistry.sessionCards.get(reference.sessionID) || null;
    }
    if (reference.type === "fix-trigger") {
      return focusRegistry.fixTriggers.get(reference.issueID) || null;
    }
    if (reference.type === "fix-history") {
      return focusRegistry.fixHistoryRows.get(reference.annotationID) || null;
    }
    if (reference.type === "fix-observation") {
      return (
        focusRegistry.fixObservationRows.get(reference.recurrenceID) || null
      );
    }
    if (reference.type === "fix-retraction") {
      return (
        focusRegistry.fixRetractionTriggers.get(reference.annotationID) || null
      );
    }
    return null;
  }

  function canReceiveFocus(element) {
    if (
      !element ||
      !element.isConnected ||
      typeof element.focus !== "function" ||
      element.closest("[hidden]") ||
      element.closest('[aria-hidden="true"]')
    ) {
      return false;
    }
    let current = element;
    while (current) {
      if (current.inert === true) return false;
      current = current.parentElement;
    }
    return true;
  }

  function focusCurrentElement(element) {
    if (!canReceiveFocus(element)) return false;
    element.focus();
    return true;
  }

  function restoreLogicalFocus(reference, fallback) {
    return (
      focusCurrentElement(resolveFocusReference(reference)) ||
      focusCurrentElement(fallback)
    );
  }

  async function refreshAttention(preserveSelection, suppressFailureNotice = false) {
    const refreshGeneration = ++state.attentionRefreshGeneration;
    const selectedID = preserveSelection ? state.selectedIssueID : "";
    const selectedKind = preserveSelection ? state.selectedIssueKind : "";
    const selectedSource = preserveSelection ? state.selectedIssueSource : "";
    const selectedFamilyID =
      preserveSelection && state.selectedFamily
        ? readText(state.selectedFamily.family_id)
        : preserveSelection
          ? readText(state.selectedIssueFamilyID)
          : "";
    const selectedFamilyMemberPageBudget = Math.max(
      1,
      Math.ceil(
        state.familyMembers.length / pageLimits.familyMembers.page,
      ),
    );
    const selectedBucket =
      selectedKind === "evidence_gap" ? state.evidenceGaps : state.issues;
    const selectedPageBudget = Math.max(
      1,
      Math.ceil(selectedBucket.data.length / pageLimits.issues.page),
    );
    if (
      (selectedID || selectedFamilyID) &&
      selectedSource !== "monitoring"
    ) {
      closeIssueDetail(false, true);
      clearAttentionFamilyDetailState();
      showAttentionNotice(
        "Refreshing Attention; selected detail is hidden until current data confirms it.",
        "pending",
      );
    } else if (!preserveSelection) {
      closeIssueDetail(false, true);
      clearAttentionFamilyDetailState();
    }
    resetIssueBucket(state.issues);
    resetIssueBucket(state.evidenceGaps);
    resetFixMonitoringBucket();
    const [issuesReady, gapsReady, monitoringReady] = await Promise.all([
      loadIssueBucket(state.issues, false),
      loadIssueBucket(state.evidenceGaps, false),
      loadFixMonitoring(false),
    ]);
    if (refreshGeneration !== state.attentionRefreshGeneration) return false;
    if (selectedID && selectedSource === "monitoring") {
      if (!monitoringReady) {
        state.fixHistoryStale = true;
        state.fixHistoryError =
          "Monitoring refresh failed. Previously loaded attempt detail may be stale.";
        renderFixAttempts();
        return false;
      }
      const summary = state.fixMonitoring.data.find(
        (entry) => readText(entry.issue_id) === selectedID,
      );
      if (!summary) {
        closeIssueDetail(false);
        showAttentionNotice(
          state.fixMonitoring.hasMore
            ? "Follow-up evidence refreshed. The selected item is outside the loaded results."
            : "Follow-up evidence refreshed. The selected item is no longer visible under the current filters.",
          "status",
          7000,
        );
        return issuesReady && gapsReady;
      }
      const detailReady = await refreshSelectedMonitoringIssue(summary);
      if (refreshGeneration !== state.attentionRefreshGeneration) return false;
      if (!detailReady) {
        state.fixHistoryStale = true;
        state.fixHistoryError =
          "Monitoring list refreshed, but attempt detail could not be refreshed.";
        renderFixAttempts();
        return false;
      }
      showAttentionNotice(
        "Attention refreshed; follow-up evidence was reloaded from the current view.",
        "success",
        4000,
      );
      return issuesReady && gapsReady;
    }
    if (!issuesReady || !gapsReady) {
      if (!suppressFailureNotice && !state.attentionExpiryRefresh) {
        showAttentionNotice(
          selectedID
            ? "Attention refresh failed. Selected detail remains closed to avoid showing stale data."
            : "Attention refresh failed. The issue lists may be incomplete; retry before relying on them.",
          "error",
        );
      }
      return false;
    }
    if (!selectedID && !selectedFamilyID) return true;

    if (selectedKind === "evidence_gap") {
      const chainReady = await loadIssuePagesForSelection(
        state.evidenceGaps,
        selectedID,
        selectedPageBudget,
      );
      if (!chainReady) return false;
      const summary = state.evidenceGaps.data.find(
        (issue) => readText(issue.issue_id) === selectedID,
      );
      return summary ? selectIssue(summary, selectedKind, false) : true;
    }

    const chainReady = await loadFamilyPagesForSelection(
      selectedFamilyID,
      selectedID,
      selectedPageBudget,
    );
    if (refreshGeneration !== state.attentionRefreshGeneration) return false;
    if (!chainReady) {
      if (!suppressFailureNotice && !state.attentionExpiryRefresh) {
        showAttentionNotice(
          "Attention refresh failed while confirming the selected finding. Its detail remains closed.",
          "error",
        );
      }
      return false;
    }
    const summary = state.issues.data.find(
      (family) =>
        (selectedFamilyID &&
          readText(family.family_id) === selectedFamilyID) ||
        (!selectedFamilyID &&
          readText(family.kind) === "exact_issue" &&
          readText(family.representative_issue_id) === selectedID),
    );
    if (!summary) {
      showAttentionNotice(
        state.issues.hasMore
          ? "Attention refreshed. The selected finding was not confirmed in the refreshed loaded results; load more to find it."
          : "Attention refreshed. The selected finding is no longer visible under the current filters.",
        "status",
        7000,
      );
      return true;
    }
    let detailReady = false;
    if (readText(summary.kind) === "mapped_upstream") {
      detailReady = await selectAttentionFamily(summary, false);
      if (detailReady && selectedID) {
        const memberChainReady = await loadFamilyMemberPagesForSelection(
          selectedID,
          selectedFamilyMemberPageBudget,
        );
        if (!memberChainReady) {
          closeFamilyDetail(false);
          detailReady = false;
        }
        const member = state.familyMembers.find(
          (value) => readText(value.issue && value.issue.issue_id) === selectedID,
        );
        if (memberChainReady && !member) {
          const moreMembersRemain = state.familyMemberHasMore;
          closeFamilyDetail(false);
          showAttentionNotice(
            moreMembersRemain
              ? "Attention refreshed, but the selected item was not found in the sessions currently loaded. Reopen the finding and load more sessions to select it again."
              : "Attention refreshed, but the selected finding is no longer visible in this group.",
            "status",
            8000,
          );
          return true;
        }
        if (memberChainReady && member) {
          const returnToFamily =
            selectedSource === "family" &&
            toFiniteNumber(summary.supporting_issue_count) > 1;
          const returnFocus = returnToFamily
            ? {
                type: "family-member",
                key: familyMemberFocusKey(
                  readText(summary.family_id),
                  selectedID,
                ),
              }
            : {
                type: "family",
                familyID: readText(summary.family_id),
              };
          if (!returnToFamily) closeFamilyDetail(false);
          detailReady = await selectIssue(member.issue, "issue", false, {
            viewCursor: readCursor(member.view_cursor),
            catalog: member.catalog,
            source: returnToFamily ? "family" : "attention",
            familyID: readText(summary.family_id),
            evidenceContext: attentionFamilyEvidenceContext(summary),
            returnFocus,
          });
        }
      }
    } else {
      detailReady = await selectIssue(
        exactFamilyRepresentative(summary),
        "issue",
        false,
        {
          viewCursor: readCursor(summary.view_cursor),
          catalog: summary.catalog,
          familyID: readText(summary.family_id),
          evidenceContext: attentionFamilyEvidenceContext(summary),
          returnFocus: {
            type: "family",
            familyID: readText(summary.family_id),
          },
        },
      );
    }
    if (refreshGeneration !== state.attentionRefreshGeneration) return false;
    if (!detailReady) {
      closeIssueDetail(false);
      if (!suppressFailureNotice && !state.attentionExpiryRefresh) {
        showAttentionNotice(
          "Attention lists refreshed, but the selected detail could not be reloaded and remains closed.",
          "error",
        );
      }
      return false;
    }
    showAttentionNotice(
      "Attention refreshed; selected detail was reloaded from the current view.",
      "success",
      4000,
    );
    return true;
  }

  async function loadIssuePagesForSelection(bucket, issueID, pageBudget) {
    for (let page = 1; page < pageBudget; page += 1) {
      if (
        bucket.data.some((issue) => readText(issue.issue_id) === issueID) ||
        !bucket.hasMore
      ) {
        break;
      }
      const pageReady = await loadIssueBucket(bucket, true);
      if (!pageReady) return false;
    }
    return true;
  }

  async function loadFamilyPagesForSelection(familyID, issueID, pageBudget) {
    for (let page = 1; page < pageBudget; page += 1) {
      if (
        state.issues.data.some(
          (family) =>
            (familyID && readText(family.family_id) === familyID) ||
            (!familyID &&
              readText(family.kind) === "exact_issue" &&
              readText(family.representative_issue_id) === issueID),
        ) ||
        !state.issues.hasMore
      ) {
        break;
      }
      const pageReady = await loadIssueBucket(state.issues, true);
      if (!pageReady) return false;
    }
    return true;
  }

  async function loadFamilyMemberPagesForSelection(issueID, pageBudget) {
    for (let page = 1; page < pageBudget; page += 1) {
      if (
        state.familyMembers.some(
          (member) =>
            readText(member.issue && member.issue.issue_id) === issueID,
        ) ||
        !state.familyMemberHasMore
      ) {
        break;
      }
      const pageReady = await loadAttentionFamilyDetail(true);
      if (!pageReady) return false;
    }
    return true;
  }

  function resetAndLoadAttention() {
    state.attentionRefreshGeneration += 1;
    closeIssueDetail(false, true);
    clearAttentionFamilyDetailState();
    hideAttentionNotice();
    resetIssueBucket(state.issues);
    resetIssueBucket(state.evidenceGaps);
    resetFixMonitoringBucket();
    renderAttentionFilters();
    void Promise.all([
      loadIssueBucket(state.issues, false),
      loadIssueBucket(state.evidenceGaps, false),
      loadFixMonitoring(false),
    ]);
  }

  function resetIssueBucket(bucket) {
    bucket.requestGeneration += 1;
    bucket.data = [];
    bucket.nextCursor = "";
    bucket.hasMore = false;
    bucket.viewCursor = "";
    bucket.analysis = null;
    bucket.selection = null;
    bucket.status = "idle";
    bucket.error = null;
  }

  function resetFixMonitoringBucket(render = true) {
    state.fixMonitoring.requestGeneration += 1;
    state.fixMonitoring.data = [];
    state.fixMonitoring.nextCursor = "";
    state.fixMonitoring.hasMore = false;
    state.fixMonitoring.viewCursor = "";
    state.fixMonitoring.evidenceEvaluatedAt = "";
    state.fixMonitoring.status = "idle";
    state.fixMonitoring.error = null;
    focusRegistry.monitoringCards.clear();
    if (render) renderFixMonitoring();
  }

  function syncFixMonitoringFilters() {
    state.fixMonitoringFilters.harness =
      elements.fixMonitoringFilterHarness.value.trim();
    state.fixMonitoringFilters.issueID =
      elements.fixMonitoringFilterIssueID.value.trim();
    const recordedAfter = elements.fixMonitoringFilterRecordedAfter.value;
    if (!recordedAfter) {
      state.fixMonitoringFilters.recordedAfter = "";
      return;
    }
    const date = new Date(recordedAfter);
    state.fixMonitoringFilters.recordedAfter = Number.isFinite(date.getTime())
      ? date.toISOString()
      : "";
  }

  function renderFixMonitoringFilters() {
    const filters = state.fixMonitoringFilters;
    elements.fixMonitoringFilterState.value = filters.state;
    elements.fixMonitoringFilterChangeKind.value = filters.changeKind;
    elements.fixMonitoringFilterSeverity.value = filters.severity;
    elements.fixMonitoringFilterHarness.value = filters.harness;
    elements.fixMonitoringFilterIssueID.value = filters.issueID;
    elements.fixMonitoringFilterRetracted.checked = filters.includeRetracted;
    if (!filters.recordedAfter) {
      elements.fixMonitoringFilterRecordedAfter.value = "";
    }
    elements.fixMonitoringFilterNote.textContent = filters.includeRetracted
      ? "Including all-retracted history."
      : "Showing active attempts.";
  }

  function resetAndLoadFixMonitoring() {
    if (state.selectedIssueSource === "monitoring") {
      closeIssueDetail(false);
      showAttentionNotice(
        "Follow-up evidence filters changed. The prior detail was closed until a current result is selected.",
        "status",
        5000,
      );
    }
    resetFixMonitoringBucket();
    renderFixMonitoringFilters();
    void loadFixMonitoring(false);
  }

  function buildFixMonitoringPath(cursor) {
    const parameters = new URLSearchParams();
    if (cursor) {
      parameters.set("cursor", cursor);
      return `/v1/fix-monitoring?${parameters.toString()}`;
    }
    const filters = state.fixMonitoringFilters;
    parameters.set("limit", String(pageLimits.fixMonitoring.page));
    if (filters.state) parameters.set("state", filters.state);
    if (filters.changeKind) {
      parameters.set("change_kind", filters.changeKind);
    }
    if (filters.severity) parameters.set("severity", filters.severity);
    if (filters.harness) parameters.set("harness", filters.harness);
    if (filters.issueID) parameters.set("issue_id", filters.issueID);
    if (filters.recordedAfter) {
      parameters.set("recorded_after", filters.recordedAfter);
    }
    if (filters.includeRetracted) {
      parameters.set("include_retracted", "true");
    }
    return `/v1/fix-monitoring?${parameters.toString()}`;
  }

  async function loadFixMonitoring(append) {
    const bucket = state.fixMonitoring;
    if (append && (!bucket.hasMore || !bucket.nextCursor)) return false;
    const generation = ++bucket.requestGeneration;
    const cursor = append ? bucket.nextCursor : "";
    bucket.status = append ? "loading-more" : "loading";
    bucket.error = null;
    renderFixMonitoring();
    try {
      const response = await apiGet(buildFixMonitoringPath(cursor));
      requireFixMonitoringSchema(response);
      if (generation !== bucket.requestGeneration) return false;
      const page = Array.isArray(response.data)
        ? response.data.filter(
            (entry) =>
              isRecord(entry) &&
              readText(entry.issue_id) &&
              readText(entry.annotation_id),
          )
        : [];
      const data =
        append && cursor
          ? deduplicateByID(bucket.data.concat(page), "issue_id")
          : deduplicateByID(page, "issue_id");
      const nextCursor = readCursor(response.next_cursor);
      if (response.has_more === true && !nextCursor) {
        throw new Error(
          "Local API returned monitoring pagination without a cursor.",
        );
      }
      const viewCursor = readCursor(response.monitoring_view_cursor);
      if (!viewCursor) {
        throw new Error(
          "Local API returned monitoring results without a view cursor.",
        );
      }
      bucket.data = data;
      bucket.nextCursor = nextCursor;
      bucket.hasMore = Boolean(nextCursor);
      bucket.viewCursor = viewCursor;
      bucket.evidenceEvaluatedAt = readText(response.evidence_evaluated_at);
      bucket.status = "ready";
      renderFixMonitoring();
      return true;
    } catch (error) {
      if (generation !== bucket.requestGeneration) return false;
      if (isCursorExpired(error) && append) {
        const closedSelectedMonitoringDetail =
          state.selectedIssueSource === "monitoring";
        if (closedSelectedMonitoringDetail) {
          state.attentionRefreshGeneration += 1;
          closeIssueDetail(false);
        }
        resetFixMonitoringBucket();
        const refreshed = await loadFixMonitoring(false);
        bucket.error = refreshed
          ? closedSelectedMonitoringDetail
            ? "The saved After attempts view was out of date. It was refreshed, and the open detail was closed."
            : "The saved After attempts view was out of date and has been refreshed."
          : "The saved After attempts view was out of date and could not be refreshed.";
        renderFixMonitoring();
        return refreshed;
      }
      bucket.status = "error";
      bucket.error = fixMonitoringErrorMessage(error);
      renderFixMonitoring();
      return false;
    }
  }

  function fixMonitoringErrorMessage(error) {
    if (error instanceof LocalAPIError && error.status === 503) {
      if (
        error.problemType ===
        "belay.local/monitoring-catchup-in-progress"
      ) {
        return "Follow-up evidence is catching up. Findings, evidence gaps, sessions, and recorded changes remain available.";
      }
      if (
        error.problemType === "belay.local/monitoring-catchup-failed"
      ) {
        return "Follow-up evidence could not finish updating. Restart or retry Belay Local; other views remain available.";
      }
    }
    if (isCursorExpired(error)) {
      return "The saved After attempts view is out of date. Refresh it.";
    }
    return "Follow-up evidence could not be loaded. Other Local views remain available.";
  }

  function renderFixMonitoring() {
    const bucket = state.fixMonitoring;
    focusRegistry.monitoringCards.clear();
    const fragment = document.createDocumentFragment();
    bucket.data.forEach((summary) => {
      fragment.append(createFixMonitoringCard(summary));
    });
    elements.fixMonitoringList.replaceChildren(fragment);
    elements.fixMonitoringCount.textContent =
      bucket.status === "ready" ? formatNumber(bucket.data.length) : "—";
    elements.fixMonitoringLoading.hidden = bucket.status !== "loading";
    elements.fixMonitoringEmpty.hidden =
      bucket.status !== "ready" || bucket.data.length !== 0;
    elements.fixMonitoringEmptyTitle.textContent =
      state.fixMonitoringFilters.includeRetracted
        ? "No monitored attempt history matches these filters"
        : "No active monitored attempts match these filters";
    elements.fixMonitoringEmptyDetail.textContent =
      "Recording an attempt is optional. Belay checks later activity only after you record one.";
    elements.fixMonitoringPagination.hidden = !bucket.hasMore;
    elements.fixMonitoringLoadMore.disabled =
      bucket.status === "loading-more";
    elements.fixMonitoringPageStatus.textContent = bucket.hasMore
      ? `Showing ${bucket.data.length}; more monitored findings are available.`
      : `Showing ${bucket.data.length} monitored ${
          bucket.data.length === 1 ? "finding" : "findings"
        }.`;
    elements.fixMonitoringStatus.hidden = !bucket.error;
    elements.fixMonitoringStatus.textContent = bucket.error || "";
    elements.fixMonitoringStatus.dataset.tone =
      bucket.status === "error" ? "error" : "status";
    elements.fixMonitoringSection.hidden =
      bucket.status === "ready" &&
      bucket.data.length === 0 &&
      !fixMonitoringFiltersActive();
  }

  function fixMonitoringFiltersActive() {
    const filters = state.fixMonitoringFilters;
    return Boolean(
      filters.state ||
        filters.changeKind ||
        filters.severity ||
        filters.harness ||
        filters.issueID ||
        filters.recordedAfter ||
        filters.includeRetracted,
    );
  }

  function createFixMonitoringCard(summary) {
    const issueID = readText(summary.issue_id);
    const annotationID = readText(summary.annotation_id);
    const article = createElement("article", "issue-card monitoring-card");
    const button = createElement("button", "issue-card-main");
    button.type = "button";
    const stateEntry = fixMonitoringStateEntry(
      summary.fix_recurrence_state,
    );
    button.dataset.monitoringTone = stateEntry.tone;
    const catalog = monitoringSubjectCatalog(summary);
    const header = createElement("span", "issue-card-top");
    header.append(
      createElement("strong", "", catalog.title),
      createToneBadge(summary.severity),
    );
    const category = fixChangeCatalogEntry(summary.change_kind);
    const metadata = createElement("span", "issue-card-meta");
    const activeAttempts = toOptionalCount(summary.active_attempt_count);
    const observedAttempts = toOptionalCount(
      summary.observed_attempt_count,
    );
    const sameSession = toOptionalCount(
      summary.same_anchor_session_observation_count,
    );
    const otherSessions = toOptionalCount(
      summary.other_session_observation_count,
    );
    metadata.append(
      createElement("span", "", category.label),
      createElement(
        "span",
        "",
        `${
          activeAttempts === null
            ? "Active attempt count unavailable"
            : `${formatNumber(activeAttempts)} active`
        } · ${
          observedAttempts === null
            ? "Observed-attempt count unavailable"
            : `${formatNumber(observedAttempts)} with matching evidence`
        }`,
      ),
      createElement(
        "span",
        "",
        `${
          sameSession === null
            ? "Original-session count unavailable"
            : `${formatNumber(sameSession)} original-session`
        } · ${
          otherSessions === null
            ? "Other-session count unavailable"
            : `${formatNumber(otherSessions)} other-session`
        }`,
      ),
      createElement(
        "span",
        "",
        formatRelativeTime(parseDate(summary.recorded_at)),
      ),
    );
    button.append(
      header,
      createElement("span", "monitoring-state-title", stateEntry.title),
    );
    if (stateEntry.detail) {
      button.append(
        createElement("span", "issue-card-caveat", stateEntry.detail),
      );
    }
    button.append(metadata);
    if (toFiniteNumber(summary.same_anchor_session_observation_count) > 0) {
      button.append(
        createElement(
          "span",
          "issue-card-caveat",
          "This may be continuation within the original session.",
        ),
      );
    }
    if (
      summary.analysis_complete === false &&
      normalizeFixRecurrenceState(summary.fix_recurrence_state) ===
        "matching_evidence_observed"
    ) {
      button.append(
        createElement(
          "span",
          "issue-card-caveat",
          "Additional matching evidence may exist because monitoring coverage is incomplete.",
        ),
      );
    }
    if (
      normalizeFixRecurrenceState(summary.fix_recurrence_state) ===
        "retracted" &&
      toOptionalCount(summary.historical_matching_evidence_count) > 0
    ) {
      button.append(
        createElement(
          "span",
          "issue-card-caveat",
          "Matching evidence was recorded before this attempt record was retracted.",
        ),
      );
    }
    const focusKey = `${issueID}\u0000${annotationID}`;
    focusRegistry.monitoringCards.set(focusKey, button);
    const selected =
      state.selectedIssueSource === "monitoring" &&
      state.selectedIssueID === issueID &&
      state.selectedDrivingAnnotationID === annotationID;
    article.classList.toggle("is-selected", selected);
    button.setAttribute("aria-pressed", String(selected));
    button.setAttribute("aria-current", selected ? "true" : "false");
    button.addEventListener("click", () => {
      void selectMonitoringIssue(summary, true);
    });
    article.append(button);
    return article;
  }

  function selectMonitoringIssue(summary, moveFocus) {
    const issueID = readText(summary && summary.issue_id);
    const annotationID = readText(summary && summary.annotation_id);
    if (!issueID || !annotationID) return Promise.resolve(false);
    const monitoringViewCursor = readCursor(
      state.fixMonitoring.viewCursor,
    );
    if (!monitoringViewCursor) {
      state.fixMonitoring.status = "error";
      state.fixMonitoring.error =
        "This saved view is out of date. Refresh After attempts before opening it.";
      renderFixMonitoring();
      return Promise.resolve(false);
    }
    if (moveFocus) state.attentionRefreshGeneration += 1;
    state.selectedIssueID = issueID;
    state.selectedIssueKind = "issue";
    state.selectedIssueSource = "monitoring";
    state.selectedIssueFamilyID = "";
    state.selectedIssueEvidenceContext = "";
    state.selectedDrivingAnnotationID = annotationID;
    state.selectedIssue = monitoringSummaryIssue(summary);
    state.issueReturnFocus = {
      type: "monitoring",
      key: `${issueID}\u0000${annotationID}`,
    };
    state.occurrences = [];
    state.occurrenceNextCursor = "";
    state.occurrenceHasMore = false;
    state.occurrenceStatus = "idle";
    resetIssueEvidencePreview();
    resetFixIssueState();
    state.fixEligibilityStatus = "loading";
    elements.attentionWelcome.hidden = true;
    elements.issueDetail.hidden = false;
    document.body.classList.add("is-attention-detail-open");
    renderIssueBucket(state.issues);
    renderIssueBucket(state.evidenceGaps);
    renderFixMonitoring();
    renderIssueDetail();
    applyPaneAccessibility();
    if (moveFocus) focusCurrentElement(elements.issueDetailHeading);
    return loadFixMonitoringDetail(
      issueID,
      false,
      monitoringViewCursor,
      false,
      moveFocus ? annotationID : "",
    );
  }

  function refreshSelectedMonitoringIssue(summary) {
    const issueID = readText(summary && summary.issue_id);
    const annotationID = readText(summary && summary.annotation_id);
    if (
      !issueID ||
      !annotationID ||
      issueID !== state.selectedIssueID ||
      state.selectedIssueSource !== "monitoring"
    ) {
      return Promise.resolve(false);
    }
    const monitoringViewCursor = readCursor(
      state.fixMonitoring.viewCursor,
    );
    if (!monitoringViewCursor) {
      state.fixHistoryStale = true;
      state.fixHistoryError =
        "The saved After attempts view is out of date. The open attempt was preserved; refresh before loading it again.";
      renderFixAttempts();
      return Promise.resolve(false);
    }
    state.selectedDrivingAnnotationID = annotationID;
    state.issueReturnFocus = {
      type: "monitoring",
      key: `${issueID}\u0000${annotationID}`,
    };
    renderFixMonitoring();
    return loadFixMonitoringDetail(
      issueID,
      false,
      monitoringViewCursor,
      true,
      annotationID,
    );
  }

  function monitoringSummaryIssue(summary) {
    return {
      issue_id: readText(summary.issue_id),
      title_code: readText(summary.title_code),
      severity: readText(summary.severity),
      analysis_status: "",
      evidence_complete: null,
      retained_history_only: true,
    };
  }

  async function loadIssueBucket(bucket, append) {
    if (bucket.kind === "family") {
      return loadAttentionFamilies(bucket, append);
    }
    const generation = ++bucket.requestGeneration;
    const cursor = append ? bucket.nextCursor : "";
    bucket.status = append ? "loading-more" : "loading";
    bucket.error = null;
    renderIssueBucket(bucket);
    try {
      const response = await apiGet(buildIssuePath(bucket.kind, cursor));
      if (generation !== bucket.requestGeneration) return false;
      const page = Array.isArray(response.data) ? response.data : [];
      const pagination = requireCursorPage(
        response,
        cursor,
        bucket.kind === "evidence_gap" ? "evidence-gap" : "issue-list",
      );
      const viewCursor = readCursor(response.view_cursor);
      if (!viewCursor) {
        throw new Error(
          "Local API returned issue results without a view cursor.",
        );
      }
      if (cursor && viewCursor !== bucket.viewCursor) {
        throw new Error(
          "Local API changed the issue-list view cursor during continuation.",
        );
      }
      const selection = readIssueSelection(response.selection, bucket.kind);
      bucket.data =
        append && cursor
          ? deduplicateByID(bucket.data.concat(page), "issue_id")
          : deduplicateByID(page, "issue_id");
      bucket.nextCursor = pagination.nextCursor;
      bucket.hasMore = pagination.hasMore;
      bucket.viewCursor = viewCursor;
      bucket.analysis = isRecord(response.analysis) ? response.analysis : null;
      bucket.selection = selection;
      bucket.status = "ready";
      renderIssueBucket(bucket);
      renderAnalysisCoverage();
      return true;
    } catch (error) {
      if (generation !== bucket.requestGeneration) return false;
      if (isCursorExpired(error)) {
        await refreshAttentionAfterExpiry();
        return false;
      }
      bucket.status = "error";
      bucket.error = error;
      renderIssueBucket(bucket);
      renderAnalysisCoverage();
      showError(
        bucket.kind === "evidence_gap"
          ? "Unable to load evidence gaps"
          : "Unable to load Attention",
        error,
      );
      return false;
    }
  }

  async function loadAttentionFamilies(bucket, append) {
    const generation = ++bucket.requestGeneration;
    const cursor = append ? bucket.nextCursor : "";
    bucket.status = append ? "loading-more" : "loading";
    bucket.error = null;
    renderIssueBucket(bucket);
    try {
      const response = await apiGet(buildAttentionFamilyPath(cursor));
      if (generation !== bucket.requestGeneration) return false;
      const page = Array.isArray(response.data) ? response.data : [];
      const pagination = requireCursorPage(
        response,
        cursor,
        "attention-family-list",
      );
      const selection = readIssueSelection(response.selection, "issue");
      page.forEach(requireAttentionFamilySummary);
      bucket.data =
        append && cursor
          ? deduplicateByID(bucket.data.concat(page), "family_id")
          : deduplicateByID(page, "family_id");
      bucket.nextCursor = pagination.nextCursor;
      bucket.hasMore = pagination.hasMore;
      bucket.analysis = readGlobalAnalysisCoverage(
        response.global_analysis_coverage,
        readGlobalAnalysisCoverage(response.analysis, bucket.analysis),
      );
      bucket.selection = selection;
      bucket.status = "ready";
      renderIssueBucket(bucket);
      renderAnalysisCoverage();
      return true;
    } catch (error) {
      if (generation !== bucket.requestGeneration) return false;
      if (isCursorExpired(error)) {
        await refreshAttentionAfterExpiry();
        return false;
      }
      bucket.status = "error";
      bucket.error = error;
      renderIssueBucket(bucket);
      renderAnalysisCoverage();
      showError("Unable to load Attention findings", error);
      return false;
    }
  }

  function buildAttentionFamilyPath(cursor) {
    if (cursor) {
      return `/v1/attention-families?${new URLSearchParams({ cursor }).toString()}`;
    }
    const parameters = new URLSearchParams({
      limit: String(pageLimits.issues.page),
      attention_kind: "issue",
      experimental: state.issueFilters.experimental ? "include" : "stable",
    });
    if (state.issueFilters.severity) {
      parameters.set("severity", state.issueFilters.severity);
    }
    if (state.issueFilters.harness) {
      parameters.set("harness", state.issueFilters.harness);
    }
    if (state.issueFilters.origin) {
      parameters.set("origin", state.issueFilters.origin);
    }
    if (state.issueFilters.analysisStatus) {
      parameters.set("analysis_status", state.issueFilters.analysisStatus);
    }
    return `/v1/attention-families?${parameters.toString()}`;
  }

  function buildIssuePath(kind, cursor) {
    if (cursor) {
      return `/v1/issues?${new URLSearchParams({ cursor }).toString()}`;
    }
    const parameters = new URLSearchParams({
      limit: String(pageLimits.issues.page),
      attention_kind: kind,
      experimental: state.issueFilters.experimental ? "include" : "stable",
    });
    if (state.issueFilters.severity) {
      parameters.set("severity", state.issueFilters.severity);
    }
    if (state.issueFilters.harness) {
      parameters.set("harness", state.issueFilters.harness);
    }
    if (state.issueFilters.origin) {
      parameters.set("origin", state.issueFilters.origin);
    }
    if (state.issueFilters.analysisStatus) {
      parameters.set("analysis_status", state.issueFilters.analysisStatus);
    }
    return `/v1/issues?${parameters.toString()}`;
  }

  function renderIssueBucket(bucket) {
    if (bucket.kind === "family") {
      renderAttentionFamilyBucket(bucket);
      return;
    }
    const selection = bucket.selection || expectedIssueSelection(bucket.kind);
    const isGap = selection.attention_kind === "evidence_gap";
    const list = isGap ? elements.evidenceGapList : elements.issueList;
    const loading = isGap
      ? elements.evidenceGapsLoading
      : elements.issuesLoading;
    const empty = isGap ? elements.evidenceGapsEmpty : elements.issuesEmpty;
    const count = isGap ? elements.evidenceGapCount : elements.issueCount;
    const pagination = isGap
      ? elements.evidenceGapsPagination
      : elements.issuesPagination;
    const pageStatus = isGap
      ? elements.evidenceGapsPageStatus
      : elements.issuesPageStatus;
    const loadMore = isGap
      ? elements.evidenceGapsLoadMore
      : elements.issuesLoadMore;
    clearIssueFocusRegistry(bucket.kind);
    const fragment = document.createDocumentFragment();
    bucket.data.forEach((issue) => {
      fragment.append(createIssueCard(issue, bucket.kind));
    });
    list.replaceChildren(fragment);
    loading.hidden = bucket.status !== "loading";
    empty.hidden =
      bucket.data.length !== 0 ||
      bucket.status === "loading" ||
      bucket.status === "idle";
    count.textContent = bucket.hasMore
      ? `${bucket.data.length}+`
      : String(bucket.data.length);
    pagination.hidden = !bucket.hasMore;
    loadMore.disabled = bucket.status === "loading-more";
    pageStatus.textContent = bucket.hasMore
      ? `Showing ${bucket.data.length}; more are available.`
      : `Showing ${bucket.data.length} returned ${
          isGap ? "evidence gaps" : "issues"
        }.`;
    if (!empty.hidden) renderIssueEmptyState(bucket);
    renderAttentionTotals();
    renderAttentionFilters();
  }

  function renderAttentionFamilyBucket(bucket) {
    focusRegistry.familyCards.clear();
    const fragment = document.createDocumentFragment();
    bucket.data.forEach((family) => {
      fragment.append(createAttentionFamilyCard(family));
    });
    elements.issueList.replaceChildren(fragment);
    elements.issuesLoading.hidden = bucket.status !== "loading";
    elements.issuesEmpty.hidden =
      bucket.data.length !== 0 ||
      bucket.status === "loading" ||
      bucket.status === "idle";
    elements.issueCount.textContent = bucket.hasMore
      ? `${bucket.data.length}+`
      : String(bucket.data.length);
    elements.issuesPagination.hidden = !bucket.hasMore;
    elements.issuesLoadMore.disabled = bucket.status === "loading-more";
    elements.issuesPageStatus.textContent = bucket.hasMore
      ? `Showing ${bucket.data.length} findings; more are available.`
      : `Showing ${bucket.data.length} ${
          bucket.data.length === 1 ? "finding" : "findings"
        }.`;
    if (!elements.issuesEmpty.hidden) renderIssueEmptyState(bucket);
    renderAttentionTotals();
    renderAttentionFilters();
  }

  function renderIssueEmptyState(bucket) {
    const isGap = bucket.kind === "evidence_gap";
    const title = isGap
      ? elements.evidenceGapsEmptyTitle
      : elements.issuesEmptyTitle;
    const detail = isGap
      ? elements.evidenceGapsEmptyDetail
      : elements.issuesEmptyDetail;
    if (bucket.status === "error") {
      title.textContent = isGap
        ? "Evidence gaps unavailable"
        : experiencePageText("Attention grouping is unavailable");
      detail.textContent = isGap
        ? "The Local read failed. Existing session data remains available."
        : "Session data remains available.";
      return;
    }
    if (attentionFiltersActive()) {
      title.textContent = isGap
        ? "No evidence gaps match these filters"
        : "No findings match these filters";
      detail.textContent =
        "Analysis coverage is shown above; this is not a claim that all activity is problem-free.";
      return;
    }
    const analysis = bucket.analysis || state.issues.analysis;
    if (analysis && analysis.complete === true) {
      title.textContent = isGap
        ? "No evidence gaps reported by available checks"
        : "No findings were reported in completed local analysis";
      detail.textContent = isGap
        ? "Supported completed analysis did not report a verification evidence gap."
        : experiencePageText(
            "No reviewed finding type produced a stable Attention item.",
          );
      return;
    }
    title.textContent = isGap
      ? "No evidence gaps are available from completed analysis"
      : "No findings are available from completed analysis";
    detail.textContent = incompleteCoverageText(analysis);
  }

  function renderAnalysisCoverage() {
    const analysis = state.issues.analysis || state.evidenceGaps.analysis;
    elements.coverageCurrent.textContent = analysis
      ? formatNumber(analysis.current_sessions)
      : "—";
    elements.coveragePending.textContent = analysis
      ? formatNumber(analysis.pending_sessions)
      : "—";
    elements.coverageFailed.textContent = analysis
      ? formatNumber(analysis.failed_sessions)
      : "—";
    elements.coverageTruncated.textContent = analysis
      ? formatNumber(analysis.truncated_sessions)
      : "—";
    elements.coverageCompleteness.textContent =
      analysis && analysis.complete === true ? "Complete" : "Incomplete";
    const unscoped = analysis ? toFiniteNumber(analysis.unscoped_sessions) : 0;
    elements.coverageUnscoped.textContent = unscoped
      ? `${formatNumber(unscoped)} fully analyzed ${
          unscoped === 1 ? "session is" : "sessions are"
        } missing enough project information to determine whether findings are related.`
      : "Project information is available where it was reported.";
    elements.coverageThrough.textContent = parseDate(
      analysis && analysis.analysis_through,
    )
      ? `Latest completed analysis · ${formatRelativeTime(
          analysis.analysis_through,
        )}`
      : "Latest completed analysis unavailable";
  }

  function renderAttentionTotals() {
    if (state.attentionMode === "issues") {
      const total = state.costIssues.length;
      elements.attentionCount.textContent =
        state.costIssueStatus === "ready" ? String(total) : "—";
      elements.attentionNavCount.hidden = total === 0;
      elements.attentionNavCount.textContent = String(total);
      return;
    }
    const total = state.issues.data.length;
    elements.attentionCount.textContent = state.issues.hasMore
      ? `${total}+`
      : String(total);
    elements.attentionNavCount.hidden = total === 0;
    elements.attentionNavCount.textContent = state.issues.hasMore
      ? `${total}+`
      : String(total);
  }

  function renderAttentionFilters() {
    const selection =
      state.issues.selection || expectedIssueSelection("issue");
    const experimental =
      selection.includes_experimental === true ||
      state.issueFilters.experimental;
    const active = attentionFiltersActive();
    elements.attentionFilterNote.textContent = experimental
      ? "Experimental findings are included and labeled."
      : active
        ? "Reviewed findings matching the selected filters."
        : "Showing reviewed findings.";
    elements.clearAttentionFilters.disabled = !active;
    elements.issueFilterDisclosure.textContent = occurrenceFiltersActive()
      ? "Displayed finding and evidence-gap counts match the active filters. The analysis summary still covers all stored sessions in this saved view."
      : "";
  }

  function attentionFiltersActive() {
    return (
      state.issueFilters.experimental ||
      [
        "severity",
        "harness",
        "origin",
        "analysisStatus",
      ].some((key) => Boolean(state.issueFilters[key]))
    );
  }

  function occurrenceFiltersActive() {
    return Boolean(
      state.issueFilters.harness ||
        state.issueFilters.analysisStatus ||
        state.issueFilters.severity ||
        state.issueFilters.origin,
    );
  }

  function requireAttentionFamilySummary(family) {
    if (
      !isRecord(family) ||
      !readText(family.family_id) ||
      !["exact_issue", "mapped_upstream"].includes(readText(family.kind)) ||
      !readText(family.representative_issue_id) ||
      !readCursor(family.view_cursor) ||
      !isRecord(family.catalog)
    ) {
      throw new Error("Local API returned an invalid Attention family.");
    }
    return family;
  }

  function createAttentionFamilyCard(family) {
    const familyID = readText(family.family_id);
    const kind = readText(family.kind);
    const catalog = isRecord(family.catalog) ? family.catalog : {};
    const article = createElement("article", "issue-card family-card");
    const button = createElement("button", "issue-card-main");
    button.type = "button";
    button.setAttribute(
      "aria-pressed",
      String(
        Boolean(state.selectedFamily) &&
          readText(state.selectedFamily.family_id) === familyID,
      ),
    );
    button.addEventListener("click", () => {
      if (kind === "exact_issue") {
        const issue = exactFamilyRepresentative(family);
        void selectIssue(issue, "issue", true, {
          viewCursor: readCursor(family.view_cursor),
          catalog,
          familyID,
          returnFocus: {
            type: "family",
            familyID,
          },
        });
        return;
      }
      if (toFiniteNumber(family.supporting_issue_count) === 1) {
        void selectSingleMemberAttentionFamily(family, true);
        return;
      }
      void selectAttentionFamily(family, true);
    });
    if (familyID) focusRegistry.familyCards.set(familyFocusKey(familyID), button);

    const top = createElement("span", "issue-card-top");
    const severity = createElement(
      "span",
      "severity-badge",
      readableLabel(family.severity, "Reported"),
    );
    severity.dataset.tone = severityTone(family.severity);
    const title = createElement("span", "issue-card-title");
    title.append(
      createElement(
        "strong",
        "",
        readText(catalog.display_title) || "Finding",
      ),
      createElement("small", "", attentionFamilyObservationSummary(family)),
    );
    top.append(severity, title);
    const status = normalizeAnalysisStatus(family.analysis_status);
    if (status !== "current") {
      top.append(
        createElement("span", "analysis-badge", analysisStatusLabel(status)),
      );
    }
    const meta = createElement("span", "issue-card-meta");
    meta.append(
      createElement(
        "span",
        "",
        `${formatNumber(family.session_count)} ${
          toFiniteNumber(family.session_count) === 1 ? "session" : "sessions"
        } with this finding`,
      ),
      createElement("span", "", issueHarnessLabel(family.harnesses)),
      createElement(
        "time",
        "",
        `Latest ${formatFullDate(parseDate(family.last_observed_at)) || "date unavailable"}`,
      ),
    );
    button.append(top, meta);
    button.append(
      createElement(
        "span",
        "issue-card-action",
        kind === "mapped_upstream"
          ? "Review what was observed and the supporting sessions"
          : issueNextActionLabel(catalog.next_evidence_action),
      ),
    );
    if (family.experimental === true) {
      button.append(
        createElement("span", "experimental-label", "Experimental finding"),
      );
    }
    article.append(button);
    return article;
  }

  function attentionFamilyObservationSummary(family) {
    const sessions = Number(family && family.session_count);
    const sessionText =
      Number.isFinite(sessions) && sessions > 0
        ? `${formatNumber(sessions)} ${
            sessions === 1 ? "session" : "sessions"
          } with this finding`
        : "Session count unavailable";
    return `${sessionText} · ${issueHarnessLabel(
      family && family.harnesses,
    )} · latest ${
      formatFullDate(parseDate(family && family.last_observed_at)) ||
      "date unavailable"
    }`;
  }

  function exactFamilyRepresentative(family) {
    return {
      issue_id: readText(family.representative_issue_id),
      title_code: "",
      severity: readText(family.severity),
      confidence: readText(family.confidence),
      first_observed_at: family.first_observed_at,
      last_observed_at: family.last_observed_at,
      occurrence_count: family.occurrence_count,
      session_count: family.session_count,
      harnesses: Array.isArray(family.harnesses) ? family.harnesses : [],
      analysis_status: readText(family.analysis_status),
      evidence_complete: family.evidence_complete,
      retained_history_only: family.retained_history_only === true,
      experimental: family.experimental === true,
    };
  }

  async function selectAttentionFamily(
    family,
    moveFocus,
    revealDetail = true,
  ) {
    const familyID = readText(family && family.family_id);
    const viewCursor = readCursor(family && family.view_cursor);
    if (
      !familyID ||
      readText(family && family.kind) !== "mapped_upstream" ||
      !viewCursor
    ) {
      return false;
    }
    if (moveFocus) state.attentionRefreshGeneration += 1;
    state.selectedFamily = family;
    state.selectedFamilyViewCursor = viewCursor;
    state.familyMembers = [];
    state.familyMemberNextCursor = "";
    state.familyMemberHasMore = false;
    state.familyMemberStatus = "loading";
    state.familyMemberError = "";
    state.familyReturnFocus = { type: "family", familyID };
    focusRegistry.familyMembers.clear();
    elements.attentionWelcome.hidden = revealDetail;
    elements.issueDetail.hidden = true;
    elements.familyDetail.hidden = !revealDetail;
    document.body.classList.toggle(
      "is-attention-detail-open",
      revealDetail,
    );
    renderIssueBucket(state.issues);
    renderAttentionFamilyDetail();
    applyPaneAccessibility();
    if (moveFocus && revealDetail) {
      focusCurrentElement(elements.familyDetailHeading);
    }
    return loadAttentionFamilyDetail(false);
  }

  async function selectSingleMemberAttentionFamily(family, moveFocus) {
    const ready = await selectAttentionFamily(family, false, false);
    if (!ready) {
      closeFamilyDetail(false);
      showAttentionNotice(
        "The selected session could not be opened. Refresh Attention and try again.",
        "error",
      );
      return false;
    }
    if (
      state.familyMembers.length !== 1 ||
      state.familyMemberHasMore
    ) {
      elements.attentionWelcome.hidden = true;
      elements.familyDetail.hidden = false;
      document.body.classList.add("is-attention-detail-open");
      renderAttentionFamilyDetail();
      applyPaneAccessibility();
      if (moveFocus) focusCurrentElement(elements.familyDetailHeading);
      return true;
    }
    const member = state.familyMembers[0];
    const familyID = readText(family && family.family_id);
    const evidenceContext = attentionFamilyEvidenceContext(family);
    closeFamilyDetail(false);
    return selectIssue(member.issue, "issue", moveFocus, {
      viewCursor: readCursor(member.view_cursor),
      catalog: member.catalog,
      familyID,
      evidenceContext,
      returnFocus: { type: "family", familyID },
    });
  }

  function attentionFamilyEvidenceContext(family) {
    const catalog = isRecord(family && family.catalog) ? family.catalog : {};
    return readText(catalog.mapping_key) ===
      "attention.agent_guardrails_configuration"
      ? "mapped-guardrail"
      : "";
  }

  function issueEvidenceContext(issue, catalog, previous = "") {
    if (previous === "mapped-guardrail") return previous;
    const sourceSignalCode =
      safeSourceSignalCode(catalog && catalog.source_signal_code) ||
      safeSourceSignalCode(issue && issue.source_signal_code);
    return sourceSignalCode === "tamper.guardrails_off"
      ? "mapped-guardrail"
      : "";
  }

  async function loadAttentionFamilyDetail(append) {
    const family = state.selectedFamily;
    const familyID = readText(family && family.family_id);
    if (!familyID) return false;
    const generation = ++state.familyMemberRequestGeneration;
    const cursor = append ? state.familyMemberNextCursor : "";
    state.familyMemberStatus = append ? "loading-more" : "loading";
    state.familyMemberError = "";
    if (!append) {
      state.familyMembers = [];
      state.familyMemberNextCursor = "";
      state.familyMemberHasMore = false;
    }
    renderAttentionFamilyDetail();
    const parameters = cursor
      ? new URLSearchParams({ cursor })
      : new URLSearchParams({
          limit: String(pageLimits.familyMembers.page),
          view_cursor: state.selectedFamilyViewCursor,
        });
    try {
      const response = await apiGet(
        `/v1/attention-families/${encodeURIComponent(
          familyID,
        )}?${parameters.toString()}`,
      );
      if (
        generation !== state.familyMemberRequestGeneration ||
        familyID !==
          readText(state.selectedFamily && state.selectedFamily.family_id)
      ) {
        return false;
      }
      const payload = isRecord(response.data) ? response.data : {};
      const responseFamily = requireAttentionFamilySummary(payload.family);
      if (
        readText(responseFamily.family_id) !== familyID ||
        readText(responseFamily.kind) !== "mapped_upstream"
      ) {
        throw new Error(
          "Local API returned inconsistent Attention family detail.",
        );
      }
      const responseViewCursor = readCursor(response.view_cursor);
      if (
        !responseViewCursor ||
        responseViewCursor !== state.selectedFamilyViewCursor
      ) {
        throw new Error(
          "Local API changed the Attention family view cursor.",
        );
      }
      const members = Array.isArray(payload.members) ? payload.members : [];
      members.forEach(requireAttentionFamilyMember);
      const pagination = requireCursorPage(
        response,
        cursor,
        "attention-family-members",
      );
      state.selectedFamily = responseFamily;
      state.familyMembers =
        append && cursor
          ? deduplicateFamilyMembers(state.familyMembers.concat(members))
          : deduplicateFamilyMembers(members);
      state.familyMemberNextCursor = pagination.nextCursor;
      state.familyMemberHasMore = pagination.hasMore;
      state.familyMemberStatus = "ready";
      state.issues.analysis = readGlobalAnalysisCoverage(
        response.global_analysis_coverage,
        state.issues.analysis,
      );
      renderAttentionFamilyDetail();
      renderAnalysisCoverage();
      return true;
    } catch (error) {
      if (
        generation !== state.familyMemberRequestGeneration ||
        familyID !==
          readText(state.selectedFamily && state.selectedFamily.family_id)
      ) {
        return false;
      }
      if (isCursorExpired(error)) {
        clearAttentionFamilyDetailState();
        await refreshAttentionAfterExpiry();
        return false;
      }
      state.familyMemberStatus = "error";
      state.familyMemberError = experiencePageText(
        "Sessions with this finding could not be loaded. Refresh Attention and try again.",
      );
      renderAttentionFamilyDetail();
      return false;
    }
  }

  function requireAttentionFamilyMember(member) {
    if (
      !isRecord(member) ||
      !isRecord(member.issue) ||
      !readText(member.issue.issue_id) ||
      !isRecord(member.catalog) ||
      readText(member.session_selection) !== "latest_matching_session" ||
      !readCursor(member.view_cursor)
    ) {
      throw new Error("Local API returned an invalid Attention family member.");
    }
    return member;
  }

  function deduplicateFamilyMembers(values) {
    const seen = new Set();
    return values.filter((member) => {
      const issueID = readText(member && member.issue && member.issue.issue_id);
      if (!issueID || seen.has(issueID)) return false;
      seen.add(issueID);
      return true;
    });
  }

  function renderAttentionFamilyDetail() {
    const family = state.selectedFamily;
    if (!family) return;
    const catalog = isRecord(family.catalog) ? family.catalog : {};
    const status = normalizeAnalysisStatus(family.analysis_status);
    elements.familyDetailHeading.textContent =
      readText(catalog.display_title) || "Finding";
    elements.familyObservation.textContent =
      readText(catalog.observation_statement) ||
      "Belay found local activity that may need your review.";
    elements.familyCaveat.textContent = [
      readText(catalog.caveat),
      "Belay grouped these records because the same permission-mode finding appeared. The sessions may be unrelated.",
    ]
      .filter(Boolean)
      .join(" ");
    elements.familyNextAction.textContent = issueNextActionLabel(
      catalog.next_evidence_action,
    );
    elements.familyAnalysisQualifier.textContent =
      analysisQualifiers[status] || "";
    elements.familyDetailBadges.replaceChildren(
      createToneBadge(family.severity),
      createElement("span", "meta-badge", analysisStatusLabel(status)),
    );
    elements.familySummary.textContent = attentionFamilyDetailSummary(family);
    focusRegistry.familyMembers.clear();
    const fragment = document.createDocumentFragment();
    state.familyMembers.forEach((member) => {
      fragment.append(createAttentionFamilyMember(member));
    });
    if (state.familyMemberStatus === "error") {
      fragment.append(
        createElement(
          "p",
          "family-member-error",
          state.familyMemberError ||
            experiencePageText(
              "Sessions with this finding could not be loaded. Other Attention data remains available.",
            ),
        ),
      );
    }
    elements.familyMemberList.replaceChildren(fragment);
    elements.familyMembersLoading.hidden =
      state.familyMemberStatus !== "loading";
    elements.familyMembersEmpty.hidden =
      state.familyMemberStatus !== "ready" ||
      state.familyMembers.length !== 0;
    elements.familyMemberCount.textContent = state.familyMemberHasMore
      ? `${state.familyMembers.length}+`
      : String(state.familyMembers.length);
    elements.familyMembersPagination.hidden = !state.familyMemberHasMore;
    elements.familyMembersLoadMore.disabled =
      state.familyMemberStatus === "loading-more";
    elements.familyMembersPageStatus.textContent = state.familyMemberHasMore
      ? `Showing ${state.familyMembers.length} sessions with this finding; more are available.`
      : `Showing ${state.familyMembers.length} ${
          state.familyMembers.length === 1 ? "session" : "sessions"
        } with this finding.`;
  }

  function attentionFamilyDetailSummary(family) {
    return `${formatNumber(family.session_count)} ${
      toFiniteNumber(family.session_count) === 1 ? "session" : "sessions"
    } with this finding · latest ${
      formatFullDate(parseDate(family.last_observed_at)) || "date unavailable"
    }`;
  }

  function createAttentionFamilyMember(member) {
    const issue = member.issue;
    const familyID = readText(
      state.selectedFamily && state.selectedFamily.family_id,
    );
    const issueID = readText(issue.issue_id);
    const key = familyMemberFocusKey(familyID, issueID);
    const button = createElement("button", "family-member-card");
    button.type = "button";
    const issueSessionCount = Number(issue.session_count);
    const multipleSessions =
      readText(member.session_selection) === "latest_matching_session" &&
      Number.isFinite(issueSessionCount) &&
      issueSessionCount > 1;
    const citedCount = toFiniteNumber(member.cited_event_count);
    const evidenceWindow = attentionEvidenceWindow(member);
    button.append(
      createElement(
        "strong",
        "",
        multipleSessions
          ? `Latest of ${formatNumber(issueSessionCount)} matching sessions`
          : `${issueHarnessLabel(issue.harnesses)} session`,
      ),
      createElement(
        "span",
        "",
        multipleSessions
          ? `${issueHarnessLabel(issue.harnesses)} · ${attentionSessionWindow(member)}`
          : attentionSessionWindow(member),
      ),
      createElement(
        "span",
        "family-member-state",
        `${formatNumber(citedCount)} cited ${
          citedCount === 1 ? "event" : "events"
        }${evidenceWindow ? ` · ${evidenceWindow}` : ""}`,
      ),
      createElement("span", "issue-card-action", "Review evidence"),
    );
    button.addEventListener("click", () => {
      void selectIssue(issue, "issue", true, {
        viewCursor: readCursor(member.view_cursor),
        catalog: member.catalog,
        source: "family",
        familyID,
        evidenceContext: attentionFamilyEvidenceContext(
          state.selectedFamily,
        ),
        returnFocus: { type: "family-member", key },
      });
    });
    focusRegistry.familyMembers.set(key, button);
    return button;
  }

  function attentionSessionWindow(member) {
    const start = parseDate(member && member.session_started_at);
    const lastActive = parseDate(member && member.session_last_active_at);
    if (!start && !lastActive) return "Session start unavailable";
    if (!start) {
      return `Session start unavailable · last activity ${formatFullDate(lastActive)}`;
    }
    if (!lastActive || start.getTime() === lastActive.getTime()) {
      return `Session started ${formatFullDate(start)}`;
    }
    return `Session activity · ${formatFullDate(start)} to ${formatFullDate(lastActive)}`;
  }

  function attentionEvidenceWindow(member) {
    const first = parseDate(member && member.evidence_first_at);
    const last = parseDate(member && member.evidence_last_at);
    if (!first && !last) return "";
    if (!first || !last || first.getTime() === last.getTime()) {
      return `Evidence from ${formatFullDate(first || last)}`;
    }
    return `Evidence from ${formatFullDate(first)} to ${formatFullDate(last)}`;
  }

  function familyScopeReliability(value) {
    return projectRelationshipLabel(value);
  }

  function createIssueCard(issue, kind) {
    const issueID = readText(issue.issue_id);
    const article = createElement("article", "issue-card");
    const button = createElement("button", "issue-card-main");
    button.type = "button";
    button.setAttribute(
      "aria-pressed",
      String(
        state.selectedIssueSource === "attention" &&
          issueID === state.selectedIssueID &&
          state.selectedIssueKind === kind,
      ),
    );
    button.addEventListener("click", () => {
      void selectIssue(issue, kind, true);
    });
    if (issueID) {
      focusRegistry.issueCards.set(issueFocusKey(kind, issueID), button);
    }
    const top = createElement("span", "issue-card-top");
    const severity = createElement(
      "span",
      "severity-badge",
      readableLabel(issue.severity, "Reported"),
    );
    severity.dataset.tone = severityTone(issue.severity);
    const title = createElement("span", "issue-card-title");
    const catalog = issueDisplayCatalog(issue);
    title.append(
      createElement("strong", "", catalog.title),
      createElement(
        "small",
        "",
        issueRecurrenceLabel(issue),
      ),
    );
    top.append(severity, title);
    const status = normalizeAnalysisStatus(issue.analysis_status);
    if (status !== "current") {
      top.append(
        createElement("span", "analysis-badge", analysisStatusLabel(status)),
      );
    }
    const meta = createElement("span", "issue-card-meta");
    meta.append(
      createElement(
        "span",
        "",
        `${formatNumber(issue.occurrence_count)} ${
          toFiniteNumber(issue.occurrence_count) === 1
            ? "occurrence"
            : "occurrences"
        }`,
      ),
      createElement("span", "", issueHarnessLabel(issue.harnesses)),
      createElement(
        "time",
        "",
        formatRelativeTime(issue.last_observed_at),
      ),
    );
    button.append(top, meta);
    const caveat = issueCaveat(issue);
    if (caveat) button.append(createElement("span", "issue-card-caveat", caveat));
    button.append(
      createElement(
        "span",
        "issue-card-action",
        catalog.action || "Inspect retained evidence",
      ),
    );
    if (catalog.experimental || issue.experimental === true) {
      button.append(
        createElement("span", "experimental-label", "Experimental finding"),
      );
    }
    article.append(button);
    return article;
  }

  function selectIssue(issue, kind, moveFocus, options = {}) {
    const issueID = readText(issue.issue_id);
    if (!issueID) return Promise.resolve(false);
    if (moveFocus) state.attentionRefreshGeneration += 1;
    state.selectedIssueID = issueID;
    state.selectedIssueKind = kind === "evidence_gap" ? "evidence_gap" : "issue";
    state.selectedIssueSource =
      options.source === "family" ? "family" : "attention";
    state.selectedIssueFamilyID = readText(options.familyID);
    state.selectedIssueEvidenceContext = readText(options.evidenceContext);
    state.selectedDrivingAnnotationID = "";
    state.selectedIssue = issue;
    state.selectedIssueCatalog = isRecord(options.catalog)
      ? options.catalog
      : null;
    state.selectedGlobalAnalysisCoverage = null;
    state.selectedIssueViewCursor = "";
    state.issueReturnFocus = options.returnFocus || {
      type: "issue",
      key: issueFocusKey(state.selectedIssueKind, issueID),
    };
    state.occurrences = [];
    state.occurrenceNextCursor = "";
    state.occurrenceHasMore = false;
    state.occurrenceStatus = "loading";
    resetIssueEvidencePreview();
    resetFixIssueState();
    elements.attentionWelcome.hidden = true;
    elements.familyDetail.hidden = true;
    elements.issueDetail.hidden = false;
    document.body.classList.add("is-attention-detail-open");
    renderIssueBucket(state.issues);
    renderIssueBucket(state.evidenceGaps);
    renderIssueDetail();
    applyPaneAccessibility();
    if (moveFocus) focusCurrentElement(elements.issueDetailHeading);
    const bucket =
      state.selectedIssueKind === "evidence_gap"
        ? state.evidenceGaps
        : state.issues;
    const viewCursor =
      readCursor(options.viewCursor) || readCursor(bucket.viewCursor);
    void loadFixEligibility(issueID, viewCursor);
    void loadFixMonitoringDetail(issueID, false, "", false, "");
    return loadIssueDetail(false, viewCursor);
  }

  async function loadIssueDetail(append, viewCursor) {
    const issueID = state.selectedIssueID;
    if (!issueID) return false;
    const generation = ++state.occurrenceRequestGeneration;
    state.occurrenceStatus = append ? "loading-more" : "loading";
    renderIssueDetail();
    const cursor = append ? state.occurrenceNextCursor : "";
    const parameters = cursor
      ? new URLSearchParams({ cursor })
      : new URLSearchParams({
          limit: String(pageLimits.occurrences.page),
        });
    if (!cursor && viewCursor) {
      parameters.set("view_cursor", viewCursor);
    }
    try {
      const response = await apiGet(
        `/v1/issues/${encodeURIComponent(issueID)}/occurrences?${parameters.toString()}`,
      );
      if (
        generation !== state.occurrenceRequestGeneration ||
        issueID !== state.selectedIssueID
      ) {
        return false;
      }
      const payload = isRecord(response.data) ? response.data : {};
      const issue = firstRecord(payload.issue, response.issue);
      const page = Array.isArray(payload.occurrences)
        ? payload.occurrences
        : Array.isArray(response.occurrences)
          ? response.occurrences
          : [];
      if (!issue || readText(issue.issue_id) !== issueID) {
        throw new Error("Local API returned inconsistent issue detail.");
      }
      const pagination = requireCursorPage(
        response,
        cursor,
        "issue-occurrence",
      );
      const responseViewCursor = readCursor(response.view_cursor);
      if (!responseViewCursor) {
        throw new Error(
          "Local API returned issue detail without a view cursor.",
        );
      }
      if (
        cursor &&
        responseViewCursor !== state.selectedIssueViewCursor
      ) {
        throw new Error(
          "Local API changed the issue-detail view cursor during continuation.",
        );
      }
      const catalog = readIssueCatalog(
        response.catalog,
        issue,
        state.selectedIssueCatalog,
      );
      const evidenceContext = issueEvidenceContext(
        issue,
        catalog,
        state.selectedIssueEvidenceContext,
      );
      const globalCoverage = readGlobalAnalysisCoverage(
        response.global_analysis_coverage,
        state.selectedGlobalAnalysisCoverage ||
          selectedIssueListCoverage(),
      );
      state.selectedIssue = issue;
      state.selectedIssueCatalog = catalog;
      state.selectedIssueEvidenceContext = evidenceContext;
      state.selectedGlobalAnalysisCoverage = globalCoverage;
      state.selectedIssueViewCursor = responseViewCursor;
      state.occurrences =
        append && cursor
          ? deduplicateByID(
              state.occurrences.concat(page),
              "occurrence_id",
            )
          : deduplicateByID(page, "occurrence_id");
      state.occurrenceNextCursor = pagination.nextCursor;
      state.occurrenceHasMore = pagination.hasMore;
      state.occurrenceStatus = "ready";
      renderIssueDetail();
      if (!append && state.occurrences.length > 0) {
        void loadIssueEvidencePreview(state.occurrences[0], false);
      }
      return true;
    } catch (error) {
      if (
        generation !== state.occurrenceRequestGeneration ||
        issueID !== state.selectedIssueID
      ) {
        return false;
      }
      if (isCursorExpired(error)) {
        await refreshAttentionAfterExpiry();
        return false;
      }
      state.occurrenceStatus = "error";
      renderIssueDetail();
      showError("Unable to load supporting sessions", error);
      return false;
    }
  }

  function loadMoreOccurrences() {
    if (
      !state.selectedIssueID ||
      !state.occurrenceHasMore ||
      !state.occurrenceNextCursor
    ) {
      return;
    }
    void loadIssueDetail(true, "");
  }

  function resetIssueEvidencePreview() {
    state.issueEvidencePreviewRequestGeneration += 1;
    if (state.issueEvidencePreviewController) {
      state.issueEvidencePreviewController.abort();
      state.issueEvidencePreviewController = null;
    }
    state.issueEvidencePreview = createIssueEvidencePreview();
    renderIssueEvidencePreview();
  }

  async function loadIssueEvidencePreview(occurrence, expanded) {
    const issueID = state.selectedIssueID;
    const sessionID = readText(occurrence && occurrence.session_id);
    const occurrenceID = readText(occurrence && occurrence.occurrence_id);
    const availableEventIDs = occurrenceEventIDs(occurrence);
    const eventIDs = availableEventIDs.slice(
      0,
      expanded ? pageLimits.events.maximum / 10 : 3,
    );
    if (state.issueEvidencePreviewController) {
      state.issueEvidencePreviewController.abort();
      state.issueEvidencePreviewController = null;
    }
    const generation = ++state.issueEvidencePreviewRequestGeneration;
    state.issueEvidencePreview = {
      ...createIssueEvidencePreview(),
      issueID,
      occurrenceID,
      sessionID,
      status: eventIDs.length ? "loading" : "ready",
      requestedCount: eventIDs.length,
      availableEventIDs,
      expanded,
    };
    renderIssueEvidencePreview();
    if (!issueID || !sessionID || eventIDs.length === 0) return;

    const controller = new AbortController();
    state.issueEvidencePreviewController = controller;
    const parameters = new URLSearchParams();
    eventIDs.forEach((eventID) => parameters.append("event_id", eventID));
    try {
      const response = await apiGet(
        `/v1/sessions/${encodeURIComponent(sessionID)}/events/lookup?${parameters.toString()}`,
        controller.signal,
      );
      if (
        generation !== state.issueEvidencePreviewRequestGeneration ||
        issueID !== state.selectedIssueID ||
        occurrenceID !== state.issueEvidencePreview.occurrenceID ||
        controller !== state.issueEvidencePreviewController
      ) {
        return;
      }
      const events = Array.isArray(response.data) ? response.data : [];
      state.issueEvidencePreview = {
        ...state.issueEvidencePreview,
        status: "ready",
        events,
        requestedCount:
          toFiniteNumber(response.requested_count) || eventIDs.length,
        foundCount: toFiniteNumber(response.found_count) || events.length,
        missingCount: Number.isFinite(Number(response.missing_count))
          ? toFiniteNumber(response.missing_count)
          : Math.max(0, eventIDs.length - events.length),
        missingEventIDs: Array.isArray(response.missing_event_ids)
          ? response.missing_event_ids.map(readText).filter(Boolean)
          : [],
      };
      renderIssueEvidencePreview();
    } catch (error) {
      if (
        generation !== state.issueEvidencePreviewRequestGeneration ||
        issueID !== state.selectedIssueID ||
        controller.signal.aborted
      ) {
        return;
      }
      if (isCursorExpired(error)) {
        resetIssueEvidencePreview();
        await refreshAttentionAfterExpiry();
        return;
      }
      state.issueEvidencePreview.status = "error";
      state.issueEvidencePreview.error = customerErrorMessage(
        error,
        "Cited evidence could not be loaded.",
      );
      renderIssueEvidencePreview();
    } finally {
      if (state.issueEvidencePreviewController === controller) {
        state.issueEvidencePreviewController = null;
      }
    }
  }

  function renderIssueEvidencePreview() {
    const preview = state.issueEvidencePreview;
    if (!elements.issueEvidencePreviewList) return;
    const fragment = document.createDocumentFragment();
    if (preview.status === "loading") {
      fragment.append(
        createElement("p", "overview-empty", "Loading cited events…"),
      );
    } else if (preview.status === "error") {
      fragment.append(
        createElement(
          "p",
          "overview-empty",
          preview.error || "Cited evidence could not be loaded.",
        ),
      );
    } else if (preview.status === "ready" && preview.events.length) {
      preview.events.forEach((event) => {
        fragment.append(
          createLookupEvent(event, state.selectedIssueEvidenceContext),
        );
      });
    } else if (preview.status === "ready") {
      fragment.append(
        createElement(
          "p",
          "overview-empty",
          preview.requestedCount
            ? "The cited events are no longer stored."
            : "No cited events were provided for this finding.",
        ),
      );
    }
    elements.issueEvidencePreviewList.replaceChildren(fragment);
    elements.issueEvidencePreviewCount.textContent =
      preview.status === "ready"
        ? `${preview.foundCount}/${preview.requestedCount}`
        : "—";
    elements.issueEvidencePreviewDisclosure.textContent =
      preview.status !== "ready"
        ? "Loading cited evidence…"
        : preview.requestedCount === 0
          ? "No cited events were provided for this finding."
          : preview.foundCount === 0
            ? "The cited events are no longer stored."
            : preview.missingCount > 0
              ? "Some cited events are no longer stored."
              : "Cited events for this finding.";
    elements.issueEvidencePreviewAll.hidden =
      preview.status === "idle" ||
      preview.status === "loading" ||
      preview.expanded ||
      preview.availableEventIDs.length <= 3;
  }

  function renderIssueDetail() {
    const issue = state.selectedIssue || {};
    const reducedApprovalMode = isReducedApprovalIssueContext(
      issue,
      state.selectedIssueCatalog,
    );
    const catalog =
      state.selectedIssueSource === "attention" ||
      state.selectedIssueSource === "family"
        ? issueDetailCatalog(issue, state.selectedIssueCatalog)
        : monitoringSubjectCatalog(issue);
    const historyOnly =
      state.fixHistoryCurrentIssueAvailable === false;
    const currentProjectionPending =
      state.selectedIssueSource === "monitoring" &&
      state.fixHistoryCurrentIssueAvailable === null;
    const kind =
      historyOnly
        ? "Retained attempt history"
        : currentProjectionPending
          ? "Follow-up evidence"
        : state.selectedIssueKind === "evidence_gap"
          ? "Evidence gap"
          : reducedApprovalMode
            ? "Permission mode finding"
            : "Issue";
    elements.issueDetailKind.textContent = kind;
    elements.issueDetailHeading.textContent = catalog.title;
    elements.issueExplanation.textContent = catalog.explanation;
    elements.issueNextAction.textContent = historyOnly
      ? "Inspect retained attempt history."
      : currentProjectionPending
        ? "Wait while Belay checks whether this finding is still current."
        : reducedApprovalMode
          ? sourceSignalCatalog["numbat/tamper.guardrails_off"].action
          : issueNextActionLabel(catalog.nextEvidenceAction);
    const status = normalizeAnalysisStatus(issue.analysis_status);
    const coverageQualifier = globalCoverageQualifier(
      state.selectedGlobalAnalysisCoverage,
    );
    const analysisQualifier = analysisQualifiers[status] || "";
    elements.issueAnalysisQualifier.textContent =
      historyOnly
        ? "This finding is no longer in the current results. Its recorded attempt history remains available."
        : currentProjectionPending
          ? "Checking whether this finding is still current."
          : [analysisQualifier, coverageQualifier].filter(Boolean).join(" ");
    elements.issueScopeDisclosure.textContent = historyOnly
      ? "New attempt recording and supporting sessions are unavailable because this finding is no longer current."
      : currentProjectionPending
        ? "Loading attempt history from the saved After attempts view."
        : issueScopeDisclosure(issue, catalog.caveat);
    const badges = [
      createToneBadge(issue.severity),
    ];
    if (!historyOnly && !currentProjectionPending) {
      if (!reducedApprovalMode) {
        badges.push(
          createElement("span", "meta-badge", issueRecurrenceLabel(issue)),
        );
      }
      badges.push(
        createElement("span", "meta-badge", analysisStatusLabel(status)),
      );
    } else {
      badges.push(createElement("span", "meta-badge", "History only"));
    }
    if (catalog.experimental || issue.experimental === true) {
      badges.push(createElement("span", "experimental-label", "Experimental"));
    }
    elements.issueDetailBadges.replaceChildren(...badges);
    elements.issueFingerprint.textContent =
      readText(issue.fingerprint_id) || "Unavailable";
    elements.copyIssueFingerprint.disabled = !readText(issue.fingerprint_id);
    elements.issueTechnicalDetails.hidden =
      reducedApprovalMode || historyOnly || currentProjectionPending;
    elements.fingerprintPanel.hidden =
      reducedApprovalMode || historyOnly || currentProjectionPending;
    elements.matchingSection.hidden = historyOnly || currentProjectionPending;
    elements.issueEvidencePreview.hidden =
      historyOnly || currentProjectionPending;
    renderIssueMetadata(issue);
    renderIssueEvidencePreview();
    renderFixAttempts();
    if (!historyOnly && !currentProjectionPending) renderOccurrences();
  }

  function monitoringSubjectCatalog(value) {
    const subject = isRecord(value && value.subject) ? value.subject : value;
    const titleCode = readText(subject && subject.title_code);
    return issueCatalogEntry(
      titleCode && !titleCode.startsWith("issue.")
        ? `issue.${titleCode}`
        : titleCode,
    );
  }

  function issueDetailCatalog(issue, metadata) {
    const display = monitoringSubjectCatalog(issue);
    const catalog = metadata || fallbackIssueCatalog(issue);
    if (isReducedApprovalIssueContext(issue, catalog)) {
      const reducedApproval =
        sourceSignalCatalog["numbat/tamper.guardrails_off"];
      return {
        ...display,
        title: reducedApproval.title,
        explanation: reducedApproval.explanation,
        caveat: reducedApproval.caveat,
        nextEvidenceAction: "review_agent_permissions",
      };
    }
    if (readText(issue && issue.title_code) === "issue.numbat_finding") {
      const importedFinding = issueCatalog["issue.numbat_finding"];
      return {
        ...display,
        title: importedFinding.title,
        explanation: importedFinding.explanation,
        caveat: importedFinding.caveat,
        nextEvidenceAction: "inspect_cited_events",
      };
    }
    return {
      ...display,
      title: readText(catalog.display_title) || display.title,
      explanation:
        readText(catalog.observation_statement) || display.explanation,
      caveat: readText(catalog.caveat),
      nextEvidenceAction:
        readText(catalog.next_evidence_action) || "inspect_cited_events",
    };
  }

  function issueNextActionLabel(action) {
    const labels = {
      inspect_cited_events: "Next: inspect the cited events.",
      inspect_matching_sessions:
        "Next: compare the related sessions.",
      inspect_verification_events:
        "Next: inspect the verification evidence.",
      review_agent_permissions:
        "Review the current agent permission mode. If this was intentional, no change may be needed.",
    };
    return labels[action] || labels.inspect_cited_events;
  }

  function monitoringAttemptSubjectIssue(attempt) {
    if (!isRecord(attempt) || !isRecord(attempt.subject)) return null;
    return {
      issue_id: readText(attempt.issue_id),
      title_code: readText(attempt.subject.title_code),
      severity: readText(attempt.subject.severity),
      confidence: readText(attempt.subject.confidence),
      origin: readText(attempt.subject.origin),
      detector_id: readText(attempt.subject.detector_id),
      detector_version: readText(attempt.subject.detector_version),
      fingerprint_version: readText(attempt.subject.fingerprint_version),
      retained_history_only: true,
    };
  }

  function renderIssueMetadata(issue) {
    if (
      isReducedApprovalIssueContext(issue, state.selectedIssueCatalog)
    ) {
      elements.issueMetadata.replaceChildren();
      return;
    }
    const metadata = [
      ["Observed", retainedInterval(issue)],
      ["Confidence", readableLabel(issue.confidence, "Unavailable")],
      [
        "Project relationship",
        projectRelationshipLabel(issue.scope_quality),
      ],
      ["Source", issueSourceLabel(issue.origin)],
      ["Check version", detectorVersionLabel(issue)],
      [
        "Evidence",
        evidenceCompletenessLabel(issue.evidence_complete),
      ],
    ];
    const fragment = document.createDocumentFragment();
    metadata.forEach(([label, value]) => {
      const row = createElement("div");
      row.append(createElement("dt", "", label), createElement("dd", "", value));
      fragment.append(row);
    });
    elements.issueMetadata.replaceChildren(fragment);
  }

  function issueSourceLabel(value) {
    return readText(value).toLowerCase() === "numbat"
      ? "Imported local check"
      : "Belay check";
  }

  function isReducedApprovalIssueContext(issue, catalog) {
    if (state.selectedIssueEvidenceContext === "mapped-guardrail") return true;
    const sourceSignalCode =
      safeSourceSignalCode(catalog && catalog.source_signal_code) ||
      safeSourceSignalCode(issue && issue.source_signal_code);
    return sourceSignalCode === "tamper.guardrails_off";
  }

  function resetFixIssueState() {
    state.fixEligibilityRequestGeneration += 1;
    state.fixHistoryRequestGeneration += 1;
    state.fixEligibility = null;
    state.fixEligibilityStatus = "idle";
    state.fixHistory = [];
    state.fixHistoryNextCursor = "";
    state.fixHistoryHasMore = false;
    state.fixHistoryStatus = "idle";
    state.fixHistoryViewCursor = "";
    state.fixHistoryCurrentIssueAvailable = null;
    state.fixHistoryStale = false;
    state.fixHistoryError = null;
    state.fixEvidenceEvaluatedAt = "";
    state.fixActionMessage = "";
    state.fixActionTone = "status";
    focusRegistry.fixTriggers.clear();
    focusRegistry.fixHistoryRows.clear();
    focusRegistry.fixRetractionTriggers.clear();
    focusRegistry.fixObservationRows.clear();
    fixObservationPages.forEach((page) => {
      page.requestGeneration += 1;
    });
    fixObservationPages.clear();
  }

  async function loadFixEligibility(issueID, viewCursor) {
    const generation = ++state.fixEligibilityRequestGeneration;
    state.fixEligibility = null;
    state.fixEligibilityStatus = "loading";
    renderFixAttempts();
    if (!viewCursor) {
      state.fixEligibilityStatus = "error";
      renderFixAttempts();
      return false;
    }
    const parameters = new URLSearchParams({ view_cursor: viewCursor });
    try {
      const response = await apiGet(
        `/v1/issues/${encodeURIComponent(issueID)}/fix-eligibility?${parameters.toString()}`,
      );
      requireFixSchema(response);
      if (
        generation !== state.fixEligibilityRequestGeneration ||
        issueID !== state.selectedIssueID
      ) {
        return false;
      }
      const eligibility = isRecord(response.data) ? response.data : {};
      state.fixEligibility = {
        eligible: eligibility.eligible === true,
        reason: readText(eligibility.reason),
        actionToken: readText(eligibility.action_token),
        expiresAt: readText(eligibility.expires_at),
        changeCatalogVersion: readText(
          eligibility.change_catalog_version,
        ),
      };
      state.fixEligibilityStatus = "ready";
      renderFixAttempts();
      return true;
    } catch (error) {
      if (
        generation !== state.fixEligibilityRequestGeneration ||
        issueID !== state.selectedIssueID
      ) {
        return false;
      }
      if (isCursorExpired(error)) {
        await refreshAttentionAfterExpiry();
        return false;
      }
      state.fixEligibility = null;
      state.fixEligibilityStatus = "error";
      renderFixAttempts();
      return false;
    }
  }

  async function loadMonitoringIssueEligibility(issueID) {
    const loadedBucket = state.issues.data.some(
      (entry) => readText(entry.issue_id) === issueID,
    )
      ? state.issues
      : state.evidenceGaps.data.some(
            (entry) => readText(entry.issue_id) === issueID,
          )
        ? state.evidenceGaps
        : null;
    const loadedViewCursor = readCursor(
      loadedBucket && loadedBucket.viewCursor,
    );
    if (loadedViewCursor) {
      return loadFixEligibility(issueID, loadedViewCursor);
    }

    const generation = ++state.fixEligibilityRequestGeneration;
    state.fixEligibility = null;
    state.fixEligibilityStatus = "loading";
    renderFixAttempts();
    const parameters = new URLSearchParams({ limit: "1" });
    try {
      const response = await apiGet(
        `/v1/issues/${encodeURIComponent(issueID)}/occurrences?${parameters.toString()}`,
      );
      if (
        generation !== state.fixEligibilityRequestGeneration ||
        issueID !== state.selectedIssueID ||
        state.selectedIssueSource !== "monitoring"
      ) {
        return false;
      }
      if (
        !isRecord(response) ||
        response.schema_version !== "belay.read.v1" ||
        !isRecord(response.data) ||
        !isRecord(response.data.issue)
      ) {
        throw new Error("Local API returned an invalid issue result.");
      }
      const issue = response.data.issue;
      const viewCursor = readCursor(response.view_cursor);
      if (readText(issue.issue_id) !== issueID || !viewCursor) {
        throw new Error(
          "Local API did not return a current issue snapshot.",
        );
      }
      state.selectedIssue = issue;
      renderIssueDetail();
      return loadFixEligibility(issueID, viewCursor);
    } catch (error) {
      if (
        generation !== state.fixEligibilityRequestGeneration ||
        issueID !== state.selectedIssueID ||
        state.selectedIssueSource !== "monitoring"
      ) {
        return false;
      }
      state.fixEligibility = null;
      state.fixEligibilityStatus = "error";
      renderFixAttempts();
      return false;
    }
  }

  async function loadFixMonitoringDetail(
    issueID,
    append,
    viewCursor,
    preserveOnFailure,
    focusAnnotationID,
  ) {
    const generation = ++state.fixHistoryRequestGeneration;
    const cursor = append ? state.fixHistoryNextCursor : "";
    state.fixHistoryStatus = append ? "loading-more" : "loading";
    state.fixHistoryError = null;
    state.fixHistoryStale = false;
    renderFixAttempts();
    const parameters = new URLSearchParams({
      limit: String(pageLimits.fixMonitoringDetail.page),
    });
    if (cursor) {
      parameters.delete("limit");
      parameters.set("cursor", cursor);
    } else if (viewCursor) {
      parameters.set("view_cursor", viewCursor);
    }
    try {
      const response = await apiGet(
        `/v1/issues/${encodeURIComponent(issueID)}/fix-monitoring?${parameters.toString()}`,
      );
      requireFixMonitoringSchema(response);
      if (
        generation !== state.fixHistoryRequestGeneration ||
        issueID !== state.selectedIssueID
      ) {
        return false;
      }
      const evaluatedAt = readText(response.evidence_evaluated_at);
      const currentIssueAvailable =
        response.current_issue_available === true;
      const currentIssue = response.current_issue;
      if (
        (currentIssueAvailable && !isRecord(currentIssue)) ||
        (!currentIssueAvailable && currentIssue !== null) ||
        (currentIssueAvailable &&
          readText(currentIssue.issue_id) !== issueID)
      ) {
        throw new Error(
          "Local API returned an invalid current-issue monitoring state.",
        );
      }
      const page = Array.isArray(response.data)
        ? response.data
            .filter(
              (annotation) =>
                isRecord(annotation) &&
                readText(annotation.annotation_id) &&
                readText(annotation.issue_id) === issueID,
            )
            .map((annotation) => ({
              ...annotation,
              browser_evidence_evaluated_at: evaluatedAt,
            }))
        : [];
      const history =
        append && cursor
          ? deduplicateByID(
              state.fixHistory.concat(page),
              "annotation_id",
            )
          : deduplicateByID(page, "annotation_id");
      const nextCursor = readCursor(response.next_cursor);
      if (append && nextCursor && nextCursor === cursor) {
        throw new Error(
          "Local API returned a non-advancing attempt cursor.",
        );
      }
      if (response.has_more === true && !nextCursor) {
        throw new Error(
          "Local API returned attempt pagination without a cursor.",
        );
      }
      const monitoringViewCursor = readCursor(
        response.monitoring_view_cursor,
      );
      if (!monitoringViewCursor) {
        throw new Error(
          "Local API returned attempt monitoring without a view cursor.",
        );
      }
      state.fixHistory = history;
      state.fixHistoryNextCursor = nextCursor;
      state.fixHistoryHasMore = Boolean(state.fixHistoryNextCursor);
      state.fixHistoryViewCursor = monitoringViewCursor;
      state.fixHistoryCurrentIssueAvailable = currentIssueAvailable;
      state.fixEvidenceEvaluatedAt = evaluatedAt;
      state.fixHistoryStatus = "ready";
      state.fixHistoryError = null;
      state.fixHistoryStale = false;
      if (!append) {
        if (
          currentIssueAvailable &&
          state.selectedIssueSource === "monitoring"
        ) {
          state.selectedIssue = currentIssue;
          state.occurrenceStatus = "loading";
          void loadIssueDetail(false, "");
          void loadMonitoringIssueEligibility(issueID);
        } else if (!currentIssueAvailable) {
          if (state.selectedIssueSource === "monitoring") {
            state.selectedIssue =
              monitoringAttemptSubjectIssue(
                state.fixHistory.find(
                  (attempt) =>
                    readText(attempt.annotation_id) ===
                    state.selectedDrivingAnnotationID,
                ) || state.fixHistory[0],
              ) ||
              state.selectedIssue;
          }
          state.occurrenceRequestGeneration += 1;
          state.occurrenceStatus = "unavailable";
          state.fixEligibilityRequestGeneration += 1;
          state.fixEligibility = null;
          state.fixEligibilityStatus = "unavailable";
        }
      }
      renderFixAttempts();
      renderIssueDetail();
      if (
        focusAnnotationID &&
        !state.fixHistory.some(
          (attempt) =>
            readText(attempt.annotation_id) === focusAnnotationID,
        ) &&
        state.fixHistoryHasMore &&
        state.fixHistoryNextCursor
      ) {
        return loadFixMonitoringDetail(
          issueID,
          true,
          "",
          true,
          focusAnnotationID,
        );
      }
      if (focusAnnotationID) {
        const drivingAttempt =
          focusRegistry.fixHistoryRows.get(focusAnnotationID) || null;
        focusCurrentElement(drivingAttempt) ||
          focusCurrentElement(elements.issueDetailHeading);
      }
      return true;
    } catch (error) {
      if (
        generation !== state.fixHistoryRequestGeneration ||
        issueID !== state.selectedIssueID
      ) {
        return false;
      }
      if (isCursorExpired(error)) {
        return recoverExpiredFixMonitoringDetail(issueID, append);
      }
      if (preserveOnFailure && state.fixHistory.length) {
        state.fixHistoryStatus = "ready";
        state.fixHistoryStale = true;
      } else {
        state.fixHistoryStatus = "error";
      }
      state.fixHistoryError = fixMonitoringErrorMessage(error);
      renderFixAttempts();
      return false;
    }
  }

  async function recoverExpiredFixMonitoringDetail(issueID, append) {
    if (issueID !== state.selectedIssueID) return false;
    const expiredSurface = append
      ? "Attempt pagination"
      : "Attempt detail";
    state.attentionRefreshGeneration += 1;
    resetFixMonitoringBucket(false);
    closeIssueDetail(false);
    showAttentionNotice(
      `${expiredSurface} is out of date. The open attempt details were cleared before refreshing After attempts…`,
      "pending",
    );
    const refreshed = await loadFixMonitoring(false);
    showAttentionNotice(
      refreshed
        ? `${expiredSurface} was out of date. After attempts was refreshed; reopen the finding to inspect current evidence.`
        : `${expiredSurface} was out of date. The details remain closed because After attempts could not be refreshed.`,
      refreshed ? "status" : "error",
      refreshed ? 7000 : 0,
    );
    return false;
  }

  function loadMoreFixMonitoringDetail() {
    if (
      !state.selectedIssueID ||
      !state.fixHistoryHasMore ||
      !state.fixHistoryNextCursor
    ) {
      return;
    }
    void loadFixMonitoringDetail(
      state.selectedIssueID,
      true,
      "",
      true,
      "",
    );
  }

  function renderFixAttempts() {
    const issueID = state.selectedIssueID;
    if (!issueID) return;
    if (
      isReducedApprovalIssueContext(
        state.selectedIssue,
        state.selectedIssueCatalog,
      )
    ) {
      elements.fixAttemptsSection.hidden = true;
      elements.recordFixAttempt.hidden = true;
      elements.recordFixAttempt.disabled = true;
      return;
    }
    focusRegistry.fixTriggers.set(issueID, elements.recordFixAttempt);
    const eligibility = state.fixEligibility || {};
    const retainedDraft = fixDrafts.get(issueID);
    const canResume =
      Boolean(retainedDraft) &&
      retainedDraft.unresolved === true &&
      Boolean(retainedDraft.idempotencyKey) &&
      Boolean(retainedDraft.actionToken);
    const monitoringCurrentIssueUnavailable =
      state.fixHistoryCurrentIssueAvailable === false ||
      (state.selectedIssueSource === "monitoring" &&
        state.fixHistoryCurrentIssueAvailable === null);
    const catalogReady =
      eligibility.changeCatalogVersion === "fix-change.v1";
    const canRecord =
      !monitoringCurrentIssueUnavailable &&
      (canResume ||
        (state.fixEligibilityStatus === "ready" &&
          eligibility.eligible === true &&
          Boolean(eligibility.actionToken) &&
          catalogReady));
    const hasDurableHistory =
      state.fixHistory.length > 0 ||
      (state.selectedIssueSource === "monitoring" &&
        Boolean(state.selectedDrivingAnnotationID));
    const showFixSection = canResume || canRecord || hasDurableHistory;
    elements.fixAttemptsSection.hidden = !showFixSection;
    const serverReportedIneligible =
      state.fixEligibilityStatus === "ready" &&
      eligibility.eligible !== true &&
      !canResume;
    elements.recordFixAttempt.hidden =
      !canRecord ||
      monitoringCurrentIssueUnavailable ||
      serverReportedIneligible;
    elements.recordFixAttempt.disabled = !canRecord;
    elements.recordFixAttempt.textContent = canResume
      ? "Resume attempt"
      : "Record attempt";
    if (canResume && monitoringCurrentIssueUnavailable) {
      elements.fixEligibilityStatus.textContent =
        "A previous submission was not confirmed, but it cannot resume because this finding is no longer current.";
    } else if (canResume) {
      elements.fixEligibilityStatus.textContent =
        "A previous submission was not confirmed. Retry it unchanged, or abandon it before choosing different input.";
    } else if (state.fixEligibilityStatus === "loading") {
      elements.fixEligibilityStatus.textContent =
        "Checking whether an attempt can be recorded for this finding…";
    } else if (state.fixEligibilityStatus === "unavailable") {
      elements.fixEligibilityStatus.textContent =
        "A new attempt cannot be recorded because this finding is no longer current.";
    } else if (state.fixEligibilityStatus === "error") {
      elements.fixEligibilityStatus.textContent =
        "Eligibility could not be confirmed. Matching-session evidence remains available.";
    } else if (canRecord) {
      const expiresAt = parseDate(eligibility.expiresAt);
      elements.fixEligibilityStatus.textContent = expiresAt
        ? `An optional attempt can be recorded. Confirmation expires ${formatRelativeTime(expiresAt)}.`
        : "An optional attempt can be recorded.";
    } else if (state.fixEligibilityStatus === "ready") {
      elements.fixEligibilityStatus.textContent = catalogReady
        ? fixEligibilityMessages[eligibility.reason] ||
          "An attempt cannot currently be recorded for this finding."
        : "The available change choices could not be loaded.";
    } else {
      elements.fixEligibilityStatus.textContent =
        "Eligibility has not been checked.";
    }
    elements.fixMonitoringDetailStatus.hidden =
      !state.fixHistoryError && !state.fixHistoryStale;
    elements.fixMonitoringDetailStatus.textContent =
      state.fixHistoryError ||
      (state.fixHistoryStale
        ? "Attempt monitoring may be stale. Other issue detail remains available."
        : "");
    elements.fixMonitoringDetailStatus.dataset.tone =
      state.fixHistoryError ? "error" : "status";
    renderFixActionStatus();
    renderFixHistory();
  }

  function renderFixActionStatus() {
    const safeTone = ["status", "success", "error", "pending"].includes(
      state.fixActionTone,
    )
      ? state.fixActionTone
      : "status";
    elements.fixActionStatus.hidden = !state.fixActionMessage;
    elements.fixActionStatus.textContent = state.fixActionMessage;
    elements.fixActionStatus.dataset.tone = safeTone;
    elements.fixActionStatus.setAttribute(
      "role",
      safeTone === "error" ? "alert" : "status",
    );
    elements.fixActionStatus.setAttribute(
      "aria-live",
      safeTone === "error" ? "assertive" : "polite",
    );
  }

  function renderFixHistory() {
    focusRegistry.fixHistoryRows.clear();
    focusRegistry.fixRetractionTriggers.clear();
    focusRegistry.fixObservationRows.clear();
    const fragment = document.createDocumentFragment();
    state.fixHistory.forEach((annotation) => {
      fragment.append(createFixHistoryRow(annotation));
    });
    elements.fixHistoryList.replaceChildren(fragment);
    elements.fixHistoryLoading.hidden =
      state.fixHistoryStatus !== "loading";
    elements.fixHistoryEmpty.hidden =
      state.fixHistoryStatus !== "ready" || state.fixHistory.length !== 0;
    if (state.fixHistoryStatus === "error" && state.fixHistory.length === 0) {
      elements.fixHistoryList.append(
        createElement(
          "p",
          "overview-empty",
          "Attempt monitoring could not be loaded. Matching sessions and fix actions remain available.",
        ),
      );
    }
    elements.fixHistoryPagination.hidden = !state.fixHistoryHasMore;
    elements.fixHistoryLoadMore.disabled =
      state.fixHistoryStatus === "loading-more";
    elements.fixHistoryPageStatus.textContent = state.fixHistoryHasMore
      ? `Showing ${state.fixHistory.length}; more monitored attempts are available.`
      : `Showing ${state.fixHistory.length} monitored ${
          state.fixHistory.length === 1 ? "attempt" : "attempts"
        }.`;
    const evaluatedAt = parseDate(state.fixEvidenceEvaluatedAt);
    elements.fixEvidenceEvaluated.textContent = evaluatedAt
      ? `Stored evidence was checked ${formatRelativeTime(evaluatedAt)}. Attempt dates and matching-event counts may remain even if cited events are later removed.`
      : state.fixHistory.length
        ? "The last evidence check time is unavailable. Attempt dates and matching-event counts may remain even if cited events are later removed."
        : "";
  }

  function createFixHistoryRow(annotation) {
    const annotationID = readText(annotation && annotation.annotation_id);
    const row = createElement("article", "fix-history-card");
    row.tabIndex = -1;
    if (annotationID) {
      focusRegistry.fixHistoryRows.set(annotationID, row);
    }
    const category = fixChangeCatalogEntry(annotation.change_kind);
    const annotationState = normalizeFixAnnotationState(annotation.state);
    const monitoringState = normalizeFixRecurrenceState(
      annotation.fix_recurrence_state,
    );
    const monitoringEntry = fixMonitoringStateEntry(monitoringState);
    const header = createElement("div", "fix-history-header");
    const title = createElement("div");
    title.append(
      createElement("strong", "", category.label),
      createElement(
        "small",
        "",
        monitoringSubjectCatalog(annotation).title,
      ),
    );
    const badges = createElement("div", "fix-history-badges");
    const stateBadge = createElement(
      "span",
      "fix-state-badge",
      annotationState === "retracted"
        ? "Retracted attempt"
        : annotationState === "active"
          ? "Active attempt"
          : "Attempt status unavailable",
    );
    stateBadge.dataset.tone = annotationState;
    const evidenceStatus = normalizeFixEvidenceStatus(
      annotation.anchor_evidence_currently_retained,
    );
    const evidenceBadge = createElement(
      "span",
      "fix-evidence-badge",
      fixEvidenceStatusLabel(evidenceStatus),
    );
    evidenceBadge.dataset.tone = evidenceStatus;
    badges.append(stateBadge, evidenceBadge);
    header.append(title, badges);
    const status = createElement("div", "fix-monitoring-state");
    status.dataset.tone = monitoringEntry.tone;
    status.append(
      createElement("strong", "", monitoringEntry.title),
      createElement("p", "", monitoringEntry.detail),
    );
    const metadata = createElement("dl", "fix-history-metadata");
    appendFixMetadata(
      metadata,
      "Recorded",
      formatFullDate(parseDate(annotation.recorded_at)) || "Time unavailable",
    );
    appendFixMetadata(
      metadata,
      "Monitoring began",
      formatFullDate(parseDate(annotation.monitor_from)) || "Time unavailable",
    );
    appendFixMetadata(
      metadata,
      "Latest matching evidence",
      formatFullDate(parseDate(annotation.last_recurrence_observed_at)) ||
        "No matching-evidence time available",
    );
    appendFixMetadata(
      metadata,
      "Evidence recorded with attempt",
      fixEvidenceStatusExplanation(evidenceStatus),
    );
    appendOptionalFixCount(
      metadata,
      "Matching observations",
      annotation.fix_recurrence_count,
      annotation.count_is_lower_bound === true,
    );
    appendOptionalFixCount(
      metadata,
      "Original session",
      annotation.same_anchor_session_observation_count,
      false,
    );
    appendOptionalFixCount(
      metadata,
      "Other sessions",
      annotation.other_session_observation_count,
      false,
    );
    appendMonitoringCoverage(metadata, annotation.coverage);
    appendRecurrenceEvidence(metadata, annotation.recurrence_evidence);
    if (annotationState === "retracted") {
      appendFixMetadata(
        metadata,
        "Retraction",
        `${fixRetractionReasonLabel(annotation.retraction_reason)} · ${
          formatFullDate(parseDate(annotation.retracted_at)) ||
          "Time unavailable"
        }`,
      );
    }
    row.append(
      header,
      createElement("p", "fix-history-description", category.description),
      status,
      metadata,
    );
    if (
      toOptionalCount(annotation.same_anchor_session_observation_count) > 0
    ) {
      row.append(
        createElement(
          "p",
          "fix-monitoring-caveat",
          "This may be continuation within the original session.",
        ),
      );
    }
    if (
      monitoringState === "matching_evidence_observed" &&
      annotation.analysis_complete === false
    ) {
      row.append(
        createElement(
          "p",
          "fix-monitoring-caveat",
          "The matching-evidence count is a lower bound because monitoring coverage is incomplete.",
        ),
      );
    }
    if (
      annotationState === "active" &&
      annotation.future_comparison_available === false
    ) {
      row.append(
        createElement(
          "p",
          "fix-monitoring-caveat",
          futureComparisonUnavailableText(
            annotation.future_comparison_unavailable_reason,
          ),
        ),
      );
    }
    if (
      annotationState === "retracted" &&
      toOptionalCount(annotation.historical_matching_evidence_count) > 0
    ) {
      row.append(
        createElement(
          "p",
          "fix-monitoring-caveat",
          "Matching evidence was recorded before this attempt record was retracted.",
        ),
      );
    }
    const actions = createElement("div", "fix-history-actions");
    const observationCount = toOptionalCount(
      annotation.fix_recurrence_count,
    );
    const observationCursor = readCursor(annotation.observation_view_cursor);
    if (
      observationCount !== null &&
      observationCount > 0 &&
      observationCursor &&
      annotationID
    ) {
      const observations = createElement(
        "button",
        "secondary-button",
        "Show observations",
      );
      observations.type = "button";
      observations.setAttribute(
        "aria-expanded",
        String(Boolean(fixObservationPages.get(annotationID)?.expanded)),
      );
      observations.textContent = fixObservationPages.get(annotationID)?.expanded
        ? "Hide observations"
        : "Show observations";
      observations.addEventListener("click", () => {
        toggleFixRecurrenceObservations(annotation, observations, row);
      });
      actions.append(observations);
    }
    if (
      annotationState === "active" &&
      monitoringState !== "unknown" &&
      annotationID
    ) {
      const retract = createElement(
        "button",
        "secondary-button",
        "Retract",
      );
      retract.type = "button";
      retract.addEventListener("click", () => {
        openFixRetractionDialog(annotationID);
      });
      focusRegistry.fixRetractionTriggers.set(annotationID, retract);
      actions.append(retract);
    }
    if (actions.childElementCount) row.append(actions);
    if (annotationState === "retracted") {
      row.append(
        createElement(
          "p",
          "fix-retraction-note",
          "Preserved in history and excluded from active monitoring.",
        ),
      );
    } else if (annotationState === "unknown") {
      row.append(
        createElement(
          "p",
          "fix-retraction-note",
          "Attempt status is unavailable. Retraction is disabled until Belay confirms this attempt is active.",
        ),
      );
    }
    renderFixObservationSection(annotation, row);
    return row;
  }

  function appendOptionalFixCount(list, label, value, lowerBound) {
    const count = toOptionalCount(value);
    appendFixMetadata(
      list,
      label,
      count === null
        ? "Unavailable"
        : `${lowerBound ? "At least " : ""}${formatNumber(count)}`,
    );
  }

  function appendMonitoringCoverage(list, value) {
    if (!isRecord(value)) {
      appendFixMetadata(list, "Monitoring coverage", "Unavailable");
      return;
    }
    const counts = [
      ["current", toOptionalCount(value.comparable_current)],
      ["pending", toOptionalCount(value.comparable_pending)],
      ["failed", toOptionalCount(value.comparable_failed)],
      ["partial", toOptionalCount(value.comparable_truncated)],
    ];
    if (counts.some((entry) => entry[1] === null)) {
      appendFixMetadata(list, "Monitoring coverage", "Unavailable");
      return;
    }
    appendFixMetadata(
      list,
      "Monitoring coverage",
      counts
        .map(([label, count]) => `${formatNumber(count)} ${label}`)
        .join(" · "),
    );
    appendFixMetadata(
      list,
      "Analysis through",
      formatFullDate(parseDate(value.analysis_through)) ||
        "Comparable analysis time unavailable",
    );
  }

  function appendRecurrenceEvidence(list, value) {
    if (!isRecord(value)) {
      appendFixMetadata(list, "Observation evidence", "Unavailable");
      return;
    }
    const counts = [
      ["retained", toOptionalCount(value.available)],
      ["partial", toOptionalCount(value.partial)],
      ["pruned", toOptionalCount(value.pruned)],
      ["unknown", toOptionalCount(value.unknown)],
    ];
    if (counts.some((entry) => entry[1] === null)) {
      appendFixMetadata(list, "Observation evidence", "Unavailable");
      return;
    }
    appendFixMetadata(
      list,
      "Observation evidence",
      counts
        .map(([label, count]) => `${formatNumber(count)} ${label}`)
        .join(" · "),
    );
  }

  function toggleFixRecurrenceObservations(annotation, button) {
    const annotationID = readText(annotation.annotation_id);
    if (!annotationID) return;
    const page = getFixObservationPage(annotationID);
    if (page.expanded) {
      page.expanded = false;
      renderFixHistory();
      restoreLogicalFocus(
        { type: "fix-history", annotationID },
        button,
      );
      return;
    }
    page.expanded = true;
    if (page.status === "idle") {
      void loadFixRecurrences(annotation, false);
    } else {
      renderFixHistory();
    }
  }

  function getFixObservationPage(annotationID) {
    let page = fixObservationPages.get(annotationID);
    if (!page) {
      page = {
        data: [],
        nextCursor: "",
        hasMore: false,
        status: "idle",
        error: "",
        expired: false,
        expanded: false,
        requestGeneration: 0,
      };
      fixObservationPages.set(annotationID, page);
    }
    return page;
  }

  async function loadFixRecurrences(annotation, append) {
    const issueID = state.selectedIssueID;
    const annotationID = readText(annotation.annotation_id);
    const viewCursor = readCursor(annotation.observation_view_cursor);
    if (!issueID || !annotationID) return false;
    const pageState = getFixObservationPage(annotationID);
    if (
      append &&
      (!pageState.hasMore || !pageState.nextCursor)
    ) {
      return false;
    }
    const generation = ++pageState.requestGeneration;
    pageState.status = append ? "loading-more" : "loading";
    pageState.error = "";
    pageState.expired = false;
    renderFixHistory();
    const parameters = new URLSearchParams();
    if (append) {
      parameters.set("cursor", pageState.nextCursor);
    } else {
      if (!viewCursor) {
        pageState.status = "error";
        pageState.error =
          "Matching-event history is unavailable for this saved attempt view.";
        renderFixHistory();
        return false;
      }
      parameters.set("observation_view_cursor", viewCursor);
      parameters.set("limit", String(pageLimits.fixRecurrences.page));
    }
    try {
      const response = await apiGet(
        `/v1/issues/${encodeURIComponent(issueID)}/fixes/${encodeURIComponent(annotationID)}/recurrences?${parameters.toString()}`,
      );
      requireFixMonitoringSchema(response);
      if (
        generation !== pageState.requestGeneration ||
        issueID !== state.selectedIssueID
      ) {
        return false;
      }
      if (
        readText(response.issue_id) !== issueID ||
        readText(response.annotation_id) !== annotationID
      ) {
        throw new Error("Local API returned mismatched observation history.");
      }
      const rows = Array.isArray(response.data)
        ? response.data.filter(
            (entry) =>
              isRecord(entry) &&
              readText(entry.recurrence_id) &&
              readText(entry.session_id),
          )
        : [];
      pageState.data =
        append && pageState.nextCursor
          ? deduplicateByID(
              pageState.data.concat(rows),
              "recurrence_id",
            )
          : deduplicateByID(rows, "recurrence_id");
      pageState.nextCursor = readCursor(response.next_cursor);
      if (response.has_more === true && !pageState.nextCursor) {
        throw new Error(
          "Local API returned observation pagination without a cursor.",
        );
      }
      pageState.hasMore = Boolean(pageState.nextCursor);
      pageState.status = "ready";
      pageState.error = "";
      pageState.expired = false;
      renderFixHistory();
      return true;
    } catch (error) {
      if (
        generation !== pageState.requestGeneration ||
        issueID !== state.selectedIssueID
      ) {
        return false;
      }
      pageState.status = "error";
      pageState.expired = isCursorExpired(error);
      pageState.error = pageState.expired
        ? "The saved matching-event view is out of date. Reload the attempt to inspect currently stored evidence."
        : "Matching-event history could not be loaded. The attempt remains available.";
      renderFixHistory();
      return false;
    }
  }

  function renderFixObservationSection(annotation, row) {
    const annotationID = readText(annotation.annotation_id);
    const page = fixObservationPages.get(annotationID);
    if (!page || !page.expanded) return;
    const section = createElement("section", "fix-observation-section");
    section.setAttribute("aria-label", "Matching observations");
    if (page.status === "loading" && !page.data.length) {
      section.append(
        createElement("p", "overview-empty", "Loading observations…"),
      );
    }
    if (page.error) {
      const error = createElement(
        "p",
        "monitoring-status",
        page.error,
      );
      error.dataset.tone = "error";
      error.setAttribute("role", "status");
      section.append(error);
      const retry = createElement(
        "button",
        "secondary-button",
        page.expired
          ? "Reload attempt monitoring"
          : "Retry observations",
      );
      retry.type = "button";
      retry.addEventListener("click", () => {
        if (page.expired) {
          fixObservationPages.delete(annotationID);
          void loadFixMonitoringDetail(
            state.selectedIssueID,
            false,
            "",
            true,
            annotationID,
          );
          return;
        }
        void loadFixRecurrences(annotation, false);
      });
      section.append(retry);
    }
    page.data.forEach((observation) => {
      section.append(createFixObservationRow(observation));
    });
    if (page.status === "ready" && !page.data.length) {
      section.append(
        createElement(
          "p",
          "overview-empty",
          "No matching events were returned for this attempt.",
        ),
      );
    }
    if (page.hasMore) {
      const more = createElement(
        "button",
        "secondary-button",
        "Load more observations",
      );
      more.type = "button";
      more.disabled = page.status === "loading-more";
      more.addEventListener("click", () => {
        void loadFixRecurrences(annotation, true);
      });
      section.append(more);
    }
    row.append(section);
  }

  function createFixObservationRow(observation) {
    const recurrenceID = readText(observation.recurrence_id);
    const sessionID = readText(observation.session_id);
    const article = createElement("article", "fix-observation-card");
    article.tabIndex = -1;
    if (recurrenceID) {
      focusRegistry.fixObservationRows.set(recurrenceID, article);
    }
    const header = createElement("div", "fix-observation-header");
    header.append(
      createElement("strong", "", "Related observation"),
      createElement(
        "span",
        "fix-evidence-badge",
        fixEvidenceStatusLabel(
          normalizeFixEvidenceStatus(
            observation.evidence_currently_retained,
          ),
        ),
      ),
    );
    const metadata = createElement("dl", "fix-history-metadata");
    appendFixMetadata(
      metadata,
      "First matching evidence",
      formatFullDate(parseDate(observation.first_qualifying_event_at)) ||
        "Time unavailable",
    );
    appendFixMetadata(
      metadata,
      "Last matching evidence",
      formatFullDate(parseDate(observation.last_qualifying_event_at)) ||
        "Time unavailable",
    );
    appendFixMetadata(
      metadata,
      "Session",
      compactID(sessionID) || "Unavailable",
    );
    appendFixMetadata(
      metadata,
      "Check version",
      detectorVersionLabel(observation),
    );
    appendFixMetadata(
      metadata,
      "Detection evidence",
      evidenceCompletenessLabel(observation.evidence_complete),
    );
    appendFixMetadata(
      metadata,
      "Observation recorded",
      formatFullDate(parseDate(observation.observed_at)) ||
        "Time unavailable",
    );
    appendObservationEvidenceMetadata(metadata, observation);
    article.append(header, metadata);
    if (observation.same_session_as_anchor === true) {
      article.append(
        createElement(
          "p",
          "fix-monitoring-caveat",
          "This may be continuation within the original session.",
        ),
      );
    }
    const retainedEventIDs = recurrenceEventIDs(observation);
    if (sessionID) {
      const actions = createElement("div", "fix-history-actions");
      let evidence = null;
      if (retainedEventIDs.length) {
        const inspect = createElement(
          "button",
          "secondary-button",
          "Inspect cited evidence",
        );
        inspect.type = "button";
        inspect.setAttribute("aria-expanded", "false");
        evidence = createElement("div", "occurrence-evidence");
        evidence.hidden = true;
        inspect.addEventListener("click", () => {
          void loadRecurrenceEvidence(
            sessionID,
            retainedEventIDs,
            evidence,
            inspect,
          );
        });
        actions.append(inspect);
      }
      const open = createElement(
        "button",
        "primary-button",
        "Open full session",
      );
      open.type = "button";
      if (recurrenceID) {
        focusRegistry.fixObservationRows.set(recurrenceID, open);
      }
      open.addEventListener("click", () => {
        state.sessionReturnFocus = {
          type: "fix-observation",
          recurrenceID,
        };
        state.sessionReturnView = "attention";
        openSession(sessionID, null);
      });
      actions.append(open);
      article.append(actions);
      if (evidence) article.append(evidence);
    }
    return article;
  }

  function appendObservationEvidenceMetadata(metadata, observation) {
    const status = normalizeFixEvidenceStatus(
      observation.evidence_currently_retained,
    );
    if (status === "unknown") {
      appendFixMetadata(
        metadata,
        "Evidence retention",
        "Evidence status unknown; retained and missing counts are unavailable.",
      );
      return;
    }
    const retained = toOptionalCount(observation.retained_event_count);
    const missing = toOptionalCount(observation.missing_event_count);
    const total = toOptionalCount(observation.qualifying_citation_count);
    const truncation =
      observation.evidence_truncated === true
        ? " · returned IDs are truncated to 50"
        : "";
    appendFixMetadata(
      metadata,
      "Evidence retention",
      retained === null || missing === null || total === null
        ? fixEvidenceStatusLabel(status)
        : `${formatNumber(retained)} retained · ${formatNumber(
            missing,
          )} missing · ${formatNumber(total)} cited${truncation}`,
    );
  }

  function recurrenceEventIDs(observation) {
    if (
      normalizeFixEvidenceStatus(
        observation.evidence_currently_retained,
      ) === "unknown"
    ) {
      return [];
    }
    const values = Array.isArray(observation.retained_event_ids)
      ? observation.retained_event_ids
      : [];
    return values
      .map(readText)
      .filter((value) => canonicalUUIDv7Pattern.test(value))
      .slice(0, 50);
  }

  async function loadRecurrenceEvidence(
    sessionID,
    eventIDs,
    container,
    button,
  ) {
    if (container.dataset.loaded === "true") {
      container.hidden = !container.hidden;
      button.setAttribute("aria-expanded", String(!container.hidden));
      return;
    }
    container.hidden = false;
    button.disabled = true;
    button.setAttribute("aria-expanded", "true");
    container.replaceChildren(
      createElement("p", "overview-empty", "Loading cited events…"),
    );
    const parameters = new URLSearchParams();
    eventIDs.slice(0, 50).forEach((eventID) => {
      parameters.append("event_id", eventID);
    });
    try {
      const response = await apiGet(
        `/v1/sessions/${encodeURIComponent(sessionID)}/events/lookup?${parameters.toString()}`,
      );
      renderOccurrenceEvidence(container, response, eventIDs.length);
      container.dataset.loaded = "true";
    } catch {
      container.replaceChildren(
        createElement(
          "p",
          "overview-empty",
          "Cited events could not be loaded. The finding remains recorded.",
        ),
      );
    } finally {
      button.disabled = false;
    }
  }

  function appendFixMetadata(list, label, value) {
    const item = createElement("div");
    item.append(
      createElement("dt", "", label),
      createElement("dd", "", value),
    );
    list.append(item);
  }

  function fixChangeCatalogEntry(value) {
    return (
      fixChangeCatalog.find((entry) => entry.value === readText(value)) || {
        value: "",
        label: "Category unavailable",
        description:
          "The recorded category is outside this browser catalog version.",
      }
    );
  }

  function normalizeFixAnnotationState(value) {
    const stateValue = readText(value);
    return ["active", "retracted"].includes(stateValue)
      ? stateValue
      : "unknown";
  }

  function normalizeFixRecurrenceState(value) {
    const recurrenceState = readText(value);
    return Object.prototype.hasOwnProperty.call(
      fixMonitoringCatalog,
      recurrenceState,
    )
      ? recurrenceState
      : "unknown";
  }

  function fixMonitoringStateEntry(value) {
    return fixMonitoringCatalog[normalizeFixRecurrenceState(value)];
  }

  function normalizeFixEvidenceStatus(value) {
    const status = readText(value);
    return ["available", "partial", "pruned", "unknown"].includes(status)
      ? status
      : "unknown";
  }

  function futureComparisonUnavailableText(value) {
    const messages = {
      scope_unavailable:
        "Future comparison is unavailable because Belay does not have enough project information.",
      source_positive_only:
        "This check can report later matches but cannot confirm that no match occurred.",
      capability_unavailable:
        "Future comparison is unavailable for this stored check version.",
      baseline_time_unavailable:
        "Future comparison is unavailable because Belay does not have the original comparison time.",
      fingerprint_version_unsupported:
        "Future comparison is unavailable for this stored comparison format.",
    };
    return (
      messages[readText(value)] ||
      "Future comparison capability is unavailable for this attempt."
    );
  }

  function fixEvidenceStatusLabel(status) {
    const labels = {
      available: "Evidence retained",
      partial: "Evidence partly retained",
      pruned: "Evidence pruned",
      unknown: "Evidence status unknown",
    };
    return labels[status] || labels.unknown;
  }

  function fixEvidenceStatusExplanation(status) {
    const explanations = {
      available: "All events cited when this attempt was recorded are still stored.",
      partial: "Some events cited when this attempt was recorded are still stored.",
      pruned: "The events cited when this attempt was recorded are no longer stored.",
      unknown: "Belay could not determine whether the originally cited events are still stored.",
    };
    return explanations[status] || explanations.unknown;
  }

  function fixEvidenceHistoryText(annotation, status) {
    const explanation = fixEvidenceStatusExplanation(status);
    const evaluatedAt = parseDate(
      annotation && annotation.browser_evidence_evaluated_at,
    );
    return evaluatedAt
      ? `${explanation} Evaluated ${formatFullDate(evaluatedAt)}.`
      : `${explanation} Evaluation time unavailable.`;
  }

  function fixRetractionReasonLabel(value) {
    const entry = fixRetractionCatalog.find(
      (candidate) => candidate.value === readText(value),
    );
    return entry ? entry.label : "Reason unavailable";
  }

  function renderFixDialogChoices() {
    const categoryFragment = document.createDocumentFragment();
    fixChangeCatalog.forEach((entry) => {
      categoryFragment.append(
        createDialogChoice(
          "fix-change-kind",
          `fix-change-${entry.value}`,
          entry.value,
          entry.label,
          entry.description,
        ),
      );
    });
    elements.fixCategoryOptions.replaceChildren(categoryFragment);
    const retractionFragment = document.createDocumentFragment();
    fixRetractionCatalog.forEach((entry) => {
      retractionFragment.append(
        createDialogChoice(
          "fix-retraction-reason",
          `fix-retraction-${entry.value}`,
          entry.value,
          entry.label,
          "",
        ),
      );
    });
    elements.fixRetractionOptions.replaceChildren(retractionFragment);
  }

  function createDialogChoice(name, id, value, label, description) {
    const wrapper = createElement("label", "choice-option");
    const input = createElement("input");
    input.type = "radio";
    input.name = name;
    input.id = id;
    input.value = value;
    const copy = createElement("span");
    copy.append(createElement("strong", "", label));
    if (description) copy.append(createElement("small", "", description));
    wrapper.append(input, copy);
    return wrapper;
  }

  function openFixAttemptDialog() {
    const issueID = state.selectedIssueID;
    const eligibility = state.fixEligibility;
    const retainedDraft = fixDrafts.get(issueID);
    const canResume =
      Boolean(retainedDraft) &&
      retainedDraft.unresolved === true &&
      Boolean(retainedDraft.idempotencyKey) &&
      Boolean(retainedDraft.actionToken);
    if (
      !issueID ||
      (!canResume &&
        (state.fixEligibilityStatus !== "ready" ||
          !eligibility ||
          eligibility.eligible !== true ||
          !eligibility.actionToken ||
          eligibility.changeCatalogVersion !== "fix-change.v1"))
    ) {
      return;
    }
    state.dialogReturnFocus = { type: "fix-trigger", issueID };
    state.activeModal = "fix-attempt";
    hideModalAlert(elements.fixAttemptAlert);
    syncFixDraftDialog();
    const draft = getFixDraft(issueID);
    const selected = elements.fixCategoryOptions.querySelector(
      'input[name="fix-change-kind"]:checked',
    );
    const first = elements.fixCategoryOptions.querySelector(
      'input[name="fix-change-kind"]',
    );
    openModalLayer(
      elements.fixAttemptModalLayer,
      elements.fixAttemptDialog,
      draft.attempted ? elements.fixAttemptConfirm : selected || first,
    );
  }

  function closeFixAttemptDialog(restoreFocus) {
    if (
      restoreFocus &&
      state.activeModal === "fix-attempt" &&
      state.modalSubmitting
    ) {
      return;
    }
    closeModalLayer(
      "fix-attempt",
      elements.fixAttemptModalLayer,
      restoreFocus,
    );
    hideModalAlert(elements.fixAttemptAlert);
  }

  function getFixDraft(issueID) {
    let draft = fixDrafts.get(issueID);
    if (!draft) {
      draft = {
        changeKind: "",
        idempotencyKey: "",
        actionToken: "",
        attempted: false,
        unresolved: false,
        pending: false,
      };
      fixDrafts.set(issueID, draft);
    }
    return draft;
  }

  function syncFixDraftDialog() {
    const draft = getFixDraft(state.selectedIssueID);
    const locked = draft.attempted || draft.pending;
    elements.fixCategoryOptions
      .querySelectorAll('input[name="fix-change-kind"]')
      .forEach((input) => {
        input.checked = input.value === draft.changeKind;
        input.disabled = locked;
      });
    elements.fixDraftRecovery.hidden = !draft.unresolved;
    elements.fixAttemptClose.disabled = draft.pending;
    elements.fixAttemptCancel.disabled = draft.pending;
    elements.abandonFixDraft.disabled = draft.pending;
    elements.fixAttemptConfirm.disabled =
      draft.pending || !fixChangeCatalogEntry(draft.changeKind).value;
    elements.fixAttemptConfirm.textContent = draft.pending
      ? "Recording…"
      : draft.attempted
        ? "Retry same attempt"
        : "Record attempt";
  }

  function updateFixDraftChoice(target) {
    if (
      !(target instanceof HTMLInputElement) ||
      target.name !== "fix-change-kind"
    ) {
      return;
    }
    const draft = getFixDraft(state.selectedIssueID);
    if (draft.attempted || draft.pending) {
      syncFixDraftDialog();
      return;
    }
    const entry = fixChangeCatalogEntry(target.value);
    draft.changeKind = entry.value;
    hideModalAlert(elements.fixAttemptAlert);
    syncFixDraftDialog();
  }

  function abandonFixDraft() {
    const draft = getFixDraft(state.selectedIssueID);
    if (draft.pending) return;
    draft.idempotencyKey = "";
    draft.actionToken = "";
    draft.attempted = false;
    draft.unresolved = false;
    syncFixDraftDialog();
    showModalAlert(
      elements.fixAttemptAlert,
      "The previous submission was abandoned. Confirming again will start a new request.",
      false,
    );
  }

  async function submitFixAttempt() {
    const issueID = state.selectedIssueID;
    const eligibility = state.fixEligibility;
    const draft = getFixDraft(issueID);
    if (!fixChangeCatalogEntry(draft.changeKind).value) {
      showModalAlert(
        elements.fixAttemptAlert,
        "Select one primary change category before recording.",
        false,
      );
      focusCurrentElement(
        elements.fixCategoryOptions.querySelector(
          'input[name="fix-change-kind"]',
        ),
      );
      return;
    }
    if (draft.pending) {
      return;
    }
    if (!draft.idempotencyKey) {
      if (
        !eligibility ||
        eligibility.eligible !== true ||
        !eligibility.actionToken ||
        eligibility.changeCatalogVersion !== "fix-change.v1"
      ) {
        showModalAlert(
          elements.fixAttemptAlert,
          "Current eligibility could not be confirmed. Nothing was recorded.",
          true,
        );
        return;
      }
      draft.idempotencyKey = createUUIDv4();
      if (!draft.idempotencyKey) {
        showModalAlert(
          elements.fixAttemptAlert,
          "Belay could not safely prepare the request. Nothing was recorded.",
          true,
        );
        return;
      }
      draft.actionToken = eligibility.actionToken;
    }
    draft.attempted = true;
    draft.unresolved = true;
    draft.pending = true;
    state.modalSubmitting = true;
    hideModalAlert(elements.fixAttemptAlert);
    syncFixDraftDialog();
    try {
      const response = await apiMutation(
        `/v1/issues/${encodeURIComponent(issueID)}/fixes`,
        {
          action_token: draft.actionToken,
          change_kind: draft.changeKind,
        },
        draft.idempotencyKey,
        "record-fix-attempt.v1",
      );
      requireFixSchema(response);
      const annotation = isRecord(response.data) ? response.data : null;
      const annotationID = readText(
        annotation && annotation.annotation_id,
      );
      if (
        !annotationID ||
        readText(annotation.issue_id) !== issueID ||
        readText(annotation.change_kind) !== draft.changeKind ||
        readText(annotation.change_catalog_version) !== "fix-change.v1"
      ) {
        throw new Error("Local API returned an invalid fix-attempt response.");
      }
      const replayed = response.replayed === true;
      fixDrafts.delete(issueID);
      state.fixActionMessage = replayed
        ? "Previously recorded attempt restored; no duplicate created."
        : "Attempt recorded · Not verified by Belay.";
      state.fixActionTone = "success";
      draft.pending = false;
      state.modalSubmitting = false;
      closeFixAttemptDialog(false);
      await reloadFixMonitoringAndFocus(issueID, annotationID);
    } catch (error) {
      draft.pending = false;
      state.modalSubmitting = false;
      if (isCursorExpired(error)) {
        draft.idempotencyKey = "";
        draft.actionToken = "";
        draft.attempted = false;
        draft.unresolved = false;
        state.fixEligibility = null;
        state.fixEligibilityStatus = "idle";
        closeFixAttemptDialog(false);
        showAttentionNotice(
          "The fix-attempt confirmation expired. Your category was preserved, but nothing was resubmitted.",
          "status",
          7000,
        );
        await refreshAttentionAfterExpiry();
        return;
      }
      syncFixDraftDialog();
      showModalAlert(
        elements.fixAttemptAlert,
        fixMutationErrorMessage(error, "record"),
        true,
      );
    }
  }

  function openFixRetractionDialog(annotationID) {
    const annotation = state.fixHistory.find(
      (entry) => readText(entry && entry.annotation_id) === annotationID,
    );
    if (
      !state.selectedIssueID ||
      !annotationID ||
      normalizeFixAnnotationState(annotation && annotation.state) !== "active" ||
      normalizeFixRecurrenceState(
        annotation && annotation.fix_recurrence_state,
      ) === "unknown"
    ) {
      return;
    }
    state.activeRetractionAnnotationID = annotationID;
    state.dialogReturnFocus = {
      type: "fix-retraction",
      annotationID,
    };
    state.activeModal = "fix-retraction";
    hideModalAlert(elements.fixRetractionAlert);
    syncFixRetractionDialog();
    const draft = getFixRetractionDraft(
      state.selectedIssueID,
      annotationID,
    );
    const selected = elements.fixRetractionOptions.querySelector(
      'input[name="fix-retraction-reason"]:checked',
    );
    const first = elements.fixRetractionOptions.querySelector(
      'input[name="fix-retraction-reason"]',
    );
    openModalLayer(
      elements.fixRetractionModalLayer,
      elements.fixRetractionDialog,
      draft.attempted ? elements.fixRetractionConfirm : selected || first,
    );
  }

  function closeFixRetractionDialog(restoreFocus) {
    if (
      restoreFocus &&
      state.activeModal === "fix-retraction" &&
      state.modalSubmitting
    ) {
      return;
    }
    closeModalLayer(
      "fix-retraction",
      elements.fixRetractionModalLayer,
      restoreFocus,
    );
    hideModalAlert(elements.fixRetractionAlert);
    state.activeRetractionAnnotationID = "";
  }

  function fixRetractionDraftKey(issueID, annotationID) {
    return `${issueID}\u0000${annotationID}`;
  }

  function getFixRetractionDraft(issueID, annotationID) {
    const key = fixRetractionDraftKey(issueID, annotationID);
    let draft = fixRetractionDrafts.get(key);
    if (!draft) {
      draft = {
        reason: "",
        idempotencyKey: "",
        attempted: false,
        unresolved: false,
        pending: false,
      };
      fixRetractionDrafts.set(key, draft);
    }
    return draft;
  }

  function syncFixRetractionDialog() {
    const draft = getFixRetractionDraft(
      state.selectedIssueID,
      state.activeRetractionAnnotationID,
    );
    const locked = draft.attempted || draft.pending;
    elements.fixRetractionOptions
      .querySelectorAll('input[name="fix-retraction-reason"]')
      .forEach((input) => {
        input.checked = input.value === draft.reason;
        input.disabled = locked;
      });
    elements.fixRetractionRecovery.hidden = !draft.unresolved;
    elements.fixRetractionClose.disabled = draft.pending;
    elements.fixRetractionCancel.disabled = draft.pending;
    elements.abandonFixRetraction.disabled = draft.pending;
    elements.fixRetractionConfirm.disabled =
      draft.pending || !fixRetractionReasonEntry(draft.reason);
    elements.fixRetractionConfirm.textContent = draft.pending
      ? "Retracting…"
      : draft.attempted
        ? "Retry same retraction"
        : "Retract attempt record";
  }

  function updateFixRetractionChoice(target) {
    if (
      !(target instanceof HTMLInputElement) ||
      target.name !== "fix-retraction-reason"
    ) {
      return;
    }
    const draft = getFixRetractionDraft(
      state.selectedIssueID,
      state.activeRetractionAnnotationID,
    );
    if (draft.attempted || draft.pending) {
      syncFixRetractionDialog();
      return;
    }
    const entry = fixRetractionReasonEntry(target.value);
    draft.reason = entry ? entry.value : "";
    hideModalAlert(elements.fixRetractionAlert);
    syncFixRetractionDialog();
  }

  function abandonFixRetraction() {
    const draft = getFixRetractionDraft(
      state.selectedIssueID,
      state.activeRetractionAnnotationID,
    );
    if (draft.pending) return;
    draft.idempotencyKey = "";
    draft.attempted = false;
    draft.unresolved = false;
    syncFixRetractionDialog();
    showModalAlert(
      elements.fixRetractionAlert,
      "The previous retraction was abandoned. Confirming again will start a new request.",
      false,
    );
  }

  function fixRetractionReasonEntry(value) {
    return (
      fixRetractionCatalog.find(
        (entry) => entry.value === readText(value),
      ) || null
    );
  }

  async function submitFixRetraction() {
    const issueID = state.selectedIssueID;
    const annotationID = state.activeRetractionAnnotationID;
    const draft = getFixRetractionDraft(issueID, annotationID);
    if (!fixRetractionReasonEntry(draft.reason)) {
      showModalAlert(
        elements.fixRetractionAlert,
        "Select one retraction reason before confirming.",
        false,
      );
      focusCurrentElement(
        elements.fixRetractionOptions.querySelector(
          'input[name="fix-retraction-reason"]',
        ),
      );
      return;
    }
    if (!issueID || !annotationID || draft.pending) return;
    if (!draft.idempotencyKey) {
      draft.idempotencyKey = createUUIDv4();
      if (!draft.idempotencyKey) {
        showModalAlert(
          elements.fixRetractionAlert,
          "Belay could not safely prepare the request. Nothing was retracted.",
          true,
        );
        return;
      }
    }
    draft.attempted = true;
    draft.unresolved = true;
    draft.pending = true;
    state.modalSubmitting = true;
    hideModalAlert(elements.fixRetractionAlert);
    syncFixRetractionDialog();
    try {
      const response = await apiMutation(
        `/v1/issues/${encodeURIComponent(issueID)}/fixes/${encodeURIComponent(annotationID)}/retractions`,
        { reason: draft.reason },
        draft.idempotencyKey,
        "retract-fix-attempt.v1",
      );
      requireFixSchema(response);
      if (
        !isRecord(response.data) ||
        readText(response.data.annotation_id) !== annotationID ||
        readText(response.data.issue_id) !== issueID
      ) {
        throw new Error("Local API returned an invalid retraction response.");
      }
      const replayed = response.replayed === true;
      fixRetractionDrafts.delete(
        fixRetractionDraftKey(issueID, annotationID),
      );
      state.fixActionMessage = replayed
        ? "Previously recorded retraction restored; no duplicate created."
        : "Attempt record retracted. The original attempt remains in history.";
      state.fixActionTone = "success";
      draft.pending = false;
      state.modalSubmitting = false;
      closeFixRetractionDialog(false);
      await reloadFixMonitoringAndFocus(issueID, annotationID);
    } catch (error) {
      draft.pending = false;
      state.modalSubmitting = false;
      syncFixRetractionDialog();
      showModalAlert(
        elements.fixRetractionAlert,
        fixMutationErrorMessage(error, "retract"),
        true,
      );
    }
  }

  async function reloadFixMonitoringAndFocus(issueID, annotationID) {
    invalidateFixMonitoringSnapshotsAfterMutation();
    void loadFixMonitoring(false);
    const loaded = await loadFixMonitoringDetail(
      issueID,
      false,
      "",
      true,
      annotationID,
    );
    if (
      !loaded ||
      issueID !== state.selectedIssueID ||
      !focusCurrentElement(focusRegistry.fixHistoryRows.get(annotationID))
    ) {
      focusCurrentElement(elements.fixActionStatus);
    }
  }

  function invalidateFixMonitoringSnapshotsAfterMutation() {
    state.fixMonitoring.requestGeneration += 1;
    state.fixMonitoring.data = [];
    state.fixMonitoring.nextCursor = "";
    state.fixMonitoring.hasMore = false;
    state.fixMonitoring.viewCursor = "";
    state.fixMonitoring.evidenceEvaluatedAt = "";
    state.fixMonitoring.status = "idle";
    state.fixMonitoring.error = null;
    focusRegistry.monitoringCards.clear();

    state.fixHistoryRequestGeneration += 1;
    state.fixHistoryNextCursor = "";
    state.fixHistoryHasMore = false;
    state.fixHistoryViewCursor = "";
    state.fixHistory = state.fixHistory.map((annotation) => ({
      ...annotation,
      observation_view_cursor: "",
    }));
    fixObservationPages.forEach((page) => {
      page.requestGeneration += 1;
    });
    fixObservationPages.clear();
    focusRegistry.fixObservationRows.clear();
    renderFixMonitoring();
    renderFixAttempts();
  }

  function fixMutationErrorMessage(error, action) {
    const verb = action === "retract" ? "retraction" : "attempt";
    if (error instanceof LocalMutationTimeoutError) {
      return `Belay did not confirm the ${verb} in time. Retry the same submission; do not start another one.`;
    }
    if (!(error instanceof LocalAPIError)) {
      return `The ${verb} was not confirmed. Retry the same submission; do not start another one.`;
    }
    if (error.status === 403) {
      return "Belay rejected this change. Reopen Belay Local using the URL printed by belay local, then try again.";
    }
    if (error.status === 409) {
      if (error.problemType === "belay.local/already-retracted") {
        return "This attempt was already retracted by another request. Reload its history before continuing.";
      }
      if (error.problemType === "belay.local/idempotency-conflict") {
        return "This request conflicts with an earlier unresolved submission. Abandon that submission before choosing different input.";
      }
      if (error.problemType === "belay.local/ineligible-fix-annotation") {
        return experiencePageText(
          "An attempt can no longer be recorded for this finding. Refresh Attention before continuing.",
        );
      }
      return `The ${verb} conflicted with newer local state. The unresolved submission is still available to retry unchanged or abandon.`;
    }
    if (error.status === 400) {
      return `The ${verb} request was rejected. Review the selected category or reason before retrying.`;
    }
    if (error.status >= 500) {
      return `Belay could not confirm the ${verb}. Retry the same submission.`;
    }
    return `The ${verb} was not confirmed. The unresolved submission is still available to retry unchanged or abandon.`;
  }

  function requireFixSchema(response) {
    if (!isRecord(response) || response.schema_version !== "belay.fix.v1") {
      throw new Error("Local API returned an unsupported fix schema.");
    }
  }

  function requireFixMonitoringSchema(response) {
    if (
      !isRecord(response) ||
      response.schema_version !== "belay.fix-monitoring.v1"
    ) {
      throw new Error(
        "Local API returned an unsupported fix-monitoring schema.",
      );
    }
  }

  function showFixActionStatus(message, tone) {
    state.fixActionMessage = message;
    state.fixActionTone = tone;
    renderFixActionStatus();
  }

  function showModalAlert(element, message, moveFocus) {
    element.textContent = message;
    element.hidden = false;
    if (moveFocus) focusCurrentElement(element);
  }

  function hideModalAlert(element) {
    element.hidden = true;
    element.textContent = "";
  }

  function openModalLayer(layer, dialog, initialFocus) {
    elements.appShell.inert = true;
    elements.appShell.setAttribute("aria-hidden", "true");
    layer.hidden = false;
    document.body.classList.add("is-modal-open");
    focusCurrentElement(initialFocus) || focusCurrentElement(dialog);
  }

  function closeModalLayer(kind, layer, restoreFocus) {
    if (state.activeModal !== kind && layer.hidden) return;
    const returnFocus = state.dialogReturnFocus;
    layer.hidden = true;
    if (state.activeModal === kind) state.activeModal = "";
    if (!state.activeModal) {
      elements.appShell.inert = false;
      elements.appShell.removeAttribute("aria-hidden");
      document.body.classList.remove("is-modal-open");
      applyPaneAccessibility();
    }
    state.dialogReturnFocus = null;
    if (restoreFocus) {
      restoreLogicalFocus(returnFocus, elements.issueDetailHeading);
    }
  }

  function handleModalKeydown(event) {
    if (!state.activeModal) return;
    if (event.key === "Escape") {
      if (state.modalSubmitting) return;
      event.preventDefault();
      if (state.activeModal === "mission-pack") {
        closeMissionPackDrawer(true);
      } else if (state.activeModal === "update") {
        closeUpdateDialog(true);
      } else if (state.activeModal === "report-evidence") {
        closeReportEvidenceDrawer(true);
      } else if (state.activeModal === "fix-attempt") {
        closeFixAttemptDialog(true);
      } else {
        closeFixRetractionDialog(true);
      }
      return;
    }
    if (event.key !== "Tab") return;
    const dialog =
      state.activeModal === "mission-pack"
        ? elements.missionPackDialog
        : state.activeModal === "update"
          ? elements.updateDialog
        : state.activeModal === "report-evidence"
        ? elements.reportEvidenceDialog
        : state.activeModal === "fix-attempt"
          ? elements.fixAttemptDialog
          : elements.fixRetractionDialog;
    const focusable = Array.from(
      dialog.querySelectorAll(
        'button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ),
    ).filter(canReceiveFocus);
    if (!focusable.length) {
      event.preventDefault();
      focusCurrentElement(dialog);
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      focusCurrentElement(last);
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      focusCurrentElement(first);
    }
  }

  function renderOccurrences() {
    focusRegistry.occurrenceActions.clear();
    const fragment = document.createDocumentFragment();
    state.occurrences.forEach((occurrence) => {
      fragment.append(createOccurrenceRow(occurrence));
    });
    elements.occurrenceList.replaceChildren(fragment);
    elements.occurrencesLoading.hidden =
      state.occurrenceStatus !== "loading";
    elements.occurrenceCount.textContent = state.occurrenceHasMore
      ? `${state.occurrences.length}+`
      : String(state.occurrences.length);
    elements.occurrencesPagination.hidden = !state.occurrenceHasMore;
    elements.occurrencesLoadMore.disabled =
      state.occurrenceStatus === "loading-more";
    elements.occurrencesPageStatus.textContent = state.occurrenceHasMore
      ? `Showing ${state.occurrences.length}; more related sessions are available.`
      : `Showing ${state.occurrences.length} related ${
          state.occurrences.length === 1 ? "session" : "sessions"
        }.`;
    if (
      state.occurrenceStatus === "ready" &&
      state.occurrences.length === 0
    ) {
      elements.occurrenceList.append(
        createElement(
          "p",
          "overview-empty",
          "No related session was returned.",
        ),
      );
    }
  }

  function createOccurrenceRow(occurrence) {
    const focusKey = occurrenceFocusKey(state.selectedIssueID, occurrence);
    const article = createElement("article", "occurrence-card");
    const header = createElement("div", "occurrence-header");
    const identity = createElement("div");
    identity.append(
      createElement(
        "strong",
        "",
        `${displayHarness(occurrence.harness)} session`,
      ),
      createElement("code", "", compactID(occurrence.session_id)),
    );
    const status = createElement(
      "span",
      "analysis-badge",
      analysisStatusLabel(occurrence.analysis_status),
    );
    header.append(identity, status);
    const metadata = createElement("p", "occurrence-meta");
    metadata.textContent = [
      retainedInterval(occurrence),
      issueSourceLabel(occurrence.origin),
      `${occurrenceEventIDs(occurrence).length} cited ${
        occurrenceEventIDs(occurrence).length === 1 ? "event" : "events"
      }`,
      occurrence.evidence_complete === false
        ? "Evidence incomplete"
        : occurrence.evidence_complete === true
          ? "Evidence complete"
          : "Evidence completeness unavailable",
    ].join(" · ");
    const actions = createElement("div", "occurrence-actions");
    const inspect = createElement(
      "button",
      "secondary-button",
      "Inspect session evidence",
    );
    inspect.type = "button";
    inspect.setAttribute("aria-expanded", "false");
    const open = createElement("button", "primary-button", "Open full session");
    open.type = "button";
    if (focusKey) focusRegistry.occurrenceActions.set(focusKey, open);
    const evidence = createElement("div", "occurrence-evidence");
    evidence.hidden = true;
    inspect.addEventListener("click", () => {
      void loadOccurrenceEvidence(occurrence, evidence, inspect);
    });
    open.addEventListener("click", () => {
      state.sessionReturnFocus = { type: "occurrence", key: focusKey };
      state.sessionReturnView = "attention";
      openSession(readText(occurrence.session_id), null);
    });
    actions.append(inspect, open);
    article.append(header, metadata, actions, evidence);
    return article;
  }

  async function loadOccurrenceEvidence(occurrence, container, button) {
    if (container.dataset.loaded === "true") {
      container.hidden = !container.hidden;
      button.setAttribute("aria-expanded", String(!container.hidden));
      return;
    }
    const sessionID = readText(occurrence.session_id);
    const eventIDs = occurrenceEventIDs(occurrence).slice(0, 50);
    container.hidden = false;
    button.disabled = true;
    button.setAttribute("aria-expanded", "true");
    container.replaceChildren(
      createElement("p", "overview-empty", "Loading cited events…"),
    );
    if (!sessionID || eventIDs.length === 0) {
      container.replaceChildren(
        createElement(
          "p",
          "overview-empty",
          "No cited events were provided for this finding.",
        ),
      );
      container.dataset.loaded = "true";
      button.disabled = false;
      return;
    }
    const parameters = new URLSearchParams();
    eventIDs.forEach((eventID) => parameters.append("event_id", eventID));
    try {
      const response = await apiGet(
        `/v1/sessions/${encodeURIComponent(sessionID)}/events/lookup?${parameters.toString()}`,
      );
      renderOccurrenceEvidence(container, response, eventIDs.length);
      container.dataset.loaded = "true";
    } catch (error) {
      container.replaceChildren(
        createElement(
          "p",
          "overview-empty",
          customerErrorMessage(
            error,
            "Cited events could not be loaded.",
          ),
        ),
      );
    } finally {
      button.disabled = false;
    }
  }

  function renderOccurrenceEvidence(container, response, requestedFallback) {
    const events = Array.isArray(response.data) ? response.data : [];
    const requested = toFiniteNumber(response.requested_count) || requestedFallback;
    const found = toFiniteNumber(response.found_count) || events.length;
    const missing = Number.isFinite(Number(response.missing_count))
      ? toFiniteNumber(response.missing_count)
      : Math.max(0, requested - found);
    const fragment = document.createDocumentFragment();
    fragment.append(
      createElement(
        "p",
        "evidence-lookup-summary",
        `${found} of ${requested} cited events are still stored; ${missing} are no longer stored.`,
      ),
    );
    events.forEach((event) =>
      fragment.append(
        createLookupEvent(event, state.selectedIssueEvidenceContext),
      ),
    );
    if (!events.length) {
      fragment.append(
        createElement(
          "p",
          "overview-empty",
          "The cited events are no longer stored.",
        ),
      );
    }
    container.replaceChildren(fragment);
  }

  function createLookupEvent(event, evidenceContext = "") {
    const observation = isRecord(event.observation) ? event.observation : {};
    const descriptor = eventDescriptor(observation, evidenceContext);
    const row = createElement("article", "lookup-event");
    row.append(
      createElement(
        "strong",
        "",
        descriptor.label,
      ),
      createElement(
        "time",
        "",
        formatFullDate(parseDate(event.occurred_at)) || "Time unavailable",
      ),
      createElement(
        "p",
        "",
        descriptor.detail ||
          readText(observation.type) ||
          "Retained event",
      ),
    );
    return row;
  }

  function closeIssueDetail(restoreFocus = true, forceList = false) {
    const returnFocus = state.issueReturnFocus;
    const returnToBrief =
      !forceList &&
      returnFocus &&
      ["brief-action", "brief-session"].includes(returnFocus.type);
    const returnToDiagnosis =
      !forceList &&
      returnFocus &&
      returnFocus.type === "diagnosis-action";
    const returnToFamily =
      !forceList &&
      !returnToBrief &&
      !returnToDiagnosis &&
      state.selectedIssueSource === "family" &&
      Boolean(state.selectedFamily);
    state.occurrenceRequestGeneration += 1;
    state.fixEligibilityRequestGeneration += 1;
    state.fixHistoryRequestGeneration += 1;
    closeFixAttemptDialog(false);
    closeFixRetractionDialog(false);
    state.selectedIssueID = "";
    state.selectedIssueKind = "";
    state.selectedIssueSource = "";
    state.selectedIssueFamilyID = "";
    state.selectedIssueEvidenceContext = "";
    state.selectedDrivingAnnotationID = "";
    state.selectedIssue = null;
    state.selectedIssueCatalog = null;
    state.selectedGlobalAnalysisCoverage = null;
    state.selectedIssueViewCursor = "";
    state.occurrences = [];
    state.occurrenceNextCursor = "";
    state.occurrenceHasMore = false;
    state.occurrenceStatus = "idle";
    resetIssueEvidencePreview();
    resetFixIssueState();
    state.issueReturnFocus = null;
    elements.issueDetail.hidden = true;
    elements.familyDetail.hidden = !returnToFamily;
    elements.attentionWelcome.hidden = returnToFamily || Boolean(state.selectedFamily);
    document.body.classList.toggle(
      "is-attention-detail-open",
      returnToFamily || Boolean(state.selectedFamily),
    );
    renderIssueBucket(state.issues);
    renderIssueBucket(state.evidenceGaps);
    renderFixMonitoring();
    if (returnToFamily) renderAttentionFamilyDetail();
    if (returnToBrief) setActiveView("brief", false);
    if (returnToDiagnosis) setActiveView("sessions", false);
    applyPaneAccessibility();
    if (restoreFocus) {
      restoreLogicalFocus(
        returnFocus,
        returnToFamily
          ? elements.familyDetailHeading
          : returnToBrief
            ? elements.briefHeading
            : returnToDiagnosis
              ? elements.sessionDiagnosisHeading
              : elements.navAttention,
      );
    }
    if (returnToBrief) state.briefSelectionID = "";
  }

  function closeFamilyDetail(restoreFocus = true) {
    const returnFocus = state.familyReturnFocus;
    const returnToBrief =
      returnFocus &&
      ["brief-action", "brief-session"].includes(returnFocus.type);
    const returnToDiagnosis =
      returnFocus && returnFocus.type === "diagnosis-action";
    if (state.selectedIssueID) closeIssueDetail(false, true);
    clearAttentionFamilyDetailState();
    elements.familyDetail.hidden = true;
    elements.issueDetail.hidden = true;
    elements.attentionWelcome.hidden = false;
    document.body.classList.remove("is-attention-detail-open");
    renderIssueBucket(state.issues);
    if (returnToBrief) setActiveView("brief", false);
    if (returnToDiagnosis) setActiveView("sessions", false);
    applyPaneAccessibility();
    if (restoreFocus) {
      restoreLogicalFocus(
        returnFocus,
        returnToBrief
          ? elements.briefHeading
          : returnToDiagnosis
            ? elements.sessionDiagnosisHeading
            : elements.navAttention,
      );
    }
    if (returnToBrief) state.briefSelectionID = "";
  }

  function clearAttentionFamilyDetailState() {
    state.familyMemberRequestGeneration += 1;
    state.selectedFamily = null;
    state.selectedFamilyViewCursor = "";
    state.familyMembers = [];
    state.familyMemberNextCursor = "";
    state.familyMemberHasMore = false;
    state.familyMemberStatus = "idle";
    state.familyMemberError = "";
    state.familyReturnFocus = null;
    focusRegistry.familyMembers.clear();
    elements.familyMemberList.replaceChildren();
    elements.familyMembersLoading.hidden = true;
    elements.familyMembersEmpty.hidden = true;
    elements.familyMembersPagination.hidden = true;
    elements.familyMembersPageStatus.textContent = "";
  }

  async function refreshAttentionAfterExpiry() {
    if (state.attentionExpiryRefresh) return;
    const returnFocus =
      state.issueReturnFocus || state.familyReturnFocus;
    const returnToBrief =
      Boolean(state.briefSelectionID) ||
      (returnFocus &&
        ["brief-action", "brief-session"].includes(returnFocus.type));
    const returnToDiagnosis =
      returnFocus && returnFocus.type === "diagnosis-action";
    state.attentionExpiryRefresh = true;
    clearExpiredIssueSnapshotState();
    if (returnToBrief) {
      state.briefSelectionID = "";
      setActiveView("brief", false);
      try {
        await loadDeveloperBrief();
        focusCurrentElement(elements.briefHeading);
      } finally {
        state.attentionExpiryRefresh = false;
      }
      return;
    }
    if (returnToDiagnosis) {
      setActiveView("sessions", false);
      try {
        if (state.selectedSessionID) {
          await loadSessionDetail(state.selectedSessionID);
        }
        focusCurrentElement(elements.sessionDiagnosisHeading);
      } finally {
        state.attentionExpiryRefresh = false;
      }
      return;
    }
    showAttentionNotice(
      "The finding view changed. Refreshing Attention with current data…",
      "pending",
    );
    try {
      const refreshed = await refreshAttention(false, true);
      if (refreshed) {
        showAttentionNotice(
          "Attention refreshed with current data.",
          "success",
          4000,
        );
      } else {
        showAttentionNotice(
          "Attention refresh failed. Retry before relying on the issue lists.",
          "error",
        );
      }
    } finally {
      state.attentionExpiryRefresh = false;
    }
  }

  function clearExpiredIssueSnapshotState() {
    resetIssueBucket(state.issues);
    resetIssueBucket(state.evidenceGaps);
    resetFixMonitoringBucket(false);
    clearExpiredFixDraftState();
    closeIssueDetail(false, true);
    clearAttentionFamilyDetailState();
    elements.familyDetail.hidden = true;
    elements.attentionWelcome.hidden = false;
    document.body.classList.remove("is-attention-detail-open");
    applyPaneAccessibility();
  }

  function clearExpiredFixDraftState() {
    fixDrafts.forEach((draft) => {
      draft.idempotencyKey = "";
      draft.actionToken = "";
      draft.attempted = false;
      draft.unresolved = false;
      draft.pending = false;
    });
    fixRetractionDrafts.forEach((draft) => {
      draft.idempotencyKey = "";
      draft.attempted = false;
      draft.unresolved = false;
      draft.pending = false;
    });
    state.modalSubmitting = false;
  }

  function showAttentionNotice(message, tone = "status", hideAfter = 0) {
    const safeTone = ["status", "pending", "success", "error"].includes(tone)
      ? tone
      : "status";
    globalThis.clearTimeout(state.refreshNoticeTimer);
    elements.attentionRefreshNotice.textContent =
      experiencePageText(message);
    elements.attentionRefreshNotice.dataset.tone = safeTone;
    elements.attentionRefreshNotice.setAttribute(
      "role",
      safeTone === "error" ? "alert" : "status",
    );
    elements.attentionRefreshNotice.setAttribute(
      "aria-live",
      safeTone === "error" ? "assertive" : "polite",
    );
    elements.attentionRefreshNotice.hidden = false;
    if (hideAfter > 0) {
      state.refreshNoticeTimer = globalThis.setTimeout(
        hideAttentionNotice,
        hideAfter,
      );
    }
  }

  function hideAttentionNotice() {
    globalThis.clearTimeout(state.refreshNoticeTimer);
    elements.attentionRefreshNotice.hidden = true;
    elements.attentionRefreshNotice.textContent = "";
    elements.attentionRefreshNotice.dataset.tone = "status";
    elements.attentionRefreshNotice.setAttribute("aria-live", "polite");
  }

  function issueCatalogEntry(titleCode) {
    const code = readText(titleCode);
    return (
      issueCatalog[code] || {
        title: "Detected issue",
        explanation:
          "Belay recorded a finding that does not yet have a plain-language explanation.",
        action: "Inspect the evidence",
      }
    );
  }

  function issueDisplayCatalog(issue) {
    const base = issueCatalogEntry(issue && issue.title_code);
    if (
      readText(issue && issue.title_code) !== "issue.numbat_finding" ||
      readText(issue && issue.origin).toLowerCase() !== "numbat"
    ) {
      return base;
    }
    const sourceSignalCode = safeSourceSignalCode(
      issue && issue.source_signal_code,
    );
    return (
      sourceSignalCatalog[`numbat/${sourceSignalCode}`] ||
      issueCatalog["issue.numbat_finding"]
    );
  }

  function safeSourceSignalCode(value) {
    const code = readText(value);
    return /^[a-z0-9][a-z0-9_.-]{0,63}$/.test(code) ? code : "";
  }

  function issueRecurrenceLabel(issue) {
    const sessions = Number(issue && issue.session_count);
    if (!Number.isFinite(sessions) || sessions < 1) {
      return "Session count unavailable";
    }
    return sessions > 1
      ? `Observed in ${formatNumber(sessions)} sessions`
      : "Observed in one session";
  }

  function issueHarnessLabel(values) {
    const harnesses = Array.isArray(values)
      ? values.map(displayHarness).filter(Boolean)
      : [];
    return harnesses.length ? harnesses.join(", ") : "Agent unavailable";
  }

  function issueCaveat(issue) {
    if (issue.evidence_complete === false) return "Evidence is incomplete.";
    if (issue.evidence_complete !== true) {
      return "Evidence completeness is unavailable.";
    }
    const quality = readText(issue.scope_quality).toLowerCase();
    if (quality === "unscoped") {
      return "Belay could not determine whether these observations belong to the same project.";
    }
    if (quality === "conflict") {
      return "Project information conflicts across these observations.";
    }
    if (issue.retained_history_only === true) {
      return "Based on retained Local history only.";
    }
    return "";
  }

  function issueScopeDisclosure(issue, catalogCaveat = "") {
    const statements = [
      readText(catalogCaveat),
      issueCaveat(issue),
    ].filter(Boolean);
    return Array.from(new Set(statements)).join(" ");
  }

  function expectedIssueSelection(kind) {
    const attentionKind = kind === "evidence_gap" ? "evidence_gap" : "issue";
    const experimental = state.issueFilters.experimental
      ? "include"
      : "stable";
    return {
      attention_kind: attentionKind,
      experimental,
      includes_evidence_gaps: attentionKind === "evidence_gap",
      includes_experimental: experimental !== "stable",
    };
  }

  function readIssueSelection(value, kind) {
    const expected = expectedIssueSelection(kind);
    if (!isRecord(value)) return expected;
    const selection = {
      attention_kind: readText(value.attention_kind).toLowerCase(),
      experimental: readText(value.experimental).toLowerCase(),
      includes_evidence_gaps: value.includes_evidence_gaps === true,
      includes_experimental: value.includes_experimental === true,
    };
    if (
      selection.attention_kind !== expected.attention_kind ||
      selection.experimental !== expected.experimental ||
      selection.includes_evidence_gaps !==
        expected.includes_evidence_gaps ||
      selection.includes_experimental !==
        expected.includes_experimental
    ) {
      throw new Error(
        "Local API returned issue results for a different normalized selection.",
      );
    }
    return selection;
  }

  function requireCursorPage(response, currentCursor, label) {
    const nextCursor = readCursor(response && response.next_cursor);
    const hasMore = response && response.has_more === true;
    if (hasMore !== Boolean(nextCursor)) {
      throw new Error(
        `Local API returned inconsistent ${label} pagination metadata.`,
      );
    }
    if (currentCursor && nextCursor === currentCursor) {
      throw new Error(
        `Local API returned a non-advancing ${label} cursor.`,
      );
    }
    return { nextCursor, hasMore };
  }

  function readIssueCatalog(value, issue, previous) {
    if (!isRecord(value)) {
      return previous || fallbackIssueCatalog(issue);
    }
    const catalog = {
      catalog_version: readText(value.catalog_version),
      catalog_status: readText(value.catalog_status).toLowerCase(),
      title_code: readText(value.title_code),
      display_title: readText(value.display_title),
      observation_statement: readText(value.observation_statement),
      caveat: readText(value.caveat),
      next_evidence_action: readText(value.next_evidence_action),
      source_signal_code: safeSourceSignalCode(value.source_signal_code) || null,
      source_signal_catalog_version: readText(
        value.source_signal_catalog_version,
      ),
      source_signal_catalog_status: readText(
        value.source_signal_catalog_status,
      ).toLowerCase(),
    };
    const issueTitleCode = readText(issue && issue.title_code);
    const actions = new Set([
      "inspect_cited_events",
      "inspect_matching_sessions",
      "inspect_verification_events",
      "review_agent_permissions",
    ]);
    if (
      catalog.catalog_version !== "belay.issue-explanations.v1" ||
      !["known", "unknown"].includes(catalog.catalog_status) ||
      !catalog.title_code ||
      catalog.title_code !== issueTitleCode ||
      !catalog.display_title ||
      !catalog.observation_statement ||
      !catalog.caveat ||
      !actions.has(catalog.next_evidence_action) ||
      catalog.source_signal_catalog_version !== "belay.source-signals.v1" ||
      !["known", "unknown", "not_applicable"].includes(
        catalog.source_signal_catalog_status,
      ) ||
      (catalog.source_signal_catalog_status === "known" &&
        !catalog.source_signal_code)
    ) {
      throw new Error("Local API returned invalid issue catalog metadata.");
    }
    return catalog;
  }

  function fallbackIssueCatalog(issue) {
    const titleCode = readText(issue && issue.title_code);
    const display = issueDisplayCatalog(issue);
    const sourceSignalCode = safeSourceSignalCode(
      issue && issue.source_signal_code,
    );
    return {
      catalog_version: "browser-fallback",
      catalog_status:
        display.title === "Detected issue" ? "unknown" : "known",
      title_code: titleCode,
      display_title: display.title,
      observation_statement: display.explanation,
      caveat: display.caveat || "",
      next_evidence_action: "inspect_cited_events",
      source_signal_code: sourceSignalCode || null,
      source_signal_catalog_version: "belay.source-signals.v1",
      source_signal_catalog_status:
        titleCode === "issue.numbat_finding"
          ? sourceSignalCode === "tamper.guardrails_off"
            ? "known"
            : "unknown"
          : "not_applicable",
    };
  }

  function readGlobalAnalysisCoverage(value, fallback) {
    return isRecord(value)
      ? value
      : isRecord(fallback)
        ? fallback
        : null;
  }

  function selectedIssueListCoverage() {
    const bucket =
      state.selectedIssueKind === "evidence_gap"
        ? state.evidenceGaps
        : state.issues;
    return bucket.analysis;
  }

  function globalCoverageQualifier(coverage) {
    if (!isRecord(coverage) || coverage.complete === true) return "";
    return `Analysis of stored sessions is incomplete: ${incompleteCoverageText(
      coverage,
    )}.`;
  }

  function createToneBadge(value) {
    const badge = createElement(
      "span",
      "severity-badge",
      readableLabel(value, "Reported"),
    );
    badge.dataset.tone = severityTone(value);
    return badge;
  }

  function severityTone(value) {
    const severity = readText(value).toLocaleLowerCase();
    return ["critical", "high", "medium", "low", "info"].includes(severity)
      ? severity
      : "reported";
  }

  function normalizeAnalysisStatus(value) {
    const status = readText(value).toLowerCase();
    return ["current", "pending", "failed", "truncated"].includes(status)
      ? status
      : "unknown";
  }

  function analysisStatusLabel(value) {
    const status = normalizeAnalysisStatus(value);
    if (status === "truncated") return "Partial";
    if (status === "unknown") return "Analysis status unavailable";
    return readableLabel(status);
  }

  function safeCatalogCode(value) {
    const code = readText(value).toLowerCase();
    return /^[a-z0-9_]{1,64}$/.test(code) ? code : "unknown_category";
  }

  function evidenceCompletenessLabel(value) {
    if (value === true) return "Complete";
    if (value === false) return "Incomplete";
    return "Unavailable";
  }

  function retainedInterval(value) {
    const first = parseDate(value && value.first_observed_at);
    const last = parseDate(value && value.last_observed_at);
    if (first && last) {
      return `${formatFullDate(first)} – ${formatFullDate(last)}`;
    }
    return first || last
      ? formatFullDate(first || last)
      : "Retained interval unavailable";
  }

  function detectorVersionLabel(issue) {
    const version = readText(issue.detector_version);
    if (!version) return "Unavailable";
    return version.toLowerCase().startsWith("v") ? version : `v${version}`;
  }

  function projectRelationshipLabel(value) {
    switch (readText(value).toLowerCase()) {
      case "resolved":
      case "lexical":
        return "Project identified";
      case "unscoped":
        return "Project not identified";
      case "conflict":
        return "Project information conflicts";
      default:
        return "Unavailable";
    }
  }

  function occurrenceEventIDs(occurrence) {
    const evidence = isRecord(occurrence.evidence) ? occurrence.evidence : {};
    const values = Array.isArray(evidence.cited_event_ids)
      ? evidence.cited_event_ids
      : [];
    return values.map(readText).filter(Boolean);
  }

  function incompleteCoverageText(analysis) {
    if (!analysis) return "Analysis coverage is unavailable.";
    return [
      `${formatNumber(analysis.pending_sessions)} pending`,
      `${formatNumber(analysis.failed_sessions)} failed`,
      `${formatNumber(analysis.truncated_sessions)} partial`,
    ].join(" · ");
  }

  function isCursorExpired(error) {
    return (
      error instanceof LocalAPIError &&
      (error.status === 410 || error.problemType === "belay.local/cursor-expired")
    );
  }

  async function loadStats() {
    try {
      const response = await apiGet("/v1/stats");
      state.stats = isRecord(response.data) ? response.data : null;
      renderLocalOverview(response.data_through);
    } catch {
      state.stats = null;
      renderLocalOverview("");
    }
  }

  function renderLocalOverview(dataThrough) {
    const stats = state.stats || {};
    elements.overviewSessionCount.textContent = formatNumberOrDash(
      stats.session_count,
    );
    elements.overviewEventCount.textContent = formatNumberOrDash(stats.event_count);
    elements.overviewFindingCount.textContent = formatNumberOrDash(
      stats.finding_count,
    );
    const harnessCounts = isRecord(stats.harness_counts)
      ? stats.harness_counts
      : {};
    const harnesses = Object.entries(harnessCounts)
      .filter(([, count]) => toFiniteNumber(count) > 0)
      .sort((left, right) => right[1] - left[1]);
    elements.overviewHarnesses.textContent = harnesses.length
      ? `Agents · ${harnesses
          .map(([harness, count]) => `${displayHarness(harness)} ${formatNumber(count)}`)
          .join(" · ")}`
      : "Agent activity unavailable";
    elements.overviewFreshness.textContent = parseDate(dataThrough)
      ? `Last retained data received by Belay · ${formatRelativeTime(dataThrough)}`
      : "Last retained data received by Belay · Unavailable";
    updateHarnessOptions(harnesses.map(([harness]) => harness));
  }

  function updateHarnessOptions(harnesses) {
    const current = state.filters.harness;
    const known = new Set(
      state.sessions.map((session) => readText(session.harness)).filter(Boolean),
    );
    if (state.stats && isRecord(state.stats.harness_counts)) {
      Object.keys(state.stats.harness_counts).forEach((harness) => {
        if (harness) known.add(harness);
      });
    }
    harnesses.forEach((harness) => known.add(harness));
    const options = [createOption("", "All agents")];
    Array.from(known)
      .sort((left, right) => left.localeCompare(right))
      .forEach((harness) => {
        options.push(createOption(harness, displayHarness(harness)));
      });
    elements.filterHarness.replaceChildren(...options);
    elements.filterHarness.value = known.has(current) ? current : "";
    state.filters.harness = elements.filterHarness.value;
  }

  function createOption(value, label) {
    const option = createElement("option", "", label);
    option.value = value;
    return option;
  }

  function resetAndLoadSessions() {
    state.sessionLimit = pageLimits.sessions.initial;
    state.sessionNextCursor = "";
    loadSessions(false);
  }

  async function loadSessions(append) {
    const generation = ++state.sessionsRequestGeneration;
    const cursor = append ? state.sessionNextCursor : "";
    const priorSessions = append && cursor ? state.sessions.slice() : [];
    state.lastAction = "sessions";
    hideError();
    elements.sessionsLoading.hidden = append;
    elements.sessionsEmpty.hidden = true;
    elements.sessionsLoadMore.disabled = true;
    try {
      const response = await apiGet(buildSessionPath(cursor));
      if (generation !== state.sessionsRequestGeneration) return false;
      const page = Array.isArray(response.data) ? response.data : [];
      const nextCursor = readCursor(response.next_cursor);
      state.sessions =
        append && cursor
          ? deduplicateByID(priorSessions.concat(page), "session_id")
          : page;
      state.sessionNextCursor = nextCursor;
      state.sessionHasMore = response.has_more === true || Boolean(nextCursor);
      state.sessionServerScope = filtersAcknowledged(response);
      updateHarnessOptions([]);
      renderSessions();
      renderSessionPagination();
      return true;
    } catch (error) {
      if (generation !== state.sessionsRequestGeneration) return false;
      if (!append) {
        state.sessions = [];
        state.sessionHasMore = false;
        renderSessions();
      }
      renderSessionPagination();
      showError("Unable to load sessions", error);
      return false;
    } finally {
      if (generation === state.sessionsRequestGeneration) {
        elements.sessionsLoading.hidden = true;
        elements.sessionsLoadMore.disabled = false;
        renderSessions();
        renderSessionPagination();
      }
    }
  }

  function buildSessionPath(cursor) {
    const parameters = new URLSearchParams();
    parameters.set("limit", String(state.sessionLimit));
    if (cursor) parameters.set("cursor", cursor);
    if (state.filters.query) parameters.set("query", state.filters.query);
    if (state.filters.harness) parameters.set("harness", state.filters.harness);
    if (state.filters.capture) parameters.set("history", state.filters.capture);
    if (state.sessionOccurredAfter) {
      parameters.set("occurred_after", state.sessionOccurredAfter);
    }
    return `/v1/sessions?${parameters.toString()}`;
  }

  function loadMoreSessions() {
    if (!state.sessionHasMore) return;
    if (state.sessionNextCursor) {
      loadSessions(true);
      return;
    }
    if (state.sessionLimit >= pageLimits.sessions.maximum) return;
    state.sessionLimit = Math.min(
      pageLimits.sessions.maximum,
      state.sessionLimit + pageLimits.sessions.step,
    );
    loadSessions(false);
  }

  function renderSessions() {
    const visibleSessions = filtersActive()
      ? state.sessions.filter(sessionMatchesLocally)
      : state.sessions;
    focusRegistry.sessionCards.clear();
    const fragment = document.createDocumentFragment();
    let priorGroup = "";
    visibleSessions.forEach((session) => {
      const group = recencyGroup(session);
      if (group !== priorGroup) {
        fragment.append(createElement("p", "session-group-label", group));
        priorGroup = group;
      }
      fragment.append(createSessionCard(session));
    });
    elements.sessionList.replaceChildren(fragment);
    elements.sessionCount.textContent = state.sessionHasMore
      ? `${visibleSessions.length}+`
      : String(visibleSessions.length);
    elements.sessionsEmpty.hidden =
      visibleSessions.length !== 0 || !elements.sessionsLoading.hidden;
    if (state.filters.outcome) {
      elements.sessionScopeNote.textContent =
        "Load more sessions before treating this outcome filter as complete.";
    } else if (filtersActive() && state.sessionServerScope) {
      elements.sessionScopeNote.textContent =
        "Filters were applied across all stored sessions.";
    } else if (filtersActive()) {
      elements.sessionScopeNote.textContent =
        "Filters were applied to the sessions currently loaded.";
    } else if (state.sessionHasMore) {
      elements.sessionScopeNote.textContent =
        "Showing recent sessions. Load more to see older sessions.";
    } else {
      elements.sessionScopeNote.textContent = "Showing all returned sessions.";
    }
    elements.clearFilters.disabled = !filtersActive();
  }

  function renderSessionPagination() {
    const atMaximum =
      !state.sessionNextCursor &&
      state.sessionLimit >= pageLimits.sessions.maximum;
    elements.sessionsPagination.hidden = !state.sessionHasMore;
    elements.sessionsLoadMore.hidden = !state.sessionHasMore || atMaximum;
    if (state.sessionHasMore && atMaximum) {
      elements.sessionsPageStatus.textContent =
        `Showing ${state.sessions.length} recent sessions. ` +
        "Additional older sessions exist beyond this browser bound.";
    } else if (state.sessionHasMore) {
      elements.sessionsPageStatus.textContent =
        `Showing ${state.sessions.length} sessions. More are available.`;
    } else {
      elements.sessionsPageStatus.textContent =
        `Showing ${state.sessions.length} returned sessions.`;
    }
  }

  function filtersAcknowledged(response) {
    if (!filtersActive()) return false;
    return (
      Boolean(readCursor(response.next_cursor)) ||
      response.filters_applied === true ||
      response.query_applied === true ||
      isRecord(response.applied_filters) ||
      Array.isArray(response.applied_filters)
    );
  }

  function filtersActive() {
    return Object.values(state.filters).some(Boolean);
  }

  function sessionMatchesLocally(session) {
    const query = state.filters.query.toLocaleLowerCase();
    const harness = readText(session.harness);
    const outcome = normalizeOutcome(session.outcome);
    const capture = sessionHistory(session);
    const searchable = [harness, session.session_id]
      .map(readText)
      .concat(readText(session.project))
      .join(" ")
      .toLocaleLowerCase();
    if (query && !searchable.includes(query)) return false;
    if (state.filters.harness && harness !== state.filters.harness) return false;
    if (state.filters.capture && capture !== state.filters.capture) return false;
    if (
      state.filters.outcome === "reported" &&
      !explicitOutcomes.has(outcome)
    ) {
      return false;
    }
    if (
      state.filters.outcome === "unavailable" &&
      !["unknown", "incomplete"].includes(outcome)
    ) {
      return false;
    }
    return true;
  }

  function createSessionCard(session) {
    const sessionID = readText(session.session_id);
    const harness = displayHarness(session.harness);
    const outcome = normalizeOutcome(session.outcome);
    const card = createElement("article", "session-card");
    card.classList.toggle("is-selected", sessionID === state.selectedSessionID);
    const button = createElement("button", "session-card-main");
    button.type = "button";
    button.setAttribute("aria-pressed", String(sessionID === state.selectedSessionID));
    if (sessionID) focusRegistry.sessionCards.set(sessionID, button);
    button.addEventListener("click", () => {
      state.sessionReturnFocus = { type: "session", sessionID };
      state.sessionReturnView = "sessions";
      selectSession(sessionID);
    });
    const top = createElement("span", "session-card-top");
    const avatar = createElement("span", "harness-avatar", harnessInitial(harness));
    avatar.setAttribute("aria-hidden", "true");
    const title = createElement("span", "session-card-title");
    const project = readText(session.project);
    title.append(
      createElement("strong", "", project || `${harness} session`),
      createElement(
        "small",
        "",
        [project ? harness : "", sessionDurationLabel(session)]
          .filter(Boolean)
          .join(" · ") || formatSessionDate(session),
      ),
    );
    const dot = createElement("span", "session-dot");
    dot.dataset.tone = explicitOutcomes.has(outcome) ? outcome : "unknown";
    dot.setAttribute("aria-hidden", "true");
    top.append(dot, avatar, title, sessionOutcomeBadge(outcome));
    const meta = createElement("span", "session-card-meta");
    meta.append(createElement("span", "", formatEventCount(session.event_count)));
    const history = sessionHistory(session);
    meta.append(
      createElement(
        "span",
        history === "live" ? "capture-label live" : "capture-label reconstructed",
        history === "mixed"
          ? "Mixed collection"
          : history === "historical"
            ? "Imported history"
            : "Live",
      ),
    );
    const time = createElement("time", "", formatRelativeTime(session.ended_at));
    const endedAt = parseDate(session.ended_at);
    if (endedAt) {
      time.dateTime = endedAt.toISOString();
      time.title = formatFullDate(endedAt);
    }
    meta.append(time);
    button.append(top, meta);
    button.title = sessionID;
    card.append(button);
    return card;
  }

  function sessionDurationLabel(session) {
    const milliseconds = Number(session.duration_ms);
    if (Number.isFinite(milliseconds) && milliseconds > 0) {
      return habitsDuration(milliseconds);
    }
    const startedAt = parseDate(session.started_at);
    const endedAt = parseDate(session.ended_at);
    return startedAt && endedAt ? formatDuration(startedAt, endedAt) : "";
  }

  function setSessionTab(tab) {
    state.sessionTab = tab === "timeline" ? "timeline" : "summary";
    const timeline = state.sessionTab === "timeline";
    elements.sessionTabSummary.setAttribute("aria-selected", String(!timeline));
    elements.sessionTabTimeline.setAttribute("aria-selected", String(timeline));
    elements.sessionSummaryTab.hidden = timeline;
    elements.sessionTimelineTab.hidden = !timeline;
  }

  function selectSession(sessionID) {
    const session = state.sessions.find(
      (candidate) => readText(candidate.session_id) === sessionID,
    );
    if (!session) return;
    beginSessionSelection(sessionID, session);
    void loadTimeline(sessionID, false);
    void loadSessionDetail(sessionID);
    void loadFindings(sessionID);
  }

  function openSession(sessionID, optionalKnownSummary) {
    if (!sessionID) return;
    const known =
      optionalKnownSummary ||
      state.sessions.find(
        (candidate) => readText(candidate.session_id) === sessionID,
      );
    setActiveView("sessions", false);
    if (!known) {
      const generation = ++state.directSessionRequestGeneration;
      void loadDirectSession(sessionID, generation);
      return;
    }
    beginSessionSelection(sessionID, known);
    void loadTimeline(sessionID, false);
    void loadSessionDetail(sessionID);
    void loadFindings(sessionID);
  }

  function beginSessionSelection(sessionID, session) {
    state.selectedSessionID = sessionID;
    state.selectedEventTotal = toFiniteNumber(session.event_count);
    state.selectedSessionDetail = session;
    state.selectedOverview = null;
    state.selectedDiagnosis = null;
    state.events = [];
    state.findings = [];
    state.findingNextCursor = "";
    state.findingHasMore = false;
    state.findingsMayHaveMore = true;
    state.findingsStatus = "loading";
    state.overviewStatus = "loading";
    state.eventLimit = pageLimits.events.initial;
    state.eventNextCursor = "";
    state.eventHasMore = false;
    state.showAllEvents = false;
    state.lastAction = "timeline";
    elements.allEventsToggle.setAttribute("aria-pressed", "false");
    elements.allEventsToggle.classList.remove("is-active");
    setSessionTab("summary");
    elements.sessionVisual.hidden = true;
    elements.sessionVisual.replaceChildren();
    renderSessions();
    renderSessionHeader(session);
    renderSessionDiagnosis();
    renderSessionOverview();
    hideError();
    elements.welcomeState.hidden = true;
    elements.timelineView.hidden = false;
    elements.timelineLoading.hidden = false;
    elements.eventsEmpty.hidden = true;
    elements.eventList.replaceChildren();
    elements.eventsPagination.hidden = true;
    elements.backButton.setAttribute(
      "aria-label",
      state.sessionReturnView === "attention"
        ? "Back to issue detail"
        : state.sessionReturnView === "brief"
          ? experiencePageText("Back to Developer Brief")
          : state.experience === "value-first"
            ? "Back to History"
            : "Back to sessions",
    );
    document.body.classList.add("is-timeline-open");
    applyPaneAccessibility();
    focusCurrentElement(elements.selectedHarness);
  }

  async function loadSessionDetail(sessionID) {
    try {
      const response = await apiGet(
        `/v1/sessions/${encodeURIComponent(sessionID)}`,
      );
      if (state.selectedSessionID !== sessionID) return;
      const detail = isRecord(response.data) ? response.data : {};
      state.selectedSessionDetail = detail;
      state.selectedEventTotal = toFiniteNumber(detail.event_count);
      state.selectedOverview = firstRecord(
        response.overview,
        detail.overview,
        response.data_overview,
      );
      state.selectedDiagnosis = isRecord(response.diagnosis)
        ? response.diagnosis
        : null;
      state.overviewStatus = state.selectedOverview ? "complete" : "partial";
      renderSessionHeader(detail);
      renderSessionDiagnosis();
      renderSessionOverview();
    } catch {
      if (state.selectedSessionID !== sessionID) return;
      state.overviewStatus = "partial";
      state.selectedDiagnosis = null;
      renderSessionDiagnosis();
      renderSessionOverview();
    }
  }

  async function loadDirectSession(sessionID, generation) {
    hideError();
    try {
      const response = await apiGet(
        `/v1/sessions/${encodeURIComponent(sessionID)}`,
      );
      if (
        generation !== state.directSessionRequestGeneration ||
        state.activeView !== "sessions"
      ) {
        return;
      }
      const detail = isRecord(response.data) ? response.data : null;
      if (!detail) throw new Error("Local session detail was unavailable.");
      beginSessionSelection(sessionID, detail);
      state.selectedSessionDetail = detail;
      state.selectedEventTotal = toFiniteNumber(detail.event_count);
      state.selectedOverview = firstRecord(
        response.overview,
        detail.overview,
        response.data_overview,
      );
      state.selectedDiagnosis = isRecord(response.diagnosis)
        ? response.diagnosis
        : null;
      state.overviewStatus = state.selectedOverview ? "complete" : "partial";
      renderSessionHeader(detail);
      renderSessionDiagnosis();
      renderSessionOverview();
      void loadTimeline(sessionID, false);
      void loadFindings(sessionID);
    } catch (error) {
      if (generation !== state.directSessionRequestGeneration) return;
      const returnView = state.sessionReturnView;
      const returnFocus = state.sessionReturnFocus;
      state.sessionReturnView = "";
      state.sessionReturnFocus = null;
      if (returnView === "attention") {
        setActiveView("attention", false);
        restoreLogicalFocus(
          returnFocus,
          state.selectedIssueID
            ? elements.issueDetailHeading
            : elements.navAttention,
        );
      } else if (returnView === "brief") {
        setActiveView("brief", false);
        restoreLogicalFocus(returnFocus, elements.briefHeading);
        state.briefSelectionID = "";
      } else {
        applyPaneAccessibility();
        restoreLogicalFocus(returnFocus, elements.navSessions);
      }
      showError("Unable to open selected session", error);
    }
  }

  async function loadFindings(sessionID) {
    const append = arguments.length > 1 && arguments[1] === true;
    state.findingsStatus = "loading";
    state.findingsMayHaveMore = append
      ? state.findingHasMore
      : true;
    renderFindingList();
    try {
      const cursor = append ? state.findingNextCursor : "";
      const parameters = new URLSearchParams({
        limit: String(pageLimits.findings.page),
        session_id: sessionID,
      });
      if (cursor) parameters.set("cursor", cursor);
      const response = await apiGet(`/v1/findings?${parameters.toString()}`);
      if (state.selectedSessionID !== sessionID) return;
      const page = Array.isArray(response.data) ? response.data : [];
      if (
        page.some(
          (finding) => readText(finding.session_id) !== sessionID,
        )
      ) {
        throw new Error(
          "Local findings response did not match the selected session.",
        );
      }
      state.findings =
        append && cursor
          ? deduplicateByID(state.findings.concat(page), "finding_id")
          : deduplicateByID(page, "finding_id");
      state.findingNextCursor = readCursor(response.next_cursor);
      state.findingHasMore =
        response.has_more === true || Boolean(state.findingNextCursor);
      if (state.findingHasMore && !state.findingNextCursor) {
        throw new Error("Local findings response omitted its continuation cursor.");
      }
      state.findingsStatus = "ready";
      state.findingsMayHaveMore = state.findingHasMore;
      renderSessionOverview();
      renderEvents();
    } catch {
      if (state.selectedSessionID !== sessionID) return;
      state.findingsStatus = "error";
      state.findingsMayHaveMore = state.findingHasMore;
      renderSessionOverview();
      renderEvents();
    }
  }

  function loadMoreFindings() {
    if (
      !state.selectedSessionID ||
      !state.findingHasMore ||
      !state.findingNextCursor
    ) {
      return;
    }
    void loadFindings(state.selectedSessionID, true);
  }

  async function loadTimeline(sessionID, append) {
    elements.eventsLoadMore.disabled = true;
    try {
      const cursor = append ? state.eventNextCursor : "";
      const parameters = new URLSearchParams({ limit: String(state.eventLimit) });
      if (cursor) parameters.set("cursor", cursor);
      const response = await apiGet(
        `/v1/sessions/${encodeURIComponent(sessionID)}/events?${parameters.toString()}`,
      );
      if (state.selectedSessionID !== sessionID) return;
      const page = Array.isArray(response.data) ? response.data : [];
      state.events =
        append && cursor
          ? deduplicateByID(state.events.concat(page), "event_id")
          : page;
      state.eventNextCursor = readCursor(response.next_cursor);
      state.eventHasMore =
        response.has_more === true || Boolean(state.eventNextCursor);
      renderEvents();
      renderEventPagination();
      elements.timelineFreshness.textContent = formatFreshness(
        response.data_through,
      );
      renderSessionOverview();
    } catch (error) {
      if (state.selectedSessionID === sessionID) {
        state.eventHasMore = false;
        renderEventPagination();
        showError("Unable to load this timeline", error);
      }
    } finally {
      if (state.selectedSessionID === sessionID) {
        elements.timelineLoading.hidden = true;
        elements.eventsLoadMore.disabled = false;
        renderEvents();
      }
    }
  }

  function loadMoreEvents() {
    if (!state.eventHasMore || !state.selectedSessionID) return;
    if (state.eventNextCursor) {
      loadTimeline(state.selectedSessionID, true);
      return;
    }
    if (state.eventLimit >= pageLimits.events.maximum) return;
    state.eventLimit = Math.min(
      pageLimits.events.maximum,
      state.eventLimit + pageLimits.events.step,
    );
    loadTimeline(state.selectedSessionID, false);
  }

  function renderSessionDiagnosis() {
    const diagnosis = state.selectedDiagnosis;
    focusRegistry.diagnosisActions.clear();
    elements.sessionDiagnosisActions.replaceChildren();
    elements.sessionDiagnosisLimitations.replaceChildren();
    if (
      !isRecord(diagnosis) ||
      readText(diagnosis.projection_version) !==
        "belay.session-diagnosis.v1" ||
      !["ready", "limited", "insufficient_detail"].includes(
        readText(diagnosis.status),
      ) ||
      !isRecord(diagnosis.summary)
    ) {
      elements.sessionDiagnosis.hidden = true;
      return;
    }
    elements.sessionDiagnosis.hidden = false;
    elements.sessionDiagnosisStatus.textContent =
      readText(diagnosis.status) === "ready"
        ? "Available"
        : readText(diagnosis.status) === "limited"
          ? "Limited"
          : "Limited detail";
    elements.sessionDiagnosisStatus.dataset.tone =
      readText(diagnosis.status) === "ready" ? "current" : "unknown";
    elements.sessionDiagnosisTitle.textContent =
      readText(diagnosis.summary.title) || "Review the recorded activity";
    elements.sessionDiagnosisDetail.textContent =
      readText(diagnosis.summary.detail) ||
      "Belay does not have enough retained detail to identify a reviewed next step.";
    const cards = Array.isArray(diagnosis.action_cards)
      ? diagnosis.action_cards.slice(0, 5)
      : [];
    const actionFragment = document.createDocumentFragment();
    cards.forEach((card, index) => {
      actionFragment.append(createDiagnosisActionCard(card, index));
    });
    elements.sessionDiagnosisActions.replaceChildren(actionFragment);
    const coverage = isRecord(diagnosis.coverage) ? diagnosis.coverage : {};
    const limitations = Array.isArray(coverage.limitations)
      ? coverage.limitations.slice(0, 20)
      : [];
    const limitationFragment = document.createDocumentFragment();
    limitations.forEach((limitation) => {
      const message = readText(limitation && limitation.message);
      if (message) limitationFragment.append(createElement("li", "", message));
    });
    elements.sessionDiagnosisLimitations.replaceChildren(limitationFragment);
    elements.sessionDiagnosisLimitations.hidden = limitations.length === 0;
  }

  function createDiagnosisActionCard(card, index) {
    const cardID =
      readText(card && card.card_id) || `diagnosis-action-${index}`;
    const article = createElement("article", "diagnosis-action-card");
    article.append(
      createElement(
        "strong",
        "",
        readText(card && card.title) || "Review recorded activity",
      ),
      createElement(
        "p",
        "",
        readText(card && card.observation) ||
          "Belay recorded activity that may deserve review.",
      ),
    );
    const evidence = briefEvidenceSummary(card && card.evidence);
    if (evidence) article.append(createElement("p", "brief-evidence", evidence));
    const limitation = readText(card && card.limitation);
    if (limitation) {
      article.append(createElement("p", "brief-limitation", limitation));
    }
    const nextStep = isRecord(card && card.next_step) ? card.next_step : {};
    const button = createElement(
      "button",
      "secondary-button",
      readText(nextStep.label) || "Review evidence",
    );
    button.type = "button";
    button.disabled = !supportedNextStep(nextStep);
    if (button.disabled) button.textContent = "Next step unavailable";
    const reference = { type: "diagnosis-action", key: cardID };
    button.addEventListener("click", () => {
      void navigateSupportedNextStep(card, nextStep, reference);
    });
    focusRegistry.diagnosisActions.set(cardID, button);
    article.append(button);
    return article;
  }

  function renderSessionHeader(session) {
    const harness = displayHarness(session.harness);
    const outcome = normalizeOutcome(session.outcome);
    const history = sessionHistory(session);
    const startedAt = parseDate(session.started_at);
    const endedAt = parseDate(session.ended_at);
    elements.selectedAvatar.textContent = harnessInitial(harness);
    const project = readText(session.project);
    elements.selectedHarness.textContent = project || `${harness} session`;
    const cost = Number(session.cost_usd);
    elements.selectedMeta.textContent = [
      project ? harness : "",
      sessionDurationLabel(session),
      Number.isFinite(cost) && cost > 0 ? formatReportDollars(cost) : "",
      startedAt ? formatFullDate(startedAt) : "",
    ]
      .filter(Boolean)
      .join(" · ");
    setSessionOutcomeBadge(elements.selectedOutcome, outcome);
    const overview = isRecord(session.overview) ? session.overview : {};
    const outcomeDetail = isRecord(overview.outcome) ? overview.outcome : {};
    if (readText(outcomeDetail.explanation)) {
      elements.selectedOutcome.title = readText(outcomeDetail.explanation);
    }
    elements.selectedHistorical.hidden = history === "live";
    elements.selectedHistorical.textContent =
      history === "mixed" ? "Mixed collection" : "Imported history";
    elements.selectedSessionID.textContent = compactID(session.session_id);
    elements.selectedSessionID.title = readText(session.session_id);
    elements.selectedEventCount.textContent = formatNumber(
      state.selectedEventTotal,
    );
    elements.selectedDuration.textContent = formatDuration(startedAt, endedAt);
    elements.selectedEndedAt.textContent = endedAt
      ? formatRelativeTime(endedAt)
      : "Unavailable";
    elements.selectedEndedAt.title = endedAt ? formatFullDate(endedAt) : "";
  }

  function renderSessionOverview() {
    const overview = buildOverview();
    if (state.overviewStatus === "complete") {
      elements.overviewState.textContent = "Complete session summary";
    } else if (state.overviewStatus === "partial") {
      elements.overviewState.textContent =
        `Partial session summary · ${state.events.length} loaded ` +
        `${state.events.length === 1 ? "event" : "events"}`;
    } else {
      elements.overviewState.textContent =
        "Loading the complete session summary; counts below use the events loaded so far.";
    }
    const metrics = [
      ["Commands", overview.commands],
      ["Tools", overview.tools],
      ["Files", overview.files],
      ["Network", overview.network],
      ["Permissions", overview.permissions],
      ["Explicit failures", overview.failures],
      ["Findings", overview.findings],
      ["Outcome unreported", overview.unreported],
    ];
    const fragment = document.createDocumentFragment();
    metrics.forEach(([label, count]) => {
      const item = createElement("div");
      item.append(
        createElement("dt", "", label),
        createElement("dd", "", formatNumber(count)),
      );
      fragment.append(item);
    });
    elements.activityGrid.replaceChildren(fragment);
    elements.overviewQuality.textContent = [
      overview.coverage ? `Collection detail · ${overview.coverage}` : "",
      overview.confidence ? `Confidence · ${overview.confidence}` : "",
    ]
      .filter(Boolean)
      .join("   ");
    const resourceFragment = document.createDocumentFragment();
    overview.resources.slice(0, 12).forEach((resource) => {
      resourceFragment.append(createElement("span", "resource-chip", resource));
    });
    if (!overview.resources.length) {
      resourceFragment.append(
        createElement("p", "overview-empty", "No resource metadata reported."),
      );
    } else if (overview.resources.length > 12) {
      resourceFragment.append(
        createElement(
          "span",
          "resource-chip more",
          `+${overview.resources.length - 12} more`,
        ),
      );
    }
    elements.resourceList.replaceChildren(resourceFragment);
    if (overview.partial) {
      elements.resourceDisclosure.textContent =
        "Partial: resources are derived from loaded timeline events.";
    } else if (overview.resourcesTruncated) {
      elements.resourceDisclosure.textContent =
        `Showing ${Math.min(12, overview.resources.length)} of ` +
        `${overview.resources.length} referenced resources returned; ` +
        "additional referenced resources were not included in this summary.";
    } else if (overview.resources.length > 12) {
      elements.resourceDisclosure.textContent =
        `Showing 12 of ${overview.resources.length} referenced resources.`;
    } else {
      elements.resourceDisclosure.textContent = "";
    }
    renderSessionStory(overview);
    renderFindingList();
    renderSessionVisual(overview);
    elements.sessionTabTimelineCount.textContent = state.selectedEventTotal
      ? formatNumber(state.selectedEventTotal)
      : "";
  }

  const sessionVisualBins = 36;
  const sessionTestCommandPattern =
    /\b(go test|npm test|pnpm test|yarn test|pytest|jest|vitest|cargo test|make (?:test|verify|check)|mix test|rspec|phpunit|dotnet test|gradle test|mvn test|ctest|bun test)\b/i;

  function renderSessionVisual(overview) {
    const events = state.events;
    const container = elements.sessionVisual;
    container.replaceChildren();
    if (!events.length) {
      container.hidden = true;
      return;
    }
    const times = events.map((event) => parseDate(event.occurred_at)).filter(Boolean);
    const detail = state.selectedSessionDetail || {};
    const start =
      parseDate(detail.started_at) ||
      (times.length ? new Date(Math.min(...times.map((d) => d.getTime()))) : null);
    const end =
      parseDate(detail.ended_at) ||
      (times.length ? new Date(Math.max(...times.map((d) => d.getTime()))) : null);
    if (!start || !end || end.getTime() <= start.getTime()) {
      container.hidden = true;
      return;
    }
    const span = end.getTime() - start.getTime();
    const bins = Array.from({ length: sessionVisualBins }, () => ({
      edits: 0,
      reads: 0,
      commands: 0,
    }));
    const marks = [];
    events.forEach((event) => {
      const at = parseDate(event.occurred_at);
      if (!at) return;
      const position = Math.min(1, Math.max(0, (at.getTime() - start.getTime()) / span));
      const bin = bins[Math.min(sessionVisualBins - 1, Math.floor(position * sessionVisualBins))];
      const observation = isRecord(event.observation) ? event.observation : {};
      const type = readText(observation.type).toLocaleLowerCase();
      const outcome = normalizeOutcome(observation.outcome);
      if (["file.write", "file.create", "file.delete"].includes(type)) bin.edits += 1;
      else if (type === "file.read" || type.startsWith("tool.")) bin.reads += 1;
      else if (type.startsWith("command.")) bin.commands += 1;
      if (type === "command.result" || type === "command.exec") {
        const commandText = commandDisplayDetail(observation);
        if (
          commandText &&
          sessionTestCommandPattern.test(commandText) &&
          explicitOutcomes.has(outcome)
        ) {
          marks.push({
            position,
            tone: outcome,
            label: `${commandText} · ${explicitOutcomeLabel(outcome)}`,
          });
        }
      } else if (outcome === "failed") {
        marks.push({
          position,
          tone: "failed",
          label: `${eventDescriptor(observation).label} · failed`,
        });
      }
    });
    const peak = Math.max(1, ...bins.map((bin) => bin.edits + bin.reads + bin.commands));

    const timeRow = createElement("div", "session-visual-row");
    timeRow.append(createElement("span", "session-visual-label", "Over time"));
    const timeBody = createElement("div", "session-visual-body");
    const strip = createElement("div", "session-strip");
    strip.setAttribute("role", "img");
    strip.setAttribute("aria-label", "Activity across the session, oldest to newest");
    bins.forEach((bin) => {
      const column = createElement("span", "session-strip-column");
      [
        ["reads", bin.reads],
        ["commands", bin.commands],
        ["edits", bin.edits],
      ].forEach(([kind, count]) => {
        if (!count) return;
        const segment = createElement("span", `session-strip-segment session-strip-${kind}`);
        segment.style.height = `${Math.max(2, Math.round((count / peak) * 44))}px`;
        column.append(segment);
      });
      strip.append(column);
    });
    const markRow = createElement("div", "session-marks");
    marks.slice(0, 40).forEach((mark) => {
      const dot = createElement("span", "session-mark");
      dot.dataset.tone = mark.tone;
      dot.style.left = `${mark.position * 100}%`;
      dot.title = mark.label;
      markRow.append(dot);
    });
    const axis = createElement("div", "session-visual-axis");
    axis.append(
      createElement("span", "", formatClockTime(start)),
      createElement("span", "", formatClockTime(end)),
    );
    const legend = createElement("div", "session-visual-legend");
    [
      ["failed", "Failed check"],
      ["succeeded", "Passed check"],
      ["edits", "File edit"],
      ["commands", "Command"],
      ["reads", "Read or search"],
    ].forEach(([kind, label]) => {
      const item = createElement("span", "session-legend-item");
      const swatch = createElement("span", `session-legend-swatch session-strip-${kind}`);
      swatch.dataset.tone = kind;
      item.append(swatch, document.createTextNode(label));
      legend.append(item);
    });
    timeBody.append(markRow, strip, axis, legend);
    if (state.eventHasMore || state.overviewStatus !== "complete") {
      timeBody.append(
        createElement(
          "p",
          "overview-caveat",
          `Drawn from the ${formatNumber(events.length)} loaded ${events.length === 1 ? "event" : "events"}.`,
        ),
      );
    }
    timeRow.append(timeBody);

    const mixRow = createElement("div", "session-visual-row");
    mixRow.append(createElement("span", "session-visual-label", "Activity mix"));
    const mixBody = createElement("div", "session-visual-body session-mix");
    const parts = [
      ["Tool calls", overview.tools, "reads"],
      ["Commands", overview.commands, "commands"],
      ["File changes", overview.files, "edits"],
      ["Permissions", overview.permissions, "permissions"],
    ].filter(([, count]) => count > 0);
    const largest = Math.max(1, ...parts.map(([, count]) => count));
    parts.forEach(([label, count, kind]) => {
      const line = createElement("div", "session-mix-line");
      line.append(createElement("span", "session-mix-label", label));
      const track = createElement("span", "session-mix-track");
      const fill = createElement("span", `session-mix-fill session-strip-${kind}`);
      fill.style.width = `${Math.max(2, Math.round((count / largest) * 100))}%`;
      track.append(fill);
      line.append(track, createElement("span", "session-mix-count", formatNumber(count)));
      mixBody.append(line);
    });
    if (!parts.length) {
      mixBody.append(createElement("p", "overview-empty", "No activity counts were reported."));
    }
    mixRow.append(mixBody);
    container.append(timeRow, mixRow);
    container.hidden = false;
  }

  function renderSessionStory(overview) {
    const attention = [];
    if (overview.failures > 0) {
      attention.push(
        `${formatNumber(overview.failures)} explicit ${
          overview.failures === 1 ? "failure" : "failures"
        }`,
      );
    }
    const deniedPermissions = state.events.filter(isDeniedPermissionEvent).length;
    if (deniedPermissions > 0) {
      attention.push(
        `${formatNumber(deniedPermissions)} denied ${
          deniedPermissions === 1 ? "permission" : "permissions"
        } in loaded events`,
      );
    }
    if (overview.findings > 0) {
      attention.push(
        `${formatNumber(overview.findings)} reported ${
          overview.findings === 1 ? "finding" : "findings"
        }`,
      );
    }
    const sessionOutcome = normalizeOutcome(
      state.selectedSessionDetail && state.selectedSessionDetail.outcome,
    );
    if (sessionOutcome === "interrupted") {
      attention.push("The session was reported as interrupted");
    }
    elements.needsAttentionSummary.replaceChildren(
      createStoryList(
        attention,
        "No explicit failure, denied permission, interruption, or explained finding is shown in the available overview.",
      ),
    );

    const work = [
      ["command", overview.commands],
      ["tool", overview.tools],
      ["file operation", overview.files],
      ["network indicator", overview.network],
      ["permission event", overview.permissions],
    ]
      .filter(([, count]) => count > 0)
      .map(
        ([label, count]) =>
          `${formatNumber(count)} ${label}${count === 1 ? "" : "s"}`,
      );
    elements.observedWorkSummary.replaceChildren(
      createStoryList(work, "No supported work metadata was reported."),
    );

    const highlights = selectSessionHighlights(state.events);
    const fragment = document.createDocumentFragment();
    highlights.forEach((event) => {
      const observation = isRecord(event.observation) ? event.observation : {};
      const descriptor = eventDescriptor(observation);
      const item = createElement("li", "session-highlight");
      const heading = createElement("div");
      heading.append(
        createElement("strong", "", descriptor.label),
        createElement(
          "time",
          "",
          formatClockTime(parseDate(event.occurred_at)) || "Time unavailable",
        ),
      );
      item.append(heading);
      if (descriptor.detail) {
        item.append(createElement("p", "", descriptor.detail));
      }
      fragment.append(item);
    });
    if (!highlights.length) {
      fragment.append(
        createElement(
          "li",
          "overview-empty",
          state.events.length
            ? "No launch-priority highlight was present in the loaded events."
            : "Timeline events are still loading.",
        ),
      );
    }
    elements.sessionHighlights.replaceChildren(fragment);
  }

  function createStoryList(values, emptyMessage) {
    if (!values.length) {
      return createElement("p", "overview-empty", emptyMessage);
    }
    const list = createElement("ul", "story-list");
    values.forEach((value) => list.append(createElement("li", "", value)));
    return list;
  }

  function selectSessionHighlights(events) {
    const cited = citedFindingMap();
    const candidates = events
      .map((event, index) => ({
        event,
        index,
        priority: sessionHighlightPriority(event, cited),
      }))
      .filter((candidate) => candidate.priority < 100);
    candidates.sort(
      (left, right) =>
        left.priority - right.priority || left.index - right.index,
    );
    return candidates
      .slice(0, 5)
      .sort((left, right) => left.index - right.index)
      .map((candidate) => candidate.event);
  }

  function sessionHighlightPriority(event, cited) {
    const observation = isRecord(event.observation) ? event.observation : {};
    const type = readText(observation.type).toLowerCase();
    const outcome = normalizeOutcome(observation.outcome);
    if (["failed", "interrupted"].includes(outcome)) return 0;
    if (isDeniedPermissionEvent(event)) return 1;
    if (cited.has(readText(event.event_id))) return 2;
    if (type === "command.result" && commandDisplayDetail(observation)) return 3;
    if (type === "command.exec") return 4;
    if (["file.write", "file.create", "file.delete"].includes(type)) return 5;
    if (type === "tool.call") return 6;
    return 100;
  }

  function isDeniedPermissionEvent(event) {
    const observation = isRecord(event.observation) ? event.observation : {};
    const type = readText(observation.type).toLowerCase();
    if (!type.startsWith("permission.")) return false;
    if (normalizeOutcome(observation.outcome) === "failed") return true;
    const details = isRecord(observation.details) ? observation.details : {};
    return [details.decision, details.approval_decision]
      .map((value) => readText(value).toLowerCase())
      .some((value) => ["denied", "rejected"].includes(value));
  }

  function buildOverview() {
    const derived = deriveOverview(state.events);
    const supplied = isRecord(state.selectedOverview)
      ? state.selectedOverview
      : {};
    return {
      commands: overviewCount(supplied, derived.commands, "commands", "command_count"),
      tools: overviewCount(
        supplied,
        derived.tools,
        "tools",
        "tool_count",
        "tool_calls",
      ),
      files: overviewFileCount(supplied, derived.files),
      network: overviewCount(
        supplied,
        derived.network,
        "network",
        "network_count",
        "network_indicators",
      ),
      permissions: overviewCount(
        supplied,
        derived.permissions,
        "permissions",
        "permission_count",
        "permission_events",
      ),
      failures: overviewCount(
        supplied,
        derived.failures,
        "explicit_failures",
        "failure_count",
        "failed_events",
        "explicit_failed_events",
      ),
      findings: overviewCount(
        supplied,
        state.findings.length,
        "findings",
        "finding_count",
      ),
      unreported: overviewCount(
        supplied,
        derived.unreported,
        "unreported_outcomes",
        "unreported_outcome_count",
        "unknown_outcomes",
        "source_unreported_outcomes",
      ),
      resources: overviewResources(supplied, derived.resources),
      resourcesTruncated: supplied.salient_resources_truncated === true,
      coverage: overviewCoverage(supplied, derived.coverage, "depths"),
      confidence: overviewCoverage(supplied, derived.confidence, "confidences"),
      partial: !state.selectedOverview,
    };
  }

  function deriveOverview(events) {
    const result = {
      commands: 0,
      tools: 0,
      files: 0,
      network: 0,
      permissions: 0,
      failures: 0,
      unreported: 0,
      resources: [],
      coverage: "",
      confidence: "",
    };
    const resources = new Set();
    const coverage = new Set();
    const confidence = new Set();
    events.forEach((event) => {
      const observation = isRecord(event.observation) ? event.observation : {};
      const type = readText(observation.type).toLocaleLowerCase();
      if (type.startsWith("command.")) result.commands += 1;
      if (type.startsWith("tool.")) result.tools += 1;
      if (type.startsWith("file.")) result.files += 1;
      if (type.startsWith("network.")) result.network += 1;
      if (type.startsWith("permission.")) result.permissions += 1;
      const outcome = normalizeOutcome(observation.outcome);
      if (outcome === "failed") result.failures += 1;
      if (outcome === "unknown") result.unreported += 1;
      const resource = isRecord(observation.resource)
        ? observation.resource
        : {};
      const resourceText = readText(resource.name) || readText(resource.kind);
      if (resourceText) resources.add(resourceText);
      const eventCoverage = isRecord(event.coverage) ? event.coverage : {};
      if (readText(eventCoverage.depth)) coverage.add(readText(eventCoverage.depth));
      if (readText(eventCoverage.confidence)) {
        confidence.add(readText(eventCoverage.confidence));
      }
    });
    result.resources = Array.from(resources);
    result.coverage = joinObservedValues(coverage);
    result.confidence = joinObservedValues(confidence);
    return result;
  }

  function overviewCount(overview, fallback, ...keys) {
    const sources = [overview, overview.counts, overview.activity_counts].filter(
      isRecord,
    );
    for (const source of sources) {
      for (const key of keys) {
        if (Number.isFinite(Number(source[key]))) {
          return toFiniteNumber(source[key]);
        }
      }
    }
    return fallback;
  }

  function overviewText(overview, fallback, ...keys) {
    for (const key of keys) {
      if (readText(overview[key])) return readText(overview[key]);
    }
    return fallback;
  }

  function overviewFileCount(overview, fallback) {
    const direct = overviewCount(overview, -1, "files", "file_count");
    if (direct >= 0) return direct;
    const counts = isRecord(overview.counts) ? overview.counts : overview;
    const keys = ["file_reads", "file_writes", "file_deletes"];
    if (keys.some((key) => Number.isFinite(Number(counts[key])))) {
      return keys.reduce((total, key) => total + toFiniteNumber(counts[key]), 0);
    }
    return fallback;
  }

  function overviewCoverage(overview, fallback, key) {
    const observed = isRecord(overview.observed_coverage)
      ? overview.observed_coverage
      : {};
    if (key === "depths") {
      if (Array.isArray(observed[key])) {
        return collectionDetailSummary(observed[key]);
      }
      return collectionDetailSummary(
        overviewText(overview, fallback, "coverage", "coverage_depth"),
      );
    }
    if (Array.isArray(observed[key])) {
      return observed[key].map(readableLabel).filter(Boolean).join(", ");
    }
    return overviewText(
      overview,
      fallback,
      key === "depths" ? "coverage" : "confidence",
      key === "depths" ? "coverage_depth" : "coverage_confidence",
    );
  }

  function collectionDetailSummary(value) {
    const rawValues = (Array.isArray(value) ? value : [value]).flatMap(
      (item) => readText(item).split(","),
    );
    const labels = rawValues
      .map((item) => collectionDetailLabel(item))
      .filter(Boolean);
    return Array.from(new Set(labels)).join(", ");
  }

  function collectionDetailLabel(value) {
    const raw = readText(value).trim().toLowerCase();
    if (!raw) return "";
    const normalized = raw.replace(/[\s-]+/g, "_");
    if (normalized === "artifact") return "Imported activity metadata";
    if (normalized === "hook") return "Live agent activity";
    if (normalized === "tool_call") return "Tool-call activity";
    if (
      normalized === "otel" ||
      normalized === "span" ||
      normalized === "otel/span"
    ) {
      return "Tracing activity";
    }
    return "Activity metadata";
  }

  function overviewResources(overview, fallback) {
    const candidates = [
      overview.salient_resources,
      overview.resources_touched,
      overview.resources,
      overview.resource_names,
    ];
    for (const candidate of candidates) {
      if (Array.isArray(candidate)) {
        return candidate
          .map((value) => {
            if (isRecord(value)) {
              return readText(value.name) || readText(value.kind);
            }
            return readText(value);
          })
          .filter(Boolean);
      }
    }
    return fallback;
  }

  function renderFindingList() {
    const fragment = document.createDocumentFragment();
    const presentation = consolidateSessionFindings(state.findings);
    const supplied = isRecord(state.selectedOverview)
      ? state.selectedOverview
      : {};
    const expectedCount = overviewCount(
      supplied,
      -1,
      "findings",
      "finding_count",
    );
    if (!state.findings.length) {
      if (state.findingsStatus === "loading") {
        fragment.append(
          createElement(
            "p",
            "overview-empty",
            "Loading session findings…",
          ),
        );
      } else if (state.findingsStatus === "error") {
        fragment.append(
          createElement(
            "p",
            "overview-empty",
            "Findings unavailable; cited timeline events may be incomplete.",
          ),
        );
      } else if (expectedCount > 0) {
        fragment.append(
          createElement(
            "p",
            "overview-empty",
            `The session overview reports ${expectedCount} ` +
              `${expectedCount === 1 ? "finding" : "findings"}, ` +
              "but the session findings response returned none.",
          ),
        );
      } else {
        fragment.append(
          createElement(
            "p",
            "overview-empty",
            "No findings were reported for this session.",
          ),
        );
      }
    } else {
      presentation.data.forEach((finding) => {
        const item = createElement("article", "finding-item");
        const severity = readText(finding.severity) || "reported";
        const display = finding._display;
        const heading = createElement("div");
        const badge = createElement("span", "finding-badge", readableLabel(severity));
        badge.dataset.tone = severityTone(severity);
        heading.append(
          badge,
          createElement(
            "strong",
            "",
            display.title,
          ),
        );
        item.append(heading);
        const evidenceCount = findingEventIDs(finding).length;
        item.append(
          createElement(
            "small",
            "",
            `${evidenceCount} cited ${evidenceCount === 1 ? "event" : "events"}${
              finding._reportCount > 1
                ? ` · ${formatNumber(finding._reportCount)} reports combined`
                : ""
            }`,
          ),
        );
        item.append(
          createElement("p", "", display.explanation),
          createElement("p", "overview-caveat", display.caveat),
          createElement("p", "issue-next-action", display.action),
        );
        fragment.append(item);
      });
      if (presentation.hiddenCount > 0) {
        fragment.append(
          createElement(
            "small",
            "overview-caveat",
            `${formatNumber(presentation.hiddenCount)} additional imported ${
              presentation.hiddenCount === 1 ? "finding is" : "findings are"
            } hidden because Belay does not yet have a clear explanation for ${
              presentation.hiddenCount === 1 ? "it" : "them"
            }.`,
          ),
        );
      }
      if (state.findingsStatus === "loading" || state.findingsMayHaveMore) {
        fragment.append(
          createElement(
            "small",
            "overview-caveat",
            state.findingsStatus === "loading"
              ? "Loading another findings page…"
              : "More findings are available. Use Load more to view them.",
          ),
        );
      } else if (state.findingsStatus === "error") {
        fragment.append(
          createElement(
            "small",
            "overview-caveat",
            "Some session findings could not be loaded.",
          ),
        );
      } else if (expectedCount >= 0 && expectedCount !== state.findings.length) {
        fragment.append(
          createElement(
            "small",
            "overview-caveat",
            `Loaded ${state.findings.length} of ${expectedCount} findings ` +
              "reported by the session overview.",
          ),
        );
      }
    }
    elements.findingList.replaceChildren(fragment);
    renderFindingPagination();
  }

  function renderFindingPagination() {
    elements.findingsPagination.hidden = !state.findingHasMore;
    elements.findingsLoadMore.disabled =
      state.findingsStatus === "loading";
    elements.findingsPageStatus.textContent = state.findingHasMore
      ? `Loaded ${state.findings.length} findings; more are available.`
      : `Loaded ${state.findings.length} returned findings.`;
  }

  function findingDisplayCatalog(finding) {
    const sourceSignalCode = safeSourceSignalCode(finding && finding.rule_id);
    const display = sourceSignalCatalog[`numbat/${sourceSignalCode}`];
    return display
      ? {
          ...display,
          key: `numbat/${sourceSignalCode}`,
          known: true,
        }
      : { key: "", known: false };
  }

  function consolidateSessionFindings(findings) {
    const groups = new Map();
    let hiddenCount = 0;
    findings.forEach((finding) => {
      const display = findingDisplayCatalog(finding);
      if (!display.known) {
        hiddenCount += 1;
        return;
      }
      let group = groups.get(display.key);
      if (!group) {
        group = {
          ...finding,
          cited_event_ids: [],
          _display: display,
          _reportCount: 0,
        };
        groups.set(display.key, group);
      }
      group._reportCount += 1;
      group.cited_event_ids = Array.from(
        new Set(group.cited_event_ids.concat(findingEventIDs(finding))),
      );
    });
    return { data: Array.from(groups.values()), hiddenCount };
  }

  function renderEvents() {
    const cited = citedFindingMap();
    const signalEvents = state.events.filter((event) => isSignalEvent(event, cited));
    const visible = state.showAllEvents ? state.events : signalEvents;
    const hiddenCount = state.events.length - signalEvents.length;
    const fragment = document.createDocumentFragment();
    visible.forEach((event) => {
      fragment.append(createEventRow(event, cited.get(readText(event.event_id)) || []));
    });
    elements.eventList.replaceChildren(fragment);
    elements.eventsEmpty.hidden =
      visible.length !== 0 || !elements.timelineLoading.hidden;
    elements.timelineScope.textContent = state.showAllEvents
      ? `Showing all ${visible.length} loaded events.`
      : hiddenCount > 0
        ? `Showing ${visible.length} notable loaded events; ${hiddenCount} lower-priority lifecycle events hidden.`
        : `Showing ${visible.length} notable loaded events.`;
    elements.allEventsToggle.disabled = state.events.length === 0;
  }

  function renderEventPagination() {
    const atMaximum =
      !state.eventNextCursor && state.eventLimit >= pageLimits.events.maximum;
    elements.eventsPagination.hidden = !state.eventHasMore;
    elements.eventsLoadMore.hidden = !state.eventHasMore || atMaximum;
    if (state.eventHasMore && atMaximum) {
      elements.eventsPageStatus.textContent =
        `Loaded ${state.events.length} of ${state.selectedEventTotal} events. ` +
        "Additional events exist beyond this browser bound.";
    } else if (state.eventHasMore) {
      elements.eventsPageStatus.textContent =
        `Loaded ${state.events.length} of ${state.selectedEventTotal} events.`;
    } else {
      elements.eventsPageStatus.textContent =
        `Loaded ${state.events.length} events returned for this session.`;
    }
  }

  function createEventRow(event, findings) {
    const observation = isRecord(event.observation) ? event.observation : {};
    const outcome = normalizeOutcome(observation.outcome);
    const occurredAt = parseDate(event.occurred_at);
    const descriptor = eventDescriptor(observation);
    const row = createElement(
      "article",
      findings.length ? "event-row has-finding" : "event-row",
    );
    const time = createElement(
      "time",
      "event-time",
      occurredAt ? formatClockTime(occurredAt) : "—",
    );
    if (occurredAt) {
      time.dateTime = occurredAt.toISOString();
      time.title = formatFullDate(occurredAt);
    }
    const rail = createElement("div", "event-rail");
    const node = createElement("span", "event-node");
    node.dataset.tone = explicitOutcomes.has(outcome) ? outcome : "unknown";
    node.setAttribute("aria-hidden", "true");
    rail.append(node);
    const card = createElement("div", "event-card");
    const heading = createElement("div", "event-heading");
    heading.append(createElement("strong", "", descriptor.label));
    if (explicitOutcomes.has(outcome)) {
      const badge = createElement("span", "status-badge", explicitOutcomeLabel(outcome));
      badge.dataset.tone = outcome;
      heading.append(badge);
    }
    if (findings.length) {
      heading.append(
        createElement(
          "span",
          "finding-badge",
          `${findings.length} ${findings.length === 1 ? "finding" : "findings"}`,
        ),
      );
    }
    card.append(heading);
    if (descriptor.detail) {
      card.append(createElement("p", "event-summary", descriptor.detail));
    }
    if (outcome === "unknown") {
      card.append(
        createElement(
          "p",
          "outcome-unreported",
          "Outcome · Not reported",
        ),
      );
    }
    if (findings.length) {
      const findingRefs = createElement("div", "event-findings");
      findings.forEach((finding) => {
        const display = findingDisplayCatalog(finding);
        findingRefs.append(
          createElement(
            "span",
            "",
            `Cited by ${display.title}`,
          ),
        );
      });
      card.append(findingRefs);
    }
    card.append(createEvidenceDetails(event));
    row.append(time, rail, card);
    return row;
  }

  function createEvidenceDetails(event) {
    const observation = isRecord(event.observation) ? event.observation : {};
    const coverage = isRecord(event.coverage) ? event.coverage : {};
    const redaction = isRecord(event.redaction) ? event.redaction : {};
    const historical = isRecord(event.historical) ? event.historical : {};
    const resource = isRecord(observation.resource) ? observation.resource : {};
    const descriptor = eventDescriptor(observation);
    const details = createElement("details", "evidence-details");
    details.append(createElement("summary", "", "Evidence"));
    const list = createElement("dl");
    appendEvidence(list, "Event ID", event.event_id);
    appendEvidence(list, "Occurred", formatFullDate(parseDate(event.occurred_at)));
    appendEvidence(list, "Action", descriptor.label);
    appendEvidence(list, "Event type", observation.type);
    appendEvidence(list, "Outcome", observation.outcome);
    appendEvidence(list, "Minimized summary", observation.summary);
    appendEvidence(list, "Resource kind", resource.kind);
    appendEvidence(list, "Resource name", resource.name);
    appendEvidence(
      list,
      "Collection detail",
      collectionDetailLabel(coverage.depth),
    );
    appendEvidence(list, "Confidence", coverage.confidence);
    if (historical.is_historical) {
      appendEvidence(list, "History source", "Imported history");
    } else {
      appendEvidence(list, "Collection", "Live");
    }
    if (observation.exit_code !== undefined && observation.exit_code !== null) {
      appendEvidence(list, "Exit code", observation.exit_code);
    }
    if (observation.duration_ms !== undefined && observation.duration_ms !== null) {
      appendEvidence(list, "Duration (ms)", observation.duration_ms);
    }
    appendEvidence(list, "Fields removed", redaction.fields_removed);
    appendEvidence(
      list,
      "Detected secrets removed",
      redaction.secrets_removed,
    );
    details.append(list);
    return details;
  }

  function appendEvidence(list, label, value) {
    const text = readText(value);
    if (!text) return;
    const row = createElement("div");
    row.append(createElement("dt", "", label), createElement("dd", "", text));
    list.append(row);
  }

  function eventDescriptor(observation, evidenceContext = "") {
    const type = readText(observation.type).toLocaleLowerCase();
    const resource = isRecord(observation.resource) ? observation.resource : {};
    const resourceName = readText(resource.name);
    const summary = readText(observation.summary);
    const labels = {
      "session.start": "Session started",
      "session.end": "Session ended",
      "command.exec": "Ran command",
      "command.result": "Command result",
      "tool.call": "Called tool",
      "tool.result": "Tool returned",
      "file.read": "Read file",
      "file.write": "Changed file",
      "file.create": "Created file",
      "file.delete": "Deleted file",
      "permission.request": "Requested permission",
      "permission.decision": "Permission decided",
      "config.agent": "Agent configuration observed",
      "network.indicator": "Observed network target",
      "prompt.user": "User input recorded — content not retained",
      "message.assistant": "Assistant response recorded — content not retained",
      "reasoning.start": "Reasoning started — content not retained",
      "reasoning.end": "Reasoning ended — content not retained",
    };
    const reducedApprovalEvidence =
      ["config.agent", "session.start"].includes(type) &&
      evidenceContext === "mapped-guardrail";
    const label = reducedApprovalEvidence
      ? "Fewer approval prompts enabled"
      : labels[type] || readableLabel(observation.action, "Activity");
    const detail =
      reducedApprovalEvidence
        ? "Belay recorded a setting that lets actions already permitted by the agent run without asking for approval each time."
        : type === "config.agent"
          ? resourceName || "Agent configuration metadata was reported."
        : type.startsWith("command.")
          ? commandDisplayDetail(observation)
          : resourceName || summary;
    return { label, detail };
  }

  function commandDisplayDetail(observation) {
    const resource = isRecord(observation && observation.resource)
      ? observation.resource
      : {};
    const candidates = [
      readText(observation && observation.summary),
      readText(resource.name),
    ];
    const generic = new Set([
      "command completed",
      "command complete",
      "command finished",
      "command result",
      "completed",
      "finished",
    ]);
    return (
      candidates.find(
        (candidate) =>
          candidate && !generic.has(candidate.trim().toLocaleLowerCase()),
      ) || ""
    );
  }

  function isSignalEvent(event, cited) {
    const observation = isRecord(event.observation) ? event.observation : {};
    const type = readText(observation.type).toLocaleLowerCase();
    const eventID = readText(event.event_id);
    const outcome = normalizeOutcome(observation.outcome);
    if (cited.has(eventID) || explicitOutcomes.has(outcome)) return true;
    if (type === "command.result") {
      return Boolean(commandDisplayDetail(observation));
    }
    return (
      ["session.", "command.", "tool.", "file.", "permission.", "network."].some(
        (prefix) => type.startsWith(prefix),
      )
    );
  }

  function citedFindingMap() {
    const result = new Map();
    consolidateSessionFindings(state.findings).data.forEach((finding) => {
      findingEventIDs(finding).forEach((eventID) => {
        const values = result.get(eventID) || [];
        values.push(finding);
        result.set(eventID, values);
      });
    });
    return result;
  }

  function findingEventIDs(finding) {
    const values = Array.isArray(finding.cited_event_ids)
      ? finding.cited_event_ids
      : Array.isArray(finding.event_ids)
        ? finding.event_ids
        : [];
    return values.map(readText).filter(Boolean);
  }

  function sessionOutcomeBadge(outcome) {
    const badge = createElement("span", "status-badge");
    setSessionOutcomeBadge(badge, outcome);
    return badge;
  }

  function setSessionOutcomeBadge(badge, outcome) {
    badge.textContent = sessionOutcomeLabel(outcome);
    badge.dataset.tone = outcome;
    badge.title = sessionOutcomeExplanation(outcome);
  }

  function sessionOutcomeLabel(outcome) {
    if (outcome === "unknown") return "Outcome not reported";
    if (outcome === "incomplete") return "Outcome not reported";
    return explicitOutcomeLabel(outcome);
  }

  function sessionOutcomeExplanation(outcome) {
    if (outcome === "unknown") {
      return "An outcome was not reported.";
    }
    if (outcome === "incomplete") {
      return "Belay did not receive an outcome for this session.";
    }
    return `The session was reported as ${explicitOutcomeLabel(outcome).toLocaleLowerCase()}.`;
  }

  function explicitOutcomeLabel(outcome) {
    return readableLabel(outcome, "Outcome not reported");
  }

  function closeTimeline() {
    const returnView = state.sessionReturnView;
    const returnFocus = state.sessionReturnFocus;
    state.selectedSessionID = "";
    state.selectedEventTotal = 0;
    state.selectedSessionDetail = null;
    state.selectedOverview = null;
    state.selectedDiagnosis = null;
    state.events = [];
    state.findings = [];
    state.findingNextCursor = "";
    state.findingHasMore = false;
    state.findingsMayHaveMore = false;
    state.findingsStatus = "idle";
    state.overviewStatus = "idle";
    state.eventNextCursor = "";
    state.eventHasMore = false;
    document.body.classList.remove("is-timeline-open");
    elements.timelineView.hidden = true;
    elements.timelineLoading.hidden = true;
    elements.welcomeState.hidden = false;
    elements.eventsPagination.hidden = true;
    elements.findingsPagination.hidden = true;
    renderSessionDiagnosis();
    renderSessions();
    if (returnView === "attention") {
      setActiveView("attention", false);
      restoreLogicalFocus(
        returnFocus,
        state.selectedIssueID
          ? elements.issueDetailHeading
          : elements.navAttention,
      );
    } else if (returnView === "brief") {
      setActiveView("brief", false);
      restoreLogicalFocus(returnFocus, elements.briefHeading);
      state.briefSelectionID = "";
    } else {
      applyPaneAccessibility();
      restoreLogicalFocus(returnFocus, elements.navSessions);
    }
    state.sessionReturnFocus = null;
    state.sessionReturnView = "";
  }

  function retryLastAction() {
    if (state.activeView === "brief") {
      void loadDeveloperBrief();
      return;
    }
    if (state.activeView === "habits") {
      void loadUserInsights();
      return;
    }
    if (state.activeView === "attention") {
      refreshAttention(false);
      return;
    }
    if (state.lastAction === "timeline" && state.selectedSessionID) {
      selectSession(state.selectedSessionID);
      return;
    }
    refreshAll(false);
  }

  class LocalAPIError extends Error {
    constructor(message, status, problemType) {
      super(message);
      this.name = "LocalAPIError";
      this.status = status;
      this.problemType = problemType;
    }
  }

  class LocalMutationTimeoutError extends Error {
    constructor() {
      super(
        "The Local mutation deadline elapsed before a response was confirmed.",
      );
      this.name = "LocalMutationTimeoutError";
    }
  }

  async function apiGet(path, signal) {
    return apiRequest(path, "GET", null, null, null, signal);
  }

  async function apiPost(path, body) {
    return apiRequest(path, "POST", body, null, null, null);
  }

  async function apiMutation(path, body, idempotencyKey, intent) {
    if (!isCanonicalUUIDv4(idempotencyKey)) {
      throw new Error("A canonical UUIDv4 retry key is required.");
    }
    if (
      ![
        "record-fix-attempt.v1",
        "retract-fix-attempt.v1",
        "propose-cost-issue-fix.v1",
        "record-cost-issue-fix.v1",
      ].includes(intent)
    ) {
      throw new Error("The Local write intent is invalid.");
    }
    const controller = new AbortController();
    let deadlineReached = false;
    const deadline = globalThis.setTimeout(() => {
      deadlineReached = true;
      controller.abort();
    }, mutationRequestDeadlineMilliseconds);
    try {
      return await apiRequest(
        path,
        "POST",
        body,
        idempotencyKey,
        intent,
        controller.signal,
      );
    } catch (error) {
      if (deadlineReached || controller.signal.aborted) {
        throw new LocalMutationTimeoutError();
      }
      throw error;
    } finally {
      globalThis.clearTimeout(deadline);
    }
  }

  async function apiRequest(
    path,
    method,
    body,
    idempotencyKey,
    intent,
    signal,
  ) {
    if (!state.token) {
      throw new Error(
        "This page was not opened from a valid Belay Local link. Reopen it using the URL printed by belay local.",
      );
    }
    const headers = {
      Accept: "application/json",
      Authorization: `Bearer ${state.token}`,
    };
    const request = {
      method,
      cache: "no-store",
      credentials: "omit",
      headers,
    };
    if (signal) request.signal = signal;
    if (method === "POST") {
      headers["Content-Type"] = "application/json";
      if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;
      if (intent) headers["X-Belay-Intent"] = intent;
      request.body = JSON.stringify(body);
    }
    const response = await fetch(`${state.apiBase}${path}`, {
      ...request,
    });
    throwIfMutationAborted(signal);
    const responseBody = await readResponseBody(response);
    throwIfMutationAborted(signal);
    if (!response.ok) {
      const detail =
        isRecord(responseBody) && readText(responseBody.detail)
          ? readText(responseBody.detail)
          : `Local API returned ${response.status}.`;
      throw new LocalAPIError(
        detail,
        response.status,
        isRecord(responseBody) ? readText(responseBody.type) : "",
      );
    }
    if (!isRecord(responseBody)) {
      throw new Error("Local API returned an invalid response.");
    }
    return responseBody;
  }

  function throwIfMutationAborted(signal) {
    if (signal && signal.aborted) {
      throw new LocalMutationTimeoutError();
    }
  }

  async function readResponseBody(response) {
    const contentType = response.headers.get("content-type") || "";
    if (!contentType.toLocaleLowerCase().includes("json")) return null;
    try {
      return await response.json();
    } catch {
      return null;
    }
  }

  async function copyText(value, button) {
    if (!value || !globalThis.navigator.clipboard) return;
    try {
      await globalThis.navigator.clipboard.writeText(value);
      const prior = button.textContent;
      button.textContent = "Copied";
      globalThis.setTimeout(() => {
        button.textContent = prior;
      }, 1200);
    } catch {
      button.title = "Clipboard access was unavailable.";
    }
  }

  async function loadUpdateStatus(attempt = 0) {
    try {
      const response = await apiGet("/v1/update");
      if (
        !isRecord(response) ||
        readText(response.schema_version) !== updateSchemaVersion
      ) {
        return;
      }
      state.updateStatus = response;
      renderUpdateStatus();
      if (response.checking && attempt < 3) {
        globalThis.clearTimeout(state.updatePollTimer);
        state.updatePollTimer = globalThis.setTimeout(() => {
          void loadUpdateStatus(attempt + 1);
        }, 2_000);
      }
    } catch {
      state.updateStatus = null;
      renderUpdateStatus();
    }
  }

  function renderUpdateStatus() {
    const status = state.updateStatus;
    const available = Boolean(status && status.update_available);
    elements.updateNotice.hidden = !available;
    if (!available) return;
    const version = readText(status.latest_version);
    elements.updateNoticeLabel.textContent = version
      ? `Belay ${version} available`
      : "Update available";
  }

  function openUpdateDialog() {
    const status = state.updateStatus;
    if (!status || !status.update_available) return;
    state.dialogReturnFocus = document.activeElement;
    state.activeModal = "update";
    elements.updateCurrentVersion.textContent =
      readText(status.current_version) || "Unknown";
    elements.updateLatestVersion.textContent =
      readText(status.latest_version) || "New release";
    const name = readText(status.release_name);
    const published = formatUpdateDate(readText(status.published_at));
    elements.updateReleaseNote.textContent = [name, published]
      .filter(Boolean)
      .join(" · ");
    elements.updateReleaseNote.hidden = !elements.updateReleaseNote.textContent;
    elements.updateAlert.hidden = true;
    elements.updateAlert.textContent = "";
    elements.updateRemind.disabled = false;
    elements.updateSkip.disabled = false;
    openModalLayer(
      elements.updateModalLayer,
      elements.updateDialog,
      elements.updateCopyCommand,
    );
  }

  function closeUpdateDialog(restoreFocus) {
    closeModalLayer(
      "update",
      elements.updateModalLayer,
      restoreFocus,
    );
  }

  async function submitUpdateAction(action) {
    const status = state.updateStatus;
    const version = readText(status && status.latest_version);
    if (!version) return;
    elements.updateRemind.disabled = true;
    elements.updateSkip.disabled = true;
    elements.updateAlert.hidden = true;
    try {
      const response = await apiPost("/v1/update", { action, version });
      state.updateStatus = response;
      renderUpdateStatus();
      closeUpdateDialog(true);
    } catch {
      elements.updateAlert.textContent =
        "Belay could not save that choice. Try again.";
      elements.updateAlert.hidden = false;
      elements.updateRemind.disabled = false;
      elements.updateSkip.disabled = false;
    }
  }

  function formatUpdateDate(value) {
    if (!value) return "";
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return "";
    return parsed.toLocaleDateString(undefined, {
      month: "short",
      day: "numeric",
      year: "numeric",
    });
  }

  function agentIssueCommand(agent, issueID) {
    const selector = readText(issueID);
    if (!selector) return "";
    return agent === "codex"
      ? `$belay start --issue ${selector}`
      : `/belay start --issue ${selector}`;
  }

  function resolveToken(bootstrapConfig) {
    const fromConfig = readText(
      bootstrapConfig.token || bootstrapConfig.authToken,
    );
    const historyState = isRecord(globalThis.history.state)
      ? globalThis.history.state
      : {};
    const fromHistory = readText(historyState.belayToken);
    const fragment = globalThis.location.hash.slice(1);
    let fromFragment = "";
    if (fragment) {
      const parameters = new URLSearchParams(fragment);
      fromFragment = readText(
        parameters.get("token") || parameters.get("access_token"),
      );
      if (!fromFragment && !fragment.includes("=")) {
        try {
          fromFragment = decodeURIComponent(fragment);
        } catch {
          fromFragment = fragment;
        }
      }
      globalThis.history.replaceState(
        { ...historyState, belayToken: fromFragment || fromConfig || fromHistory },
        document.title,
        `${globalThis.location.pathname}${globalThis.location.search}`,
      );
    }
    return fromFragment || fromConfig || fromHistory;
  }

  function normalizeApiBase(value) {
    const base = readText(value).trim();
    if (!base) return "";
    try {
      const url = new URL(base, globalThis.location.origin);
      if (url.origin !== globalThis.location.origin) return "";
      return url.pathname.replace(/\/+$/, "");
    } catch {
      return "";
    }
  }

  function showError(title, error) {
    elements.errorTitle.textContent = experiencePageText(title);
    elements.errorDetail.textContent = customerErrorMessage(
      error,
      "Belay Local could not complete this request. Try again.",
    );
    elements.errorBanner.hidden = false;
  }

  function customerErrorMessage(error, fallback) {
    const message = error instanceof Error ? readText(error.message) : "";
    if (!message) return fallback;
    if (
      /(api|schema|cursor|snapshot|projection|uuid|idempotency|mutation|listener)/i.test(
        message,
      )
    ) {
      return fallback;
    }
    return message;
  }

  function hideError() {
    elements.errorBanner.hidden = true;
    elements.errorDetail.textContent = "";
  }

  async function loadReportHabits() {
    const generation = ++state.reportHabitsRequestGeneration;
    if (state.reportHabitsStatus !== "ready") {
      state.reportHabitsStatus = "loading";
      renderReportHabits();
    }
    try {
      const response = await apiGet("/v1/user-insights?limit=3");
      if (generation !== state.reportHabitsRequestGeneration) return false;
      state.reportHabits = requireUserInsights(response);
      state.reportHabitsStatus = "ready";
      renderReportHabits();
      return true;
    } catch {
      if (generation !== state.reportHabitsRequestGeneration) return false;
      state.reportHabits = null;
      state.reportHabitsStatus = "error";
      renderReportHabits();
      return false;
    }
  }

  function renderReportHabits() {
    const insights = state.reportHabits;
    const loading = state.reportHabitsStatus === "loading";
    elements.reportHabitsList.replaceChildren();
    elements.reportHabitSummary.hidden = true;
    if (!insights) {
      elements.reportHabitsEmpty.hidden = true;
      elements.reportHabitsStatus.textContent = loading
        ? "Loading your latest debriefs…"
        : state.reportHabitsStatus === "error"
          ? "Debriefs are unavailable right now. Open Habits to retry."
          : "";
      return;
    }
    const sessions = insights.sessions.slice(0, 3);
    elements.reportHabitsEmpty.hidden = sessions.length > 0;
    const ready = sessions.filter((session) => session.debrief);
    const pending = sessions.length - ready.length;
    elements.reportHabitsStatus.textContent =
      sessions.length === 0
        ? ""
        : pending === 0
          ? `${sessions.length} of your last ${sessions.length} sessions debriefed by your ${habitsHarnessLabel(insights.harness.name)}.`
          : insights.harness.available
            ? `${ready.length} of ${sessions.length} debriefed. Belay is writing the ${pending === 1 ? "other one" : `other ${pending}`} with your ${habitsHarnessLabel(insights.harness.name)} in the background.`
            : "Install Claude Code or Codex on this machine and Belay will debrief these sessions automatically.";
    const cards = document.createDocumentFragment();
    sessions.forEach((session) => {
      cards.appendChild(renderReportHabitCard(session, insights.harness));
    });
    elements.reportHabitsList.replaceChildren(cards);
    renderReportHabitSummary(ready);
  }

  function renderReportHabitCard(session, harness) {
    const card = createElement("article", "report-habit-card");
    card.dataset.sessionKey = readText(session.session_key);
    const header = createElement("header", "report-habit-header");
    const identity = createElement("div");
    identity.appendChild(createElement("h3", "", readText(session.project) || "Untitled project"));
    const finished = parseDate(session.ended_at || session.started_at);
    identity.appendChild(
      createElement(
        "p",
        "habits-card-meta",
        [
          displayHarness(session.agent),
          finished && finished.getFullYear() > 2000 ? formatRelativeTime(finished) : "",
          habitsDuration(session.duration_ms),
          habitsDollars(session.cost_usd),
        ]
          .filter(Boolean)
          .join(" · "),
      ),
    );
    header.appendChild(identity);
    header.appendChild(
      createElement(
        "span",
        `habits-outcome habits-outcome-${habitsOutcomeClass(session.outcome)}`,
        habitsOutcomeLabel(session.outcome),
      ),
    );
    card.appendChild(header);
    const record = session.debrief;
    if (!record) {
      const pending = createElement("div", "report-habit-pending");
      if (harness.available) {
        pending.appendChild(createElement("span", "spinner"));
        pending.appendChild(
          createElement("p", "", `Being written by your ${habitsHarnessLabel(harness.name)}…`),
        );
      } else {
        pending.appendChild(createElement("p", "", "No harness installed to write this debrief."));
      }
      card.appendChild(pending);
      return card;
    }
    const debrief = record.debrief;
    if (debrief.phases.length) {
      const bar = createElement("div", "habits-phase-bar report-habit-bar");
      const total = debrief.phases.reduce((sum, phase) => {
        const span = Number(phase.to_minute) - Number(phase.from_minute);
        return sum + (Number.isFinite(span) && span > 0 ? span : 0);
      }, 0);
      debrief.phases.forEach((phase) => {
        const span = Number(phase.to_minute) - Number(phase.from_minute);
        const segment = createElement(
          "span",
          `habits-phase-segment habits-phase-${habitsPhaseVerdict(phase.verdict)}`,
        );
        segment.style.flexGrow = String(total > 0 ? Math.max(span > 0 ? span : 0, total * 0.03) : 1);
        segment.title = `${readText(phase.label)} · ${habitsPhaseLabel(habitsPhaseVerdict(phase.verdict))}`;
        bar.appendChild(segment);
      });
      card.appendChild(bar);
    }
    card.appendChild(createElement("p", "report-habit-headline", readText(debrief.headline)));
    const lead = debrief.insights[0];
    if (lead) {
      const insight = createElement("div", "report-habit-insight");
      const title = createElement("p", "report-habit-insight-title");
      title.appendChild(createElement("span", "habits-kind", habitsInsightLabel(lead.kind)));
      title.appendChild(document.createTextNode(readText(lead.title)));
      insight.appendChild(title);
      const message = readText(lead.say_this_instead);
      if (message) {
        insight.appendChild(createElement("p", "report-habit-say", message));
      }
      card.appendChild(insight);
    }
    const foot = createElement("footer", "report-habit-foot");
    foot.appendChild(
      createElement(
        "span",
        "",
        `${debrief.insights.length} ${debrief.insights.length === 1 ? "insight" : "insights"}` +
          (debrief.keep_doing.length ? ` · ${debrief.keep_doing.length} kept` : ""),
      ),
    );
    const open = createElement("button", "text-button", "Read the full debrief");
    open.type = "button";
    open.addEventListener("click", () => {
      setActiveView("habits", true);
      if (state.habitsStatus === "idle") void loadUserInsights();
    });
    foot.appendChild(open);
    card.appendChild(foot);
    return card;
  }

  function renderReportHabitSummary(readySessions) {
    const counts = new Map();
    let attentionPhases = 0;
    let attentionMinutes = 0;
    readySessions.forEach((session) => {
      const debrief = session.debrief.debrief;
      const kinds = new Set(debrief.insights.map((insight) => habitsInsightKind(insight.kind)));
      kinds.forEach((kind) => counts.set(kind, (counts.get(kind) || 0) + 1));
      debrief.phases.forEach((phase) => {
        const verdict = habitsPhaseVerdict(phase.verdict);
        const span = Number(phase.to_minute) - Number(phase.from_minute);
        if (!Number.isFinite(span) || span <= 0) return;
        if (verdict === "wasted" || verdict === "partly-wasted") {
          attentionPhases += 1;
          attentionMinutes += span;
        }
      });
    });
    if (readySessions.length === 0) {
      elements.reportHabitSummary.hidden = true;
      return;
    }
    const fragment = document.createDocumentFragment();
    const wasted = createElement("div", "report-habit-stat");
    wasted.appendChild(
      createElement("strong", "", `${attentionPhases} ${attentionPhases === 1 ? "phase" : "phases"}`),
    );
    wasted.appendChild(
      createElement(
        "span",
        "",
        `need attention · ${habitsWallClockSpan(attentionMinutes)} wall-clock span flagged`,
      ),
    );
    wasted.title =
      "Wall-clock span is the sum of phases marked wasted or partly wasted. It can include idle gaps and is not active time or guaranteed savings.";
    fragment.appendChild(wasted);
    Array.from(counts.entries())
      .sort((left, right) => right[1] - left[1])
      .slice(0, 3)
      .forEach(([kind, count]) => {
        const chip = createElement("div", "report-habit-stat");
        chip.appendChild(createElement("strong", "", `${count} of ${readySessions.length}`));
        chip.appendChild(
          createElement("span", "", `${readySessions.length === 1 ? "session" : "sessions"} raised a ${habitsInsightLabel(kind).toLowerCase()} habit`),
        );
        fragment.appendChild(chip);
      });
    elements.reportHabitSummary.replaceChildren(fragment);
    elements.reportHabitSummary.hidden = false;
  }

  const habitsErrorFallback =
    "Belay could not prepare your debriefs. Report and Sessions remain available.";
  const habitsGenerating = new Set();
  const habitsCardErrors = new Map();
  let habitsBatchRunning = false;

  async function loadUserInsights() {
    const generation = ++state.habitsRequestGeneration;
    state.habitsStatus = "loading";
    state.habitsError = "";
    renderUserInsights();
    try {
      const response = await apiGet("/v1/user-insights");
      if (generation !== state.habitsRequestGeneration) return false;
      state.userInsights = requireUserInsights(response);
      state.habitsStatus = "ready";
      renderUserInsights();
      return true;
    } catch (error) {
      if (generation !== state.habitsRequestGeneration) return false;
      state.userInsights = null;
      state.habitsStatus = "error";
      state.habitsError = customerErrorMessage(error, habitsErrorFallback);
      renderUserInsights();
      return false;
    }
  }

  function requireUserInsights(response) {
    if (
      !isRecord(response) ||
      readText(response.projection_version) !== "belay.user-insights.v1" ||
      !Array.isArray(response.sessions) ||
      !isRecord(response.coverage)
    ) {
      throw new Error("Local API returned invalid habits.");
    }
    const sessions = response.sessions
      .slice(0, 25)
      .filter(isRecord)
      .map((session) => ({
        ...session,
        debrief: requireHabitsDebrief(session.debrief),
        debrief_status: readText(session.debrief_status) || "missing",
      }));
    const limitations = Array.isArray(response.limitations)
      ? response.limitations.filter((note) => typeof note === "string" && note)
      : [];
    const harness = isRecord(response.harness)
      ? { available: response.harness.available === true, name: readText(response.harness.name) }
      : { available: false, name: "" };
    return { ...response, sessions, limitations, harness };
  }

  function requireHabitsDebrief(value) {
    if (!isRecord(value) || !isRecord(value.debrief)) return null;
    const debrief = value.debrief;
    if (!readText(debrief.headline)) return null;
    return {
      ...value,
      debrief: {
        ...debrief,
        phases: Array.isArray(debrief.phases) ? debrief.phases.filter(isRecord).slice(0, 6) : [],
        insights: Array.isArray(debrief.insights) ? debrief.insights.filter(isRecord).slice(0, 5) : [],
        keep_doing: Array.isArray(debrief.keep_doing) ? debrief.keep_doing.filter(isRecord).slice(0, 3) : [],
        prompt_length_read: isRecord(debrief.prompt_length_read) ? debrief.prompt_length_read : null,
      },
      sanitization: Array.isArray(value.sanitization)
        ? value.sanitization.filter((note) => typeof note === "string")
        : [],
    };
  }

  function habitsHarnessLabel(name) {
    const value = readText(name).toLowerCase();
    if (value === "claude" || value === "claude-code") return "Claude Code";
    if (value === "codex") return "Codex";
    return "your agent";
  }

  function habitsSelectedSession(sessions) {
    const key = readText(state.habitsSelectedKey);
    const match = sessions.find((session) => readText(session.session_key) === key);
    return match || sessions[0] || null;
  }

  function selectHabitsSession(key, moveFocus) {
    state.habitsSelectedKey = readText(key);
    state.habitsTab = "change";
    state.habitsOpenInsight = 0;
    renderUserInsights();
    if (moveFocus) {
      const title = document.querySelector("#habits-detail-title");
      if (title) focusCurrentElement(title);
    }
  }

  function renderUserInsights() {
    const loading = state.habitsStatus === "loading";
    const failed = state.habitsStatus === "error";
    const insights = state.userInsights;
    elements.habitsLoading.hidden = !loading;
    elements.habitsError.hidden = !failed;
    elements.habitsContent.hidden = !insights || loading || failed;
    elements.habitsErrorDetail.textContent =
      state.habitsError || habitsErrorFallback;
    if (!insights) {
      elements.habitsStatus.textContent = "";
      elements.habitsList.replaceChildren();
      elements.habitsDebriefAll.hidden = true;
      elements.habitsOlder.textContent = "";
      return;
    }
    const sessions = insights.sessions;
    const selected = habitsSelectedSession(sessions);
    state.habitsSelectedKey = selected ? readText(selected.session_key) : "";
    const missing = sessions.filter(
      (session) => !session.debrief || session.debrief_status !== "ready",
    );
    elements.habitsWindow.textContent = insights.harness.available
      ? `One debrief per session, written by your ${habitsHarnessLabel(insights.harness.name)}.`
      : "Install Claude Code or Codex on this machine and Belay will write a debrief for each session.";
    elements.habitsStatus.textContent = habitsCoverageText(insights.coverage);
    elements.habitsEmpty.hidden = sessions.length > 0;
    const rail = document.createDocumentFragment();
    sessions.forEach((session) => {
      rail.appendChild(
        renderHabitsRailItem(session, insights.harness, session === selected),
      );
    });
    elements.habitsList.replaceChildren(rail);
    const older = Math.max(
      0,
      Number(insights.coverage.candidate_sessions) - sessions.length,
    );
    elements.habitsOlder.textContent =
      Number.isFinite(older) && older > 0
        ? `${older} older ${older === 1 ? "session" : "sessions"}`
        : "";
    elements.habitsDebriefAll.hidden = !insights.harness.available || missing.length === 0;
    elements.habitsDebriefAll.disabled = habitsBatchRunning;
    elements.habitsDebriefAll.textContent = habitsBatchRunning
      ? "Writing…"
      : missing.length === 1
        ? "Write the remaining debrief"
        : `Write ${missing.length} remaining debriefs`;
    elements.habitsDetail.replaceChildren();
    if (selected) {
      elements.habitsDetail.appendChild(renderHabitsDetail(selected, insights.harness));
    }
    const notes = document.createDocumentFragment();
    insights.limitations.forEach((note) => {
      notes.appendChild(createElement("li", "", note));
    });
    elements.habitsLimitations.replaceChildren(notes);
    elements.habitsAbout.hidden = insights.limitations.length === 0;
  }

  function habitsCoverageText(coverage) {
    const evaluated = Number(coverage.evaluated_sessions);
    const candidates = Number(coverage.candidate_sessions);
    if (!Number.isFinite(evaluated) || !Number.isFinite(candidates)) return "";
    const parts = [`${evaluated} of ${candidates} recent sessions`];
    const inProgress = Number(coverage.skipped_incomplete);
    if (Number.isFinite(inProgress) && inProgress > 0) {
      parts.push(`${inProgress} still in progress`);
    }
    return parts.join(" · ");
  }

  function habitsWhen(session) {
    const finished = parseDate(session.ended_at || session.started_at);
    return finished && finished.getFullYear() > 2000 ? formatRelativeTime(finished) : "";
  }

  function habitsOutcomeDot(outcome) {
    switch (readText(outcome)) {
      case "verified_pass":
        return "ok";
      case "verified_fail":
      case "unverified_changes":
        return "warn";
      default:
        return "muted";
    }
  }

  function renderHabitsRailItem(session, harness, active) {
    const key = readText(session.session_key);
    const item = createElement("button", "habits-rail-item");
    item.type = "button";
    item.dataset.sessionKey = key;
    if (active) item.setAttribute("aria-current", "true");
    const head = createElement("span", "habits-rail-head");
    head.appendChild(createElement("span", `habits-dot habits-dot-${habitsOutcomeDot(session.outcome)}`));
    head.appendChild(createElement("span", "habits-rail-project", readText(session.project) || "Untitled project"));
    item.appendChild(head);
    item.appendChild(
      createElement(
        "span",
        "habits-rail-meta",
        [habitsDuration(session.duration_ms), habitsDollars(session.cost_usd), habitsWhen(session)]
          .filter(Boolean)
          .join(" · "),
      ),
    );
    let snippet = "";
    if (habitsGenerating.has(key)) {
      snippet = "Writing now…";
    } else if (session.debrief) {
      snippet = readText(session.debrief.debrief.headline);
    } else if (habitsCardErrors.has(key)) {
      snippet = "Debrief failed. Open to retry.";
    } else if (harness.available) {
      snippet = "Not debriefed yet";
    } else {
      snippet = "No debrief";
    }
    item.appendChild(createElement("span", "habits-rail-snippet", snippet));
    item.addEventListener("click", () => {
      selectHabitsSession(key, true);
    });
    return item;
  }

  async function generateHabitsDebrief(sessionKey, refresh) {
    const key = readText(sessionKey);
    if (!key || habitsGenerating.has(key)) return false;
    habitsGenerating.add(key);
    habitsCardErrors.delete(key);
    renderUserInsights();
    try {
      const path = `/v1/user-insights/${encodeURIComponent(key)}/debrief?generate=1${refresh ? "&refresh=1" : ""}`;
      const response = await apiGet(path);
      const record = isRecord(response) ? requireHabitsDebrief(response.data) : null;
      if (!record) throw new Error("Local API returned an invalid debrief.");
      if (state.userInsights) {
        state.userInsights.sessions = state.userInsights.sessions.map((session) =>
          readText(session.session_key) === key
            ? { ...session, debrief: record, debrief_status: "ready" }
            : session,
        );
      }
      return true;
    } catch (error) {
      habitsCardErrors.set(
        key,
        customerErrorMessage(error, "Your harness did not return a debrief. Try again."),
      );
      return false;
    } finally {
      habitsGenerating.delete(key);
      renderUserInsights();
    }
  }

  async function generateAllHabitsDebriefs() {
    if (habitsBatchRunning || !state.userInsights) return;
    habitsBatchRunning = true;
    renderUserInsights();
    try {
      const pending = state.userInsights.sessions
        .filter((session) => !session.debrief || session.debrief_status !== "ready")
        .map((session) => readText(session.session_key));
      for (const key of pending) {
        if (state.activeView !== "habits") break;
        await generateHabitsDebrief(key, false);
      }
    } finally {
      habitsBatchRunning = false;
      renderUserInsights();
    }
  }

  function habitsQuickFacts(session) {
    const m = isRecord(session.measurements) ? session.measurements : {};
    const parts = [];
    const user = Number(m.user_turns);
    if (Number.isFinite(user) && user > 0) parts.push(`${user} messages from you`);
    const edits = Number(m.file_change_turns);
    parts.push(edits > 0 ? `${edits} file edits` : "no file edits seen");
    const checks = Number(m.verification_runs);
    parts.push(checks > 0 ? `${checks} checks run` : "no checks run");
    const corrections = Number(m.corrections);
    if (Number.isFinite(corrections) && corrections > 0) parts.push(`${corrections} corrections`);
    return `${parts.join(" · ")}.`;
  }

  function renderHabitsDetail(session, harness) {
    const key = readText(session.session_key);
    const record = session.debrief;
    const generating = habitsGenerating.has(key);
    const root = createElement("article", "habits-debrief");
    root.dataset.sessionKey = key;

    const header = createElement("header", "habits-detail-header");
    const meta = createElement("p", "habits-detail-meta");
    meta.appendChild(createElement("span", `habits-dot habits-dot-${habitsOutcomeDot(session.outcome)}`));
    meta.appendChild(createElement("strong", "", readText(session.project) || "Untitled project"));
    meta.appendChild(
      document.createTextNode(
        " · " +
          [
            displayHarness(session.agent),
            habitsDuration(session.duration_ms),
            habitsDollars(session.cost_usd),
            habitsWhen(session),
            habitsOutcomeLabel(session.outcome).toLowerCase(),
          ]
            .filter(Boolean)
            .join(" · "),
      ),
    );
    header.appendChild(meta);
    if (record && harness.available && !generating) {
      const rewrite = createElement("button", "text-button habits-rewrite", "Rewrite debrief");
      rewrite.type = "button";
      rewrite.addEventListener("click", () => {
        void generateHabitsDebrief(key, true);
      });
      header.appendChild(rewrite);
    }
    root.appendChild(header);

    const title = createElement(
      "h2",
      "habits-detail-title",
      (record && readText(record.debrief.title)) || readText(session.project) || "Session",
    );
    title.id = "habits-detail-title";
    title.tabIndex = -1;
    root.appendChild(title);
    root.appendChild(
      createElement(
        "p",
        "habits-detail-context",
        record && readText(record.debrief.task_summary)
          ? readText(record.debrief.task_summary)
          : habitsQuickFacts(session),
      ),
    );

    if (generating) {
      const wait = createElement("div", "habits-generating");
      wait.appendChild(createElement("span", "spinner"));
      wait.appendChild(
        createElement(
          "p",
          "",
          `Your ${habitsHarnessLabel(harness.name)} is reading this session. This usually takes one to three minutes.`,
        ),
      );
      root.appendChild(wait);
      return root;
    }
    const cardError = habitsCardErrors.get(key);
    if (cardError) {
      root.appendChild(createElement("p", "habits-card-error", cardError));
    }
    if (!record) {
      const actions = createElement("div", "habits-actions");
      if (harness.available) {
        const button = createElement(
          "button",
          "primary-button",
          `Debrief this session with ${habitsHarnessLabel(harness.name)}`,
        );
        button.type = "button";
        button.addEventListener("click", () => {
          void generateHabitsDebrief(key, false);
        });
        actions.appendChild(button);
      } else {
        actions.appendChild(
          createElement(
            "p",
            "habits-quiet",
            "Install Claude Code or Codex on this machine to write a debrief for this session.",
          ),
        );
      }
      root.appendChild(actions);
      return root;
    }
    if (session.debrief_status === "stale") {
      root.appendChild(
        createElement(
          "p",
          "habits-stale",
          "This debrief was written with an older version of Belay's prompt. Rewrite it for the current one.",
        ),
      );
    }
    const debrief = record.debrief;
    root.appendChild(renderHabitsStats(debrief, session));
    root.appendChild(renderHabitsTimeExplanation(debrief));
    root.appendChild(renderHabitsTimeline(debrief, session));
    root.appendChild(renderHabitsTabs(debrief, key));
    const provenance = [
      `Written ${(() => {
        const at = parseDate(record.generated_at);
        return at ? formatRelativeTime(at) : "recently";
      })()}`,
      readText(record.model) ? `by ${readText(record.model)}` : "",
      record.sanitization.length
        ? `Belay adjusted ${record.sanitization.length} ${record.sanitization.length === 1 ? "field" : "fields"} to its limits`
        : "",
    ]
      .filter(Boolean)
      .join(" · ");
    root.appendChild(createElement("p", "habits-provenance", provenance));
    return root;
  }

  function habitsPhaseSpan(phase) {
    const span = Number(phase.to_minute) - Number(phase.from_minute);
    return Number.isFinite(span) && span > 0 ? span : 0;
  }

  function habitsInsightDollars(insight) {
    const dollars = Number(insight.dollars_cost);
    return Number.isFinite(dollars) && dollars > 0 ? dollars : 0;
  }

  function habitsInsightMinutes(insight) {
    const minutes = Number(insight.minutes_cost);
    return Number.isFinite(minutes) && minutes > 0 ? minutes : 0;
  }

  function renderHabitsStats(debrief, session) {
    let attentionPhases = 0;
    let attentionMinutes = 0;
    debrief.phases.forEach((phase) => {
      const verdict = habitsPhaseVerdict(phase.verdict);
      if (verdict === "wasted" || verdict === "partly-wasted") {
        attentionPhases += 1;
        attentionMinutes += habitsPhaseSpan(phase);
      }
    });
    const spent = debrief.insights.reduce((sum, insight) => sum + habitsInsightDollars(insight), 0);
    const total = Number(session.cost_usd);
    const row = createElement("div", "habits-stats");
    const stat = (value, label, note = "") => {
      const block = createElement("div", "habits-stat");
      block.appendChild(createElement("strong", "", value));
      block.appendChild(createElement("span", "", label));
      if (note) block.appendChild(createElement("small", "", note));
      return block;
    };
    row.appendChild(
      stat(
        `${attentionPhases} ${attentionPhases === 1 ? "phase" : "phases"}`,
        "need attention",
        `${habitsWallClockSpan(attentionMinutes)} wall-clock span flagged`,
      ),
    );
    row.appendChild(
      stat(
        spent > 0
          ? Number.isFinite(total) && total > 0
            ? `${formatReportDollars(spent)} of ${formatReportDollars(total)}`
            : formatReportDollars(spent)
          : Number.isFinite(total) && total > 0
            ? formatReportDollars(total)
            : "—",
        spent > 0 ? "went to rework and debate" : "session spend",
      ),
    );
    row.appendChild(
      stat(
        `${debrief.insights.length} to change · ${debrief.keep_doing.length} to keep`,
        "habits in this session",
      ),
    );
    return row;
  }

  function renderHabitsTimeExplanation(debrief) {
    const spans = debrief.phases
      .filter((phase) => {
        const verdict = habitsPhaseVerdict(phase.verdict);
        return verdict === "wasted" || verdict === "partly-wasted";
      })
      .map(habitsPhaseSpan)
      .filter((span) => span > 0);
    const total = spans.reduce((sum, span) => sum + span, 0);
    const details = createElement("details", "habits-time-explanation");
    details.appendChild(createElement("summary", "", "How the time span is calculated"));
    details.appendChild(
      createElement(
        "p",
        "",
        "Belay adds the first-to-last transcript time of every phase your agent marked wasted or partly wasted. It can include breaks or overnight gaps, so this is context—not active work time or guaranteed savings.",
      ),
    );
    if (spans.length > 0) {
      details.appendChild(
        createElement(
          "p",
          "habits-time-formula",
          `${spans.map(habitsWallClockSpan).join(" + ")} = ${habitsWallClockSpan(total)}`,
        ),
      );
    }
    return details;
  }

  function renderHabitsTimeline(debrief, session) {
    const block = createElement("section", "habits-timeline");
    block.setAttribute("aria-label", "Where the session's time went");
    const phases = debrief.phases;
    const phaseEnd = phases.reduce((max, phase) => Math.max(max, Number(phase.to_minute) || 0), 0);
    const durationMinutes = Number(session.duration_ms) / 60000;
    const total = Math.max(phaseEnd, Number.isFinite(durationMinutes) ? durationMinutes : 0, 1);
    const track = createElement("div", "habits-track");
    const bar = createElement("div", "habits-phase-bar");
    phases.forEach((phase) => {
      const segment = createElement(
        "span",
        `habits-phase-segment habits-phase-${habitsPhaseVerdict(phase.verdict)}`,
      );
      segment.style.flexGrow = String(Math.max(habitsPhaseSpan(phase), total * 0.02));
      segment.title = `${readText(phase.label)} · ${habitsMinutes(habitsPhaseSpan(phase))}`;
      bar.appendChild(segment);
    });
    track.appendChild(bar);
    const markers = createElement("div", "habits-markers");
    debrief.insights.forEach((insight, index) => {
      const at = Number(insight.at_minute);
      if (!Number.isFinite(at) || at < 0) return;
      const marker = createElement("button", "habits-marker", String(index + 1));
      marker.type = "button";
      marker.style.left = `${Math.min(100, (at / total) * 100)}%`;
      marker.title = readText(insight.title);
      marker.setAttribute("aria-label", `Insight ${index + 1}: ${readText(insight.title)}`);
      marker.addEventListener("click", () => {
        state.habitsTab = "change";
        state.habitsOpenInsight = index;
        renderUserInsights();
      });
      markers.appendChild(marker);
    });
    if (readText(session.outcome) === "verified_pass") {
      const done = createElement("span", "habits-marker habits-marker-done", "✓");
      done.style.left = "100%";
      done.title = "Checks passed";
      markers.appendChild(done);
    }
    track.appendChild(markers);
    const axis = createElement("div", "habits-axis");
    const ticks = [0];
    phases.forEach((phase) => {
      const end = Number(phase.to_minute);
      const previous = ticks[ticks.length - 1];
      if (
        Number.isFinite(end) &&
        end > 0 &&
        end < total * 0.88 &&
        end - previous >= total * 0.1
      ) {
        ticks.push(end);
      }
    });
    ticks.push(total);
    ticks.forEach((minute, index) => {
      const tick = createElement("span", "habits-axis-tick", index === 0 ? "0" : habitsMinutes(minute));
      tick.style.left = `${(minute / total) * 100}%`;
      if (index === ticks.length - 1) tick.classList.add("habits-axis-tick-end");
      axis.appendChild(tick);
    });
    track.appendChild(axis);
    block.appendChild(track);
    const legend = createElement("ul", "habits-legend");
    phases.forEach((phase) => {
      const item = createElement("li", `habits-legend-item habits-phase-${habitsPhaseVerdict(phase.verdict)}`);
      item.appendChild(createElement("span", "habits-legend-swatch"));
      item.appendChild(
        document.createTextNode(`${readText(phase.label)} · ${habitsMinutes(habitsPhaseSpan(phase))}`),
      );
      item.title = readText(phase.what_happened);
      legend.appendChild(item);
    });
    block.appendChild(legend);
    return block;
  }

  const habitsTabs = Object.freeze([
    { id: "change", label: "What to change" },
    { id: "message", label: "Your first message" },
    { id: "well", label: "What went well" },
    { id: "next", label: "Next time" },
  ]);

  function renderHabitsTabs(debrief, key) {
    const wrap = createElement("div", "habits-tabs-wrap");
    const list = createElement("div", "habits-tabs");
    list.setAttribute("role", "tablist");
    const active = habitsTabs.some((tab) => tab.id === state.habitsTab) ? state.habitsTab : "change";
    habitsTabs.forEach((tab) => {
      const count =
        tab.id === "change"
          ? debrief.insights.length
          : tab.id === "well"
            ? debrief.keep_doing.length
            : 0;
      const button = createElement("button", "habits-tab", tab.label);
      button.type = "button";
      button.setAttribute("role", "tab");
      button.setAttribute("aria-selected", tab.id === active ? "true" : "false");
      if (count) button.appendChild(createElement("span", "habits-tab-count", ` · ${count}`));
      button.addEventListener("click", () => {
        state.habitsTab = tab.id;
        renderUserInsights();
      });
      list.appendChild(button);
    });
    wrap.appendChild(list);
    const panel = createElement("div", "habits-tab-panel");
    panel.setAttribute("role", "tabpanel");
    if (active === "change") {
      debrief.insights.forEach((insight, index) => {
        panel.appendChild(renderHabitsInsight(insight, index, index === state.habitsOpenInsight));
      });
      if (!debrief.insights.length) {
        panel.appendChild(createElement("p", "habits-quiet", "Nothing to change in this session."));
      }
    } else if (active === "message") {
      panel.appendChild(renderHabitsPromptLength(debrief.prompt_length_read));
    } else if (active === "well") {
      if (!debrief.keep_doing.length) {
        panel.appendChild(createElement("p", "habits-quiet", "The debrief did not single anything out here."));
      }
      debrief.keep_doing.forEach((item) => {
        const block = createElement("section", "habits-keep-item");
        const heading = createElement("h4");
        heading.appendChild(createElement("span", "habits-dot habits-dot-ok"));
        heading.appendChild(document.createTextNode(readText(item.title)));
        block.appendChild(heading);
        if (readText(item.why)) block.appendChild(createElement("p", "", readText(item.why)));
        const turns = Array.isArray(item.evidence_turns) ? item.evidence_turns.filter((v) => Number.isFinite(Number(v))) : [];
        if (turns.length) block.appendChild(createElement("p", "habits-foot", `Turns ${turns.join(", ")}`));
        panel.appendChild(block);
      });
    } else {
      const opener = readText(debrief.next_session_opener);
      if (opener) {
        panel.appendChild(createElement("p", "habits-lead", "Open your next session in this project with:"));
        panel.appendChild(habitsQuote(opener, "Copy opening"));
      } else {
        panel.appendChild(createElement("p", "habits-quiet", "No opening message was suggested for this session."));
      }
    }
    wrap.appendChild(panel);
    return wrap;
  }

  function renderHabitsInsight(insight, index, open) {
    const block = createElement("section", `habits-insight${open ? " habits-insight-open" : ""}`);
    const toggle = createElement("button", "habits-insight-toggle");
    toggle.type = "button";
    toggle.setAttribute("aria-expanded", open ? "true" : "false");
    toggle.appendChild(createElement("span", "habits-insight-number", String(index + 1)));
    const titleWrap = createElement("span", "habits-insight-title");
    titleWrap.appendChild(createElement("span", "", readText(insight.title)));
    if (!open) {
      const preview = readText(insight.what_you_did);
      if (preview) titleWrap.appendChild(createElement("span", "habits-insight-preview", preview));
    }
    toggle.appendChild(titleWrap);
    const cost = [];
    const minutes = habitsInsightMinutes(insight);
    if (minutes >= 1) cost.push(habitsMinutes(minutes));
    const dollars = habitsInsightDollars(insight);
    if (dollars >= 0.5) cost.push(`~${formatReportDollars(dollars)}`);
    toggle.appendChild(createElement("span", "habits-insight-cost", cost.join(" · ")));
    toggle.appendChild(createElement("span", "habits-chevron", open ? "⌃" : "⌄"));
    toggle.addEventListener("click", () => {
      state.habitsOpenInsight = open ? -1 : index;
      renderUserInsights();
    });
    block.appendChild(toggle);
    if (!open) return block;
    const body = createElement("div", "habits-insight-body");
    const grid = createElement("div", "habits-insight-grid");
    [
      ["What you did", insight.what_you_did],
      ["What an expert would have done", insight.ideal_path],
    ].forEach(([label, value]) => {
      const cell = createElement("div");
      cell.appendChild(createElement("span", "habits-label", label));
      cell.appendChild(createElement("p", "", readText(value)));
      grid.appendChild(cell);
    });
    body.appendChild(grid);
    if (readText(insight.what_it_cost)) {
      const costLine = createElement("p", "habits-cost-line");
      costLine.appendChild(createElement("span", "habits-label-inline", "What it cost: "));
      costLine.appendChild(document.createTextNode(readText(insight.what_it_cost)));
      body.appendChild(costLine);
    }
    const message = readText(insight.say_this_instead);
    if (message) {
      body.appendChild(createElement("span", "habits-label", "Say this instead"));
      body.appendChild(habitsQuote(message, "Copy message"));
    }
    const foot = createElement("div", "habits-insight-foot");
    const turns = Array.isArray(insight.evidence_turns)
      ? insight.evidence_turns.filter((value) => Number.isFinite(Number(value)))
      : [];
    foot.appendChild(createElement("span", "", turns.length ? `Turns ${turns.join(", ")}` : ""));
    const confidence = Number(insight.confidence);
    foot.appendChild(
      createElement(
        "span",
        "",
        Number.isFinite(confidence) && confidence > 0 ? `${Math.round(confidence * 100)}% confident` : "",
      ),
    );
    body.appendChild(foot);
    block.appendChild(body);
    return block;
  }

  function renderHabitsPromptLength(read) {
    const block = createElement("section", "habits-length");
    if (!isRecord(read)) {
      block.appendChild(createElement("p", "habits-quiet", "The debrief did not judge the first message."));
      return block;
    }
    const verdict = readText(read.verdict);
    const heading = createElement("h4");
    heading.appendChild(
      createElement(
        "span",
        `habits-dot habits-dot-${verdict === "about_right" ? "ok" : "warn"}`,
      ),
    );
    heading.appendChild(
      document.createTextNode(
        verdict === "over_specified"
          ? "Your first message said more than the agent needed"
          : verdict === "under_specified"
            ? "Your first message left out what the agent needed"
            : "Your first message was about the right length",
      ),
    );
    block.appendChild(heading);
    if (readText(read.explanation)) {
      block.appendChild(createElement("p", "", readText(read.explanation)));
    }
    const rewrite = readText(read.rewrite);
    if (rewrite && verdict !== "about_right") {
      block.appendChild(createElement("span", "habits-label", "The version that would have worked"));
      block.appendChild(habitsQuote(rewrite, "Copy rewrite"));
    }
    return block;
  }

  function habitsQuote(value, copyLabel) {
    const box = createElement("div", "habits-quote");
    box.appendChild(createElement("p", "habits-quote-text", `“${value}”`));
    const copy = createElement("button", "habits-copy");
    copy.type = "button";
    copy.setAttribute("aria-label", copyLabel);
    copy.title = copyLabel;
    copy.appendChild(habitsCopyIcon());
    copy.addEventListener("click", () => {
      void copyText(value, copy);
    });
    box.appendChild(copy);
    return box;
  }

  function habitsCopyIcon() {
    const ns = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("width", "16");
    svg.setAttribute("height", "16");
    svg.setAttribute("aria-hidden", "true");
    const rect = document.createElementNS(ns, "rect");
    rect.setAttribute("x", "9");
    rect.setAttribute("y", "9");
    rect.setAttribute("width", "12");
    rect.setAttribute("height", "12");
    rect.setAttribute("rx", "2");
    const path = document.createElementNS(ns, "path");
    path.setAttribute("d", "M15 9V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h4");
    svg.appendChild(rect);
    svg.appendChild(path);
    return svg;
  }

  function habitsPhaseVerdict(value) {
    const verdict = readText(value);
    return ["productive", "partly_wasted", "wasted", "unclear"].includes(verdict)
      ? verdict.replace(/_/g, "-")
      : "unclear";
  }

  function habitsPhaseLabel(verdict) {
    switch (verdict) {
      case "productive":
        return "productive";
      case "partly-wasted":
        return "partly wasted";
      case "wasted":
        return "wasted";
      default:
        return "unclear";
    }
  }

  function habitsInsightKind(value) {
    const kind = readText(value);
    return ["prompting", "scoping", "verification", "delegation", "context", "workflow", "review"].includes(kind)
      ? kind
      : "workflow";
  }

  function habitsInsightLabel(value) {
    switch (habitsInsightKind(value)) {
      case "prompting":
        return "Prompting";
      case "scoping":
        return "Scoping";
      case "verification":
        return "Verification";
      case "delegation":
        return "Delegation";
      case "context":
        return "Context";
      case "review":
        return "Review";
      default:
        return "Workflow";
    }
  }

  function habitsMinutes(value) {
    const minutes = Number(value);
    if (!Number.isFinite(minutes) || minutes <= 0) return "under a minute";
    const rounded = Math.round(minutes);
    if (rounded < 1) return "under a minute";
    const hours = Math.floor(rounded / 60);
    if (hours) return `${hours}h ${String(rounded % 60).padStart(2, "0")}`;
    return `${rounded} min`;
  }

  function habitsWallClockSpan(value) {
    const minutes = Number(value);
    if (!Number.isFinite(minutes) || minutes <= 0) return "0m";
    const rounded = Math.round(minutes);
    const hours = Math.floor(rounded / 60);
    if (hours) return `${hours}h ${String(rounded % 60).padStart(2, "0")}m`;
    return `${rounded}m`;
  }

  function habitsDuration(value) {
    const milliseconds = Number(value);
    if (!Number.isFinite(milliseconds) || milliseconds <= 0) return "";
    const minutes = Math.round(milliseconds / 60000);
    const hours = Math.floor(minutes / 60);
    if (hours) return `${hours}h ${String(minutes % 60).padStart(2, "0")}m`;
    return `${Math.max(1, minutes)}m`;
  }

  function habitsDollars(value) {
    const amount = Number(value);
    if (!Number.isFinite(amount) || amount <= 0) return "";
    return formatReportDollars(amount);
  }

  function habitsOutcomeLabel(value) {
    switch (readText(value)) {
      case "verified_pass":
        return "Checks passed";
      case "verified_fail":
        return "Last check failed";
      case "unverified_changes":
        return "Unchecked changes";
      case "no_changes":
        return "No file changes seen";
      default:
        return "Outcome not reported";
    }
  }

  function habitsOutcomeClass(value) {
    const outcome = readText(value);
    return ["verified_pass", "verified_fail", "unverified_changes", "no_changes"].includes(
      outcome,
    )
      ? outcome.replace(/_/g, "-")
      : "unknown";
  }

  function createElement(tagName, className, text) {
    const element = document.createElement(tagName);
    if (className) element.className = className;
    if (text !== undefined && text !== null) element.textContent = String(text);
    return element;
  }

  function isRecord(value) {
    return Boolean(value) && typeof value === "object" && !Array.isArray(value);
  }

  function firstRecord(...values) {
    return values.find(isRecord) || null;
  }

  function readText(value) {
    if (typeof value === "string") return value;
    if (typeof value === "number" && Number.isFinite(value)) return String(value);
    return "";
  }

  function readCursor(value) {
    return typeof value === "string" && value.trim() ? value : "";
  }

  function createUUIDv4() {
    if (
      !globalThis.crypto ||
      typeof globalThis.crypto.randomUUID !== "function"
    ) {
      return "";
    }
    const value = globalThis.crypto.randomUUID();
    return isCanonicalUUIDv4(value) ? value : "";
  }

  function isCanonicalUUIDv4(value) {
    return /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(
      readText(value),
    );
  }

  function toFiniteNumber(value) {
    const number = Number(value);
    return Number.isFinite(number) && number >= 0 ? number : 0;
  }

  function toOptionalCount(value) {
    if (
      value === null ||
      value === undefined ||
      value === "" ||
      typeof value === "boolean"
    ) {
      return null;
    }
    const number = Number(value);
    return Number.isInteger(number) && number >= 0 ? number : null;
  }

  function deduplicateByID(values, key) {
    const seen = new Set();
    return values.filter((value) => {
      const id = readText(value && value[key]);
      if (!id || seen.has(id)) return false;
      seen.add(id);
      return true;
    });
  }

  function displayHarness(value) {
    return readableLabel(value, "Unknown agent");
  }

  function readableLabel(value, fallback = "") {
    const text = readText(value).trim();
    if (!text) return fallback;
    return text
      .split(/[-_.\s]+/)
      .filter(Boolean)
      .map((part) => part.charAt(0).toLocaleUpperCase() + part.slice(1))
      .join(" ");
  }

  function harnessInitial(harness) {
    return Array.from(harness.trim())[0] || "?";
  }

  function normalizeOutcome(value) {
    const outcome = readText(value).toLocaleLowerCase();
    return ["succeeded", "failed", "interrupted", "incomplete"].includes(outcome)
      ? outcome
      : "unknown";
  }

  function compactID(value) {
    const text = readText(value);
    return text.length <= 22 ? text : `${text.slice(0, 11)}…${text.slice(-7)}`;
  }

  function parseDate(value) {
    if (value instanceof Date && Number.isFinite(value.getTime())) return value;
    const date = new Date(readText(value));
    return Number.isFinite(date.getTime()) && date.getUTCFullYear() > 1
      ? date
      : null;
  }

  function dateLowerBound(days) {
    const count = Number(days);
    if (!Number.isFinite(count) || count <= 0) return "";
    return new Date(Date.now() - count * 86400000).toISOString();
  }

  function recencyGroup(session) {
    const date = parseDate(session.ended_at) || parseDate(session.started_at);
    if (!date) return "Date unavailable";
    const today = new Date();
    const startToday = new Date(
      today.getFullYear(),
      today.getMonth(),
      today.getDate(),
    );
    const startSession = new Date(
      date.getFullYear(),
      date.getMonth(),
      date.getDate(),
    );
    const days = Math.round(
      (startToday.getTime() - startSession.getTime()) / 86400000,
    );
    if (days <= 0) return "Today";
    if (days === 1) return "Yesterday";
    if (days < 7) return "Earlier this week";
    return new Intl.DateTimeFormat(undefined, {
      month: "long",
      year: "numeric",
    }).format(date);
  }

  function formatSessionDate(session) {
    const date = parseDate(session.started_at);
    return date
      ? new Intl.DateTimeFormat(undefined, {
          month: "short",
          day: "numeric",
          hour: "numeric",
          minute: "2-digit",
        }).format(date)
      : "Start time unavailable";
  }

  function formatNumber(value) {
    return new Intl.NumberFormat().format(toFiniteNumber(value));
  }

  function formatNumberOrDash(value) {
    return Number.isFinite(Number(value)) ? formatNumber(value) : "—";
  }

  function formatEventCount(count) {
    const value = toFiniteNumber(count);
    return `${formatNumber(value)} ${value === 1 ? "event" : "events"}`;
  }

  function formatClockTime(date) {
    return new Intl.DateTimeFormat(undefined, {
      hour: "numeric",
      minute: "2-digit",
      second: "2-digit",
    }).format(date);
  }

  function formatFullDate(date) {
    if (!(date instanceof Date) || !Number.isFinite(date.getTime())) return "";
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: "medium",
      timeStyle: "medium",
    }).format(date);
  }

  function formatRelativeTime(value) {
    const date = value instanceof Date ? value : parseDate(value);
    if (!date) return "Unavailable";
    const seconds = Math.round((date.getTime() - Date.now()) / 1000);
    const absolute = Math.abs(seconds);
    let amount = seconds;
    let unit = "second";
    if (absolute >= 86400) {
      amount = Math.round(seconds / 86400);
      unit = "day";
    } else if (absolute >= 3600) {
      amount = Math.round(seconds / 3600);
      unit = "hour";
    } else if (absolute >= 60) {
      amount = Math.round(seconds / 60);
      unit = "minute";
    }
    return new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(
      amount,
      unit,
    );
  }

  function formatDuration(startedAt, endedAt) {
    if (!startedAt || !endedAt) return "Unavailable";
    const milliseconds = Math.max(0, endedAt.getTime() - startedAt.getTime());
    const minutes = Math.floor(milliseconds / 60000);
    const hours = Math.floor(minutes / 60);
    if (hours) return `${hours}h ${minutes % 60}m`;
    if (minutes) return `${minutes}m`;
    return `${Math.max(1, Math.round(milliseconds / 1000))}s`;
  }

  function formatFreshness(value) {
    const date = parseDate(value);
    return date
      ? `Last retained data received by Belay · ${formatRelativeTime(date)}`
      : "";
  }

  function joinObservedValues(values) {
    return Array.from(values).map(readableLabel).join(", ");
  }

  function sessionHistory(session) {
    const value = readText(session.history).toLocaleLowerCase();
    if (["historical", "live", "mixed"].includes(value)) return value;
    return session.historical ? "historical" : "live";
  }
})();
