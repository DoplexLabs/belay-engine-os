// Command package-preview creates a deterministic tar.gz from a staged
// developer-preview directory using only the Go standard library.
package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type entry struct {
	path string
	name string
	info fs.FileInfo
}

func main() {
	source := flag.String("source", "", "staged package directory")
	output := flag.String("output", "", "output .tar.gz path")
	epoch := flag.String("epoch", "", "source date epoch")
	flag.Parse()

	if *source == "" || *output == "" || *epoch == "" {
		fatalf("-source, -output, and -epoch are required")
	}
	seconds, err := strconv.ParseInt(*epoch, 10, 64)
	if err != nil || seconds < 0 {
		fatalf("invalid source date epoch %q", *epoch)
	}
	if err := packageDirectory(*source, *output, time.Unix(seconds, 0).UTC()); err != nil {
		fatalf("%v", err)
	}
}

func packageDirectory(source, output string, modTime time.Time) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("inspect source: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	parent := filepath.Dir(source)
	var entries []entry
	err = filepath.Walk(source, func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in package: %s", path)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular package entry: %s", path)
		}
		name, err := filepath.Rel(parent, path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{
			path: path,
			name: filepath.ToSlash(name),
			info: info,
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk source: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporary := output + ".tmp"
	_ = os.Remove(temporary)
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(temporary)
		}
	}()

	gzipWriter, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	gzipWriter.Header.ModTime = modTime
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)

	for _, item := range entries {
		mode := int64(0o644)
		typeFlag := byte(tar.TypeReg)
		name := item.name
		if item.info.IsDir() {
			mode = 0o755
			typeFlag = tar.TypeDir
			name = strings.TrimSuffix(name, "/") + "/"
		} else if item.info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		header := &tar.Header{
			Name:       name,
			Mode:       mode,
			ModTime:    modTime,
			AccessTime: modTime,
			ChangeTime: modTime,
			Typeflag:   typeFlag,
			Uid:        0,
			Gid:        0,
			Uname:      "root",
			Gname:      "root",
			Format:     tar.FormatPAX,
		}
		if !item.info.IsDir() {
			header.Size = item.info.Size()
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("write archive header %s: %w", item.name, err)
		}
		if item.info.IsDir() {
			continue
		}
		input, err := os.Open(item.path)
		if err != nil {
			return fmt.Errorf("open package file %s: %w", item.name, err)
		}
		_, copyErr := io.Copy(tarWriter, input)
		closeErr := input.Close()
		if copyErr != nil {
			return fmt.Errorf("archive package file %s: %w", item.name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close package file %s: %w", item.name, closeErr)
		}
	}

	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	if err := os.Rename(temporary, output); err != nil {
		return fmt.Errorf("publish local archive: %w", err)
	}
	cleanup = false
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "package-preview: "+format+"\n", args...)
	os.Exit(1)
}
