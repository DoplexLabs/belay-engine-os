package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/analysis"
	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/detection"
	"github.com/DoplexLabs/belay-engine/internal/initialization"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/localaction"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/localhttp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/localmcp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
	"github.com/DoplexLabs/belay-engine/internal/updatecheck"
)

// Set these at build time for packaged distributions:
//
//	-X main.bundledNumbatSHA256=<lowercase-sha256>
//	-X main.bundledNumbatVersionMarker=<version-output-substring>
var (
	bundledNumbatSHA256        string
	bundledNumbatVersionMarker string
	currentExecutablePath      = os.Executable
)

type localRuntimeFlags struct {
	home            *string
	numbatBinary    *string
	numbatSHA256    *string
	versionMarker   *string
	allowUnverified *bool
}

type preparedRuntime struct {
	paths           localapp.Paths
	config          localapp.Config
	client          *numbat.Client
	belayExecutable string
}

type localLaunchOptions struct {
	runtime          localRuntimeFlags
	listen           string
	experience       localhttp.Experience
	installHooks     bool
	installMCP       bool
	allowCodexMCPAdd bool
	historicalScan   bool
	analyze          bool
	analyzeAgent     string
	openBrowser      bool
	commandName      string
}

type runningLocalServer interface {
	BrowserURL() string
	Wait() error
}

type localMCPCommandServer interface {
	RunStdio(context.Context) error
}

var (
	launchLocal                 = runLocalLaunch
	discoverAndScan             = localapp.DiscoverAndScan
	importRecentTranscriptsOnce = func(
		ctx context.Context,
		paths localapp.Paths,
		store *local.Store,
	) error {
		return localapp.ImportRecentTranscriptsOnce(ctx, paths, store)
	}
	drainScanTranscripts = func(
		ctx context.Context,
		paths localapp.Paths,
		store *local.Store,
	) error {
		return localapp.DrainScanTranscripts(ctx, paths, store)
	}
	scanTranscripts = func(
		ctx context.Context,
		paths localapp.Paths,
		store *local.Store,
	) error {
		return localapp.ScanTranscripts(ctx, paths, store)
	}
	scanHistoricalTranscripts = func(
		ctx context.Context,
		paths localapp.Paths,
		store *local.Store,
	) error {
		return localapp.ScanHistoricalTranscripts(ctx, paths, store)
	}
	reconcileMissionPackReceiptsOnce = func(
		ctx context.Context,
		store *local.Store,
	) error {
		reconciliation, err := localapp.NewMissionPackReceiptReconciliationCoordinator(
			store,
		)
		if err != nil {
			return err
		}
		reconciliationErr := reconciliation.Reconcile(
			ctx,
			time.Now().UTC(),
			64,
		)
		materialization, err :=
			localapp.NewMissionPackApplicationMaterializationCoordinator(store)
		if err != nil {
			return errors.Join(reconciliationErr, err)
		}
		_, materializationErr := materialization.Materialize(ctx, 64)
		return errors.Join(reconciliationErr, materializationErr)
	}
	evaluateExperienceApplicationsOnce = func(
		ctx context.Context,
		store *local.Store,
	) error {
		coordinator, err := localapp.NewExperienceEvaluationCoordinator(store)
		if err != nil {
			return err
		}
		_, err = coordinator.Evaluate(ctx, 64)
		return err
	}
	deriveExperienceImpactsOnce = func(
		ctx context.Context,
		store *local.Store,
	) error {
		coordinator, err := localapp.NewExperienceImpactCoordinator(store)
		if err != nil {
			return err
		}
		_, err = coordinator.Observe(ctx, 64)
		return err
	}
	deriveSessionTrajectoriesOnce = func(
		ctx context.Context,
		store *local.Store,
	) error {
		_, err := localapp.AnalyzeTrajectorySessionsOnce(ctx, store, 25)
		return err
	}
	newMissionPackAcceptanceService  = localapp.NewMissionPackAcceptanceService
	withMissionPackAcceptanceService = localmcp.WithMissionPackAcceptanceService
	newMissionPackStatusService      = localapp.NewMissionPackStatusService
	withMissionPackStatusService     = localmcp.WithMissionPackStatusService
	newExperienceLearningService     = func(
		store localapp.ExperienceLearningStore,
		options ...localapp.ExperienceLearningServiceOption,
	) (*localapp.ExperienceLearningService, error) {
		return localapp.NewExperienceLearningService(store, options...)
	}
	withExperienceLearningService = localmcp.WithExperienceLearningService
	pollLive                      = localapp.PollLive
	startLocalHTTPServer          = func(
		ctx context.Context,
		store *local.Store,
		token string,
		address string,
		initializationProvider initialization.Provider,
		experience localhttp.Experience,
		updates localhttp.UpdateService,
	) (runningLocalServer, error) {
		server, err := newLocalHTTPServer(
			store,
			token,
			experience,
			updates,
			initializationProvider,
		)
		if err != nil {
			return nil, err
		}
		return server.Start(ctx, address)
	}
	openLocalCommandStore = func(path string) (*local.Store, error) {
		return local.Open(path, local.NewPlatformKeyProvider(path))
	}
	newLocalMCPCommandServer = func(
		store *local.Store,
	) (localMCPCommandServer, error) {
		return newLocalMCPServer(store)
	}
	startMCPRecoveryWorker              = startLocalRecovery
	startIntelligenceRuntime            = localapp.StartIntelligenceRuntime
	startLocalRecoveryRuntime           = startLocalRecoveryWithDedicatedStore
	runIntelligenceWorkerLoops          = runIntelligenceWorkers
	runQuickstartSemanticAnalysisWorker = runQuickstartSemanticAnalysis
)

