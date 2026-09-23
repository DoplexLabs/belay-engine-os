package localapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	acquisition "github.com/DoplexLabs/belay-engine/internal/acquisition/transcript"
	"github.com/DoplexLabs/belay-engine/internal/canonical/numbatmap"
	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	transcriptActiveWindow   = 5 * time.Minute
	transcriptTailChunk      = 128 << 10
	transcriptBackfillChunk  = 512 << 10
	transcriptChunkScanBytes = 64 << 10
	transcriptFastStartMax   = 8 << 20
	gitRemoteTimeout         = 750 * time.Millisecond
	maxGitRemoteBytes        = 16 << 10
)

type transcriptImportOwner string

const (
	transcriptOwnerTail     transcriptImportOwner = "tail"
	transcriptOwnerBackfill transcriptImportOwner = "backfill"
)

type transcriptBatchStore interface {
	AppendTranscriptBatch(
		context.Context,
		belaytranscript.Session,
		[]belaytranscript.Turn,
	) (int, error)
}

type transcriptCursor struct {
	Offset            int64                 `json:"offset"`
	Device            uint64                `json:"device"`
	Inode             uint64                `json:"inode"`
	PrefixBytes       int                   `json:"prefix_bytes"`
	PrefixSHA256      string                `json:"prefix_sha256"`
	HadIssues         bool                  `json:"had_issues"`
	Owner             transcriptImportOwner `json:"owner,omitempty"`
	InactiveFinalized bool                  `json:"inactive_finalized,omitempty"`
	ServicedAt        time.Time             `json:"serviced_at,omitempty"`
	State             acquisition.State     `json:"state"`
}

type transcriptGroupPlan struct {
	key           string
	sources       []acquisition.Source
	snapshotSizes map[string]int64
	modified      time.Time
	bytes         int64
	recent        bool
	tailOwned     bool
	complete      bool
	pending       bool
	serviced      time.Time
}

type orderedTranscriptTurn struct {
	turn       belaytranscript.Turn
	sourcePath string
	fileOrder  int64
}

type parsedTranscriptSource struct {
	source   acquisition.Source
	result   acquisition.Result
	modified time.Time
}

type openTranscriptSource struct {
	source      acquisition.Source
	cursorPath  string
	cursor      transcriptCursor
	file        *os.File
	identity    fileIdentity
	size        int64
	reset       bool
	startOffset int64
	readEnd     int64
	changed     bool
	result      acquisition.Result
	modified    time.Time
}

func ScanTranscripts(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
) error {
	if store == nil {
		return errors.New("transcript store is required")
	}
	sources, discoveryErr := acquisition.Discover()
	groups := groupTranscriptSources(sources)
	var scanErrors []error
	for _, key := range sortedGroupKeys(groups) {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(scanErrors, err)...)
		}
		parsed, partial, live, err := parseHistoricalGroup(ctx, groups[key])
		if err != nil {
			scanErrors = append(scanErrors, err)
			continue
		}
		coverage := belaytranscript.CoverageComplete
		if partial {
			coverage = belaytranscript.CoveragePartial
		} else if live {
			coverage = belaytranscript.CoverageLive
		}
		session, turns, err := assembleTranscriptGroup(ctx, parsed, coverage)
		if err != nil {
			scanErrors = append(scanErrors, err)
			continue
		}
		if _, err := store.AppendTranscriptBatch(ctx, session, turns); err != nil {
			scanErrors = append(scanErrors, err)
		}
	}
	return errors.Join(append([]error{discoveryErr}, scanErrors...)...)
}

// ScanHistoricalTranscripts backfills inactive, unclaimed transcript groups.
// Fast-start-sized groups are processed before unusually large archives, then
// newest-first, so useful history reaches the UI promptly. Successfully
// imported groups receive backfill cursors and are not reconsidered by the live
// tailer unless they become active again.
func ScanHistoricalTranscripts(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
) error {
	return scanHistoricalTranscriptsAt(ctx, paths, store, time.Now())
}

func scanHistoricalTranscriptsAt(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
	now time.Time,
) error {
	if store == nil {
		return errors.New("transcript store is required")
	}
	sources, discoveryErr := acquisition.Discover()
	for {
		plans, planErr := planTranscriptGroups(paths, sources, now)
		var scanErrors []error
		pending := 0
		for _, plan := range plans {
			if plan.recent || plan.tailOwned || plan.complete {
				continue
			}
			pending++
			if err := ctx.Err(); err != nil {
				return errors.Join(
					discoveryErr,
					planErr,
					errors.Join(scanErrors...),
					err,
				)
			}
			if err := importTranscriptGroupAt(
				ctx,
				paths,
				store,
				plan.sources,
				transcriptOwnerBackfill,
				now,
			); err != nil {
				scanErrors = append(scanErrors, err)
			}
		}
		if planErr != nil || len(scanErrors) > 0 || pending == 0 {
			return errors.Join(
				discoveryErr,
				planErr,
				errors.Join(scanErrors...),
			)
		}
	}
}

