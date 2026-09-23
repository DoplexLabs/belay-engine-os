package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/analysis"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/pipeline"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "belay:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("missing command")
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "belay %s (%s)\n", buildVersion, buildCommit)
		return nil
	case "quickstart":
		return runQuickstart(ctx, args[1:], stdout, stderr)
	case "local":
		return runLocal(ctx, args[1:], stdout, stderr)
	case "scan":
		return runScan(ctx, args[1:], stdout, stderr)
	case "agents":
		return runAgents(ctx, args[1:], stdout, stderr)
	case "analyze":
		return runAnalyze(ctx, args[1:], stdout, stderr)
	case "hooks":
		return runHooks(ctx, args[1:], stdout, stderr)
	case "mcp":
		return runMCP(ctx, args[1:], stderr)
	case "mcp-config":
		return runMCPConfig(ctx, args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr)
	case "telemetry":
		return runTelemetry(ctx, args[1:], stdout, stderr)
	case "updates":
		return runUpdates(ctx, args[1:], stdout, stderr)
	case "import":
		return runImport(ctx, args[1:], stdin, stdout, stderr)
	case "sessions":
		return runSessions(ctx, args[1:], stdout, stderr)
	case "timeline":
		return runTimeline(ctx, args[1:], stdout, stderr)
	case "prune":
		return runPrune(ctx, args[1:], stdout, stderr)
	case "verify-numbat":
		return runVerifyNumbat(ctx, args[1:], stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runImport(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "", "path to the Belay Local SQLite database")
	inputPath := flags.String("input", "-", "Numbat NDJSON file, or - for stdin")
	installationID := flags.String("installation-id", "", "random Belay installation ID")
	engineVersion := flags.String("engine-version", numbat.ResearchCommit, "pinned Numbat release or research commit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *installationID == "" {
		return errors.New("--db and --installation-id are required")
	}
	input := stdin
	var file *os.File
	if *inputPath != "-" {
		var err error
		file, err = os.Open(*inputPath)
		if err != nil {
			return err
		}
		defer file.Close()
		input = file
	}
	store, err := openLocalStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	report, err := pipeline.New(store, *installationID, *engineVersion).Import(ctx, input)
	if err != nil {
		return err
	}
	if _, reconcileErr := analysis.NewReconciler(store).Drain(ctx); reconcileErr != nil {
		fmt.Fprintln(stderr, "belay import: issue analysis pending")
	}
	return writeJSON(stdout, report)
}

func runSessions(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sessions", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "", "path to the Belay Local SQLite database (default <home>/belay.sqlite)")
	home := flags.String("home", "", "Belay Local state directory (default BELAY_HOME or ~/.belay)")
	limit := flags.Int("limit", 20, "maximum sessions")
	if err := flags.Parse(args); err != nil {
		return err
	}
	databasePath, err := resolveLocalDatabase(*dbPath, *home)
	if err != nil {
		return err
	}
	store, err := openLocalStore(databasePath)
	if err != nil {
		return err
	}
	defer store.Close()
	response, err := readmodel.New(store).ListSessions(ctx, *limit)
	if err != nil {
		return err
	}
	return writeJSON(stdout, response)
}

func runTimeline(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("timeline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "", "path to the Belay Local SQLite database (default <home>/belay.sqlite)")
	home := flags.String("home", "", "Belay Local state directory (default BELAY_HOME or ~/.belay)")
	sessionID := flags.String("session", "", "Belay session ID")
	limit := flags.Int("limit", 100, "maximum events")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return errors.New("--session is required")
	}
	databasePath, err := resolveLocalDatabase(*dbPath, *home)
	if err != nil {
		return err
	}
	store, err := openLocalStore(databasePath)
	if err != nil {
		return err
	}
	defer store.Close()
	response, err := readmodel.New(store).GetSessionTimeline(ctx, *sessionID, *limit)
	if err != nil {
		return err
	}
	return writeJSON(stdout, response)
}