func addLocalRuntimeFlags(flags *flag.FlagSet) localRuntimeFlags {
	return localRuntimeFlags{
		home:            flags.String("home", "", "Belay Local state directory (default BELAY_HOME or ~/.belay)"),
		numbatBinary:    flags.String("numbat", "", "path to the pinned stock Numbat executable"),
		numbatSHA256:    flags.String("numbat-sha256", "", "expected SHA-256 for the pinned Numbat executable"),
		versionMarker:   flags.String("numbat-version-marker", "", "required substring in `numbat version`"),
		allowUnverified: flags.Bool("allow-unverified-numbat", false, "development only: run Numbat without a configured checksum pin"),
	}
}

func prepareRuntime(ctx context.Context, options localRuntimeFlags) (preparedRuntime, error) {
	paths, err := localapp.ResolvePaths(*options.home)
	if err != nil {
		return preparedRuntime{}, err
	}
	config, err := localapp.LoadOrCreateConfig(paths)
	if err != nil {
		return preparedRuntime{}, err
	}
	changed := false
	if *options.numbatSHA256 != "" {
		config.NumbatSHA256 = *options.numbatSHA256
		changed = true
	}
	if *options.versionMarker != "" {
		config.NumbatVersionMarker = *options.versionMarker
		changed = true
	}
	belayExecutable, _ := currentExecutablePath()
	packagedExecutable := belayExecutable
	if resolvedExecutable, resolveErr := filepath.EvalSymlinks(belayExecutable); resolveErr == nil {
		packagedExecutable = resolvedExecutable
	}
	if advertisedExecutable := os.Getenv("BELAY_EXECUTABLE_PATH"); advertisedExecutable != "" {
		advertisedExecutable, err = filepath.Abs(advertisedExecutable)
		if err != nil {
			return preparedRuntime{}, errors.New("BELAY_EXECUTABLE_PATH is invalid")
		}
		resolvedAdvertised, resolveErr := filepath.EvalSymlinks(advertisedExecutable)
		if resolveErr != nil ||
			filepath.Clean(resolvedAdvertised) != filepath.Clean(packagedExecutable) {
			return preparedRuntime{}, errors.New("BELAY_EXECUTABLE_PATH does not resolve to the running Belay binary")
		}
		belayExecutable = advertisedExecutable
	}
	binary, err := localapp.ResolveNumbatBinaryForExecutable(
		paths,
		config,
		*options.numbatBinary,
		packagedExecutable,
	)
	if err != nil {
		return preparedRuntime{}, err
	}
	switch {
	case config.NumbatSHA256 == "" && config.NumbatVersionMarker == "":
		if !*options.allowUnverified {
			pin, available, err := compiledNumbatPin()
			if err != nil {
				return preparedRuntime{}, err
			}
			if !available {
				return preparedRuntime{}, errors.New("Numbat is not pinned; provide --numbat-sha256 and --numbat-version-marker, or use --allow-unverified-numbat for development")
			}
			if !isPackagedSiblingNumbat(packagedExecutable, binary) {
				return preparedRuntime{}, errors.New("the compiled Numbat pin may bootstrap only the packaged sibling bin/numbat executable")
			}
			verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			materialized, verifyErr := localapp.MaterializePinnedNumbat(
				verifyCtx,
				paths,
				binary,
				pin,
			)
			cancel()
			if verifyErr != nil {
				return preparedRuntime{}, verifyErr
			}
			config.NumbatBinary = materialized
			config.NumbatSHA256 = pin.SHA256
			config.NumbatVersionMarker = pin.VersionMarker
			changed = true
			binary = materialized
		} else {
			binary, err = localapp.MaterializeUnverifiedNumbat(paths, binary)
			if err != nil {
				return preparedRuntime{}, err
			}
		}
	case config.NumbatSHA256 == "" || config.NumbatVersionMarker == "":
		return preparedRuntime{}, errors.New("Numbat pin is incomplete; configure both SHA-256 and version marker")
	default:
		verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		binary, err = localapp.MaterializePinnedNumbat(verifyCtx, paths, binary, numbat.BinaryPin{
			SHA256:        config.NumbatSHA256,
			VersionMarker: config.NumbatVersionMarker,
		})
		cancel()
		if err != nil {
			return preparedRuntime{}, err
		}
	}
	if config.NumbatBinary != binary {
		config.NumbatBinary = binary
		changed = true
	}
	if changed {
		if err := localapp.SaveConfig(paths.Config, config); err != nil {
			return preparedRuntime{}, err
		}
	}
	client, err := numbat.NewClient(binary)
	if err != nil {
		return preparedRuntime{}, err
	}
	return preparedRuntime{
		paths:           paths,
		config:          config,
		client:          client,
		belayExecutable: belayExecutable,
	}, nil
}

func compiledNumbatPin() (numbat.BinaryPin, bool, error) {
	checksum := bundledNumbatSHA256
	marker := bundledNumbatVersionMarker
	if checksum == "" && marker == "" {
		return numbat.BinaryPin{}, false, nil
	}
	if len(checksum) != 64 || marker == "" {
		return numbat.BinaryPin{}, false, errors.New("compiled Numbat pin is invalid")
	}
	decoded, err := hex.DecodeString(checksum)
	if err != nil || hex.EncodeToString(decoded) != checksum {
		return numbat.BinaryPin{}, false, errors.New("compiled Numbat pin is invalid")
	}
	if len(marker) > 256 {
		return numbat.BinaryPin{}, false, errors.New("compiled Numbat pin is invalid")
	}
	for _, character := range marker {
		if unicode.IsControl(character) {
			return numbat.BinaryPin{}, false, errors.New("compiled Numbat pin is invalid")
		}
	}
	return numbat.BinaryPin{SHA256: checksum, VersionMarker: marker}, true, nil
}

func isPackagedSiblingNumbat(belayExecutable, numbatExecutable string) bool {
	belayPath, err := filepath.Abs(belayExecutable)
	if err != nil || filepath.Base(filepath.Dir(belayPath)) != "bin" {
		return false
	}
	numbatPath, err := filepath.Abs(numbatExecutable)
	if err != nil {
		return false
	}
	return filepath.Clean(numbatPath) ==
		filepath.Clean(filepath.Join(filepath.Dir(belayPath), localapp.NumbatExecutableName()))
}