func ImportTranscriptsOnce(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
) error {
	if store == nil {
		return errors.New("transcript store is required")
	}
	sources, discoveryErr := acquisition.Discover()
	groups := groupTranscriptSources(sources)
	var importErrors []error
	for _, key := range sortedGroupKeys(groups) {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(importErrors, err)...)
		}
		if err := importTranscriptGroupOnce(ctx, paths, store, groups[key]); err != nil {
			importErrors = append(importErrors, err)
		}
	}
	return errors.Join(append([]error{discoveryErr}, importErrors...)...)
}

// ImportRecentTranscriptsOnce tails only active transcript groups and groups
// already claimed by the tailer. This keeps the two-second poll independent
// from the historical backlog while retaining ownership long enough to run an
// inactivity finalization pass.
func ImportRecentTranscriptsOnce(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
) error {
	return importRecentTranscriptsAt(ctx, paths, store, time.Now())
}

// DrainScanTranscripts snapshots every active or tail-owned transcript group,
// then drains each snapshot completely using tail-sized chunks. A second call
// discovers a new snapshot, allowing explicit scans to catch appends that
// arrived while another long-running scan phase was in progress.
func DrainScanTranscripts(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
) error {
	if store == nil {
		return errors.New("transcript store is required")
	}
	now := time.Now()
	sources, discoveryErr := acquisition.Discover()
	plans, planErr := planTranscriptGroups(paths, sources, now)
	candidates := make([]transcriptGroupPlan, 0, len(plans))
	for _, plan := range plans {
		if plan.recent || plan.tailOwned {
			candidates = append(candidates, plan)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.pending != right.pending {
			return left.pending
		}
		leftFast := left.bytes <= transcriptFastStartMax
		rightFast := right.bytes <= transcriptFastStartMax
		if leftFast != rightFast {
			return leftFast
		}
		if left.bytes != right.bytes {
			return left.bytes < right.bytes
		}
		if !left.modified.Equal(right.modified) {
			return left.modified.After(right.modified)
		}
		return left.key < right.key
	})

	var drainErrors []error
	for _, plan := range candidates {
		if err := ctx.Err(); err != nil {
			return errors.Join(
				discoveryErr,
				planErr,
				errors.Join(drainErrors...),
				err,
			)
		}
		if err := drainTranscriptGroupSnapshot(
			ctx,
			paths,
			store,
			plan,
			now,
		); err != nil {
			drainErrors = append(drainErrors, err)
		}
	}
	return errors.Join(discoveryErr, planErr, errors.Join(drainErrors...))
}

func importRecentTranscriptsAt(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
	now time.Time,
) error {
	if store == nil {
		return errors.New("transcript store is required")
	}
	sources, discoveryErr := acquisition.Discover()
	plans, planErr := planTranscriptGroups(paths, sources, now)
	var importErrors []error
	candidates := make([]transcriptGroupPlan, 0, len(plans))
	for _, plan := range plans {
		if !plan.recent && !plan.tailOwned {
			continue
		}
		candidates = append(candidates, plan)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.serviced.IsZero() != right.serviced.IsZero() {
			return left.serviced.IsZero()
		}
		if !left.serviced.Equal(right.serviced) {
			return left.serviced.Before(right.serviced)
		}
		if !left.modified.Equal(right.modified) {
			return left.modified.After(right.modified)
		}
		return left.key < right.key
	})
	if len(candidates) > 0 {
		if err := ctx.Err(); err != nil {
			return errors.Join(discoveryErr, planErr, err)
		}
		if err := importTranscriptGroupAt(
			ctx,
			paths,
			store,
			candidates[0].sources,
			transcriptOwnerTail,
			now,
		); err != nil {
			importErrors = append(importErrors, err)
		}
	}
	return errors.Join(discoveryErr, planErr, errors.Join(importErrors...))
}

