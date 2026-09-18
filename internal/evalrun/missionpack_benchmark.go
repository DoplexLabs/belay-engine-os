package evalrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	MissionPackBenchmarkPlanSchemaVersion     = "belay.missionpack-benchmark-plan.v1"
	MissionPackBenchmarkScheduleSchemaVersion = "belay.missionpack-benchmark-schedule.v1"
)

type MissionPackBenchmarkArm string

const (
	MissionPackArmNoContext MissionPackBenchmarkArm = "N"
	MissionPackArmStatic    MissionPackBenchmarkArm = "S"
	MissionPackArmHuman     MissionPackBenchmarkArm = "H"
	MissionPackArmPack      MissionPackBenchmarkArm = "P"
)

type MissionPackBenchmarkHarness string

const (
	MissionPackHarnessClaude MissionPackBenchmarkHarness = "claude"
	MissionPackHarnessCodex  MissionPackBenchmarkHarness = "codex"
)

type MissionPackBenchmarkPlan struct {
	SchemaVersion     string                        `json:"schema_version"`
	StudyID           string                        `json:"study_id"`
	Phase             string                        `json:"phase"`
	TaskID            string                        `json:"task_id"`
	RandomizationSeed string                        `json:"randomization_seed"`
	BlocksPerHarness  int                           `json:"blocks_per_harness"`
	Arms              []MissionPackBenchmarkArm     `json:"arms"`
	Harnesses         []MissionPackBenchmarkHarness `json:"harnesses"`
}

type MissionPackBenchmarkSchedule struct {
	SchemaVersion string                              `json:"schema_version"`
	PlanSHA256    string                              `json:"plan_sha256"`
	Entries       []MissionPackBenchmarkScheduleEntry `json:"entries"`
}

type MissionPackBenchmarkScheduleEntry struct {
	Sequence int                         `json:"sequence"`
	Block    int                         `json:"block"`
	Phase    string                      `json:"phase"`
	TaskID   string                      `json:"task_id"`
	Harness  MissionPackBenchmarkHarness `json:"harness"`
	Arm      MissionPackBenchmarkArm     `json:"arm"`
	RunID    string                      `json:"run_id"`
}

func BuildMissionPackBenchmarkSchedule(
	plan MissionPackBenchmarkPlan,
) (MissionPackBenchmarkSchedule, error) {
	if err := validateMissionPackBenchmarkPlan(plan); err != nil {
		return MissionPackBenchmarkSchedule{}, err
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return MissionPackBenchmarkSchedule{}, fmt.Errorf(
			"encode mission-pack benchmark plan: %w",
			err,
		)
	}
	digest := sha256.Sum256(body)
	result := MissionPackBenchmarkSchedule{
		SchemaVersion: MissionPackBenchmarkScheduleSchemaVersion,
		PlanSHA256:    hex.EncodeToString(digest[:]),
		Entries: make(
			[]MissionPackBenchmarkScheduleEntry,
			0,
			plan.BlocksPerHarness*len(plan.Harnesses)*len(plan.Arms),
		),
	}
	sequence := 0
	for block := 1; block <= plan.BlocksPerHarness; block++ {
		harnesses := orderedBenchmarkHarnesses(
			plan.Harnesses,
			fmt.Sprintf(
				"%s|%s|%s|block:%d|harness",
				plan.RandomizationSeed,
				plan.Phase,
				plan.TaskID,
				block,
			),
		)
		for _, harness := range harnesses {
			arms := orderedBenchmarkArms(
				plan.Arms,
				fmt.Sprintf(
					"%s|%s|%s|block:%d|harness:%s|arm",
					plan.RandomizationSeed,
					plan.Phase,
					plan.TaskID,
					block,
					harness,
				),
			)
			for _, arm := range arms {
				sequence++
				result.Entries = append(
					result.Entries,
					MissionPackBenchmarkScheduleEntry{
						Sequence: sequence,
						Block:    block,
						Phase:    plan.Phase,
						TaskID:   plan.TaskID,
						Harness:  harness,
						Arm:      arm,
						RunID: fmt.Sprintf(
							"%s-%s-%s-b%03d-%s-%s",
							plan.StudyID,
							plan.TaskID,
							plan.Phase,
							block,
							harness,
							strings.ToLower(string(arm)),
						),
					},
				)
			}
		}
	}
	return result, nil
}