func runPrune(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("prune", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "", "path to the Belay Local SQLite database (default <home>/belay.sqlite)")
	home := flags.String("home", "", "Belay Local state directory (default BELAY_HOME or ~/.belay)")
	maxAge := flags.Duration("max-age", 0, "delete payloads older than this age")
	maxEvents := flags.Int("max-events", 0, "retain at most this many canonical events")
	maxBytes := flags.Int64("max-bytes", 0, "retain at most this many encrypted payload bytes")
	apply := flags.Bool("apply", false, "apply the reported destructive pruning plan")
	if err := flags.Parse(args); err != nil {
		return err
	}
	databasePath, err := resolveLocalDatabase(*dbPath, *home)
	if err != nil {
		return err
	}
	policy := local.RetentionPolicy{
		MaxAge:          *maxAge,
		MaxEventCount:   *maxEvents,
		MaxPayloadBytes: *maxBytes,
	}
	store, err := openLocalStore(databasePath)
	if err != nil {
		return err
	}
	defer store.Close()
	if !*apply {
		diagnostics, err := store.RetentionDiagnostics(ctx, policy, time.Now().UTC())
		if err != nil {
			return err
		}
		return writeJSON(stdout, diagnostics)
	}
	result, err := store.Prune(ctx, policy, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func runVerifyNumbat(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("verify-numbat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	binary := flags.String("binary", "", "path to the stock Numbat binary")
	checksum := flags.String("sha256", "", "expected lowercase SHA-256")
	version := flags.String("version-marker", "", "required substring in numbat version output")
	timeout := flags.Duration("timeout", 5*time.Second, "version command timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *binary == "" || *checksum == "" || *version == "" {
		return errors.New("--binary, --sha256, and --version-marker are required")
	}
	verifyCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	return numbat.VerifyBinary(verifyCtx, *binary, numbat.BinaryPin{
		SHA256:        *checksum,
		VersionMarker: *version,
	})
}

// resolveLocalDatabase returns the explicit --db path, or else the database
// that Belay Local keeps under --home (BELAY_HOME or ~/.belay by default).
// The default is only used when it already exists, so a read command never
// creates an empty store in the wrong place.
func resolveLocalDatabase(dbPath, home string) (string, error) {
	if dbPath != "" {
		return dbPath, nil
	}
	paths, err := localapp.ResolvePaths(home)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(paths.Database); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf(
				"no Belay Local database at %s; run belay quickstart or belay scan first, or pass --db PATH",
				paths.Database,
			)
		}
		return "", fmt.Errorf("inspect Belay Local database: %w", err)
	}
	return paths.Database, nil
}

func openLocalStore(path string) (*local.Store, error) {
	return local.Open(path, local.NewPlatformKeyProvider(path))
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `usage: belay COMMAND

Commands:
  version         print the installed Belay version
  quickstart      consent to private setup, monitor-only hooks, scan, and browser
  local           scan agents and run the offline Local browser
  scan            discover and backfill supported local agent history
  agents          show the Codex, Claude Code, Cursor, and Antigravity agent inventory
  analyze         refine deterministic issues with your installed agent
  hooks           install, inspect, or remove monitor-only live hooks
  mcp             run the Local MCP server over stdio
  mcp-config      install, inspect, or remove Local MCP registration
  doctor          verify Local configuration, storage, and Numbat discovery
  telemetry       show or switch the de-identified usage ping (status, on, off)
  updates         show, check, or switch release notifications
  import          import strict Numbat 0.3.0 or 0.4.0 NDJSON into Belay Local
  sessions        list Local session summaries
  timeline        get one Local session timeline
  prune           inspect retention bounds; deletion requires --apply
  verify-numbat   verify a pinned Numbat binary checksum and version marker

belay quickstart changes only local Belay state and detected
Codex/Claude/Cursor/Antigravity hook, skill, and user-scoped MCP configuration.
Hooks are monitor-only. MCP can read local evidence and store bounded fix
proposals or application records, but it does not apply file changes. Belay
Local retains full local transcripts encrypted on-device. The only upload is a
de-identified install and daily active ping (see belay telemetry; BELAY_TELEMETRY=0 or DO_NOT_TRACK=1 turn it
off). Packaged builds also read public GitHub
release metadata at most every 18 hours (see belay updates). Use --no-mcp to
skip MCP registration and --no-open to print the loopback URL without opening
a browser.`)
}
