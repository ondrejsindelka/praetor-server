package executor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var templateVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// Substitute replaces ${var} placeholders in tmpl with values from vars.
// Unknown placeholders are left unchanged.
func Substitute(tmpl string, vars map[string]string) string {
	return templateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := match[2 : len(match)-1] // strip ${ and }
		if v, ok := vars[key]; ok {
			return v
		}
		return match
	})
}

// buildTemplateVars constructs the substitution map for a given request and
// accumulated step results so far.
func buildTemplateVars(req InvestigationRequest, playbookName string, prevResults map[string]StepResult) map[string]string {
	vars := map[string]string{
		"host_id":      req.HostID,
		"host_name":    req.HostID, // host_name falls back to host_id when not available
		"triggered_at": req.TriggeredAt.UTC().Format("2006-01-02T15:04:05Z"),
		"playbook_name": playbookName,
	}

	// Extract well-known fields from TriggerData.
	if v, ok := req.TriggerData["rule_name"]; ok {
		vars["rule_name"] = fmt.Sprintf("%v", v)
	}
	if v, ok := req.TriggerData["trigger_metric"]; ok {
		vars["trigger_metric"] = fmt.Sprintf("%v", v)
	}
	if v, ok := req.TriggerData["trigger_value"]; ok {
		vars["trigger_value"] = fmt.Sprintf("%v", v)
	}

	// Inject previous step outputs as steps.X.output.
	for id, res := range prevResults {
		key := "steps." + id + ".output"
		if res.Output == nil {
			vars[key] = ""
			continue
		}
		b, err := json.Marshal(res.Output)
		if err != nil {
			vars[key] = ""
		} else {
			vars[key] = strings.TrimSpace(string(b))
		}
	}

	return vars
}
