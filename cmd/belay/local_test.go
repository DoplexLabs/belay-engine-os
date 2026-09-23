package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/detection"
	"github.com/DoplexLabs/belay-engine/internal/initialization"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/localhttp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/localmcp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestPrepareRuntimeBootstrapsVerifiedPackagedSiblingPin(t *testing.T) {
	home := t.TempDir()
	belayExecutable, numbatExecutable, checksum := packagedNumbatFixture(
		t,
		"numbat packaged-marker",
	)
	setCompiledNumbatTestState(t, checksum, "packaged-marker", belayExecutable)
	t.Setenv("BELAY_NUMBAT_BIN", "")

	runtime, err := prepareRuntime(
		context.Background(),
		testRuntimeFlags(home, "", "", "", false),
	)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.config.NumbatSHA256 != checksum ||
		runtime.config.NumbatVersionMarker != "packaged-marker" {
		t.Fatalf("bootstrapped config = %+v", runtime.config)
	}
	if runtime.config.NumbatBinary == numbatExecutable ||
		!strings.HasPrefix(runtime.config.NumbatBinary, runtime.paths.BundledBin+"-") {
		t.Fatalf("materialized binary = %q", runtime.config.NumbatBinary)
	}
	if info, err := os.Stat(runtime.config.NumbatBinary); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("materialized binary is unavailable: %v", err)
	}

	persisted, err := localapp.LoadOrCreateConfig(runtime.paths)
	if err != nil {
		t.Fatal(err)
	}
	if persisted != runtime.config {
		t.Fatalf("persisted config = %+v, want %+v", persisted, runtime.config)
	}
}

func TestPrepareRuntimeRefusesCompiledPinForNonSiblingBinary(t *testing.T) {
	home := t.TempDir()
	belayExecutable, _, _ := packagedNumbatFixture(t, "numbat packaged-marker")
	otherBinary := filepath.Join(t.TempDir(), "numbat")
	checksum := writeVersionedNumbat(t, otherBinary, "numbat packaged-marker")
	setCompiledNumbatTestState(t, checksum, "packaged-marker", belayExecutable)
	t.Setenv("BELAY_NUMBAT_BIN", otherBinary)

	_, err := prepareRuntime(
		context.Background(),
		testRuntimeFlags(home, "", "", "", false),
	)
	if err == nil || !strings.Contains(err.Error(), "packaged sibling") {
		t.Fatalf("prepareRuntime() error = %v, want sibling refusal", err)
	}
	config, loadErr := localapp.LoadOrCreateConfig(mustResolvePaths(t, home))
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if config.NumbatSHA256 != "" || config.NumbatVersionMarker != "" {
		t.Fatalf("failed bootstrap persisted compiled pin: %+v", config)
	}
}

func TestPrepareRuntimeRefusesBadPackagedHashAndVersion(t *testing.T) {
	tests := []struct {
		name   string
		hash   func(string) string
		marker string
	}{
		{
			name: "hash",
			hash: func(string) string {
				return strings.Repeat("0", 64)
			},
			marker: "packaged-marker",
		},
		{
			name:   "version",
			hash:   func(value string) string { return value },
			marker: "wrong-marker",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			belayExecutable, _, checksum := packagedNumbatFixture(
				t,
				"numbat packaged-marker",
			)
			setCompiledNumbatTestState(
				t,
				test.hash(checksum),
				test.marker,
				belayExecutable,
			)
			t.Setenv("BELAY_NUMBAT_BIN", "")

			_, err := prepareRuntime(
				context.Background(),
				testRuntimeFlags(home, "", "", "", false),
			)
			if err == nil {
				t.Fatal("prepareRuntime() succeeded with invalid packaged pin")
			}
			config, loadErr := localapp.LoadOrCreateConfig(mustResolvePaths(t, home))
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if config.NumbatSHA256 != "" || config.NumbatVersionMarker != "" {
				t.Fatalf("failed verification persisted pin: %+v", config)
			}
		})
	}
}

func TestPrepareRuntimePreservesExplicitPinForSourceBuild(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(t.TempDir(), "custom-numbat")
	checksum := writeVersionedNumbat(t, binary, "numbat source-marker")
	setCompiledNumbatTestState(
		t,
		"",
		"",
		filepath.Join(t.TempDir(), "bin", "belay"),
	)

	runtime, err := prepareRuntime(
		context.Background(),
		testRuntimeFlags(home, binary, checksum, "source-marker", false),
	)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.config.NumbatSHA256 != checksum ||
		runtime.config.NumbatVersionMarker != "source-marker" {
		t.Fatalf("explicit config = %+v", runtime.config)
	}
	if runtime.config.NumbatBinary == binary ||
		!strings.HasPrefix(runtime.config.NumbatBinary, runtime.paths.BundledBin+"-") {
		t.Fatalf("materialized binary = %q", runtime.config.NumbatBinary)
	}
}

func TestPrepareRuntimeMaterializesUnverifiedDevelopmentBinary(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "numbat")
	writeVersionedNumbat(t, source, "numbat development-marker")
	setCompiledNumbatTestState(
		t,
		"",
		"",
		filepath.Join(t.TempDir(), "bin", "belay"),
	)

	runtime, err := prepareRuntime(
		context.Background(),
		testRuntimeFlags(home, source, "", "", true),
	)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.config.NumbatBinary == source ||
		!strings.HasPrefix(runtime.config.NumbatBinary, runtime.paths.BundledBin+"-") {
		t.Fatalf("development binary = %q", runtime.config.NumbatBinary)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	reloaded, err := prepareRuntime(
		context.Background(),
		testRuntimeFlags(home, "", "", "", true),
	)
	if err != nil {
		t.Fatalf("prepareRuntime() after source removal: %v", err)
	}
	if reloaded.config.NumbatBinary != runtime.config.NumbatBinary {
		t.Fatalf(
			"reloaded development binary = %q, want %q",
			reloaded.config.NumbatBinary,
			runtime.config.NumbatBinary,
		)
	}
}

