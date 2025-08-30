package util

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/jmespath/go-jmespath"
)

// ExtractFirstValue evaluates the given JMESPath expression against each event's message
// (decoded as JSON if possible; otherwise wrapped as {"message": raw}) and returns the
// first non-empty string representation found. Array results use the first element only.
// Returns (value, true, nil) on success; ("", false, nil) if not found; or error.
func ExtractFirstValue(events []types.FilteredLogEvent, jmes string) (string, bool, error) {
	for _, e := range events {
		if e.Message == nil {
			continue
		}
		raw := *e.Message
		var input any
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			input = decoded
		} else {
			input = map[string]any{"message": raw}
		}

		res, err := jmespath.Search(jmes, input)
		if err != nil {
			return "", false, fmt.Errorf("jmespath search failed: %w", err)
		}
		// Handle nil and empties
		if isEmpty(res) {
			continue
		}
		// If array/slice, take the first element only
		rv := reflect.ValueOf(res)
		if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
			if rv.Len() == 0 {
				continue
			}
			res = rv.Index(0).Interface()
			if isEmpty(res) {
				continue
			}
		}
		// Convert to string
		switch v := res.(type) {
		case string:
			if v == "" {
				continue
			}
			return v, true, nil
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return "", false, fmt.Errorf("marshal result failed: %w", err)
			}
			if len(b) == 0 || string(b) == "null" || string(b) == "[]" || string(b) == "{}" {
				continue
			}
			return string(b), true, nil
		}
	}
	return "", false, nil
}

// BuildNextFilter evaluates a JMESPath expression against {"value": extracted} to build
// the CloudWatch filter pattern. If the expression fails to evaluate (e.g., not valid
// JMESPath), it falls back to returning the expression as-is.
func BuildNextFilter(jmes string, extracted string) (string, error) {
	input := map[string]any{"value": extracted}
	out, err := jmespath.Search(jmes, input)
	if err != nil {
		// Fallback: treat as literal pattern
		return jmes, nil
	}
	switch v := out.(type) {
	case string:
		return v, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal next-filter result failed: %w", err)
		}
		return string(b), nil
	}
}

// ReplacePlaceholder replaces all occurrences of {{name}} in expr with the JSON-escaped
// string literal of value (e.g., "WARN"), ensuring safety for subsequent JMESPath eval.
func ReplacePlaceholder(expr, name, value string) string {
	if name == "" {
		return expr
	}
	// JSON-quote the string value to produce a JMESPath string literal
	qb, _ := json.Marshal(value)
	quoted := string(qb)
	needle := "{{" + name + "}}"
	return strings.ReplaceAll(expr, needle, quoted)
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() == 0
	}
	return false
}
