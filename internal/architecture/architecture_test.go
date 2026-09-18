package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPlaneImportBoundaries(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		plane := strings.Split(filepath.ToSlash(relative), "/")
		if len(plane) < 2 || plane[0] != "internal" {
			return nil
		}
		sourcePlane := plane[1]
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			target, _ := strconv.Unquote(imported.Path.Value)
			const prefix = "github.com/DoplexLabs/belay-engine/internal/"
			if !strings.HasPrefix(target, prefix) {
				continue
			}
			targetPlane := strings.Split(strings.TrimPrefix(target, prefix), "/")[0]
			if violatesBoundary(relative, sourcePlane, targetPlane) {
				t.Errorf("%s imports forbidden plane %s", relative, targetPlane)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func violatesBoundary(relative, source, target string) bool {
	switch source {
	case "acquisition":
		return target == "canonical" || target == "storage" || target == "presentation"
	case "canonical":
		// The version-specific mapper is the intentional acquisition→canonical
		// adapter. Pure canonical model packages remain acquisition-independent.
		if strings.Contains(filepath.ToSlash(relative), "canonical/numbatmap/") {
			return target == "storage" || target == "presentation"
		}
		return target == "acquisition" || target == "storage" || target == "presentation"
	case "storage":
		return target == "acquisition" || target == "presentation" || target == "pipeline"
	case "presentation":
		return target == "acquisition" || target == "storage" || target == "pipeline"
	}
	return false
}

var _ ast.Node