func TestQuickstartAndLocalLaunchModes(t *testing.T) {
	original := launchLocal
	t.Cleanup(func() { launchLocal = original })
	var captured []localLaunchOptions
	launchLocal = func(
		_ context.Context,
		options localLaunchOptions,
		_, _ io.Writer,
	) error {
		captured = append(captured, options)
		return nil
	}

	var stdout, stderr bytes.Buffer
	if err := run(
		context.Background(),
		[]string{"quickstart"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if err := run(
		context.Background(),
		[]string{"quickstart", "--no-open"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if err := run(
		context.Background(),
		[]string{"quickstart", "--no-mcp"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if err := run(
		context.Background(),
		[]string{"quickstart", "--allow-codex-mcp-add"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if err := runLocal(context.Background(), nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := runLocal(
		context.Background(),
		[]string{"--install-hooks", "--no-scan"},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 6 {
		t.Fatalf("captured launches = %d, want 6", len(captured))
	}
	for index, options := range captured {
		if options.experience != localhttp.ExperienceCurrent {
			t.Fatalf(
				"launch %d experience = %q, want %q",
				index,
				options.experience,
				localhttp.ExperienceCurrent,
			)
		}
	}
	if !captured[0].installHooks || !captured[0].historicalScan ||
		!captured[0].installMCP || !captured[0].openBrowser ||
		captured[0].allowCodexMCPAdd ||
		captured[0].commandName != "quickstart" {
		t.Fatalf("quickstart launch = %+v", captured[0])
	}
	if captured[1].openBrowser || !captured[1].installHooks ||
		!captured[1].installMCP || !captured[1].historicalScan ||
		captured[1].commandName != "quickstart" {
		t.Fatalf("quickstart --no-open launch = %+v", captured[1])
	}
	if !captured[2].installHooks || captured[2].installMCP ||
		!captured[2].historicalScan ||
		!captured[2].openBrowser {
		t.Fatalf("quickstart --no-mcp launch = %+v", captured[2])
	}
	if !captured[3].installHooks || !captured[3].installMCP ||
		!captured[3].allowCodexMCPAdd || !captured[3].historicalScan ||
		!captured[3].openBrowser ||
		captured[3].commandName != "quickstart" {
		t.Fatalf("quickstart Codex opt-in launch = %+v", captured[3])
	}
	if captured[4].installHooks || captured[4].installMCP ||
		captured[4].allowCodexMCPAdd || captured[4].openBrowser ||
		!captured[4].historicalScan ||
		captured[4].commandName != "local" {
		t.Fatalf("normal local launch changed = %+v", captured[4])
	}
	if !captured[5].installHooks || captured[5].installMCP ||
		captured[5].allowCodexMCPAdd || captured[5].historicalScan ||
		captured[5].openBrowser ||
		captured[5].commandName != "local" {
		t.Fatalf("explicit local flags changed = %+v", captured[5])
	}
}

func TestLocalCommandsPropagateValueFirstExperience(t *testing.T) {
	original := launchLocal
	t.Cleanup(func() { launchLocal = original })
	var captured []localLaunchOptions
	launchLocal = func(
		_ context.Context,
		options localLaunchOptions,
		_, _ io.Writer,
	) error {
		captured = append(captured, options)
		return nil
	}

	var stdout, stderr bytes.Buffer
	if err := runLocal(
		context.Background(),
		[]string{"--experience", "value-first"},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if err := runQuickstart(
		context.Background(),
		[]string{"--experience=value-first"},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 2 {
		t.Fatalf("captured launches = %d, want 2", len(captured))
	}
	for index, options := range captured {
		if options.experience != localhttp.ExperienceValueFirst {
			t.Fatalf(
				"launch %d experience = %q, want %q",
				index,
				options.experience,
				localhttp.ExperienceValueFirst,
			)
		}
	}
}

func TestLocalCommandsRejectInvalidExperienceBeforeLaunch(t *testing.T) {
	original := launchLocal
	t.Cleanup(func() { launchLocal = original })
	launches := 0
	launchLocal = func(
		_ context.Context,
		_ localLaunchOptions,
		_, _ io.Writer,
	) error {
		launches++
		return nil
	}

	tests := []struct {
		name string
		run  func(context.Context, []string, io.Writer, io.Writer) error
		args []string
	}{
		{
			name: "local invalid",
			run:  runLocal,
			args: []string{"--experience=PRIVATE_MODE_CANARY"},
		},
		{
			name: "local empty",
			run:  runLocal,
			args: []string{"--experience="},
		},
		{
			name: "quickstart invalid",
			run:  runQuickstart,
			args: []string{"--experience=PRIVATE_MODE_CANARY"},
		},
		{
			name: "quickstart empty",
			run:  runQuickstart,
			args: []string{"--experience="},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := test.run(
				context.Background(),
				test.args,
				&stdout,
				&stderr,
			)
			if !errors.Is(err, localhttp.ErrInvalidExperience) {
				t.Fatalf("error = %v, want fixed invalid-experience error", err)
			}
			if err.Error() != "invalid Local experience" {
				t.Fatalf("error text = %q", err.Error())
			}
			if strings.Contains(err.Error(), "PRIVATE_MODE_CANARY") ||
				strings.Contains(stderr.String(), "PRIVATE_MODE_CANARY") {
				t.Fatalf(
					"invalid experience reflected input: err=%q stderr=%q",
					err,
					stderr.String(),
				)
			}
		})
	}
	if launches != 0 {
		t.Fatalf("invalid experience launched Local %d times", launches)
	}
}

func TestRunLocalLaunchRejectsInvalidExperienceBeforeRuntimePreparation(t *testing.T) {
	err := runLocalLaunch(
		context.Background(),
		localLaunchOptions{
			experience: localhttp.Experience("PRIVATE_MODE_CANARY"),
		},
		io.Discard,
		io.Discard,
	)
	if !errors.Is(err, localhttp.ErrInvalidExperience) {
		t.Fatalf("runLocalLaunch() error = %v, want invalid experience", err)
	}
}

func TestQuickstartStartsBrowserBeforeHistoricalScanCompletes(t *testing.T) {
	previousScan := discoverAndScan
	previousTranscriptScan := scanTranscripts
	previousHistoricalTranscriptScan := scanHistoricalTranscripts
	previousRecentTranscriptImport := importRecentTranscriptsOnce
	previousStartServer := startLocalHTTPServer
	previousOpenStore := openLocalCommandStore
	previousOpenBrowser := openBrowser
	t.Cleanup(func() {
		discoverAndScan = previousScan
		scanTranscripts = previousTranscriptScan
		scanHistoricalTranscripts = previousHistoricalTranscriptScan
		importRecentTranscriptsOnce = previousRecentTranscriptImport
		startLocalHTTPServer = previousStartServer
		openLocalCommandStore = previousOpenStore
		openBrowser = previousOpenBrowser
	})

	scanStarted := make(chan struct{})
	releaseScan := make(chan struct{})
	discoverAndScan = func(
		context.Context,
		*numbat.Client,
		*local.Store,
		localapp.Config,
	) (numbat.Inventory, []localapp.HarnessScan, error) {
		close(scanStarted)
		<-releaseScan
		return numbat.Inventory{}, nil, errors.New("private scan failure")
	}
	fullTranscriptScanCalls := make(chan struct{}, 1)
	historicalTranscriptScanCalls := make(chan struct{}, 1)
	recentTranscriptImportCalls := make(chan struct{}, 8)
	scanTranscripts = func(
		context.Context,
		localapp.Paths,
		*local.Store,
	) error {
		fullTranscriptScanCalls <- struct{}{}
		return nil
	}
	scanHistoricalTranscripts = func(
		context.Context,
		localapp.Paths,
		*local.Store,
	) error {
		historicalTranscriptScanCalls <- struct{}{}
		return nil
	}
	importRecentTranscriptsOnce = func(
		context.Context,
		localapp.Paths,
		*local.Store,
	) error {
		select {
		case recentTranscriptImportCalls <- struct{}{}:
		default:
		}
		return nil
	}
	keyProvider := &doctorKeyProvider{keys: make(map[string][]byte)}
	openLocalCommandStore = func(path string) (*local.Store, error) {
		return local.OpenWithOptions(path, local.OpenOptions{KeyProvider: keyProvider})
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serverStarted := make(chan struct{})
	var provider initialization.Provider
	var experience localhttp.Experience
	startLocalHTTPServer = func(
		serverCtx context.Context,
		_ *local.Store,
		_ string,
		_ string,
		initializationProvider initialization.Provider,
		launchExperience localhttp.Experience,
		_ localhttp.UpdateService,
	) (runningLocalServer, error) {
		provider = initializationProvider
		experience = launchExperience
		close(serverStarted)
		return fakeRunningLocalServer{
			url: "http://127.0.0.1:12345/#token=test",
			wait: func() error {
				<-serverCtx.Done()
				return nil
			},
		}, nil
	}
	browserOpened := make(chan struct{})
	openBrowser = func(context.Context, string) error {
		close(browserOpened)
		return nil
	}
	binary := filepath.Join(t.TempDir(), "numbat")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runLocalLaunch(
			ctx,
			localLaunchOptions{
				runtime:        testRuntimeFlags(home, binary, "", "", true),
				listen:         "127.0.0.1:0",
				experience:     localhttp.ExperienceValueFirst,
				historicalScan: true,
				openBrowser:    true,
				commandName:    "quickstart",
			},
			&stdout,
			&stderr,
		)
	}()

	select {
	case <-serverStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Local server did not start")
	}
	select {
	case <-scanStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial scan did not start")
	}
	select {
	case <-browserOpened:
	case <-time.After(2 * time.Second):
		t.Fatal("browser did not open while historical scan was blocked")
	}
	if provider == nil {
		t.Fatal("Local server did not receive initialization provider")
	}
	if experience != localhttp.ExperienceValueFirst {
		t.Fatalf("Local server experience = %q", experience)
	}
	if status := provider.InitializationStatus(); status.State != initialization.StateInitializing {
		t.Fatalf("blocked scan status = %+v", status)
	}
	if !strings.Contains(stdout.String(), "http://127.0.0.1:") {
		t.Fatalf("quickstart Local URL = %q", stdout.String())
	}

	close(releaseScan)
	deadline := time.Now().Add(2 * time.Second)
	for provider.InitializationStatus().State == initialization.StateInitializing &&
		time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := provider.InitializationStatus()
	if status.State != initialization.StateDegraded ||
		status.ErrorCode == nil ||
		*status.ErrorCode != initialization.ErrorHistoricalScanIncomplete {
		t.Fatalf("failed scan status = %+v", status)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runLocalLaunch() error = %v", err)
	}
	if !strings.Contains(
		stderr.String(),
		"belay quickstart: historical scan incomplete; Local will continue",
	) {
		t.Fatalf("quickstart scan warning = %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "private scan failure") {
		t.Fatalf("quickstart scan warning leaked error payload: %q", stderr.String())
	}
	if len(fullTranscriptScanCalls) != 0 {
		t.Fatalf(
			"quickstart used full transcript scanner: scan=%d",
			len(fullTranscriptScanCalls),
		)
	}
	if len(historicalTranscriptScanCalls) != 1 {
		t.Fatalf(
			"historical transcript scans = %d, want 1",
			len(historicalTranscriptScanCalls),
		)
	}
	if len(recentTranscriptImportCalls) == 0 {
		t.Fatal("recent transcript tailer did not run")
	}
}

func TestLocalBackgroundWorkersNeverReuseHTTPReadStore(t *testing.T) {
	previousOpen := openLocalCommandStore
	previousStartServer := startLocalHTTPServer
	previousIntelligence := startIntelligenceRuntime
	previousWorkerLoops := runIntelligenceWorkerLoops
	previousRecoveryWorker := startMCPRecoveryWorker
	previousPollLive := pollLive
	previousDiscoverAndScan := discoverAndScan
	previousHistoricalTranscripts := scanHistoricalTranscripts
	previousSemantic := runQuickstartSemanticAnalysisWorker
	t.Cleanup(func() {
		openLocalCommandStore = previousOpen
		startLocalHTTPServer = previousStartServer
		startIntelligenceRuntime = previousIntelligence
		runIntelligenceWorkerLoops = previousWorkerLoops
		startMCPRecoveryWorker = previousRecoveryWorker
		pollLive = previousPollLive
		discoverAndScan = previousDiscoverAndScan
		scanHistoricalTranscripts = previousHistoricalTranscripts
		runQuickstartSemanticAnalysisWorker = previousSemantic
	})

	keyProvider := &doctorKeyProvider{keys: make(map[string][]byte)}
	stores := make([]*local.Store, 0, 5)
	for index := 0; index < 5; index++ {
		store, err := local.OpenWithOptions(
			filepath.Join(
				t.TempDir(),
				fmt.Sprintf("role-%d.sqlite", index),
			),
			local.OpenOptions{KeyProvider: keyProvider},
		)
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, store)
	}
	readStore := stores[0]
	openCalls := 0
	var openMu sync.Mutex
	openLocalCommandStore = func(string) (*local.Store, error) {
		openMu.Lock()
		defer openMu.Unlock()
		openCalls++
		if openCalls > len(stores) {
			return nil, errors.New("unexpected store open")
		}
		return stores[openCalls-1], nil
	}
	var serverStore *local.Store
	var provider initialization.Provider
	recoveryStoreCh := make(chan *local.Store, 1)
	liveStoreCh := make(chan *local.Store, 1)
	intelligenceStoreCh := make(chan *local.Store, 1)
	historicalNumbatStoreCh := make(chan *local.Store, 1)
	historicalTranscriptStoreCh := make(chan *local.Store, 1)
	semanticStoreCh := make(chan *local.Store, 1)
	var recoveryStore *local.Store
	var liveStore *local.Store
	var intelligenceStore *local.Store
	var historicalNumbatStore *local.Store
	var historicalTranscriptStore *local.Store
	var semanticStore *local.Store
	startLocalHTTPServer = func(
		_ context.Context,
		store *local.Store,
		_ string,
		_ string,
		initializationProvider initialization.Provider,
		_ localhttp.Experience,
		_ localhttp.UpdateService,
	) (runningLocalServer, error) {
		serverStore = store
		provider = initializationProvider
		return fakeRunningLocalServer{
			url: "http://127.0.0.1:12345/#token=test",
			wait: func() error {
				receive := func(worker <-chan *local.Store) *local.Store {
					select {
					case store := <-worker:
						return store
					case <-time.After(2 * time.Second):
						t.Fatal("background worker did not start")
						return nil
					}
				}
				recoveryStore = receive(recoveryStoreCh)
				liveStore = receive(liveStoreCh)
				intelligenceStore = receive(intelligenceStoreCh)
				historicalNumbatStore = receive(historicalNumbatStoreCh)
				historicalTranscriptStore = receive(
					historicalTranscriptStoreCh,
				)
				semanticStore = receive(semanticStoreCh)
				deadline := time.Now().Add(2 * time.Second)
				for provider.InitializationStatus().State ==
					initialization.StateInitializing &&
					time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				return nil
			},
		}, nil
	}
	startMCPRecoveryWorker = func(
		_ context.Context,
		store *local.Store,
		_ func(),
		_ func(),
	) (context.CancelFunc, <-chan struct{}) {
		recoveryStoreCh <- store
		return func() {}, closedSignal()
	}
	pollLive = func(
		_ context.Context,
		_ localapp.Paths,
		store *local.Store,
		_ localapp.Config,
		_ time.Duration,
		_ func(error),
	) {
		liveStoreCh <- store
	}
	startIntelligenceRuntime = func(
		ctx context.Context,
		_ string,
		options localapp.IntelligenceRuntimeOptions,
	) (context.CancelFunc, <-chan struct{}) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = options.RunOwner(ctx)
		}()
		return func() {}, done
	}
	runIntelligenceWorkerLoops = func(
		_ context.Context,
		_ localapp.Paths,
		store *local.Store,
		_ string,
		_ io.Writer,
	) error {
		intelligenceStoreCh <- store
		return nil
	}
	discoverAndScan = func(
		_ context.Context,
		_ *numbat.Client,
		store *local.Store,
		_ localapp.Config,
	) (numbat.Inventory, []localapp.HarnessScan, error) {
		historicalNumbatStoreCh <- store
		return numbat.Inventory{}, nil, nil
	}
	scanHistoricalTranscripts = func(
		_ context.Context,
		_ localapp.Paths,
		store *local.Store,
	) error {
		historicalTranscriptStoreCh <- store
		return nil
	}
	runQuickstartSemanticAnalysisWorker = func(
		_ context.Context,
		_ preparedRuntime,
		store *local.Store,
		_ string,
		_ io.Writer,
	) error {
		semanticStoreCh <- store
		return nil
	}

	binary := filepath.Join(t.TempDir(), "numbat")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runLocalLaunch(
		context.Background(),
		localLaunchOptions{
			runtime:        testRuntimeFlags(t.TempDir(), binary, "", "", true),
			commandName:    "quickstart",
			openBrowser:    false,
			historicalScan: true,
			analyze:        true,
		},
		io.Discard,
		io.Discard,
	); err != nil {
		t.Fatal(err)
	}
	if openCalls != 5 {
		t.Fatalf("store opens = %d, want one read and four worker stores", openCalls)
	}
	if serverStore != readStore {
		t.Fatal("Local HTTP server did not receive the read store")
	}
	if provider == nil {
		t.Fatal("Local HTTP server did not receive initialization tracking")
	}
	if status := provider.InitializationStatus(); status.State != initialization.StateReady {
		t.Fatalf("initialization status = %+v", status)
	}
	if historicalTranscriptStore != historicalNumbatStore ||
		semanticStore != historicalNumbatStore {
		t.Fatal("historical scan and semantic analysis did not share their dedicated initialization store")
	}
	roles := map[string]*local.Store{
		"http":         serverStore,
		"recovery":     recoveryStore,
		"live":         liveStore,
		"intelligence": intelligenceStore,
		"historical":   historicalNumbatStore,
	}
	seen := make(map[*local.Store]string)
	for role, store := range roles {
		if previous, exists := seen[store]; exists {
			t.Fatalf("%s and %s reused the same store", previous, role)
		}
		seen[store] = role
	}
}

type fakeRunningLocalServer struct {
	url  string
	wait func() error
}

func (server fakeRunningLocalServer) BrowserURL() string {
	return server.url
}

func (server fakeRunningLocalServer) Wait() error {
	return server.wait()
}

func TestQuickstartHelpStatesConsentAndPrivacyBoundary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runQuickstart(
		context.Background(),
		[]string{"--help"},
		&stdout,
		&stderr,
	)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("runQuickstart(--help) error = %v, want flag.ErrHelp", err)
	}
	for _, required := range []string{
		"monitor-only hooks",
		"Codex, Claude Code, Cursor, and\nAntigravity",
		"loopback-only",
		"Full local transcripts are retained encrypted on-device",
		"de-identified usage ping",
		"user-scoped Local MCP registration",
		"--no-mcp",
		"--allow-codex-mcp-add",
		"non-atomic",
		"-no-open",
	} {
		if !strings.Contains(stderr.String(), required) {
			t.Fatalf("quickstart help missing %q:\n%s", required, stderr.String())
		}
	}
}

func TestOnboardLocalHooksReportsPartialFailureAndReturns(t *testing.T) {
	root := t.TempDir()
	client := newLocalHookClient(t, `if [ "$1" = "agents" ]; then
	printf '%s\n' '[{"agent":"codex","present":true,"detected":true},{"agent":"Claude Code","present":true,"detected":true}]'
	exit 0
fi
if [ "$1" = "hook" ] && [ "$4" = "codex" ]; then
	printf '%s\n' 'private-command-output'
	exit 0
fi
printf '%s\n' 'token=private-hook-error' >&2
exit 8`)
	paths := localapp.Paths{
		Root:        root,
		CodexSpool:  filepath.Join(root, "live", "codex.ndjson"),
		ClaudeSpool: filepath.Join(root, "live", "claude.ndjson"),
	}
	var stderr bytes.Buffer

	onboardLocalHooks(context.Background(), client, paths, &stderr)

	output := stderr.String()
	for _, want := range []string{
		`"agent": "codex"`,
		`"agent": "claude"`,
		`"error": "hook operation failed"`,
		"belay local: hook onboarding incomplete; Local remains available",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("onboarding output missing %q:\n%s", want, output)
		}
	}
	for _, prohibited := range []string{"private-command-output", "private-hook-error"} {
		if strings.Contains(output, prohibited) {
			t.Errorf("onboarding output leaked %q:\n%s", prohibited, output)
		}
	}
}

func TestOnboardLocalHooksDiscoveryFailureReturns(t *testing.T) {
	client := newLocalHookClient(t, `printf '%s\n' 'password=private-discovery-error' >&2
exit 7`)
	root := t.TempDir()
	paths := localapp.Paths{
		Root:        root,
		CodexSpool:  filepath.Join(root, "live", "codex.ndjson"),
		ClaudeSpool: filepath.Join(root, "live", "claude.ndjson"),
	}
	var stderr bytes.Buffer

	onboardLocalHooks(context.Background(), client, paths, &stderr)

	output := stderr.String()
	if !strings.Contains(output, "belay local: hook onboarding incomplete; Local remains available") {
		t.Fatalf("onboarding output missing non-fatal warning:\n%s", output)
	}
	if strings.Contains(output, "private-discovery-error") {
		t.Fatalf("onboarding output leaked discovery stderr:\n%s", output)
	}
}

func TestQuickstartHookSummaryIsFixedAndPayloadFree(t *testing.T) {
	root := t.TempDir()
	client := newLocalHookClient(t, `if [ "$1" = "agents" ]; then
	printf '%s\n' '[{"agent":"codex","present":true,"detected":true},{"agent":"Claude Code","present":true,"detected":true}]'
	exit 0
fi
if [ "$1" = "hook" ] && [ "$4" = "codex" ]; then
	exit 0
fi
printf '%s\n' 'token=private-hook-error' >&2
exit 8`)
	paths := localapp.Paths{
		Root:        root,
		CodexSpool:  filepath.Join(root, "live", "codex.ndjson"),
		ClaudeSpool: filepath.Join(root, "live", "claude.ndjson"),
	}
	var stderr bytes.Buffer

	if onboardHooks(
		context.Background(),
		client,
		paths,
		"quickstart",
		&stderr,
	) {
		t.Fatal("quickstart hooks reported complete")
	}
	if got := stderr.String(); got !=
		"belay quickstart: hooks codex=configured claude=failed "+
			"cursor=skipped_not_detected antigravity=skipped_not_detected\n" {
		t.Fatalf("hook summary = %q", got)
	}
	if strings.Contains(stderr.String(), "private-hook-error") {
		t.Fatal("hook summary leaked subprocess output")
	}
}

func TestScanAgentsAndDoctorInventoryProjectionIsPayloadFree(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "missing-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(t.TempDir(), "missing-cursor"))
	const inventoryJSON = `[
		{
			"agent":"codex",
			"present":false,
			"detected":true,
			"hook":"PRIVATE_HOOK_CANARY",
			"wired":"PRIVATE_WIRING_CANARY",
			"setup_hint":"/Users/private/setup/PRIVATE_SETUP_HINT_CANARY",
			"future_field":"PRIVATE_UNKNOWN_FIELD_CANARY"
		},
		{
			"agent":"Claude Code",
			"present":false,
			"detected":false,
			"hook":"PRIVATE_CLAUDE_HOOK_CANARY",
			"wired":"PRIVATE_CLAUDE_WIRING_CANARY",
			"setup_hint":"PRIVATE_CLAUDE_SETUP_HINT_CANARY",
			"nested_unknown":{"secret":"PRIVATE_NESTED_UNKNOWN_CANARY"}
		},
		{
			"agent":"cursor",
			"present":false,
			"detected":true,
			"hook":"PRIVATE_CURSOR_HOOK_CANARY",
			"wired":"PRIVATE_CURSOR_WIRING_CANARY",
			"setup_hint":"PRIVATE_CURSOR_SETUP_HINT_CANARY",
			"future_field":"PRIVATE_CURSOR_UNKNOWN_FIELD_CANARY"
		},
		{
			"agent":"Antigravity",
			"present":false,
			"detected":true,
			"at_rest":"PRIVATE_ANTIGRAVITY_AT_REST_CANARY (hook-only)",
			"hook":"PRIVATE_ANTIGRAVITY_HOOK_CANARY",
			"wired":"PRIVATE_ANTIGRAVITY_WIRING_CANARY",
			"setup_hint":"install: numbat hook install --agent PRIVATE_ANTIGRAVITY_SETUP_HINT_CANARY",
			"future_field":"PRIVATE_ANTIGRAVITY_UNKNOWN_FIELD_CANARY"
		},
		{
			"agent":"future-agent",
			"present":true,
			"detected":true,
			"setup_hint":"PRIVATE_FUTURE_AGENT_CANARY",
			"future_path":"/Users/private/future-agent"
		}
	]`
	binary := writeInventoryNumbat(t, inventoryJSON)
	keyProvider := &doctorKeyProvider{keys: make(map[string][]byte)}
	previousOpen := openLocalCommandStore
	openLocalCommandStore = func(path string) (*local.Store, error) {
		return local.OpenWithOptions(path, local.OpenOptions{KeyProvider: keyProvider})
	}
	t.Cleanup(func() {
		openLocalCommandStore = previousOpen
	})

	commands := []struct {
		name string
		run  func(context.Context, []string, io.Writer, io.Writer) error
	}{
		{name: "scan", run: runScan},
		{name: "agents", run: runAgents},
		{name: "doctor", run: runDoctor},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := command.run(
				context.Background(),
				[]string{
					"--home", t.TempDir(),
					"--numbat", binary,
					"--allow-unverified-numbat",
				},
				&stdout,
				&stderr,
			)
			if err != nil {
				t.Fatalf("%s error = %v stderr=%s", command.name, err, stderr.String())
			}
			for _, forbidden := range []string{
				"setup_hint",
				"future_field",
				"nested_unknown",
				"PRIVATE_",
				"/Users/private",
				`"hook"`,
				`"wired"`,
				`"at_rest"`,
				"hook-only",
				"numbat hook install",
				"future-agent",
			} {
				if strings.Contains(stdout.String(), forbidden) {
					t.Fatalf("%s output leaked %q: %s", command.name, forbidden, stdout.String())
				}
			}

			var projected cliInventory
			if command.name == "agents" {
				if err := json.Unmarshal(stdout.Bytes(), &projected); err != nil {
					t.Fatalf("decode agents output: %v", err)
				}
			} else {
				var envelope struct {
					Inventory             cliInventory                   `json:"inventory"`
					IntelligenceFreshness localapp.IntelligenceReadiness `json:"intelligence_freshness"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatalf("decode %s output: %v", command.name, err)
				}
				projected = envelope.Inventory
				if command.name == "doctor" &&
					(envelope.IntelligenceFreshness.Lease != "available" ||
						envelope.IntelligenceFreshness.Ownership != "not_reported" ||
						len(envelope.IntelligenceFreshness.Workers) != 2) {
					t.Fatalf(
						"doctor intelligence readiness = %+v",
						envelope.IntelligenceFreshness,
					)
				}
			}
			if len(projected.Rows) != 4 ||
				projected.Rows[0] != (cliInventoryRow{
					Agent: "codex", Present: false, Detected: true,
				}) ||
				projected.Rows[1] != (cliInventoryRow{
					Agent: "claude", Present: false, Detected: false,
				}) ||
				projected.Rows[2] != (cliInventoryRow{
					Agent: "cursor", Present: false, Detected: true,
				}) ||
				projected.Rows[3] != (cliInventoryRow{
					Agent: "antigravity", Present: false, Detected: true,
				}) {
				t.Fatalf("%s projected rows = %+v", command.name, projected.Rows)
			}
			if len(projected.LaunchTargets) != 4 {
				t.Fatalf(
					"%s launch target count = %d, want 4",
					command.name,
					len(projected.LaunchTargets),
				)
			}
			for _, agent := range []string{"cursor", "antigravity"} {
				if projected.LaunchTargets[agent] != (cliInventoryRow{
					Agent: agent, Present: false, Detected: true,
				}) {
					t.Fatalf(
						"%s %s launch target = %+v",
						command.name,
						agent,
						projected.LaunchTargets[agent],
					)
				}
			}
		})
	}
}

func TestDoctorAnalysisStatusExposesCatalogAndCoverage(t *testing.T) {
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &doctorKeyProvider{keys: make(map[string][]byte)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	status, err := loadDoctorAnalysis(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if status.CatalogVersion != detection.CatalogVersion ||
		!status.Coverage.Complete ||
		status.Coverage.CurrentSessions != 0 {
		t.Fatalf("doctor analysis status = %+v", status)
	}
}

type fakeDoctorTranscriptRepository struct {
	coverage transcript.CoverageCounts
	err      error
}

func (repository fakeDoctorTranscriptRepository) TranscriptCoverage(
	context.Context,
) (transcript.CoverageCounts, error) {
	return repository.coverage, repository.err
}

func TestDoctorTranscriptCoverageExposesCompletePartialAndWithoutCounts(
	t *testing.T,
) {
	want := transcript.CoverageCounts{
		CanonicalSessions:  8,
		TranscriptSessions: 6,
		WithTranscript:     5,
		WithoutTranscript:  3,
		Complete:           3,
		Partial:            2,
		Live:               1,
	}
	got, err := loadDoctorTranscriptCoverage(
		context.Background(),
		fakeDoctorTranscriptRepository{coverage: want},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("doctor transcript coverage = %+v, want %+v", got, want)
	}
}

func TestTranscriptPollingIsIndependentAndPayloadFree(t *testing.T) {
	previousImport := importRecentTranscriptsOnce
	previousReconcile := reconcileMissionPackReceiptsOnce
	previousEvaluation := evaluateExperienceApplicationsOnce
	previousImpact := deriveExperienceImpactsOnce
	previousTrajectory := deriveSessionTrajectoriesOnce
	t.Cleanup(func() {
		importRecentTranscriptsOnce = previousImport
		reconcileMissionPackReceiptsOnce = previousReconcile
		evaluateExperienceApplicationsOnce = previousEvaluation
		deriveExperienceImpactsOnce = previousImpact
		deriveSessionTrajectoriesOnce = previousTrajectory
	})
	ctx, cancel := context.WithCancel(context.Background())
	calls := make(chan struct{}, 2)
	importRecentTranscriptsOnce = func(
		context.Context,
		localapp.Paths,
		*local.Store,
	) error {
		calls <- struct{}{}
		return errors.New("private transcript payload")
	}
	reconciliations := 0
	reconcileMissionPackReceiptsOnce = func(
		context.Context,
		*local.Store,
	) error {
		reconciliations++
		return nil
	}
	evaluateExperienceApplicationsOnce = func(
		context.Context,
		*local.Store,
	) error {
		return nil
	}
	deriveExperienceImpactsOnce = func(
		context.Context,
		*local.Store,
	) error {
		return nil
	}
	deriveSessionTrajectoriesOnce = func(
		context.Context,
		*local.Store,
	) error {
		return nil
	}
	warnings := 0
	pollTranscripts(
		ctx,
		localapp.Paths{},
		nil,
		time.Millisecond,
		func() {
			warnings++
			if warnings == 2 {
				cancel()
			}
		},
	)
	if len(calls) != 2 {
		t.Fatalf("transcript import calls = %d, want 2", len(calls))
	}
	if warnings != 2 {
		t.Fatalf("transcript polling warnings = %d, want 2", warnings)
	}
	if reconciliations != 2 {
		t.Fatalf("receipt reconciliations = %d, want 2", reconciliations)
	}
}

func TestTranscriptPollingImportsDespiteReceiptReconciliationError(t *testing.T) {
	previousImport := importRecentTranscriptsOnce
	previousReconcile := reconcileMissionPackReceiptsOnce
	previousEvaluation := evaluateExperienceApplicationsOnce
	previousImpact := deriveExperienceImpactsOnce
	previousTrajectory := deriveSessionTrajectoriesOnce
	t.Cleanup(func() {
		importRecentTranscriptsOnce = previousImport
		reconcileMissionPackReceiptsOnce = previousReconcile
		evaluateExperienceApplicationsOnce = previousEvaluation
		deriveExperienceImpactsOnce = previousImpact
		deriveSessionTrajectoriesOnce = previousTrajectory
	})
	ctx, cancel := context.WithCancel(context.Background())
	imports := 0
	importRecentTranscriptsOnce = func(
		context.Context,
		localapp.Paths,
		*local.Store,
	) error {
		imports++
		return nil
	}
	reconciliations := 0
	reconcileMissionPackReceiptsOnce = func(
		context.Context,
		*local.Store,
	) error {
		reconciliations++
		return errors.New("private receipt payload")
	}
	evaluations := 0
	trajectories := 0
	deriveSessionTrajectoriesOnce = func(
		context.Context,
		*local.Store,
	) error {
		trajectories++
		return nil
	}
	evaluateExperienceApplicationsOnce = func(
		context.Context,
		*local.Store,
	) error {
		evaluations++
		return nil
	}
	deriveExperienceImpactsOnce = func(
		context.Context,
		*local.Store,
	) error {
		return nil
	}
	warnings := 0
	pollTranscripts(
		ctx,
		localapp.Paths{},
		nil,
		time.Hour,
		func() {
			warnings++
			cancel()
		},
	)
	if imports != 1 {
		t.Fatalf("transcript imports = %d, want 1", imports)
	}
	if reconciliations != 1 {
		t.Fatalf("receipt reconciliations = %d, want 1", reconciliations)
	}
	if warnings != 1 {
		t.Fatalf("receipt reconciliation warnings = %d, want 1", warnings)
	}
	if evaluations != 1 {
		t.Fatalf("experience evaluations = %d, want 1", evaluations)
	}
	if trajectories != 1 {
		t.Fatalf("trajectory derivations = %d, want 1", trajectories)
	}
}

func TestTranscriptPollingEvaluatesAfterMaterializationAndFailsOpen(
	t *testing.T,
) {
	previousImport := importRecentTranscriptsOnce
	previousReconcile := reconcileMissionPackReceiptsOnce
	previousEvaluation := evaluateExperienceApplicationsOnce
	previousImpact := deriveExperienceImpactsOnce
	previousTrajectory := deriveSessionTrajectoriesOnce
	t.Cleanup(func() {
		importRecentTranscriptsOnce = previousImport
		reconcileMissionPackReceiptsOnce = previousReconcile
		evaluateExperienceApplicationsOnce = previousEvaluation
		deriveExperienceImpactsOnce = previousImpact
		deriveSessionTrajectoriesOnce = previousTrajectory
	})
	ctx, cancel := context.WithCancel(context.Background())
	var order []string
	importRecentTranscriptsOnce = func(
		context.Context,
		localapp.Paths,
		*local.Store,
	) error {
		order = append(order, "import")
		return nil
	}
	reconcileMissionPackReceiptsOnce = func(
		context.Context,
		*local.Store,
	) error {
		order = append(order, "reconcile_materialize")
		return nil
	}
	deriveSessionTrajectoriesOnce = func(
		context.Context,
		*local.Store,
	) error {
		order = append(order, "trajectory")
		return nil
	}
	evaluateExperienceApplicationsOnce = func(
		context.Context,
		*local.Store,
	) error {
		order = append(order, "evaluate")
		return errors.New("private evaluation payload")
	}
	deriveExperienceImpactsOnce = func(
		context.Context,
		*local.Store,
	) error {
		order = append(order, "impact")
		return nil
	}
	warnings := 0
	pollTranscripts(
		ctx,
		localapp.Paths{},
		nil,
		time.Hour,
		func() {
			warnings++
			cancel()
		},
	)
	if got, want := strings.Join(order, ","), "import,reconcile_materialize,trajectory,evaluate,impact"; got != want {
		t.Fatalf("poll order = %q, want %q", got, want)
	}
	if warnings != 1 {
		t.Fatalf("evaluation warnings = %d, want 1", warnings)
	}
}

func TestRunScanDrainsRecentBeforeAndAfterHistoricalBackfill(t *testing.T) {
	previousDiscover := discoverAndScan
	previousScan := scanTranscripts
	previousDrain := drainScanTranscripts
	previousOpen := openLocalCommandStore
	t.Cleanup(func() {
		discoverAndScan = previousDiscover
		scanTranscripts = previousScan
		drainScanTranscripts = previousDrain
		openLocalCommandStore = previousOpen
	})
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "missing-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(t.TempDir(), "missing-cursor"))

	recentBeforeErr := errors.New("recent transcript drain before scan")
	numbatErr := errors.New("numbat historical scan")
	transcriptErr := errors.New("historical transcript backfill")
	recentAfterErr := errors.New("recent transcript drain after backfill")
	var order []string
	recentDrains := 0
	drainScanTranscripts = func(
		context.Context,
		localapp.Paths,
		*local.Store,
	) error {
		recentDrains++
		if recentDrains == 1 {
			order = append(order, "drain_before")
			return recentBeforeErr
		}
		order = append(order, "drain_after")
		return recentAfterErr
	}
	reports := []localapp.HarnessScan{{
		Agent:    "claude",
		Detected: true,
		ExitCode: 0,
	}}
	discoverAndScan = func(
		context.Context,
		*numbat.Client,
		*local.Store,
		localapp.Config,
	) (numbat.Inventory, []localapp.HarnessScan, error) {
		order = append(order, "numbat")
		return numbat.Inventory{}, reports, numbatErr
	}
	scanTranscripts = func(
		context.Context,
		localapp.Paths,
		*local.Store,
	) error {
		order = append(order, "historical_transcripts")
		return transcriptErr
	}
	keyProvider := &doctorKeyProvider{keys: make(map[string][]byte)}
	openLocalCommandStore = func(path string) (*local.Store, error) {
		return local.OpenWithOptions(path, local.OpenOptions{
			KeyProvider: keyProvider,
		})
	}
	binary := writeInventoryNumbat(t, "[]")
	var stdout, stderr bytes.Buffer
	err := runScan(
		context.Background(),
		[]string{
			"--home", t.TempDir(),
			"--numbat", binary,
			"--allow-unverified-numbat",
		},
		&stdout,
		&stderr,
	)
	if got, want := strings.Join(order, ","), "drain_before,numbat,historical_transcripts,drain_after"; got != want {
		t.Fatalf("runScan order = %q, want %q", got, want)
	}
	for _, want := range []error{
		recentBeforeErr,
		numbatErr,
		transcriptErr,
		recentAfterErr,
	} {
		if !errors.Is(err, want) {
			t.Errorf("runScan() error = %v, want joined error %v", err, want)
		}
	}
	var output struct {
		Scans []localapp.HarnessScan `json:"scans"`
	}
	if decodeErr := json.Unmarshal(stdout.Bytes(), &output); decodeErr != nil {
		t.Fatalf(
			"runScan() JSON = %q, decode error = %v; stderr=%s",
			stdout.String(),
			decodeErr,
			stderr.String(),
		)
	}
	if len(output.Scans) != 1 ||
		output.Scans[0].Agent != reports[0].Agent ||
		output.Scans[0].Detected != reports[0].Detected ||
		output.Scans[0].ExitCode != reports[0].ExitCode {
		t.Fatalf("runScan() scans = %#v, want %#v", output.Scans, reports)
	}
}

func TestLocalHTTPWiresFixCapabilityExplicitly(t *testing.T) {
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &doctorKeyProvider{keys: make(map[string][]byte)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := newLocalHTTPServer(
		store,
		"launch-secret",
		localhttp.ExperienceCurrent,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues/iss_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/fixes",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("fix history status = %d body=%s", response.Code, response.Body.String())
	}
	monitoringRequest := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/fix-monitoring",
		nil,
	)
	monitoringRequest.RemoteAddr = "127.0.0.1:1234"
	monitoringRequest.Header.Set("Authorization", "Bearer launch-secret")
	monitoringResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(monitoringResponse, monitoringRequest)
	if monitoringResponse.Code != http.StatusServiceUnavailable ||
		!strings.Contains(
			monitoringResponse.Body.String(),
			"belay.local/monitoring-catchup-in-progress",
		) {
		t.Fatalf("monitoring capability status = %d body=%s",
			monitoringResponse.Code, monitoringResponse.Body.String())
	}

	coreOnly := readmodel.New(store)
	if _, err := coreOnly.ListFixMonitoring(
		context.Background(),
		readmodel.FixMonitoringListRequest{},
	); err == nil {
		t.Fatal("core-only readmodel unexpectedly exposes fix monitoring")
	}
}

func TestLocalMCPConstructsAndInjectsMissionPackAcceptance(t *testing.T) {
	previousConstructor := newMissionPackAcceptanceService
	previousOption := withMissionPackAcceptanceService
	t.Cleanup(func() {
		newMissionPackAcceptanceService = previousConstructor
		withMissionPackAcceptanceService = previousOption
	})
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &doctorKeyProvider{keys: make(map[string][]byte)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	constructorCalls := 0
	newMissionPackAcceptanceService = func(
		repository localapp.MissionPackAcceptanceRepository,
	) (*localapp.MissionPackAcceptanceService, error) {
		constructorCalls++
		return previousConstructor(repository)
	}
	optionCalls := 0
	withMissionPackAcceptanceService = func(
		service localmcp.MissionPackAcceptanceService,
	) localmcp.Option {
		optionCalls++
		return previousOption(service)
	}
	if _, err := newLocalMCPServer(store); err != nil {
		t.Fatal(err)
	}
	if constructorCalls != 1 {
		t.Fatalf("acceptance constructor calls = %d, want 1", constructorCalls)
	}
	if optionCalls != 1 {
		t.Fatalf("acceptance option calls = %d, want 1", optionCalls)
	}
}

func TestLocalMCPConstructsAndInjectsMissionPackStatus(t *testing.T) {
	previousConstructor := newMissionPackStatusService
	previousOption := withMissionPackStatusService
	t.Cleanup(func() {
		newMissionPackStatusService = previousConstructor
		withMissionPackStatusService = previousOption
	})
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &doctorKeyProvider{keys: make(map[string][]byte)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	constructorCalls := 0
	newMissionPackStatusService = func(
		repository localapp.MissionPackStatusRepository,
	) (*localapp.MissionPackStatusService, error) {
		constructorCalls++
		return previousConstructor(repository)
	}
	optionCalls := 0
	withMissionPackStatusService = func(
		service localmcp.MissionPackStatusService,
	) localmcp.Option {
		optionCalls++
		return previousOption(service)
	}
	if _, err := newLocalMCPServer(store); err != nil {
		t.Fatal(err)
	}
	if constructorCalls != 1 {
		t.Fatalf("status constructor calls = %d, want 1", constructorCalls)
	}
	if optionCalls != 1 {
		t.Fatalf("status option calls = %d, want 1", optionCalls)
	}
}

func TestLocalMCPConstructsAndInjectsExperienceLearning(t *testing.T) {
	previousConstructor := newExperienceLearningService
	previousOption := withExperienceLearningService
	t.Cleanup(func() {
		newExperienceLearningService = previousConstructor
		withExperienceLearningService = previousOption
	})
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &doctorKeyProvider{keys: make(map[string][]byte)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	constructorCalls := 0
	newExperienceLearningService = func(
		repository localapp.ExperienceLearningStore,
		options ...localapp.ExperienceLearningServiceOption,
	) (*localapp.ExperienceLearningService, error) {
		constructorCalls++
		return previousConstructor(repository, options...)
	}
	optionCalls := 0
	withExperienceLearningService = func(
		service localmcp.ExperienceLearningService,
	) localmcp.Option {
		optionCalls++
		if service == nil {
			t.Fatal("experience learning service was not adapted")
		}
		return previousOption(service)
	}
	if _, err := newLocalMCPServer(store); err != nil {
		t.Fatal(err)
	}
	if constructorCalls != 1 {
		t.Fatalf("learning constructor calls = %d, want 1", constructorCalls)
	}
	if optionCalls != 1 {
		t.Fatalf("learning option calls = %d, want 1", optionCalls)
	}
}

func TestRunLocalRecoveryOrdersAnalysisCatchupAndPeriodicDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls []string
	runLocalRecovery(
		ctx,
		localRecoveryActions{
			analysisStartup: func(context.Context) error {
				calls = append(calls, "analysis")
				return nil
			},
			monitoringStartup: func(context.Context) error {
				calls = append(calls, "monitoring-startup")
				return nil
			},
			monitoringDrain: func(context.Context) error {
				calls = append(calls, "monitoring-drain")
				cancel()
				return nil
			},
		},
		time.Millisecond,
		nil,
		nil,
	)
	if got := strings.Join(calls, ","); got !=
		"analysis,monitoring-startup,monitoring-drain" {
		t.Fatalf("recovery order = %q", got)
	}
}

func TestRunLocalRecoveryRetriesCatchupAndStopsCleanlyOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	analysisStartups := 0
	startups := 0
	warnings := 0
	runLocalRecovery(
		ctx,
		localRecoveryActions{
			analysisStartup: func(context.Context) error {
				analysisStartups++
				if analysisStartups == 1 {
					return errors.New("analysis pending")
				}
				return nil
			},
			monitoringStartup: func(context.Context) error {
				startups++
				if startups == 1 {
					return errors.New("catch-up pending")
				}
				return nil
			},
			monitoringDrain: func(context.Context) error {
				cancel()
				return context.Canceled
			},
		},
		time.Millisecond,
		func() { warnings++ },
		func() { warnings++ },
	)
	if analysisStartups != 2 || startups != 2 || warnings != 2 {
		t.Fatalf(
			"analysis/monitoring attempts/warnings = %d/%d/%d, want 2/2/2",
			analysisStartups,
			startups,
			warnings,
		)
	}

	canceled, stop := context.WithCancel(context.Background())
	stop()
	warnings = 0
	runLocalRecovery(
		canceled,
		localRecoveryActions{
			analysisStartup:   func(ctx context.Context) error { return ctx.Err() },
			monitoringStartup: func(ctx context.Context) error { return ctx.Err() },
		},
		time.Millisecond,
		func() { warnings++ },
		func() { warnings++ },
	)
	if warnings != 0 {
		t.Fatalf("cancellation emitted %d recovery warnings", warnings)
	}
}

func TestRunLocalRecoveryBoundsRepeatedDrainWarnings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	drains := 0
	warnings := 0
	runLocalRecovery(
		ctx,
		localRecoveryActions{
			analysisStartup:   func(context.Context) error { return nil },
			monitoringStartup: func(context.Context) error { return nil },
			monitoringDrain: func(context.Context) error {
				drains++
				switch drains {
				case 1, 2, 4:
					return errors.New("retryable drain failure")
				case 3:
					return nil
				default:
					cancel()
					return context.Canceled
				}
			},
		},
		time.Millisecond,
		nil,
		func() { warnings++ },
	)
	if drains != 5 || warnings != 2 {
		t.Fatalf("drains/warnings = %d/%d, want 5/2", drains, warnings)
	}
}

func TestRunMCPUsesSeparateReadAndRecoveryStores(t *testing.T) {
	previousOpen := openLocalCommandStore
	previousServer := newLocalMCPCommandServer
	previousRecovery := startMCPRecoveryWorker
	previousIntelligence := startIntelligenceRuntime
	t.Cleanup(func() {
		openLocalCommandStore = previousOpen
		newLocalMCPCommandServer = previousServer
		startMCPRecoveryWorker = previousRecovery
		startIntelligenceRuntime = previousIntelligence
	})
	startIntelligenceRuntime = func(
		context.Context,
		string,
		localapp.IntelligenceRuntimeOptions,
	) (context.CancelFunc, <-chan struct{}) {
		return func() {}, closedSignal()
	}

	keyProvider := &doctorKeyProvider{keys: make(map[string][]byte)}
	readStore, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "read.sqlite"),
		local.OpenOptions{KeyProvider: keyProvider},
	)
	if err != nil {
		t.Fatal(err)
	}
	recoveryStore, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "recovery.sqlite"),
		local.OpenOptions{KeyProvider: keyProvider},
	)
	if err != nil {
		t.Fatal(err)
	}

	openCalls := 0
	var openedPaths []string
	openLocalCommandStore = func(path string) (*local.Store, error) {
		openCalls++
		openedPaths = append(openedPaths, path)
		switch openCalls {
		case 1:
			return readStore, nil
		case 2:
			return recoveryStore, nil
		default:
			return nil, errors.New("unexpected store open")
		}
	}
	recoveryStarted := make(chan struct{})
	var serverStore *local.Store
	newLocalMCPCommandServer = func(
		store *local.Store,
	) (localMCPCommandServer, error) {
		serverStore = store
		return fakeLocalMCPCommandServer{
			run: func(context.Context) error {
				<-recoveryStarted
				return nil
			},
		}, nil
	}
	var workerStore *local.Store
	startMCPRecoveryWorker = func(
		_ context.Context,
		store *local.Store,
		_ func(),
		_ func(),
	) (context.CancelFunc, <-chan struct{}) {
		workerStore = store
		close(recoveryStarted)
		return func() {}, closedSignal()
	}

	if err := runMCP(
		context.Background(),
		[]string{"--home", t.TempDir()},
		io.Discard,
	); err != nil {
		t.Fatal(err)
	}
	if openCalls != 2 {
		t.Fatalf("store opens = %d, want 2", openCalls)
	}
	if openedPaths[0] != openedPaths[1] {
		t.Fatalf("store paths = %q, want the same database", openedPaths)
	}
	if serverStore != readStore {
		t.Fatal("MCP server did not receive the read store")
	}
	if workerStore != recoveryStore {
		t.Fatal("recovery worker did not receive the recovery store")
	}
	if serverStore == workerStore {
		t.Fatal("MCP reads and recovery share one store")
	}
}

func TestRunMCPServesReadsWhenRecoveryStoreOpenFails(t *testing.T) {
	previousOpen := openLocalCommandStore
	previousServer := newLocalMCPCommandServer
	previousRecovery := startMCPRecoveryWorker
	previousIntelligence := startIntelligenceRuntime
	t.Cleanup(func() {
		openLocalCommandStore = previousOpen
		newLocalMCPCommandServer = previousServer
		startMCPRecoveryWorker = previousRecovery
		startIntelligenceRuntime = previousIntelligence
	})
	startIntelligenceRuntime = func(
		context.Context,
		string,
		localapp.IntelligenceRuntimeOptions,
	) (context.CancelFunc, <-chan struct{}) {
		return func() {}, closedSignal()
	}

	readStore, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "read.sqlite"),
		local.OpenOptions{
			KeyProvider: &doctorKeyProvider{keys: make(map[string][]byte)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	recoveryOpenAttempted := make(chan struct{})
	openCalls := 0
	openLocalCommandStore = func(string) (*local.Store, error) {
		openCalls++
		if openCalls == 1 {
			return readStore, nil
		}
		close(recoveryOpenAttempted)
		return nil, errors.New("PRIVATE_RECOVERY_OPEN_CANARY")
	}
	newLocalMCPCommandServer = func(
		*local.Store,
	) (localMCPCommandServer, error) {
		return fakeLocalMCPCommandServer{
			run: func(context.Context) error {
				<-recoveryOpenAttempted
				return nil
			},
		}, nil
	}
	startMCPRecoveryWorker = func(
		context.Context,
		*local.Store,
		func(),
		func(),
	) (context.CancelFunc, <-chan struct{}) {
		t.Fatal("recovery worker started without a recovery store")
		return nil, nil
	}

	var stderr bytes.Buffer
	if err := runMCP(
		context.Background(),
		[]string{"--home", t.TempDir()},
		&stderr,
	); err != nil {
		t.Fatal(err)
	}
	if openCalls != 2 {
		t.Fatalf("store opens = %d, want 2", openCalls)
	}
	if stderr.String() != mcpRecoveryPendingMessage+"\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "PRIVATE_RECOVERY_OPEN_CANARY") {
		t.Fatal("recovery store error leaked to stderr")
	}
}

func TestRunMCPServesReadsWhileIntelligenceLeaseIsContended(t *testing.T) {
	previousOpen := openLocalCommandStore
	previousServer := newLocalMCPCommandServer
	previousRecovery := startMCPRecoveryWorker
	t.Cleanup(func() {
		openLocalCommandStore = previousOpen
		newLocalMCPCommandServer = previousServer
		startMCPRecoveryWorker = previousRecovery
	})

	home := t.TempDir()
	ownerStarted := make(chan struct{})
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	stopOwner, ownerDone := localapp.StartIntelligenceRuntime(
		ownerCtx,
		home,
		localapp.IntelligenceRuntimeOptions{
			RetryInterval: 10 * time.Millisecond,
			RunOwner: func(ctx context.Context) error {
				close(ownerStarted)
				<-ctx.Done()
				return ctx.Err()
			},
		},
	)
	t.Cleanup(func() {
		cancelOwner()
		stopOwner()
		<-ownerDone
	})
	select {
	case <-ownerStarted:
	case <-time.After(time.Second):
		t.Fatal("lease owner did not start")
	}

	keyProvider := &doctorKeyProvider{keys: make(map[string][]byte)}
	readStore, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "read.sqlite"),
		local.OpenOptions{KeyProvider: keyProvider},
	)
	if err != nil {
		t.Fatal(err)
	}
	recoveryStore, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "recovery.sqlite"),
		local.OpenOptions{KeyProvider: keyProvider},
	)
	if err != nil {
		t.Fatal(err)
	}
	openCalls := 0
	openLocalCommandStore = func(string) (*local.Store, error) {
		openCalls++
		switch openCalls {
		case 1:
			return readStore, nil
		case 2:
			return recoveryStore, nil
		default:
			return nil, errors.New("intelligence store opened without lease")
		}
	}
	readsAvailable := make(chan struct{})
	newLocalMCPCommandServer = func(
		*local.Store,
	) (localMCPCommandServer, error) {
		return fakeLocalMCPCommandServer{
			run: func(context.Context) error {
				close(readsAvailable)
				return nil
			},
		}, nil
	}
	startMCPRecoveryWorker = func(
		context.Context,
		*local.Store,
		func(),
		func(),
	) (context.CancelFunc, <-chan struct{}) {
		return func() {}, closedSignal()
	}

	started := time.Now()
	if err := runMCP(
		context.Background(),
		[]string{"--home", home},
		io.Discard,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readsAvailable:
	default:
		t.Fatal("MCP reads were not made available")
	}
	if time.Since(started) > time.Second {
		t.Fatal("contended intelligence lease blocked MCP startup")
	}
	if openCalls != 2 {
		t.Fatalf("store opens = %d, want read and recovery only", openCalls)
	}
}

type doctorKeyProvider struct {
	keys map[string][]byte
}

type fakeLocalMCPCommandServer struct {
	run func(context.Context) error
}

func (s fakeLocalMCPCommandServer) RunStdio(ctx context.Context) error {
	return s.run(ctx)
}

func (provider *doctorKeyProvider) Load(
	_ context.Context,
	storeID string,
) ([]byte, error) {
	key, ok := provider.keys[storeID]
	if !ok {
		return nil, local.ErrKeyNotFound
	}
	return append([]byte(nil), key...), nil
}

func (provider *doctorKeyProvider) Create(
	_ context.Context,
	storeID string,
) ([]byte, error) {
	key := bytes.Repeat([]byte{0x72}, 32)
	provider.keys[storeID] = key
	return append([]byte(nil), key...), nil
}

func testRuntimeFlags(
	home string,
	binary string,
	checksum string,
	marker string,
	allowUnverified bool,
) localRuntimeFlags {
	return localRuntimeFlags{
		home:            &home,
		numbatBinary:    &binary,
		numbatSHA256:    &checksum,
		versionMarker:   &marker,
		allowUnverified: &allowUnverified,
	}
}

func setCompiledNumbatTestState(
	t *testing.T,
	checksum string,
	marker string,
	executable string,
) {
	t.Helper()
	oldChecksum := bundledNumbatSHA256
	oldMarker := bundledNumbatVersionMarker
	oldExecutable := currentExecutablePath
	bundledNumbatSHA256 = checksum
	bundledNumbatVersionMarker = marker
	currentExecutablePath = func() (string, error) { return executable, nil }
	t.Cleanup(func() {
		bundledNumbatSHA256 = oldChecksum
		bundledNumbatVersionMarker = oldMarker
		currentExecutablePath = oldExecutable
	})
}

func packagedNumbatFixture(t *testing.T, versionOutput string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	belayExecutable := filepath.Join(bin, "belay")
	numbatExecutable := filepath.Join(bin, "numbat")
	checksum := writeVersionedNumbat(t, numbatExecutable, versionOutput)
	return belayExecutable, numbatExecutable, checksum
}

func writeVersionedNumbat(t *testing.T, path, versionOutput string) string {
	t.Helper()
	body := []byte(fmt.Sprintf(`#!/bin/sh
if [ "$1" = "version" ]; then
	printf '%%s\n' %q
	exit 0
fi
exit 0
`, versionOutput))
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:])
}

func mustResolvePaths(t *testing.T, root string) localapp.Paths {
	t.Helper()
	paths, err := localapp.ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func newLocalHookClient(t *testing.T, body string) *numbat.Client {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake-numbat")
	script := fmt.Sprintf("#!/bin/sh\n%s\n", body)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := numbat.NewClient(binary)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeInventoryNumbat(t *testing.T, inventory string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake-numbat-inventory")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "agents" ]; then
	printf '%%b\n' %q
	exit 0
fi
exit 2
`, inventory)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestHooksHelpNamesEverySupportedHarnessTarget(t *testing.T) {
	for _, action := range []string{"install", "status", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runHooks(
				context.Background(),
				[]string{action, "--help"},
				&stdout,
				&stderr,
			)
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("runHooks(%s --help) error = %v, want flag.ErrHelp", action, err)
			}
			for _, required := range []string{
				"codex",
				"claude (Claude Code)",
				"cursor",
				"antigravity",
				"monitor-only",
			} {
				if !strings.Contains(stderr.String(), required) {
					t.Fatalf("hooks %s help missing %q:\n%s", action, required, stderr.String())
				}
			}
		})
	}
}
