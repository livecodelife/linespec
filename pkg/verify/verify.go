package verify

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/livecodelife/linespec/pkg/types"
)

// VerificationError represents a failed verification
type VerificationError struct {
	Target  string
	Rule    types.VerifyRule
	Actual  string
	Message string
}

func (e *VerificationError) Error() string {
	switch e.Rule.Type {
	case "CONTAINS":
		return fmt.Sprintf("VERIFY failed: expected %s to CONTAINS '%s', but got: %s",
			e.Target, e.Rule.Pattern, truncate(e.Actual, 200))
	case "NOT_CONTAINS":
		return fmt.Sprintf("VERIFY failed: expected %s to NOT_CONTAINS '%s', but got: %s",
			e.Target, e.Rule.Pattern, truncate(e.Actual, 200))
	case "MATCHES":
		return fmt.Sprintf("VERIFY failed: expected %s to MATCHES /%s/, but got: %s",
			e.Target, e.Rule.Pattern, truncate(e.Actual, 200))
	default:
		return fmt.Sprintf("VERIFY failed: unknown rule type '%s' for %s", e.Rule.Type, e.Target)
	}
}

// truncate limits string length for error messages
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// VerifySQL checks a SQL query against a set of verification rules
func VerifySQL(query string, rules []types.VerifyRule) error {
	return VerifyTarget("query", query, rules)
}

// VerifyTarget checks a target value against a set of verification rules
func VerifyTarget(targetName string, actual string, rules []types.VerifyRule) error {
	for _, rule := range rules {
		if err := verifyRule(targetName, actual, rule); err != nil {
			return err
		}
	}
	return nil
}

// verifyRule checks a single verification rule
func verifyRule(targetName string, actual string, rule types.VerifyRule) error {
	switch strings.ToUpper(rule.Type) {
	case "CONTAINS":
		if !strings.Contains(actual, rule.Pattern) {
			return &VerificationError{
				Target:  targetName,
				Rule:    rule,
				Actual:  actual,
				Message: fmt.Sprintf("expected %s to contain '%s'", targetName, rule.Pattern),
			}
		}

	case "NOT_CONTAINS":
		if strings.Contains(actual, rule.Pattern) {
			return &VerificationError{
				Target:  targetName,
				Rule:    rule,
				Actual:  actual,
				Message: fmt.Sprintf("expected %s to not contain '%s'", targetName, rule.Pattern),
			}
		}

	case "MATCHES":
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return &VerificationError{
				Target:  targetName,
				Rule:    rule,
				Actual:  actual,
				Message: fmt.Sprintf("invalid regex pattern '%s': %v", rule.Pattern, err),
			}
		}
		if !re.MatchString(actual) {
			return &VerificationError{
				Target:  targetName,
				Rule:    rule,
				Actual:  actual,
				Message: fmt.Sprintf("expected %s to match /%s/", targetName, rule.Pattern),
			}
		}

	default:
		return &VerificationError{
			Target:  targetName,
			Rule:    rule,
			Actual:  actual,
			Message: fmt.Sprintf("unknown verification rule type: %s", rule.Type),
		}
	}

	return nil
}
