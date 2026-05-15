package executor

import (
	"fmt"
	"strings"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/storage"
)

// ValidationError describes one validation failure in a playbook definition.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// allowedStepTypes is the set of valid step type values.
var allowedStepTypes = map[string]bool{
	"diagnostic":   true,
	"log_search":   true,
	"metric_query": true,
	"metric_trend": true,
	"wait":         true,
}

// ValidatePlaybook checks a playbook definition before create/update.
// Returns a (possibly empty) slice of ValidationError.
func ValidatePlaybook(p *storage.Playbook) []ValidationError {
	var errs []ValidationError

	// 1. Name must not be empty.
	if strings.TrimSpace(p.Name) == "" {
		errs = append(errs, ValidationError{Field: "name", Message: "must not be empty"})
	}

	// 2. Steps must not be empty.
	if len(p.Steps) == 0 {
		errs = append(errs, ValidationError{Field: "steps", Message: "must not be empty"})
		return errs // can't do further step validation
	}

	// Parse typed steps.
	steps, parseErrs := parseSteps(p.Steps)
	for i, pe := range parseErrs {
		errs = append(errs, ValidationError{
			Field:   fmt.Sprintf("steps[%d]", i),
			Message: pe.Error(),
		})
	}
	// Continue structural validation even with parse errors where possible.

	// 3. Step IDs must be non-empty and unique.
	seenIDs := make(map[string]bool, len(steps))
	definedIDs := make([]string, 0, len(steps)) // ordered list for forward-reference check
	for i, s := range steps {
		if strings.TrimSpace(s.ID) == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("steps[%d].id", i),
				Message: "must not be empty",
			})
			continue
		}
		if seenIDs[s.ID] {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("steps[%d].id", i),
				Message: fmt.Sprintf("duplicate step id %q", s.ID),
			})
		} else {
			seenIDs[s.ID] = true
			definedIDs = append(definedIDs, s.ID)
		}
	}

	// 4 & 5. Step type validation and tier-0 allowlist for diagnostic.
	for i, s := range steps {
		if !allowedStepTypes[s.Type] {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("steps[%d].type", i),
				Message: fmt.Sprintf("unknown step type %q", s.Type),
			})
		}
		if s.Type == "diagnostic" && s.Check != "" {
			if !tier0AllowedChecks[s.Check] {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("steps[%d].check", i),
					Message: fmt.Sprintf("check %q is not in tier-0 allowlist", s.Check),
				})
			}
		}
	}

	// 6. ${steps.X.output} references must refer to previously defined steps.
	// Build a set of IDs that are "visible" before each step.
	visibleBefore := make(map[string]bool)
	for _, s := range steps {
		// Check all string-valued params for template references.
		checkTemplateRefs := func(field, tmpl string) {
			matches := templateVarRe.FindAllStringSubmatch(tmpl, -1)
			for _, m := range matches {
				varName := m[1]
				if strings.HasPrefix(varName, "steps.") && strings.HasSuffix(varName, ".output") {
					refID := strings.TrimSuffix(strings.TrimPrefix(varName, "steps."), ".output")
					if !visibleBefore[refID] {
						errs = append(errs, ValidationError{
							Field:   fmt.Sprintf("steps[id=%s].%s", s.ID, field),
							Message: fmt.Sprintf("references step %q which is not defined before this step", refID),
						})
					}
				}
			}
		}
		checkTemplateRefs("query", s.Query)
		checkTemplateRefs("metric", s.Metric)
		for k, v := range s.Params {
			checkTemplateRefs("params."+k, v)
		}

		// 7. Cycle detection: a step must not reference itself.
		selfRefKey := "steps." + s.ID + ".output"
		if strings.Contains(s.Query, "${"+selfRefKey+"}") ||
			strings.Contains(s.Metric, "${"+selfRefKey+"}") {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("steps[id=%s]", s.ID),
				Message: "step references its own output (cycle)",
			})
		}

		// After processing this step, its ID becomes visible.
		if s.ID != "" {
			visibleBefore[s.ID] = true
		}
	}

	// 8. If LLM config block present, provider must not be empty.
	if p.LLMConfig != nil {
		if provider, ok := p.LLMConfig["provider"]; !ok || strings.TrimSpace(fmt.Sprintf("%v", provider)) == "" {
			errs = append(errs, ValidationError{
				Field:   "llm_config.provider",
				Message: "must not be empty when llm_config is present",
			})
		}
	}

	return errs
}