func runLocal(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("local", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeFlags := addLocalRuntimeFlags(flags)
	listen := flags.String("listen", "127.0.0.1:0", "loopback listen address")
	experienceValue := flags.String(
		"experience",
		string(localhttp.ExperienceCurrent),
		"Local experience: current or value-first",
	)
	installHooks := flags.Bool(
		"install-hooks",
		false,
		"explicitly install monitor-only live hooks for Codex, Claude Code, Cursor, "+
			"and Antigravity",
	)
	noScan := flags.Bool("no-scan", false, "skip the initial historical scan")
	if err := flags.Parse(args); err != nil {
		return err
	}
	experience, err := localhttp.ParseExperience(*experienceValue)
	if err != nil {
		return err
	}
	return launchLocal(ctx, localLaunchOptions{
		runtime:        runtimeFlags,
		listen:         *listen,
		experience:     experience,
		installHooks:   *installHooks,
		historicalScan: !*noScan,
		commandName:    "local",
	}, stdout, stderr)
}

func runQuickstart(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, `usage: belay quickstart [options]

Explicitly initializes private Belay Local state, verifies packaged Numbat,
installs monitor-only hooks for detected Codex, Claude Code, Cursor, and
Antigravity installations, installs user-scoped Local MCP registration and the
Belay skill for detected harnesses, imports local activity, scans local
history, and opens a loopback-only browser.
Full local transcripts are retained encrypted on-device. Optional outbound
requests are a de-identified usage ping and a content-free GitHub release
check every 18 hours; belay telemetry off and belay updates off disable them.
Use --no-mcp to opt out only from MCP registration.
Codex MCP add is disabled by default because its CLI can replace duplicate
names non-atomically. --allow-codex-mcp-add accepts that behavior after Belay
strictly verifies that no existing Codex entry named belay is present.

Options:`)
		flags.PrintDefaults()
	}
	runtimeFlags := addLocalRuntimeFlags(flags)
	listen := flags.String("listen", "127.0.0.1:0", "loopback listen address")
	experienceValue := flags.String(
		"experience",
		string(localhttp.ExperienceCurrent),
		"Local experience: current or value-first",
	)
	noOpen := flags.Bool("no-open", false, "print the Local URL without opening a browser")
	noMCP := flags.Bool("no-mcp", false, "do not modify Codex or Claude MCP configuration")
	noAnalyze := flags.Bool(
		"no-analyze",
		false,
		"skip semantic issue refinement with an installed Claude Code, Codex, "+
			"Cursor CLI, or Antigravity CLI harness",
	)
	analyzeAgent := flags.String(
		"analyze-agent",
		"auto",
		"semantic analysis harness: auto, claude, codex, cursor, or antigravity",
	)
	allowCodexMCPAdd := flags.Bool(
		"allow-codex-mcp-add",
		false,
		"accept Codex CLI non-atomic duplicate-name behavior after strict absence verification",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	experience, err := localhttp.ParseExperience(*experienceValue)
	if err != nil {
		return err
	}
	return launchLocal(ctx, localLaunchOptions{
		runtime:          runtimeFlags,
		listen:           *listen,
		experience:       experience,
		installHooks:     true,
		installMCP:       !*noMCP,
		allowCodexMCPAdd: *allowCodexMCPAdd,
		historicalScan:   true,
		analyze:          !*noAnalyze,
		analyzeAgent:     *analyzeAgent,
		openBrowser:      !*noOpen,
		commandName:      "quickstart",
	}, stdout, stderr)
}