func validateMissionPackBenchmarkPlan(
	plan MissionPackBenchmarkPlan,
) error {
	if plan.SchemaVersion != MissionPackBenchmarkPlanSchemaVersion {
		return errors.New("unsupported mission-pack benchmark plan schema")
	}
	if !boundedBenchmarkIdentifier(plan.StudyID, 3, 80) {
		return errors.New("mission-pack benchmark study_id is invalid")
	}
	if !boundedBenchmarkIdentifier(plan.TaskID, 2, 64) {
		return errors.New("mission-pack benchmark task_id is invalid")
	}
	switch plan.Phase {
	case "pilot", "phase_a", "phase_b", "phase_c":
	default:
		return errors.New("mission-pack benchmark phase is invalid")
	}
	if len(plan.RandomizationSeed) < 32 ||
		len(plan.RandomizationSeed) > 128 {
		return errors.New(
			"mission-pack benchmark randomization_seed must be 32-128 characters",
		)
	}
	if plan.BlocksPerHarness < 1 || plan.BlocksPerHarness > 100 {
		return errors.New(
			"mission-pack benchmark blocks_per_harness must be between 1 and 100",
		)
	}
	if !sameBenchmarkHarnesses(plan.Harnesses, []MissionPackBenchmarkHarness{
		MissionPackHarnessClaude,
		MissionPackHarnessCodex,
	}) {
		return errors.New(
			"mission-pack benchmark requires exactly Claude and Codex harnesses",
		)
	}
	wantArms := []MissionPackBenchmarkArm{MissionPackArmNoContext}
	if plan.Phase == "phase_b" || plan.Phase == "phase_c" {
		wantArms = []MissionPackBenchmarkArm{
			MissionPackArmNoContext,
			MissionPackArmStatic,
			MissionPackArmHuman,
			MissionPackArmPack,
		}
	}
	if !sameBenchmarkArms(plan.Arms, wantArms) {
		return fmt.Errorf(
			"mission-pack benchmark phase %s requires arms %v",
			plan.Phase,
			wantArms,
		)
	}
	return nil
}

func boundedBenchmarkIdentifier(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '-' ||
			char == '_' {
			continue
		}
		return false
	}
	return true
}

func sameBenchmarkArms(
	got []MissionPackBenchmarkArm,
	want []MissionPackBenchmarkArm,
) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[MissionPackBenchmarkArm]int, len(got))
	for _, value := range got {
		counts[value]++
	}
	for _, value := range want {
		if counts[value] != 1 {
			return false
		}
	}
	return len(counts) == len(want)
}

func sameBenchmarkHarnesses(
	got []MissionPackBenchmarkHarness,
	want []MissionPackBenchmarkHarness,
) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[MissionPackBenchmarkHarness]int, len(got))
	for _, value := range got {
		counts[value]++
	}
	for _, value := range want {
		if counts[value] != 1 {
			return false
		}
	}
	return len(counts) == len(want)
}

func orderedBenchmarkArms(
	values []MissionPackBenchmarkArm,
	material string,
) []MissionPackBenchmarkArm {
	result := append([]MissionPackBenchmarkArm(nil), values...)
	sort.Slice(result, func(left, right int) bool {
		leftDigest := sha256.Sum256(
			[]byte(material + "|" + string(result[left])),
		)
		rightDigest := sha256.Sum256(
			[]byte(material + "|" + string(result[right])),
		)
		return strings.Compare(
			hex.EncodeToString(leftDigest[:]),
			hex.EncodeToString(rightDigest[:]),
		) < 0
	})
	return result
}

func orderedBenchmarkHarnesses(
	values []MissionPackBenchmarkHarness,
	material string,
) []MissionPackBenchmarkHarness {
	result := append([]MissionPackBenchmarkHarness(nil), values...)
	sort.Slice(result, func(left, right int) bool {
		leftDigest := sha256.Sum256(
			[]byte(material + "|" + string(result[left])),
		)
		rightDigest := sha256.Sum256(
			[]byte(material + "|" + string(result[right])),
		)
		return strings.Compare(
			hex.EncodeToString(leftDigest[:]),
			hex.EncodeToString(rightDigest[:]),
		) < 0
	})
	return result
}
