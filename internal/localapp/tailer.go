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
	"os"
	"path/filepath"

	"github.com/DoplexLabs/belay-engine/internal/pipeline"
)

const (
	tailPrefixBytes = 4096
	maxTailBatch    = 16 << 20
)

type TailCursor struct {
	Offset       int64  `json:"offset"`
	Device       uint64 `json:"device"`
	Inode        uint64 `json:"inode"`
	PrefixBytes  int    `json:"prefix_bytes"`
	PrefixSHA256 string `json:"prefix_sha256"`
}

type TailResult struct {
	StartOffset int64           `json:"start_offset"`
	EndOffset   int64           `json:"end_offset"`
	Reset       bool            `json:"reset"`
	Import      pipeline.Report `json:"import"`
}

type StreamImporter interface {
	Import(context.Context, io.Reader) (pipeline.Report, error)
}

type ImporterFactory func(sequenceBase int64) StreamImporter

func ImportSpoolOnce(
	ctx context.Context,
	spoolPath string,
	cursorPath string,
	newImporter ImporterFactory,
) (TailResult, error) {
	return ImportSpoolOnceAfterCheckpoint(
		ctx,
		spoolPath,
		cursorPath,
		newImporter,
		nil,
	)
}

// ImportSpoolOnceAfterCheckpoint invokes after only after the durable cursor
// rename succeeds. Analysis callbacks therefore cannot run before acquisition
// progress is checkpointed.
func ImportSpoolOnceAfterCheckpoint(
	ctx context.Context,
	spoolPath string,
	cursorPath string,
	newImporter ImporterFactory,
	after func(context.Context, TailResult),
) (TailResult, error) {
	if newImporter == nil {
		return TailResult{}, errors.New("live spool importer factory is required")
	}
	file, identity, size, err := openRegularNoFollow(spoolPath)
	if errors.Is(err, os.ErrNotExist) {
		return TailResult{}, nil
	}
	if err != nil {
		return TailResult{}, errors.New("open live spool")
	}
	defer file.Close()

	cursor, err := loadTailCursor(cursorPath)
	if err != nil {
		return TailResult{}, err
	}
	result := TailResult{StartOffset: cursor.Offset}
	prefix := ""
	if cursor.PrefixBytes > 0 {
		prefix, err = readPrefix(file, cursor.PrefixBytes)
		if err != nil {
			return TailResult{}, err
		}
	}
	if cursor.Offset > size ||
		cursor.Device != 0 && (cursor.Device != identity.device || cursor.Inode != identity.inode) ||
		cursor.PrefixSHA256 != "" && cursor.PrefixSHA256 != prefix {
		cursor.Offset = 0
		cursor.PrefixBytes = 0
		cursor.PrefixSHA256 = ""
		result.StartOffset = 0
		result.Reset = true
	}
	if cursor.Offset == size {
		return result, nil
	}
	remaining := size - cursor.Offset
	if remaining > maxTailBatch {
		remaining = maxTailBatch
	}
	body := make([]byte, remaining)
	read, err := file.ReadAt(body, cursor.Offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return TailResult{}, fmt.Errorf("read live spool: %w", err)
	}
	body = body[:read]
	complete := bytes.LastIndexByte(body, '\n')
	if complete < 0 {
		if len(body) <= pipeline.MaxUpstreamRecordBytes {
			return result, nil
		}
		// A record larger than the upstream bound can never become valid. Let
		// the importer quarantine the bounded prefix and make forward progress.
		complete = len(body) - 1
		body = append(body[:complete+1], '\n')
	} else {
		body = body[:complete+1]
	}
	if len(body) == 0 {
		return result, nil
	}
	importer := newImporter(cursor.Offset)
	if importer == nil {
		return TailResult{}, errors.New("live spool importer is required")
	}
	report, err := importer.Import(ctx, bytes.NewReader(body))
	if err != nil {
		return TailResult{}, err
	}
	cursor.Offset += int64(complete + 1)
	cursor.Device = identity.device
	cursor.Inode = identity.inode
	cursor.PrefixBytes = tailPrefixBytes
	if cursor.Offset < int64(cursor.PrefixBytes) {
		cursor.PrefixBytes = int(cursor.Offset)
	}
	cursor.PrefixSHA256, err = readPrefix(file, cursor.PrefixBytes)
	if err != nil {
		return TailResult{}, err
	}
	if err := saveTailCursor(cursorPath, cursor); err != nil {
		return TailResult{}, err
	}
	result.EndOffset = cursor.Offset
	result.Import = report
	if after != nil {
		after(ctx, result)
	}
	return result, nil
}

func PrepareSpool(path string) error {
	directory := filepath.Dir(path)
	if err := ensurePrivateDirectory(directory); err != nil {
		return fmt.Errorf("create live spool directory: %w", err)
	}
	file, err := openOrCreatePrivateRegular(path)
	if err != nil {
		return errors.New("prepare live spool")
	}
	return file.Close()
}

func readPrefix(file *os.File, length int) (string, error) {
	if length <= 0 {
		return "", nil
	}
	if length > tailPrefixBytes {
		length = tailPrefixBytes
	}
	body := make([]byte, length)
	read, err := file.ReadAt(body, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read live spool identity: %w", err)
	}
	sum := sha256.Sum256(body[:read])
	return hex.EncodeToString(sum[:]), nil
}

func loadTailCursor(path string) (TailCursor, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return TailCursor{}, nil
	}
	if err != nil {
		return TailCursor{}, fmt.Errorf("read live cursor: %w", err)
	}
	var cursor TailCursor
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.Offset < 0 {
		return TailCursor{}, errors.New("parse live cursor")
	}
	return cursor, nil
}

func saveTailCursor(path string, cursor TailCursor) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create live cursor directory: %w", err)
	}
	body, err := json.Marshal(cursor)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".cursor-*.json")
	if err != nil {
		return fmt.Errorf("create live cursor: %w", err)
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
		return fmt.Errorf("open live cursor directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync live cursor directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close live cursor directory: %w", err)
	}
	return nil
}