func runLocalLaunch(
	ctx context.Context,
	options localLaunchOptions,
	stdout, stderr io.Writer,
) error {
	experience, err := normalizeLaunchExperience(options.experience)
	if err != nil {
		return err
	}
	options.experience = experience
	runtime, err := prepareRuntime(ctx, options.runtime)
	if err != nil {
		return err
	}
	startTelemetryActivity(ctx, runtime.paths.Root, stderr)
	store, err := openLocalCommandStore(runtime.paths.Database)
	if err != nil {
		return err
	}
	defer store.Close()

	onboardingComplete := true
	if options.installHooks {
		if !onboardHooks(ctx, runtime.client, runtime.paths, options.commandName, stderr) {
			onboardingComplete = false
		}
	}
	if options.installMCP {
		if options.allowCodexMCPAdd {
			fmt.Fprintln(
				stderr,
				"belay quickstart: Codex MCP add opt-in accepts non-atomic duplicate-name behavior",
			)
		}
		if !onboardMCPConfiguration(
			ctx,
			runtime,
			options.commandName,
			options.allowCodexMCPAdd,
			stderr,
		) {
			onboardingComplete = false
		}
	} else if options.commandName == "quickstart" {
		fmt.Fprintln(stderr, "belay quickstart: MCP setup skipped")
	}
	if options.commandName == "quickstart" && options.installHooks {
		if !onboardBelaySkills(ctx, runtime.client, stderr) {
			onboardingComplete = false
		}
	}
	if !onboardingComplete && options.commandName == "quickstart" {
		fmt.Fprintf(
			stderr,
			"belay %s: onboarding incomplete; Local will continue\n",
			options.commandName,
		)
	}
	initializationTracker := initialization.NewTracker(options.historicalScan, time.Now)
	token, err := localhttp.NewLaunchToken()
	if err != nil {
		return err
	}
	runtimeCtx, stopRuntime := context.WithCancel(ctx)
	defer stopRuntime()
	updates := updatecheck.New(runtime.paths.Root, buildVersion)
	running, err := startLocalHTTPServer(
		runtimeCtx,
		store,
		token,
		options.listen,
		initializationTracker,
		options.experience,
		updates,
	)
	if err != nil {
		return err
	}
	updates.Start(runtimeCtx)
	stopRecovery, recoveryDone := startLocalRecoveryRuntime(
		runtimeCtx,
		runtime.paths.Database,
		options.commandName,
		stderr,
	)
	stopLive, liveDone := startLocalLiveWithDedicatedStore(
		runtimeCtx,
		runtime.paths,
		runtime.config,
		options.commandName,
		stderr,
	)
	stopIntelligence, intelligenceDone := startLocalIntelligence(
		runtimeCtx,
		runtime.paths,
		options.commandName,
		stderr,
	)
	scanDone := closedSignal()
	if options.historicalScan {
		scanDone = localapp.StartHistoricalInitialization(
			runtimeCtx,
			initializationTracker,
			func(scanCtx context.Context) error {
				initializationStore, err := openLocalCommandStore(
					runtime.paths.Database,
				)
				if err != nil {
					fmt.Fprintf(
						stderr,
						"belay %s: history scan could not access local data; Local will continue\n",
						options.commandName,
					)
					return errors.New(
						"historical initialization store unavailable",
					)
				}
				defer initializationStore.Close()
				scanErr := runHistoricalScan(
					scanCtx,
					runtime,
					initializationStore,
					options.commandName,
					"historical scan",
					stderr,
				)
				if options.analyze && scanCtx.Err() == nil {
					if err := runQuickstartSemanticAnalysisWorker(
						scanCtx,
						runtime,
						initializationStore,
						options.analyzeAgent,
						stderr,
					); err != nil {
						fmt.Fprintf(
							stderr,
							"belay %s: AI-assisted analysis did not finish; deterministic results remain available\n",
							options.commandName,
						)
					}
				}
				return scanErr
			},
		)
	}
	browserURL := running.BrowserURL()
	fmt.Fprintln(stdout, browserURL)
	if options.openBrowser {
		attemptBrowserOpen(runtimeCtx, browserURL, options.commandName, stderr)
	}
	waitErr := running.Wait()
	stopRuntime()
	<-scanDone
	stopLive()
	<-liveDone
	stopIntelligence()
	<-intelligenceDone
	stopRecovery()
	<-recoveryDone
	return waitErr
}

func onboardBelaySkills(
	ctx context.Context,
	client *numbat.Client,
	stderr io.Writer,
) bool {
	discoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	inventory, _, discoveryErr := client.Discover(discoveryCtx)
	cancel()
	if discoveryErr != nil {
		fmt.Fprintf(
			stderr,
			"belay quickstart: skill %s\n",
			formatAgentStatuses(nil, "unavailable"),
		)
		return false
	}
	results, installErr := localapp.InstallBelaySkills(inventory)
	statuses := make(map[string]string, len(results))
	for _, result := range results {
		statuses[result.Agent] = result.Status
	}
	fmt.Fprintf(
		stderr,
		"belay quickstart: skill %s\n",
		formatAgentStatuses(statuses, "unavailable"),
	)
	return installErr == nil
}

// formatAgentStatuses renders one agent=status pair per supported harness in
// the fixed order Belay reports them, so the line stays parseable as harnesses
// are added. Agents missing from statuses fall back to fallback.
func formatAgentStatuses(statuses map[string]string, fallback string) string {
	var builder strings.Builder
	for _, agent := range numbat.SupportedAgents() {
		name := agent.String()
		status := fallback
		if value, ok := statuses[name]; ok && value != "" {
			status = value
		}
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(name)
		builder.WriteByte('=')
		builder.WriteString(status)
	}
	return builder.String()
}

func runQuickstartSemanticAnalysis(
	ctx context.Context,
	runtime preparedRuntime,
	store *local.Store,
	preferred string,
	stderr io.Writer,
) error {
	discoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	inventory, _, err := runtime.client.Discover(discoveryCtx)
	cancel()
	if err != nil {
		return err
	}
	harness, err := selectSemanticHarness(inventory, preferred)
	if err != nil {
		if errors.Is(err, errNoSemanticHarness) {
			fmt.Fprintln(
				stderr,
				"belay quickstart: semantic analysis skipped; no supported harness detected",
			)
			return nil
		}
		return err
	}
	if _, err := localapp.AnalyzeTranscriptIssuesOnce(ctx, store, 100); err != nil {
		return err
	}
	fmt.Fprintf(
		stderr,
		"belay quickstart: Analyzing with your %s\n",
		semanticHarnessDisplayName(harness),
	)
	_, err = runSemanticProjectAnalyses(
		ctx,
		store,
		harness,
	)
	return err
}

func closedSignal() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func runHistoricalScan(
	ctx context.Context,
	runtime preparedRuntime,
	store *local.Store,
	commandName string,
	label string,
	stderr io.Writer,
) error {
	numbatDone := make(chan error, 1)
	transcriptDone := make(chan error, 1)
	go func() {
		_, _, err := discoverAndScan(
			ctx,
			runtime.client,
			store,
			runtime.config,
		)
		numbatDone <- err
	}()
	go func() {
		transcriptDone <- scanHistoricalTranscripts(
			ctx,
			runtime.paths,
			store,
		)
	}()
	numbatErr := <-numbatDone
	transcriptErr := <-transcriptDone
	var identityErr error
	if ctx.Err() == nil {
		_, identityErr = store.ReconcileSessionIdentityLinks(ctx)
	}
	if numbatErr != nil && ctx.Err() == nil {
		fmt.Fprintf(
			stderr,
			"belay %s: %s incomplete; Local will continue\n",
			commandName,
			label,
		)
	}
	if transcriptErr != nil && ctx.Err() == nil {
		fmt.Fprintf(
			stderr,
			"belay %s: transcript scan incomplete; Local will continue\n",
			commandName,
		)
	}
	if identityErr != nil && ctx.Err() == nil {
		fmt.Fprintf(
			stderr,
			"belay %s: session evidence reconciliation incomplete; Local will continue\n",
			commandName,
		)
	}
	return errors.Join(numbatErr, transcriptErr, identityErr)
}

