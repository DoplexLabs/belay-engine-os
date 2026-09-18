package evalrun

import (
	"reflect"
	"testing"
)

func TestBuildMissionPackBenchmarkScheduleIsBalancedAndDeterministic(
	t *testing.T,
) {
	plan := validMissionPackBenchmarkPlan()
	first, err := BuildMissionPackBenchmarkSchedule(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildMissionPackBenchmarkSchedule(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("schedules differ:\n%+v\n%+v", first, second)
	}
	if len(first.Entries) != 24 {
		t.Fatalf("entries = %d, want 24", len(first.Entries))
	}
	counts := make(map[string]int)
	runIDs := make(map[string]bool)
	for index, entry := range first.Entries {
		if entry.Sequence != index+1 {
			t.Fatalf("entry %d sequence = %d", index, entry.Sequence)
		}
		if entry.Phase != plan.Phase || entry.TaskID != plan.TaskID {
			t.Fatalf("entry protocol identity = %+v", entry)
		}
		key := string(entry.Harness) + "/" + string(entry.Arm)
		counts[key]++
		if runIDs[entry.RunID] {
			t.Fatalf("duplicate run id %q", entry.RunID)
		}
		runIDs[entry.RunID] = true
	}
	for _, harness := range []MissionPackBenchmarkHarness{
		MissionPackHarnessClaude,
		MissionPackHarnessCodex,
	} {
		for _, arm := range []MissionPackBenchmarkArm{
			MissionPackArmNoContext,
			MissionPackArmStatic,
			MissionPackArmHuman,
			MissionPackArmPack,
		} {
			if got := counts[string(harness)+"/"+string(arm)]; got != 3 {
				t.Fatalf("%s/%s count = %d, want 3", harness, arm, got)
			}
		}
	}
}

func TestBuildMissionPackBenchmarkScheduleRejectsProtocolDrift(
	t *testing.T,
) {
	tests := []struct {
		name   string
		change func(*MissionPackBenchmarkPlan)
	}{
		{
			name: "pilot treatment arm",
			change: func(plan *MissionPackBenchmarkPlan) {
				plan.Phase = "pilot"
			},
		},
		{
			name: "missing harness",
			change: func(plan *MissionPackBenchmarkPlan) {
				plan.Harnesses = plan.Harnesses[:1]
			},
		},
		{
			name: "duplicate arm",
			change: func(plan *MissionPackBenchmarkPlan) {
				plan.Arms[3] = MissionPackArmPack
				plan.Arms[2] = MissionPackArmPack
			},
		},
		{
			name: "short seed",
			change: func(plan *MissionPackBenchmarkPlan) {
				plan.RandomizationSeed = "short"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validMissionPackBenchmarkPlan()
			test.change(&plan)
			if _, err := BuildMissionPackBenchmarkSchedule(plan); err == nil {
				t.Fatal("BuildMissionPackBenchmarkSchedule() error = nil")
			}
		})
	}
}

func validMissionPackBenchmarkPlan() MissionPackBenchmarkPlan {
	return MissionPackBenchmarkPlan{
		SchemaVersion:     MissionPackBenchmarkPlanSchemaVersion,
		StudyID:           "belay-mp-v1",
		Phase:             "phase_b",
		TaskID:            "task_a",
		RandomizationSeed: "e16546310598ef48ffddcc1a8f18978400addf714f693b97bdfb7cf4dd20ca11",
		BlocksPerHarness:  3,
		Arms: []MissionPackBenchmarkArm{
			MissionPackArmNoContext,
			MissionPackArmStatic,
			MissionPackArmHuman,
			MissionPackArmPack,
		},
		Harnesses: []MissionPackBenchmarkHarness{
			MissionPackHarnessClaude,
			MissionPackHarnessCodex,
		},
	}
}
