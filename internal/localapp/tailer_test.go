package localapp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/pipeline"
)

type recordingImporter struct {
	bodies [][]byte
}

func (r *recordingImporter) Import(_ context.Context, input io.Reader) (pipeline.Report, error) {
	body, err := io.ReadAll(input)
	if err != nil {
		return pipeline.Report{}, err
	}
	r.bodies = append(r.bodies, body)
	return pipeline.Report{Lines: 1}, nil
}

func TestImportSpoolOnceWaitsForCompleteLineAndCheckpoints(t *testing.T) {
	root := t.TempDir()
	spool := filepath.Join(root, "live.ndjson")
	cursor := filepath.Join(root, "live.cursor.json")
	if err := os.WriteFile(spool, []byte(`{"record_type":"event"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	importer := &recordingImporter{}
	factory := func(int64) StreamImporter { return importer }
	result, err := ImportSpoolOnce(context.Background(), spool, cursor, factory)
	if err != nil {
		t.Fatal(err)
	}
	if result.EndOffset != 0 || len(importer.bodies) != 0 {
		t.Fatalf("partial line was imported: result=%+v bodies=%q", result, importer.bodies)
	}
	file, err := os.OpenFile(spool, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = ImportSpoolOnce(context.Background(), spool, cursor, factory)
	if err != nil {
		t.Fatal(err)
	}
	if result.EndOffset == 0 || len(importer.bodies) != 1 {
		t.Fatalf("complete line was not imported: result=%+v bodies=%q", result, importer.bodies)
	}
	result, err = ImportSpoolOnce(context.Background(), spool, cursor, factory)
	if err != nil {
		t.Fatal(err)
	}
	if result.EndOffset != 0 || len(importer.bodies) != 1 {
		t.Fatalf("checkpoint replayed input: result=%+v bodies=%q", result, importer.bodies)
	}
}

func TestImportSpoolOnceDetectsReplacement(t *testing.T) {
	root := t.TempDir()
	spool := filepath.Join(root, "live.ndjson")
	cursor := filepath.Join(root, "live.cursor.json")
	importer := &recordingImporter{}
	factory := func(int64) StreamImporter { return importer }
	if err := os.WriteFile(spool, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportSpoolOnce(context.Background(), spool, cursor, factory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spool, []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ImportSpoolOnce(context.Background(), spool, cursor, factory)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reset {
		t.Fatalf("replacement did not reset cursor: %+v", result)
	}
	if len(importer.bodies) != 2 || string(importer.bodies[1]) != "replacement\n" {
		t.Fatalf("replacement body = %q", importer.bodies)
	}
}

func TestImportSpoolOnceRunsAnalysisOnlyAfterCursorSave(t *testing.T) {
	root := t.TempDir()
	spool := filepath.Join(root, "live.ndjson")
	cursorPath := filepath.Join(root, "live.cursor.json")
	if err := os.WriteFile(spool, []byte("record\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	importer := &recordingImporter{}
	callbackCalled := false
	result, err := ImportSpoolOnceAfterCheckpoint(
		context.Background(),
		spool,
		cursorPath,
		func(int64) StreamImporter { return importer },
		func(_ context.Context, imported TailResult) {
			callbackCalled = true
			cursor, loadErr := loadTailCursor(cursorPath)
			if loadErr != nil {
				t.Errorf("cursor was not readable before analysis: %v", loadErr)
				return
			}
			if cursor.Offset != imported.EndOffset || cursor.Offset == 0 {
				t.Errorf(
					"cursor offset during analysis = %d, imported end = %d",
					cursor.Offset,
					imported.EndOffset,
				)
			}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !callbackCalled || result.EndOffset == 0 {
		t.Fatalf("callback/result = %t/%+v", callbackCalled, result)
	}
}

func TestPrepareSpoolRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := PrepareSpool(link); err == nil {
		t.Fatal("PrepareSpool accepted a symlink")
	}
}