func pollTranscripts(
	ctx context.Context,
	paths localapp.Paths,
	store *local.Store,
	interval time.Duration,
	onError func(),
) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	run := func() {
		importErr := importRecentTranscriptsOnce(ctx, paths, store)
		reconciliationErr := reconcileMissionPackReceiptsOnce(ctx, store)
		trajectoryErr := deriveSessionTrajectoriesOnce(ctx, store)
		evaluationErr := evaluateExperienceApplicationsOnce(ctx, store)
		impactErr := deriveExperienceImpactsOnce(ctx, store)
		for _, err := range []error{
			importErr,
			reconciliationErr,
			trajectoryErr,
			evaluationErr,
			impactErr,
		} {
			if err != nil &&
				!errors.Is(err, context.Canceled) &&
				ctx.Err() == nil &&
				onError != nil {
				onError()
			}
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func runIntelligenceWorkers(
	ctx context.Context,
	paths localapp.Paths,
	store *local.Store,
	commandName string,
	stderr io.Writer,
) error {
	if _, err := store.ReconcileSessionIdentityLinks(ctx); err != nil &&
		!errors.Is(err, context.Canceled) &&
		ctx.Err() == nil {
		fmt.Fprintf(
			stderr,
			"belay %s: session evidence reconciliation delayed; background analysis will continue\n",
			commandName,
		)
	}
	transcriptDone := make(chan struct{})
	go func() {
		defer close(transcriptDone)
		pollTranscripts(
			ctx,
			paths,
			store,
			2*time.Second,
			func() {
				fmt.Fprintf(
					stderr,
					"belay %s: session update delayed; retrying\n",
					commandName,
				)
			},
		)
	}()
	analysisDone := make(chan struct{})
	go func() {
		defer close(analysisDone)
		localapp.PollTranscriptIssueAnalysis(
			ctx,
			store,
			2*time.Second,
			func(error) {
				fmt.Fprintf(
					stderr,
					"belay %s: issue analysis delayed; retrying\n",
					commandName,
				)
			},
		)
	}()
	habitsDone := make(chan struct{})
	go func() {
		defer close(habitsDone)
		habits, err := localapp.NewHabitDebriefService(store)
		if err != nil {
			return
		}
		localapp.PollHabitDebriefs(
			ctx,
			habits,
			0,
			0,
			func(error) {
				fmt.Fprintf(
					stderr,
					"belay %s: habits debrief delayed; retrying later\n",
					commandName,
				)
			},
		)
	}()
	<-ctx.Done()
	<-transcriptDone
	<-analysisDone
	<-habitsDone
	return ctx.Err()
}

func startLocalIntelligence(
	ctx context.Context,
	paths localapp.Paths,
	commandName string,
	stderr io.Writer,
) (context.CancelFunc, <-chan struct{}) {
	return startDedicatedIntelligence(
		ctx,
		paths,
		commandName,
		stderr,
		func() {
			fmt.Fprintf(
				stderr,
				"belay %s: background analysis delayed; Local remains available\n",
				commandName,
			)
		},
	)
}

func startLocalLiveWithDedicatedStore(
	ctx context.Context,
	paths localapp.Paths,
	config localapp.Config,
	commandName string,
	stderr io.Writer,
) (context.CancelFunc, <-chan struct{}) {
	liveCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		liveStore, err := openLocalCommandStore(paths.Database)
		if err != nil {
			fmt.Fprintf(
				stderr,
				"belay %s: live session updates unavailable; Local remains available\n",
				commandName,
			)
			return
		}
		defer liveStore.Close()
		pollLive(
			liveCtx,
			paths,
			liveStore,
			config,
			2*time.Second,
			func(error) {
				fmt.Fprintf(
					stderr,
					"belay %s: live session update delayed; retrying\n",
					commandName,
				)
			},
		)
	}()
	return cancel, done
}

func startDedicatedIntelligence(
	ctx context.Context,
	paths localapp.Paths,
	commandName string,
	stderr io.Writer,
	onError func(),
) (context.CancelFunc, <-chan struct{}) {
	return startIntelligenceRuntime(
		ctx,
		paths.Root,
		localapp.IntelligenceRuntimeOptions{
			RunOwner: func(ownerCtx context.Context) error {
				workerStore, err := openLocalCommandStore(paths.Database)
				if err != nil {
					return err
				}
				defer workerStore.Close()
				return runIntelligenceWorkerLoops(
					ownerCtx,
					paths,
					workerStore,
					commandName,
					stderr,
				)
			},
			OnError: func(error) {
				if onError != nil {
					onError()
				}
			},
		},
	)
}

func newLocalHTTPServer(
	store *local.Store,
	token string,
	experience localhttp.Experience,
	updates localhttp.UpdateService,
	providers ...initialization.Provider,
) (*localhttp.Server, error) {
	actions, err := localaction.New(store, store)
	if err != nil {
		return nil, err
	}
	costFixes, err := localapp.NewCostIssueFixService(store)
	if err != nil {
		return nil, err
	}
	experienceCompiler, err := localapp.NewExperienceCompilerService(store)
	if err != nil {
		return nil, err
	}
	missionPacks, err := localapp.NewMissionPackService(
		store,
		localapp.WithMissionPackExperienceSelector(experienceCompiler),
		localapp.WithMissionPackPreviewRepository(store),
	)
	if err != nil {
		return nil, err
	}
	habits, err := localapp.NewHabitDebriefService(store)
	if err != nil {
		return nil, err
	}
	readOptions := []readmodel.Option{
		readmodel.WithIssueRepository(store),
		readmodel.WithIssueCursorCodec(store),
		readmodel.WithFixMonitoringRepository(store),
		readmodel.WithTranscriptRepository(store),
		readmodel.WithUserInsightRepository(store),
		readmodel.WithHabitDebriefRepository(store),
		readmodel.WithEvidenceEpisodeRepository(store),
		readmodel.WithFusedSessionReads(experimentalFusedSessionReadsEnabled()),
		readmodel.WithUserInsightHarness(habits.Harness),
		readmodel.WithCostIssueRepository(store),
		readmodel.WithCostIssueRankingPolicy(costIssueRankingPolicy()),
	}
	if len(providers) > 0 && providers[0] != nil {
		readOptions = append(
			readOptions,
			readmodel.WithInitializationProvider(providers[0]),
		)
	}
	httpOptions := []localhttp.Option{
		localhttp.WithFixService(actions),
		localhttp.WithCostIssueFixService(costFixes),
		localhttp.WithMissionPackService(missionPacks),
		localhttp.WithHabitDebriefService(habits),
		localhttp.WithExperience(experience),
	}
	if updates != nil {
		httpOptions = append(httpOptions, localhttp.WithUpdateService(updates))
	}
	return localhttp.New(
		readmodel.New(store, readOptions...),
		token,
		httpOptions...,
	)
}

func normalizeLaunchExperience(
	experience localhttp.Experience,
) (localhttp.Experience, error) {
	if experience == "" {
		return localhttp.ExperienceCurrent, nil
	}
	return localhttp.ParseExperience(string(experience))
}

func newLocalMCPServer(store *local.Store) (*localmcp.Server, error) {
	if store == nil {
		return nil, errors.New("local MCP server requires a store")
	}
	fixService, err := localapp.NewCostIssueFixService(store)
	if err != nil {
		return nil, err
	}
	experienceCompiler, err := localapp.NewExperienceCompilerService(store)
	if err != nil {
		return nil, err
	}
	missionPacks, err := localapp.NewMissionPackService(
		store,
		localapp.WithMissionPackExperienceSelector(experienceCompiler),
		localapp.WithMissionPackPreviewRepository(store),
	)
	if err != nil {
		return nil, err
	}
	missionPackAcceptance, err := newMissionPackAcceptanceService(store)
	if err != nil {
		return nil, err
	}
	missionPackStatus, err := newMissionPackStatusService(store)
	if err != nil {
		return nil, err
	}
	experienceLearning, err := newExperienceLearningService(store)
	if err != nil {
		return nil, fmt.Errorf("construct experience learning service: %w", err)
	}
	return localmcp.New(readmodel.New(
		store,
		readmodel.WithIssueRepository(store),
		readmodel.WithIssueCursorCodec(store),
		readmodel.WithCostIssueRepository(store),
		readmodel.WithCostIssueRankingPolicy(costIssueRankingPolicy()),
		readmodel.WithEvidenceEpisodeRepository(store),
		readmodel.WithTranscriptRepository(store),
		readmodel.WithFusedSessionReads(experimentalFusedSessionReadsEnabled()),
	),
		localmcp.WithCostIssueFixService(fixService),
		localmcp.WithMissionPackService(missionPacks),
		withMissionPackAcceptanceService(missionPackAcceptance),
		withMissionPackStatusService(missionPackStatus),
		withExperienceLearningService(
			localmcp.AdaptExperienceLearningService(experienceLearning),
		),
	)
}

func experimentalFusedSessionReadsEnabled() bool {
	switch strings.ToLower(
		strings.TrimSpace(os.Getenv("BELAY_EXPERIMENTAL_FUSED_SESSION_READS")),
	) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func costIssueRankingPolicy() string {
	if strings.EqualFold(
		strings.TrimSpace(os.Getenv("BELAY_COST_ISSUE_RANKING")),
		"legacy",
	) {
		return issueintel.RankingPolicyLegacy
	}
	return issueintel.RankingPolicyDeterministic
}

func onboardLocalHooks(
	ctx context.Context,
	client *numbat.Client,
	paths localapp.Paths,
	stderr io.Writer,
) {
	_ = onboardHooks(ctx, client, paths, "local", stderr)
}

func onboardHooks(
	ctx context.Context,
	client *numbat.Client,
	paths localapp.Paths,
	commandName string,
	stderr io.Writer,
) bool {
	results, hookErr := localapp.ManageHooks(ctx, client, paths, "install")
	if commandName != "quickstart" {
		if err := writeJSON(stderr, results); err != nil {
			fmt.Fprintf(
				stderr,
				"belay %s: hook onboarding results could not be reported; Local remains available\n",
				commandName,
			)
		}
		if hookErr != nil {
			fmt.Fprintf(
				stderr,
				"belay %s: hook onboarding incomplete; Local remains available\n",
				commandName,
			)
		}
		return hookErr == nil
	}
	statuses := make(map[string]string, len(results))
	for _, result := range results {
		status := "configured"
		if result.Error != "" {
			status = "failed"
		}
		statuses[result.Agent] = status
	}
	fallback := "skipped_not_detected"
	if hookErr != nil && len(results) == 0 {
		fallback = "unavailable"
	}
	fmt.Fprintf(
		stderr,
		"belay %s: hooks %s\n",
		commandName,
		formatAgentStatuses(statuses, fallback),
	)
	return hookErr == nil
}

type cliInventoryRow struct {
	Agent    string `json:"agent"`
	Present  bool   `json:"present"`
	Detected bool   `json:"detected"`
}

type cliInventory struct {
	Rows          []cliInventoryRow          `json:"rows"`
	LaunchTargets map[string]cliInventoryRow `json:"launch_targets"`
}

func projectCLIInventory(inventory numbat.Inventory) cliInventory {
	supported := numbat.SupportedAgents()
	result := cliInventory{
		Rows:          make([]cliInventoryRow, 0, len(supported)),
		LaunchTargets: make(map[string]cliInventoryRow, len(supported)),
	}
	for _, agent := range supported {
		row, ok := inventory.LaunchTargets[agent]
		if !ok {
			continue
		}
		projected := cliInventoryRow{
			Agent:    agent.String(),
			Present:  row.Present,
			Detected: row.Detected,
		}
		result.Rows = append(result.Rows, projected)
		result.LaunchTargets[projected.Agent] = projected
	}
	return result
}

func runScan(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeFlags := addLocalRuntimeFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	runtime, err := prepareRuntime(ctx, runtimeFlags)
	if err != nil {
		return err
	}
	store, err := openLocalCommandStore(runtime.paths.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	recentTranscriptErr := drainScanTranscripts(ctx, runtime.paths, store)
	inventory, reports, err := discoverAndScan(ctx, runtime.client, store, runtime.config)
	transcriptErr := scanTranscripts(ctx, runtime.paths, store)
	appendedTranscriptErr := drainScanTranscripts(ctx, runtime.paths, store)
	_, identityErr := store.ReconcileSessionIdentityLinks(ctx)
	writeErr := writeJSON(stdout, map[string]any{
		"inventory": projectCLIInventory(inventory),
		"scans":     reports,
	})
	return errors.Join(
		recentTranscriptErr,
		err,
		transcriptErr,
		appendedTranscriptErr,
		identityErr,
		writeErr,
	)
}

func runAgents(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("agents", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeFlags := addLocalRuntimeFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	runtime, err := prepareRuntime(ctx, runtimeFlags)
	if err != nil {
		return err
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	inventory, _, err := runtime.client.Discover(discoveryCtx)
	if err != nil {
		return err
	}
	return writeJSON(stdout, projectCLIInventory(inventory))
}

func runHooks(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printHooksUsage(stderr)
		return errors.New("hooks requires install, status, or uninstall")
	}
	action := args[0]
	if isHelpArgument(action) {
		printHooksUsage(stdout)
		return nil
	}
	if action != "install" && action != "status" && action != "uninstall" {
		printHooksUsage(stderr)
		return fmt.Errorf("unknown hooks action %q", action)
	}
	flags := flag.NewFlagSet("hooks "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintf(stderr, "usage: belay hooks %s [flags]\n", action)
		fmt.Fprintln(
			stderr,
			"Targets: codex, claude (Claude Code), cursor, antigravity. Hooks are monitor-only.",
		)
		flags.PrintDefaults()
	}
	runtimeFlags := addLocalRuntimeFlags(flags)
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	runtime, err := prepareRuntime(ctx, runtimeFlags)
	if err != nil {
		return err
	}
	results, hookErr := localapp.ManageHooks(ctx, runtime.client, runtime.paths, action)
	if err := writeJSON(stdout, results); err != nil {
		return err
	}
	return hookErr
}

func printHooksUsage(writer io.Writer) {
	fmt.Fprintln(writer, `usage: belay hooks ACTION [flags]

Actions:
  status      show whether monitor-only hooks are installed for each agent
  install     install monitor-only hooks for detected agents
  uninstall   remove Belay hooks

Examples:
  belay hooks status
  belay hooks install

Run belay hooks ACTION -h for the flags of one action.`)
}

func isHelpArgument(argument string) bool {
	return argument == "help" || argument == "-h" || argument == "--help"
}

func runMCP(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "Belay Local state directory (default BELAY_HOME or ~/.belay)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	paths, err := localapp.ResolvePaths(*home)
	if err != nil {
		return err
	}
	if _, err := localapp.LoadOrCreateConfig(paths); err != nil {
		return err
	}
	startTelemetryActivity(ctx, paths.Root, nil)
	store, err := openLocalCommandStore(paths.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	server, err := newLocalMCPCommandServer(store)
	if err != nil {
		return err
	}
	serverDone := make(chan error, 1)
	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		serverDone <- server.RunStdio(ctx)
	}()
	<-serverStarted
	stopRecovery, recoveryDone := startMCPRecovery(
		ctx,
		paths.Database,
		stderr,
	)
	stopIntelligence, intelligenceDone := startMCPIntelligence(
		ctx,
		paths,
		stderr,
	)
	runErr := <-serverDone
	stopIntelligence()
	<-intelligenceDone
	stopRecovery()
	<-recoveryDone
	return runErr
}

const mcpRecoveryPendingMessage = "belay mcp: local data recovery delayed; reads remain available"

func startMCPRecovery(
	ctx context.Context,
	databasePath string,
	stderr io.Writer,
) (context.CancelFunc, <-chan struct{}) {
	recoveryStore, err := openLocalCommandStore(databasePath)
	if err != nil {
		fmt.Fprintln(stderr, mcpRecoveryPendingMessage)
		return func() {}, closedSignal()
	}
	stopRecovery, workerDone := startMCPRecoveryWorker(
		ctx,
		recoveryStore,
		func() {
			fmt.Fprintln(stderr, "belay mcp: issue analysis delayed; reads remain available")
		},
		func() {
			fmt.Fprintln(stderr, "belay mcp: fix history update delayed; reads remain available")
		},
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-workerDone
		_ = recoveryStore.Close()
	}()
	return stopRecovery, done
}

func startMCPIntelligence(
	ctx context.Context,
	paths localapp.Paths,
	stderr io.Writer,
) (context.CancelFunc, <-chan struct{}) {
	return startDedicatedIntelligence(
		ctx,
		paths,
		"mcp",
		stderr,
		func() {
			fmt.Fprintln(
				stderr,
				"belay mcp: background analysis delayed; reads remain available",
			)
		},
	)
}

func startLocalRecoveryWithDedicatedStore(
	ctx context.Context,
	databasePath string,
	commandName string,
	stderr io.Writer,
) (context.CancelFunc, <-chan struct{}) {
	recoveryCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		recoveryStore, err := openLocalCommandStore(databasePath)
		if err != nil {
			fmt.Fprintf(
				stderr,
				"belay %s: local data recovery delayed; Local remains available\n",
				commandName,
			)
			return
		}
		defer recoveryStore.Close()
		stopRecovery, recoveryDone := startMCPRecoveryWorker(
			recoveryCtx,
			recoveryStore,
			func() {
				fmt.Fprintf(
					stderr,
					"belay %s: issue analysis delayed; Local remains available\n",
					commandName,
				)
			},
			func() {
				fmt.Fprintf(
					stderr,
					"belay %s: fix history update delayed; Local remains available\n",
					commandName,
				)
			},
		)
		<-recoveryDone
		stopRecovery()
	}()
	return cancel, done
}

type localRecoveryActions struct {
	analysisStartup   func(context.Context) error
	monitoringStartup func(context.Context) error
	monitoringDrain   func(context.Context) error
}

const localRecoveryInterval = 2 * time.Second

func startLocalRecovery(
	ctx context.Context,
	store *local.Store,
	onAnalysisError func(),
	onMonitoringError func(),
) (context.CancelFunc, <-chan struct{}) {
	recoveryCtx, cancel := context.WithCancel(ctx)
	reconciler := analysis.NewReconciler(store)
	worker := analysis.NewRecurrenceWorker(store)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLocalRecovery(
			recoveryCtx,
			localRecoveryActions{
				analysisStartup: func(ctx context.Context) error {
					_, err := reconciler.Startup(ctx)
					return err
				},
				monitoringStartup: func(ctx context.Context) error {
					_, err := worker.Startup(ctx)
					return err
				},
				monitoringDrain: func(ctx context.Context) error {
					_, err := worker.Recover(ctx)
					return err
				},
			},
			localRecoveryInterval,
			onAnalysisError,
			onMonitoringError,
		)
	}()
	return cancel, done
}

func runLocalRecovery(
	ctx context.Context,
	actions localRecoveryActions,
	interval time.Duration,
	onAnalysisError func(),
	onMonitoringError func(),
) {
	if interval <= 0 {
		interval = localRecoveryInterval
	}
	analysisFailureReported := false
	for actions.analysisStartup != nil {
		err := actions.analysisStartup(ctx)
		if err == nil {
			break
		}
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		if !analysisFailureReported && onAnalysisError != nil {
			onAnalysisError()
			analysisFailureReported = true
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
	monitoringFailureReported := false
	for actions.monitoringStartup != nil {
		err := actions.monitoringStartup(ctx)
		if err == nil {
			break
		}
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		if !monitoringFailureReported && onMonitoringError != nil {
			onMonitoringError()
			monitoringFailureReported = true
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
	if actions.monitoringDrain == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	drainFailureReported := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := actions.monitoringDrain(ctx); err != nil &&
				!errors.Is(err, context.Canceled) &&
				onMonitoringError != nil {
				if !drainFailureReported {
					onMonitoringError()
					drainFailureReported = true
				}
				continue
			}
			drainFailureReported = false
		}
	}
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeFlags := addLocalRuntimeFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	runtime, err := prepareRuntime(ctx, runtimeFlags)
	if err != nil {
		return err
	}
	store, err := openLocalCommandStore(runtime.paths.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	discoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	inventory, command, discoverErr := runtime.client.Discover(discoveryCtx)
	analysisStatus, analysisErr := loadDoctorAnalysis(ctx, store)
	transcriptCoverage, transcriptErr := loadDoctorTranscriptCoverage(ctx, store)
	_, identityReconcileErr := store.ReconcileSessionIdentityLinks(ctx)
	identityAudit, identityReadErr := store.ReadSessionIdentityAudit(ctx)
	identityErr := errors.Join(identityReconcileErr, identityReadErr)
	episodeAudit, episodeErr := store.ReadEvidenceEpisodeAudit(ctx)
	status := map[string]any{
		"config":                 "ok",
		"encrypted_storage":      "ok",
		"numbat_pin":             "ok",
		"inventory":              projectCLIInventory(inventory),
		"detector_catalog":       detection.CatalogVersion,
		"intelligence_freshness": localapp.InspectIntelligenceReadiness(runtime.paths.Root),
	}
	if analysisErr != nil {
		status["analysis"] = "failed"
	} else {
		status["analysis"] = analysisStatus
	}
	if transcriptErr != nil {
		status["transcript_coverage"] = "failed"
	} else {
		status["transcript_coverage"] = transcriptCoverage
	}
	if identityErr != nil {
		status["session_identity"] = "failed"
	} else {
		status["session_identity"] = identityAudit
	}
	if episodeErr != nil {
		status["evidence_episodes"] = "failed"
	} else {
		status["evidence_episodes"] = episodeAudit
	}
	if discoverErr != nil {
		status["discovery"] = "failed"
		status["numbat_exit_code"] = command.ExitCode
	} else {
		status["discovery"] = "ok"
	}
	if err := writeJSON(stdout, status); err != nil {
		return err
	}
	return errors.Join(
		discoverErr,
		analysisErr,
		transcriptErr,
		identityErr,
		episodeErr,
	)
}

type doctorAnalysisStatus struct {
	CatalogVersion string                      `json:"catalog_version"`
	Coverage       model.IssueAnalysisCoverage `json:"coverage"`
}

func loadDoctorAnalysis(
	ctx context.Context,
	store *local.Store,
) (doctorAnalysisStatus, error) {
	page, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 1})
	if err != nil {
		return doctorAnalysisStatus{}, err
	}
	return doctorAnalysisStatus{
		CatalogVersion: detection.CatalogVersion,
		Coverage:       page.Analysis,
	}, nil
}

type doctorTranscriptRepository interface {
	TranscriptCoverage(context.Context) (transcript.CoverageCounts, error)
}

func loadDoctorTranscriptCoverage(
	ctx context.Context,
	repository doctorTranscriptRepository,
) (transcript.CoverageCounts, error) {
	return repository.TranscriptCoverage(ctx)
}