func parseHistoricalGroup(
	ctx context.Context,
	sources []acquisition.Source,
) ([]parsedTranscriptSource, bool, bool, error) {
	parentToolUses := make(map[string]string)
	parsed := make([]parsedTranscriptSource, 0, len(sources))
	partial := false
	live := false
	for _, source := range sources {
		file, _, size, err := openRegularNoFollow(source.Path)
		if err != nil {
			return nil, false, false, fmt.Errorf("open transcript %q: %w", source.Path, err)
		}
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return nil, false, false, fmt.Errorf("stat transcript %q: %w", source.Path, statErr)
		}
		result, parseErr := acquisition.Parse(
			ctx,
			source,
			io.NewSectionReader(file, 0, size),
			0,
			acquisition.ParseOptions{
				ParentToolUses:       parentToolUses,
				Boundary:             transcriptAcquisitionBoundary(),
				FinalizePendingUsage: true,
			},
		)
		closeErr := file.Close()
		if parseErr != nil || closeErr != nil {
			return nil, false, false, errors.Join(parseErr, closeErr)
		}
		for uuid, toolID := range result.AssistantToolUseByUUID {
			parentToolUses[uuid] = toolID
		}
		if !result.Complete || result.Issues > 0 ||
			len(result.State.EventMessageFallbacks) > 0 ||
			result.EndOffset != size {
			partial = true
		}
		age := time.Since(info.ModTime())
		if age >= 0 && age <= transcriptActiveWindow {
			live = true
		}
		parsed = append(parsed, parsedTranscriptSource{
			source:   source,
			result:   result,
			modified: info.ModTime(),
		})
	}
	return parsed, partial, live, nil
}

func importTranscriptGroupOnce(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
	sources []acquisition.Source,
) error {
	return importTranscriptGroupAt(
		ctx,
		paths,
		store,
		sources,
		transcriptOwnerTail,
		time.Now(),
	)
}

func importTranscriptGroupAt(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
	sources []acquisition.Source,
	owner transcriptImportOwner,
	now time.Time,
) error {
	return importTranscriptGroupSnapshotAt(
		ctx,
		paths,
		store,
		sources,
		owner,
		now,
		nil,
	)
}

