package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/telemetry"
)

func TestTelemetryCommandSwitchesAndReportsStatus(t *testing.T) {
	home := t.TempDir()
	runStatus := func(args ...string) telemetry.Status {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), append([]string{"telemetry"}, args...), nil, &stdout, &stderr); err != nil {
			t.Fatalf("telemetry %v: %v (%s)", args, err, stderr.String())
		}
		var status telemetry.Status
		if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
			t.Fatalf("telemetry %v output is not JSON: %s", args, stdout.String())
		}
		return status
	}
	initial := runStatus("status", "--home", home)
	if initial.TelemetryID == "" || len(initial.Fields) != 8 || initial.Endpoint != telemetry.DefaultEndpoint {
		t.Fatalf("unexpected initial status: %+v", initial)
	}
	if initial.Enabled {
		t.Fatal("dev test builds must report telemetry disabled")
	}
	off := runStatus("off", "--home", home)
	if off.Enabled || !strings.Contains(off.Reason, "opted out") || off.TelemetryID != initial.TelemetryID {
		t.Fatalf("unexpected status after off: %+v", off)
	}
	on := runStatus("on", "--home", home)
	if strings.Contains(on.Reason, "opted out") {
		t.Fatalf("on should clear the opt-out: %+v", on)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"telemetry", "bogus", "--home", home}, nil, &stdout, &stderr); err == nil {
		t.Fatal("unknown telemetry action must fail")
	}
	usage := new(bytes.Buffer)
	printUsage(usage)
	if !strings.Contains(usage.String(), "telemetry") || strings.Contains(usage.String(), "Nothing is uploaded") {
		t.Fatalf("usage text must describe the ping honestly: %s", usage.String())
	}
}
