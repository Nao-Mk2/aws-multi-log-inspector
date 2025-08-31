package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// Options holds CLI options after parsing flags and env defaults.
type Options struct {
	GroupsCSV     string
	Region        string
	Profile       string
	FilterPattern string
	Extract       string
	NextFilter    string
	PrettyJSON    bool
	StartRFC3339  string
	EndRFC3339    string
}

// Validate checks relationships and required flags.
// Returns an error message and exit code; if the filter-pattern is missing,
// it returns ("", 2) and the caller should invoke usage().
func (o *Options) Validate() (string, int) {
	if o.FilterPattern == "" {
		// Caller prints usage() which exits(2)
		return "", 2
	}
	if o.NextFilter != "" && o.Extract == "" {
		return "error: --next-filter requires --extract", 2
	}
	if CountFlagOccurrences("--extract") > 1 {
		return "error: --extract specified multiple times", 2
	}
	return "", 0
}

// ParseExtractSpec parses "name=path" into (name, path).
// Exported so main package can reuse.
func (o *Options) ParseExtractSpec() (string, string, error) {
	i := strings.Index(o.Extract, "=")
	if i <= 0 || i == len(o.Extract)-1 {
		return "", "", fmt.Errorf("invalid --extract format; expected name=path")
	}
	name := strings.TrimSpace(o.Extract[:i])
	path := strings.TrimSpace(o.Extract[i+1:])
	if name == "" || path == "" {
		return "", "", fmt.Errorf("invalid --extract format; empty name or path")
	}
	return name, path, nil
}

// CollectOptions parses flags with environment-backed defaults and returns Options.
func CollectOptions() *Options {
	var groupsCSV string
	var region string
	var profileFlag string
	var filterPattern string
	var extractFlag string
	var nextFilterFlag string
	var prettyJSON bool
	var startStr string
	var endStr string

	if v := os.Getenv("LOG_GROUP_NAMES"); v != "" {
		groupsCSV = v
	}

	flag.StringVar(&groupsCSV, "groups", groupsCSV, "Comma-separated CloudWatch log group names")
	flag.StringVar(&region, "region", os.Getenv("AWS_REGION"), "AWS region (optional; falls back to AWS defaults)")
	flag.StringVar(&profileFlag, "profile", "", "AWS shared config profile (or set AWS_PROFILE)")
	flag.StringVar(&filterPattern, "filter-pattern", "", "CloudWatch Logs filter pattern (required)")
	flag.StringVar(&extractFlag, "extract", "", "JMESPath extract in name=path form (single occurrence)")
	flag.StringVar(&nextFilterFlag, "next-filter", "", "JMESPath to build second filter; requires --extract")
	flag.BoolVar(&prettyJSON, "pretty", false, "Pretty-print JSON output for --next-filter results")
	flag.StringVar(&startStr, "start", "", "Start time RFC3339 (e.g., 2025-08-30T15:04:05Z)")
	flag.StringVar(&endStr, "end", "", "End time RFC3339 (e.g., 2025-08-31T15:04:05Z)")
	flag.Parse()

	return &Options{
		GroupsCSV:     groupsCSV,
		Region:        region,
		Profile:       profileFlag,
		FilterPattern: filterPattern,
		Extract:       extractFlag,
		NextFilter:    nextFilterFlag,
		PrettyJSON:    prettyJSON,
		StartRFC3339:  startStr,
		EndRFC3339:    endStr,
	}
}

// ParseGroupsCSV turns a comma-separated groups string into slice, trimming empties.
func ParseGroupsCSV(csv string) []string {
	if csv == "" {
		return nil
	}
	var groups []string
	for _, g := range strings.Split(csv, ",") {
		g = strings.TrimSpace(g)
		if g != "" {
			groups = append(groups, g)
		}
	}
	return groups
}

// ResolveProfile returns the profile from flag or AWS_PROFILE env, or empty.
func ResolveProfile(flagProfile string) string {
	if flagProfile != "" {
		return flagProfile
	}
	return os.Getenv("AWS_PROFILE")
}

// DefaultTimeWindow returns the [start, end] timestamps for last 24 hours.
func DefaultTimeWindow() (time.Time, time.Time) {
	end := time.Now()
	start := end.Add(-24 * time.Hour)
	return start, end
}

// ResolveTimeWindow computes the [start,end] from optional RFC3339 strings.
// Rules:
// - both empty: last 24h ending at now
// - only start: end = now
// - only end: start = end - 24h
// - both set: validate start <= end
func ResolveTimeWindow(startStr, endStr string, now time.Time) (time.Time, time.Time, error) {
	if startStr == "" && endStr == "" {
		// Mirror DefaultTimeWindow but based on provided now
		return now.Add(-24 * time.Hour), now, nil
	}
	var start time.Time
	var end time.Time
	var err error
	if startStr != "" {
		start, err = time.Parse(time.RFC3339, startStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if endStr != "" {
		end, err = time.Parse(time.RFC3339, endStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if startStr != "" && endStr == "" {
		end = now
	} else if startStr == "" && endStr != "" {
		start = end.Add(-24 * time.Hour)
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, ErrStartAfterEnd
	}
	return start, end, nil
}

// ErrStartAfterEnd represents an invalid time window where start > end.
var ErrStartAfterEnd = &timeRangeError{"start is after end"}

type timeRangeError struct{ s string }

func (e *timeRangeError) Error() string { return e.s }

// CountFlagOccurrences counts how many times a long flag (e.g., "--extract") appears
// considering both "--flag value" and "--flag=value" forms.
func CountFlagOccurrences(flagName string) int {
	count := 0
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == flagName {
			count++
			// Skip value if present and not another flag
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		if strings.HasPrefix(a, flagName+"=") {
			count++
			continue
		}
	}
	return count
}