func importTranscriptGroupSnapshotAt(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
	sources []acquisition.Source,
	owner transcriptImportOwner,
	now time.Time,
	snapshotSizes map[string]int64,
) error {
	items := make([]*openTranscriptSource, 0, len(sources))
	var openErrors []error
	for _, source := range sources {
		cursorPath := transcriptCursorPath(paths, source.Path)
		cursor, err := loadTranscriptCursor(cursorPath)
		if err != nil {
			openErrors = append(openErrors, err)
			continue
		}
		file, identity, actualSize, err := openRegularNoFollow(source.Path)
		if err != nil {
			openErrors = append(openErrors, err)
			continue
		}
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			openErrors = append(openErrors, statErr)
			continue
		}
		size := actualSize
		if snapshotSize, ok := snapshotSizes[source.Path]; ok &&
			snapshotSize < size {
			size = snapshotSize
		}
		item := &openTranscriptSource{
			source:      source,
			cursorPath:  cursorPath,
			cursor:      cursor,
			file:        file,
			identity:    identity,
			size:        size,
			modified:    info.ModTime(),
			startOffset: cursor.Offset,
		}
		prefix := ""
		if cursor.PrefixBytes > 0 {
			prefix, err = readPrefix(file, cursor.PrefixBytes)
			if err != nil {
				file.Close()
				openErrors = append(openErrors, err)
				continue
			}
		}
		if cursor.Offset > actualSize ||
			cursor.Device != 0 &&
				(cursor.Device != identity.device || cursor.Inode != identity.inode) ||
			cursor.PrefixSHA256 != "" && cursor.PrefixSHA256 != prefix {
			item.reset = true
			item.startOffset = 0
			item.cursor.State = acquisition.State{}
			item.cursor.InactiveFinalized = false
		}
		if !item.reset && item.startOffset > size {
			item.startOffset = size
		}
		chunkBytes := int64(transcriptBackfillChunk)
		if owner == transcriptOwnerTail {
			chunkBytes = transcriptTailChunk
		}
		item.readEnd, err = transcriptChunkEnd(
			item.file,
			item.startOffset,
			item.size,
			chunkBytes,
		)
		if err != nil {
			file.Close()
			openErrors = append(openErrors, err)
			continue
		}
		item.changed = item.startOffset != size || item.reset
		if item.changed {
			item.cursor.InactiveFinalized = false
		}
		items = append(items, item)
	}
	defer func() {
		for _, item := range items {
			_ = item.file.Close()
		}
	}()
	if len(items) == 0 {
		return errors.Join(openErrors...)
	}

	// Only Claude Code links a subagent turn to its parent through an assistant
	// record UUID, so only a changed Claude subagent needs the primary file
	// re-read to rebuild that map. A Cursor subagent thread carries no such
	// field; its parent linkage is stamped by the Cursor parser from the source
	// itself, so a Cursor group must not take this pre-pass.
	parentToolUses := make(map[string]string)
	if hasChangedClaudeSubagent(items) {
		for _, item := range items {
			if !item.source.Primary {
				continue
			}
			result, err := acquisition.Parse(
				ctx,
				item.source,
				io.NewSectionReader(item.file, 0, item.size),
				0,
				acquisition.ParseOptions{
					Boundary: transcriptAcquisitionBoundary(),
				},
			)
			if err != nil {
				openErrors = append(openErrors, err)
				break
			}
			for uuid, toolID := range result.AssistantToolUseByUUID {
				parentToolUses[uuid] = toolID
			}
			break
		}
	}

	var parsed []parsedTranscriptSource
	var changed []*openTranscriptSource
	for _, item := range items {
		if !item.changed {
			parsed = append(parsed, parsedTranscriptSource{
				source: item.source,
				result: acquisition.Result{State: item.cursor.State},
			})
			continue
		}
		result, err := acquisition.Parse(
			ctx,
			item.source,
			io.NewSectionReader(
				item.file,
				item.startOffset,
				item.readEnd-item.startOffset,
			),
			item.startOffset,
			acquisition.ParseOptions{
				State:          item.cursor.State,
				ParentToolUses: parentToolUses,
				Boundary:       transcriptAcquisitionBoundary(),
			},
		)
		if err != nil {
			openErrors = append(openErrors, err)
			continue
		}
		if item.readEnd < item.size {
			result.Complete = false
		}
		item.result = result
		changed = append(changed, item)
		parsed = append(parsed, parsedTranscriptSource{
			source: item.source,
			result: result,
		})
	}
	if len(changed) == 0 {
		if transcriptGroupFinalizationEligible(items, now) {
			for index, item := range items {
				if len(item.cursor.State.PendingCodexUsage) == 0 {
					continue
				}
				result := acquisition.FinalizePendingCodexUsage(
					item.source,
					item.cursor.State,
					transcriptAcquisitionBoundary(),
				)
				item.result = result
				parsed[index].result = result
			}
			coverage := belaytranscript.CoverageComplete
			for _, item := range items {
				if item.cursor.HadIssues ||
					len(item.cursor.State.EventMessageFallbacks) > 0 {
					coverage = belaytranscript.CoveragePartial
					break
				}
			}
			session, turns, err := assembleTranscriptGroup(
				ctx,
				parsed,
				coverage,
			)
			if err != nil {
				return errors.Join(append(openErrors, err)...)
			}
			if _, err := store.AppendTranscriptBatch(ctx, session, turns); err != nil {
				return errors.Join(append(openErrors, err)...)
			}
			for _, item := range items {
				if item.result.State.NativeSessionID != "" {
					item.cursor.State = sanitizeTranscriptState(item.result.State)
				}
				item.cursor.Owner = owner
				item.cursor.InactiveFinalized = true
				item.cursor.ServicedAt = now.UTC()
				if err := saveTranscriptCursor(item.cursorPath, item.cursor); err != nil {
					return errors.Join(append(openErrors, err)...)
				}
			}
		} else if owner == transcriptOwnerTail {
			for _, item := range items {
				item.cursor.Owner = owner
				item.cursor.ServicedAt = now.UTC()
				if err := saveTranscriptCursor(item.cursorPath, item.cursor); err != nil {
					return errors.Join(append(openErrors, err)...)
				}
			}
		}
		return errors.Join(openErrors...)
	}
	inactive := transcriptGroupInactiveAt(items, now)
	caughtUp := transcriptGroupCaughtUp(items)
	if inactive && caughtUp {
		for index := range parsed {
			if len(parsed[index].result.State.PendingCodexUsage) == 0 {
				continue
			}
			finalized := acquisition.FinalizePendingCodexUsage(
				parsed[index].source,
				parsed[index].result.State,
				transcriptAcquisitionBoundary(),
			)
			parsed[index].result.Turns = append(
				parsed[index].result.Turns,
				finalized.Turns...,
			)
			parsed[index].result.State = finalized.State
			for _, item := range changed {
				if item.source.Path == parsed[index].source.Path {
					item.result = parsed[index].result
					break
				}
			}
		}
	}
	coverage := belaytranscript.CoverageLive
	for _, item := range changed {
		if item.result.Issues > 0 || !item.result.Complete ||
			len(item.result.State.EventMessageFallbacks) > 0 {
			coverage = belaytranscript.CoveragePartial
			break
		}
	}
	if coverage == belaytranscript.CoverageLive && inactive && caughtUp {
		coverage = belaytranscript.CoverageComplete
	}
	session, turns, err := assembleTranscriptGroup(
		ctx,
		parsed,
		coverage,
	)
	if err != nil {
		return errors.Join(append(openErrors, err)...)
	}
	if _, err := store.AppendTranscriptBatch(ctx, session, turns); err != nil {
		return errors.Join(append(openErrors, err)...)
	}
	for _, item := range changed {
		item.cursor.Offset = item.result.EndOffset
		item.cursor.Device = item.identity.device
		item.cursor.Inode = item.identity.inode
		item.cursor.State = sanitizeTranscriptState(item.result.State)
		item.cursor.HadIssues = item.cursor.HadIssues || item.result.Issues > 0
		item.cursor.Owner = owner
		item.cursor.InactiveFinalized = inactive && caughtUp
		item.cursor.ServicedAt = now.UTC()
		item.cursor.PrefixBytes = tailPrefixBytes
		if item.cursor.Offset < int64(item.cursor.PrefixBytes) {
			item.cursor.PrefixBytes = int(item.cursor.Offset)
		}
		item.cursor.PrefixSHA256, err = readPrefix(item.file, item.cursor.PrefixBytes)
		if err != nil {
			return errors.Join(append(openErrors, err)...)
		}
		if err := saveTranscriptCursor(item.cursorPath, item.cursor); err != nil {
			return errors.Join(append(openErrors, err)...)
		}
	}
	return errors.Join(openErrors...)
}

