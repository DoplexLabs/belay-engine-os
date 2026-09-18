package numbat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type BinaryPin struct {
	SHA256        string
	VersionMarker string
}

func VerifyBinary(ctx context.Context, path string, pin BinaryPin) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read numbat binary: %w", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); pin.SHA256 == "" || got != pin.SHA256 {
		return fmt.Errorf("numbat checksum mismatch")
	}
	output, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("run numbat version: %w", err)
	}
	if pin.VersionMarker == "" || !strings.Contains(string(output), pin.VersionMarker) {
		return fmt.Errorf("numbat version marker mismatch")
	}
	return nil
}
