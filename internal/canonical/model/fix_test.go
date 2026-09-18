package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFixCatalogsAndCanonicalUUIDv4(t *testing.T) {
	for _, kind := range []FixChangeKind{
		FixChangeCode,
		FixChangeConfiguration,
		FixChangeDependency,
		FixChangePermission,
		FixChangeEnvironment,
		FixChangeAgentInstruction,
		FixChangeProjectRule,
		FixChangeMonitorHook,
		FixChangeOther,
	} {
		if !kind.Valid() {
			t.Errorf("FixChangeKind(%q).Valid() = false", kind)
		}
	}
	if FixChangeKind("free text").Valid() {
		t.Fatal("free-form change kind accepted")
	}
	for _, reason := range []FixRetractionReason{
		FixRetractionRecordedByMistake,
		FixRetractionSuperseded,
		FixRetractionOther,
	} {
		if !reason.Valid() {
			t.Errorf("FixRetractionReason(%q).Valid() = false", reason)
		}
	}
	if FixRetractionReason("because I said so").Valid() {
		t.Fatal("free-form retraction reason accepted")
	}

	for _, value := range []string{
		"00000000-0000-4000-8000-000000000000",
		"018f23ab-cdef-4abc-bdef-0123456789ab",
	} {
		if !IsCanonicalUUIDv4(value) {
			t.Errorf("IsCanonicalUUIDv4(%q) = false", value)
		}
	}
	for _, value := range []string{
		"",
		"00000000-0000-7000-8000-000000000000",
		"00000000-0000-4000-7000-000000000000",
		"00000000-0000-4000-c000-000000000000",
		"018F23AB-CDEF-4ABC-BDEF-0123456789AB",
		"00000000-0000-4000-8000-00000000000g",
	} {
		if IsCanonicalUUIDv4(value) {
			t.Errorf("IsCanonicalUUIDv4(%q) = true", value)
		}
	}
}

func TestFixAnnotationContractContainsNoFreeTextSurface(t *testing.T) {
	annotationType := reflect.TypeOf(FixAnnotation{})
	for index := 0; index < annotationType.NumField(); index++ {
		field := annotationType.Field(index)
		name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
		for _, prohibited := range []string{
			"note", "description", "command", "path", "diff", "prompt",
			"output", "rule_body", "hook_body", "environment", "url",
		} {
			if strings.Contains(name, prohibited) {
				t.Errorf("FixAnnotation exposes prohibited field %q", field.Name)
			}
		}
	}
	body, err := json.Marshal(FixAnnotation{
		ChangeKind:           FixChangeCode,
		ChangeCatalogVersion: FixChangeCatalogVersion,
		RecordedVia:          FixRecordedViaLocalUI,
		State:                FixStateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"note", "command", "diff", "prompt", "output"} {
		if strings.Contains(string(body), prohibited) {
			t.Errorf("serialized annotation contains prohibited key %q: %s", prohibited, body)
		}
	}
}