func assembleTranscriptGroup(
	ctx context.Context,
	parsed []parsedTranscriptSource,
	coverage belaytranscript.SessionCoverage,
) (belaytranscript.Session, []belaytranscript.Turn, error) {
	if len(parsed) == 0 {
		return belaytranscript.Session{}, nil, errors.New("empty transcript group")
	}
	var state acquisition.State
	agent := parsed[0].source.Agent
	nativeSessionID := parsed[0].source.NativeSessionID
	var ordered []orderedTranscriptTurn
	for _, item := range parsed {
		state = mergeTranscriptState(state, item.result.State)
		if nativeSessionID == "" {
			nativeSessionID = item.result.State.NativeSessionID
		}
		for _, turn := range item.result.Turns {
			ordered = append(ordered, orderedTranscriptTurn{
				turn:       turn,
				sourcePath: item.source.Path,
				fileOrder:  turn.TurnIndex,
			})
		}
	}
	if state.NativeSessionID != "" {
		nativeSessionID = state.NativeSessionID
	}
	if nativeSessionID == "" {
		return belaytranscript.Session{}, nil, errors.New("transcript session has no native identity")
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if !left.turn.OccurredAt.Equal(right.turn.OccurredAt) {
			return left.turn.OccurredAt.Before(right.turn.OccurredAt)
		}
		if left.sourcePath != right.sourcePath {
			return left.sourcePath < right.sourcePath
		}
		if left.turn.Payload.JSONLByteOffset != right.turn.Payload.JSONLByteOffset {
			return left.turn.Payload.JSONLByteOffset < right.turn.Payload.JSONLByteOffset
		}
		if left.fileOrder != right.fileOrder {
			return left.fileOrder < right.fileOrder
		}
		return left.turn.TurnID < right.turn.TurnID
	})
	turns := make([]belaytranscript.Turn, len(ordered))
	for index := range ordered {
		turns[index] = ordered[index].turn
		turns[index].TurnIndex = int64(index)
	}
	projectPath := scrubTranscriptMetadata(state.ProjectPath)
	if projectPath == "" {
		return belaytranscript.Session{}, nil, errors.New("transcript session has no project path")
	}
	remote := sanitizeGitRemote(state.GitRemoteURL)
	if remote == "" {
		remote = discoverGitRemote(ctx, projectPath)
	}
	identity := remote
	if identity == "" {
		identity = projectPath
	}
	session := belaytranscript.Session{
		SessionKey:      numbatmap.SessionKey(agent, nativeSessionID, ""),
		Agent:           agent,
		NativeSessionID: nativeSessionID,
		ProjectPath:     projectPath,
		GitRemoteURL:    remote,
		ProjectIdentity: identity,
		Coverage:        coverage,
	}
	return session, turns, nil
}

func groupTranscriptSources(
	sources []acquisition.Source,
) map[string][]acquisition.Source {
	result := make(map[string][]acquisition.Source)
	for _, source := range sources {
		result[source.GroupKey] = append(result[source.GroupKey], source)
	}
	for key := range result {
		sort.Slice(result[key], func(i, j int) bool {
			if result[key][i].Primary != result[key][j].Primary {
				return result[key][i].Primary
			}
			return result[key][i].Path < result[key][j].Path
		})
	}
	return result
}

