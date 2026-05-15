package executor

import (
	"testing"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

func TestValidatePlaybook_Valid(t *testing.T) {
	p := &storage.Playbook{
		Name: "healthy playbook",
		Steps: []map[string]any{
			{"id": "s1", "type": "diagnostic", "check": "cpu"},
			{"id": "s2", "type": "wait", "duration_s": 5},
		},
	}
	errs := ValidatePlaybook(p)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidatePlaybook_EmptyName(t *testing.T) {
	p := &storage.Playbook{
		Name: "  ",
		Steps: []map[string]any{
			{"id": "s1", "type": "wait", "duration_s": 1},
		},
	}
	errs := ValidatePlaybook(p)
	if !containsField(errs, "name") {
		t.Errorf("expected error on name field, got: %v", errs)
	}
}

func TestValidatePlaybook_EmptySteps(t *testing.T) {
	p := &storage.Playbook{
		Name:  "no steps",
		Steps: []map[string]any{},
	}
	errs := ValidatePlaybook(p)
	if !containsField(errs, "steps") {
		t.Errorf("expected error on steps field, got: %v", errs)
	}
}

func TestValidatePlaybook_MissingStepID(t *testing.T) {
	p := &storage.Playbook{
		Name: "missing id",
		Steps: []map[string]any{
			{"type": "wait", "duration_s": 1}, // no id
		},
	}
	errs := ValidatePlaybook(p)
	if len(errs) == 0 {
		t.Error("expected error for missing step ID")
	}
}

func TestValidatePlaybook_DuplicateStepIDs(t *testing.T) {
	p := &storage.Playbook{
		Name: "dup ids",
		Steps: []map[string]any{
			{"id": "s1", "type": "wait", "duration_s": 1},
			{"id": "s1", "type": "diagnostic", "check": "cpu"},
		},
	}
	errs := ValidatePlaybook(p)
	dupErr := false
	for _, e := range errs {
		if containsMsg(e, "duplicate") {
			dupErr = true
		}
	}
	if !dupErr {
		t.Errorf("expected duplicate step id error, got: %v", errs)
	}
}

func TestValidatePlaybook_UnknownStepType(t *testing.T) {
	p := &storage.Playbook{
		Name: "bad type",
		Steps: []map[string]any{
			{"id": "s1", "type": "magic"},
		},
	}
	errs := ValidatePlaybook(p)
	typeErr := false
	for _, e := range errs {
		if containsMsg(e, "unknown step type") {
			typeErr = true
		}
	}
	if !typeErr {
		t.Errorf("expected unknown step type error, got: %v", errs)
	}
}

func TestValidatePlaybook_DisallowedDiagnosticCheck(t *testing.T) {
	p := &storage.Playbook{
		Name: "tier2 check",
		Steps: []map[string]any{
			// "exec_command" is a Tier 2 check not in tier0AllowedChecks.
			{"id": "s1", "type": "diagnostic", "check": "exec_command"},
		},
	}
	errs := ValidatePlaybook(p)
	found := false
	for _, e := range errs {
		if containsMsg(e, "tier-0 allowlist") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected tier-0 allowlist error, got: %v", errs)
	}
}

func TestValidatePlaybook_InvalidStepOutputReference(t *testing.T) {
	p := &storage.Playbook{
		Name: "bad ref",
		Steps: []map[string]any{
			// s2 references s1 BEFORE s1 is defined (s1 appears after s2).
			{"id": "s2", "type": "log_search", "query": "${steps.s1.output}"},
			{"id": "s1", "type": "diagnostic", "check": "cpu"},
		},
	}
	errs := ValidatePlaybook(p)
	found := false
	for _, e := range errs {
		if containsMsg(e, "not defined before this step") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected forward reference error, got: %v", errs)
	}
}

func TestValidatePlaybook_LLMConfigMissingProvider(t *testing.T) {
	p := &storage.Playbook{
		Name: "no llm provider",
		Steps: []map[string]any{
			{"id": "s1", "type": "wait", "duration_s": 1},
		},
		LLMConfig: map[string]any{
			"model": "gpt-4",
			// provider intentionally omitted
		},
	}
	errs := ValidatePlaybook(p)
	found := false
	for _, e := range errs {
		if containsField([]ValidationError{e}, "llm_config.provider") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected llm_config.provider error, got: %v", errs)
	}
}

// ---- helpers ----

func containsField(errs []ValidationError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

func containsMsg(e ValidationError, substr string) bool {
	return len(e.Message) > 0 && contains(e.Message, substr)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
