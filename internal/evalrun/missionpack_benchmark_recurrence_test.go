package evalrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeMissionPackBenchmarkRecurrenceGroupsCodexFailures(t *testing.T) {
	root := t.TempDir()
	runs := make([]MissionPackBenchmarkRecurrenceRunInput, 0, 2)
	for index := 1; index <= 2; index++ {
		path := filepath.Join(root, fmt.Sprintf("codex-%d.jsonl", index))
		body := `{"type":"item.completed","item":{"id":"cmd-1","type":"command_execution","command":"/bin/zsh -lc './gradlew --offline smokeTest'","aggregated_output":"Exception in thread \"main\" java.io.FileNotFoundException: /tmp/cache.lock (Operation not permitted)\n","exit_code":1,"status":"completed"}}` + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, MissionPackBenchmarkRecurrenceRunInput{
			RunID:         fmt.Sprintf("run-%d", index),
			Harness:       MissionPackHarnessCodex,
			RawEventsPath: path,
		})
	}
	report, err := AnalyzeMissionPackBenchmarkRecurrence(
		context.Background(),
		MissionPackBenchmarkRecurrenceInput{
			SchemaVersion: MissionPackBenchmarkRecurrenceInputSchemaVersion,
			Runs:          runs,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ParserVersion != MissionPackBenchmarkCaptureParserVersion ||
		report.NativeParserVersion == "" ||
		len(report.FailedAttempts) != 2 ||
		len(report.Fingerprints) != 1 ||
		!report.Fingerprints[0].Recurrent ||
		report.Fingerprints[0].SessionCount != 2 ||
		report.Fingerprints[0].NormalizedFailureLine !=
			`exception in thread "main" java.io.filenotfoundexception: <tmp> (operation not permitted)` {
		t.Fatalf("recurrence report = %+v", report)
	}
}