func drainTranscriptGroupSnapshot(
	ctx context.Context,
	paths Paths,
	store transcriptBatchStore,
	plan transcriptGroupPlan,
	now time.Time,
) error {
	_, _, pending, _, progress, statusErr := transcriptGroupCursorStatus(
		paths,
		plan.sources,
		plan.snapshotSizes,
	)
	drainErrors := []error{statusErr}
	maxPasses := int64(1)
	if pending {
		for _, size := range plan.snapshotSizes {
			passes := size / int64(transcriptTailChunk)
			if size%int64(transcriptTailChunk) != 0 {
				passes++
			}
			if passes+1 > maxPasses {
				maxPasses = passes + 1
			}
		}
	}
	for pass := int64(0); pass < maxPasses; pass++ {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(drainErrors, err)...)
		}
		if err := importTranscriptGroupSnapshotAt(
			ctx,
			paths,
			store,
			plan.sources,
			transcriptOwnerTail,
			now,
			plan.snapshotSizes,
		); err != nil {
			drainErrors = append(drainErrors, err)
		}
		_, _, nextPending, _, nextProgress, err :=
			transcriptGroupCursorStatus(
				paths,
				plan.sources,
				plan.snapshotSizes,
			)
		if err != nil {
			drainErrors = append(drainErrors, err)
		}
		if !nextPending {
			return errors.Join(drainErrors...)
		}
		if nextProgress <= progress {
			drainErrors = append(
				drainErrors,
				fmt.Errorf(
					"drain transcript group %q made no progress",
					plan.key,
				),
			)
			return errors.Join(drainErrors...)
		}
		pending = nextPending
		progress = nextProgress
	}
	if pending {
		drainErrors = append(
			drainErrors,
			fmt.Errorf(
				"drain transcript group %q exceeded its snapshot bound",
				plan.key,
			),
		)
	}
	return errors.Join(drainErrors...)
}

func planTranscriptGroups(
	paths Paths,
	sources []acquisition.Source,
	now time.Time,
) ([]transcriptGroupPlan, error) {
	groups := groupTranscriptSources(sources)
	plans := make([]transcriptGroupPlan, 0, len(groups))
	var planErrors []error
	for key, groupSources := range groups {
		modified, totalBytes, snapshotSizes, err :=
			transcriptGroupFileStats(groupSources)
		if err != nil {
			planErrors = append(planErrors, err)
		}
		tailOwned, complete, pending, serviced, _, err :=
			transcriptGroupCursorStatus(
				paths,
				groupSources,
				snapshotSizes,
			)
		if err != nil {
			planErrors = append(planErrors, err)
		}
		plans = append(plans, transcriptGroupPlan{
			key:           key,
			sources:       groupSources,
			snapshotSizes: snapshotSizes,
			modified:      modified,
			bytes:         totalBytes,
			recent:        transcriptModificationRecent(modified, now),
			tailOwned:     tailOwned,
			complete:      complete,
			pending:       pending,
			serviced:      serviced,
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		leftFast := plans[i].bytes <= transcriptFastStartMax
		rightFast := plans[j].bytes <= transcriptFastStartMax
		if leftFast != rightFast {
			return leftFast
		}
		if !plans[i].modified.Equal(plans[j].modified) {
			return plans[i].modified.After(plans[j].modified)
		}
		if plans[i].bytes != plans[j].bytes {
			return plans[i].bytes < plans[j].bytes
		}
		return plans[i].key < plans[j].key
	})
	return plans, errors.Join(planErrors...)
}

func transcriptGroupFileStats(
	sources []acquisition.Source,
) (time.Time, int64, map[string]int64, error) {
	var latest time.Time
	var totalBytes int64
	snapshotSizes := make(map[string]int64, len(sources))
	var statErrors []error
	for _, source := range sources {
		file, _, _, err := openRegularNoFollow(source.Path)
		if err != nil {
			statErrors = append(
				statErrors,
				fmt.Errorf("open transcript %q: %w", source.Path, err),
			)
			continue
		}
		info, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil || closeErr != nil {
			statErrors = append(
				statErrors,
				fmt.Errorf(
					"stat transcript %q: %w",
					source.Path,
					errors.Join(statErr, closeErr),
				),
			)
			continue
		}
		snapshotSizes[source.Path] = info.Size()
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		totalBytes += info.Size()
	}
	return latest, totalBytes, snapshotSizes, errors.Join(statErrors...)
}

func transcriptChunkEnd(
	file *os.File,
	start, size, chunkBytes int64,
) (int64, error) {
	if start < 0 || size < start || chunkBytes <= 0 {
		return 0, errors.New("invalid transcript chunk bounds")
	}
	target := start + chunkBytes
	if target >= size {
		return size, nil
	}
	buffer := make([]byte, transcriptChunkScanBytes)
	offset := target
	for offset < size {
		length := int64(len(buffer))
		if remaining := size - offset; remaining < length {
			length = remaining
		}
		count, err := file.ReadAt(buffer[:length], offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, errors.New("find transcript chunk boundary")
		}
		if index := bytes.IndexByte(buffer[:count], '\n'); index >= 0 {
			return offset + int64(index) + 1, nil
		}
		offset += int64(count)
		if count == 0 {
			break
		}
	}
	return size, nil
}

func transcriptModificationRecent(modified, now time.Time) bool {
	if modified.IsZero() || modified.After(now) {
		return true
	}
	return !modified.Before(now.Add(-transcriptActiveWindow))
}

func transcriptGroupCursorStatus(
	paths Paths,
	sources []acquisition.Source,
	snapshotSizes map[string]int64,
) (
	tailOwned bool,
	complete bool,
	pending bool,
	serviced time.Time,
	progress int64,
	resultErr error,
) {
	if len(sources) == 0 {
		return false, false, false, time.Time{}, 0, nil
	}
	allComplete := true
	for _, source := range sources {
		cursorPath := transcriptCursorPath(paths, source.Path)
		info, err := os.Stat(source.Path)
		if err != nil {
			allComplete = false
			resultErr = errors.Join(resultErr, err)
			continue
		}
		targetSize := info.Size()
		if snapshotSize, ok := snapshotSizes[source.Path]; ok &&
			snapshotSize < targetSize {
			targetSize = snapshotSize
		}
		_, err = os.Stat(cursorPath)
		if errors.Is(err, os.ErrNotExist) {
			allComplete = false
			pending = pending || targetSize > 0
			continue
		}
		if err != nil {
			allComplete = false
			pending = true
			resultErr = errors.Join(resultErr, err)
			continue
		}
		cursor, err := loadTranscriptCursor(cursorPath)
		if err != nil {
			tailOwned = true
			allComplete = false
			pending = true
			resultErr = errors.Join(resultErr, err)
			continue
		}
		if serviced.IsZero() || cursor.ServicedAt.Before(serviced) {
			serviced = cursor.ServicedAt
		}
		if cursor.Offset < targetSize {
			progress += cursor.Offset
		} else {
			progress += targetSize
		}
		caughtUp := cursor.Offset >= targetSize
		pending = pending || !caughtUp
		finalized := caughtUp && cursor.InactiveFinalized
		if (cursor.Owner == "" || cursor.Owner == transcriptOwnerTail) &&
			!finalized {
			tailOwned = true
		}
		if !caughtUp || !cursor.InactiveFinalized {
			allComplete = false
		}
	}
	return tailOwned, allComplete && !tailOwned, pending, serviced, progress, resultErr
}

func sortedGroupKeys(groups map[string][]acquisition.Source) []string {
	result := make([]string, 0, len(groups))
	for key := range groups {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func mergeTranscriptState(
	preferred acquisition.State,
	fallback acquisition.State,
) acquisition.State {
	if preferred.NativeSessionID == "" {
		preferred.NativeSessionID = fallback.NativeSessionID
	}
	if preferred.ProjectPath == "" {
		preferred.ProjectPath = fallback.ProjectPath
	}
	if preferred.GitRemoteURL == "" {
		preferred.GitRemoteURL = fallback.GitRemoteURL
	}
	if preferred.GitBranch == "" {
		preferred.GitBranch = fallback.GitBranch
	}
	if preferred.Model == "" {
		preferred.Model = fallback.Model
	}
	return preferred
}

// hasChangedClaudeSubagent reports whether this group has a changed Claude
// subagent transcript, which is the only case that needs the primary file
// re-read for its assistant-UUID-to-tool-use map. It is deliberately gated on
// the agent: Codex and Cursor subagent threads resolve their parent linkage
// inside the parser and would only pay for a wasted full re-read here.
func hasChangedClaudeSubagent(items []*openTranscriptSource) bool {
	for _, item := range items {
		if item.source.Agent == acquisition.AgentClaude &&
			!item.source.Primary &&
			item.changed {
			return true
		}
	}
	return false
}

func transcriptGroupInactive(items []*openTranscriptSource) bool {
	return transcriptGroupInactiveAt(items, time.Now())
}

func transcriptGroupInactiveAt(
	items []*openTranscriptSource,
	now time.Time,
) bool {
	latest := time.Time{}
	for _, item := range items {
		if item.modified.After(latest) {
			latest = item.modified
		}
	}
	if latest.IsZero() {
		return false
	}
	age := now.Sub(latest)
	return age >= transcriptActiveWindow
}

func transcriptGroupCaughtUp(items []*openTranscriptSource) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		offset := item.cursor.Offset
		if item.changed {
			offset = item.result.EndOffset
		}
		if offset != item.size {
			return false
		}
	}
	return true
}

func transcriptGroupFinalizationEligible(
	items []*openTranscriptSource,
	now time.Time,
) bool {
	if !transcriptGroupInactiveAt(items, now) ||
		!transcriptGroupCaughtUp(items) {
		return false
	}
	for _, item := range items {
		if !item.cursor.InactiveFinalized ||
			len(item.cursor.State.PendingCodexUsage) > 0 {
			return true
		}
	}
	return false
}

func transcriptCursorPath(paths Paths, sourcePath string) string {
	sum := sha256.Sum256([]byte(sourcePath))
	return filepath.Join(
		paths.TranscriptCursors,
		hex.EncodeToString(sum[:])+".json",
	)
}

func loadTranscriptCursor(path string) (transcriptCursor, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return transcriptCursor{}, nil
	}
	if err != nil {
		return transcriptCursor{}, fmt.Errorf("read transcript cursor: %w", err)
	}
	var cursor transcriptCursor
	if json.Unmarshal(body, &cursor) != nil ||
		cursor.Offset < 0 {
		return transcriptCursor{}, errors.New("parse transcript cursor")
	}
	return cursor, nil
}

func saveTranscriptCursor(path string, cursor transcriptCursor) error {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	body, err := json.Marshal(cursor)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".cursor-*.json")
	if err != nil {
		return fmt.Errorf("create transcript cursor: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(body, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return err
	}
	return directory.Close()
}

func discoverGitRemote(ctx context.Context, cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	commandCtx, cancel := context.WithTimeout(ctx, gitRemoteTimeout)
	defer cancel()
	var stdout limitedBuffer
	stdout.limit = maxGitRemoteBytes
	command := exec.CommandContext(
		commandCtx,
		"git",
		"-C",
		cwd,
		"config",
		"--get",
		"remote.origin.url",
	)
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil || stdout.truncated {
		return ""
	}
	return sanitizeGitRemote(strings.TrimSpace(stdout.String()))
}

type limitedBuffer struct {
	body      strings.Builder
	limit     int
	truncated bool
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := buffer.limit - buffer.body.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		buffer.truncated = true
	}
	_, _ = buffer.body.Write(value)
	return original, nil
}

func (buffer *limitedBuffer) String() string {
	return buffer.body.String()
}

func scrubTranscriptMetadata(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value, _ = numbatmap.ScrubSecrets(value)
	return value
}

func sanitizeGitRemote(value string) string {
	value = scrubTranscriptMetadata(strings.TrimSpace(value))
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return value
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func sanitizeTranscriptState(state acquisition.State) acquisition.State {
	state.NativeSessionID = scrubTranscriptMetadata(state.NativeSessionID)
	state.ProjectPath = scrubTranscriptMetadata(state.ProjectPath)
	state.GitRemoteURL = sanitizeGitRemote(state.GitRemoteURL)
	state.GitBranch = scrubTranscriptMetadata(state.GitBranch)
	state.Model = scrubTranscriptMetadata(state.Model)
	state.CurrentTurnID = scrubTranscriptMetadata(state.CurrentTurnID)
	state.CurrentThreadID = scrubTranscriptMetadata(state.CurrentThreadID)
	state.ParentThreadID = scrubTranscriptMetadata(state.ParentThreadID)
	state.CallTools = scrubTranscriptMap(state.CallTools)
	state.ThreadParentTool = scrubTranscriptMap(state.ThreadParentTool)
	state.ResultCalls = scrubTranscriptBoolMap(state.ResultCalls)
	state.ResponseMessages = scrubTranscriptBoolMap(state.ResponseMessages)
	state.EventMessageFallbacks = scrubTranscriptBoolMap(state.EventMessageFallbacks)
	state.FinalizedCodexUsage = scrubTranscriptBoolMap(state.FinalizedCodexUsage)
	pendingUsage := make(map[string]acquisition.PendingCodexUsage, len(state.PendingCodexUsage))
	for key, pending := range state.PendingCodexUsage {
		pending.TurnID = scrubTranscriptMetadata(pending.TurnID)
		pending.Model = scrubTranscriptMetadata(pending.Model)
		pending.CWD = scrubTranscriptMetadata(pending.CWD)
		pending.GitBranch = scrubTranscriptMetadata(pending.GitBranch)
		pending.ParentToolUseID = scrubTranscriptMetadata(pending.ParentToolUseID)
		pendingUsage[scrubTranscriptMetadata(key)] = pending
	}
	if state.PendingCodexUsage != nil {
		state.PendingCodexUsage = pendingUsage
	}
	return state
}

func scrubTranscriptMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return values
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[scrubTranscriptMetadata(key)] = scrubTranscriptMetadata(value)
	}
	return result
}

func scrubTranscriptBoolMap(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return values
	}
	result := make(map[string]bool, len(values))
	for key, value := range values {
		result[scrubTranscriptMetadata(key)] = value
	}
	return result
}

func transcriptAcquisitionBoundary() acquisition.Boundary {
	return acquisition.Boundary{
		SessionKey: func(agent, nativeSessionID string) string {
			return numbatmap.SessionKey(agent, nativeSessionID, "")
		},
		ScrubSecrets: numbatmap.ScrubSecrets,
	}
}
