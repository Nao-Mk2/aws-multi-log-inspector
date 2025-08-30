package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"aws-multi-log-inspector/internal/client"
	"aws-multi-log-inspector/internal/inspector"
	"aws-multi-log-inspector/internal/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: aws-multi-log-inspector --filter-pattern <pattern> [--groups g1,g2] [--region us-east-1]")
	fmt.Fprintln(os.Stderr, "Environment: LOG_GROUP_NAMES can provide comma-separated groups; AWS credentials from default sources.")
	os.Exit(2)
}

func main() {
	var groupsCSV string
	var region string
	var profileFlag string
	var filterPattern string
	var extractFlag string
	var nextFilterFlag string
	var prettyJSON bool

	if v := os.Getenv("LOG_GROUP_NAMES"); v != "" {
		groupsCSV = v
	}

	flag.StringVar(&groupsCSV, "groups", groupsCSV, "Comma-separated CloudWatch log group names")
	flag.StringVar(&region, "region", os.Getenv("AWS_REGION"), "AWS region (optional; falls back to AWS defaults)")
	flag.StringVar(&profileFlag, "profile", "", "AWS shared config profile (or set AWS_PROFILE)")
	flag.StringVar(&filterPattern, "filter-pattern", "", "CloudWatch Logs filter pattern (required)")
	flag.StringVar(&extractFlag, "extract", "", "JMESPath extract in name=path form (single occurrence)")
	// Examples:
	// 1) Extract userId then use it in second search
	//    --filter-pattern "ERROR" \
	//    --extract "userID=user.id" \
	//    --next-filter "userId={{userID}}"
	// 2) Extract string from non-JSON and build @message match
	//    --filter-pattern "WARN" \
	//    --extract "value=message" \
	//    --next-filter "join('', ['@message = \"', {{value}}, '\"'])"
	flag.StringVar(&nextFilterFlag, "next-filter", "", "JMESPath to build second filter; requires --extract")
	flag.BoolVar(&prettyJSON, "pretty", false, "Pretty-print JSON output for --next-filter results")
	flag.Parse()

	// --filter-pattern is required
	if filterPattern == "" {
		usage()
	}

	// Validate flag relationships and multiplicity for --extract/--next-filter
	if nextFilterFlag != "" && extractFlag == "" {
		fmt.Fprintln(os.Stderr, "error: --next-filter requires --extract")
		os.Exit(2)
	}
	if countFlagOccurrences("--extract") > 1 {
		fmt.Fprintln(os.Stderr, "error: --extract specified multiple times")
		os.Exit(2)
	}

	var groups []string
	if groupsCSV != "" {
		for _, g := range strings.Split(groupsCSV, ",") {
			g = strings.TrimSpace(g)
			if g != "" {
				groups = append(groups, g)
			}
		}
	}
	if len(groups) == 0 {
		fmt.Fprintln(os.Stderr, "error: no log groups provided (use --groups or LOG_GROUP_NAMES)")
		os.Exit(1)
	}

	// Fixed search window: last 24 hours
	end := time.Now()
	start := end.Add(-24 * time.Hour)

	// Resolve profile: --profile > AWS_PROFILE; otherwise error
	resolvedProfile := profileFlag
	if resolvedProfile == "" {
		resolvedProfile = os.Getenv("AWS_PROFILE")
	}
	if resolvedProfile == "" {
		fmt.Fprintln(os.Stderr, "error: AWS profile required (use --profile or set AWS_PROFILE)")
		os.Exit(1)
	}

	ctx := context.Background()
	cw, err := client.NewCloudWatchClient(ctx, region, resolvedProfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create CloudWatch client: %v\n", err)
		os.Exit(1)
	}

	insp := inspector.New(cw, groups, start, end)
	records, err := insp.Search(ctx, filterPattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search error: %v\n", err)
		os.Exit(1)
	}

	// If --extract is not used, keep the original behavior
	if extractFlag == "" {
		if len(records) == 0 {
			fmt.Printf("No logs found for the given pattern `%s` in the last 24h.\n", filterPattern)
			return
		}
		for _, r := range records {
			ts := r.Timestamp.UTC().Format(time.RFC3339)
			prefix := fmt.Sprintf("%s %s/%s", ts, r.LogGroup, r.LogStream)
			if pretty, ok := prettyIfJSON(r.Message); ok {
				fmt.Printf("%s\n%s\n", prefix, pretty)
			} else {
				fmt.Printf("%s %s\n", prefix, r.Message)
			}
		}
		return
	}

	// Extract flow
	if len(records) == 0 {
		fmt.Fprintln(os.Stderr, "no logs found in initial search (24h)")
		os.Exit(3)
	}

	// Parse extract flag: name=path
	extractName, extractPath, err := parseExtractSpec(extractFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(2)
	}

	// Build minimal []types.FilteredLogEvent with only Message populated
	evs := make([]types.FilteredLogEvent, 0, len(records))
	for _, r := range records {
		msg := r.Message
		evs = append(evs, types.FilteredLogEvent{Message: aws.String(msg)})
	}

	extracted, ok, err := util.ExtractFirstValue(evs, extractPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract error: %v\n", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "no extractable value found from initial logs")
		os.Exit(3)
	}

	// If no --next-filter, just output {"value": "..."}
	if nextFilterFlag == "" {
		out := map[string]string{"value": extracted}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Build next filter pattern
	replaced := util.ReplacePlaceholder(nextFilterFlag, extractName, extracted)
	nextPattern, err := util.BuildNextFilter(replaced, extracted)
	if err != nil {
		fmt.Fprintf(os.Stderr, "next-filter build error: %v\n", err)
		os.Exit(1)
	}

	// Second search using the nextPattern (exactly as given), across groups
	nextInspector := inspector.New(cw, groups, start, end)
	nextRecords, err := nextInspector.Search(ctx, nextPattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "second search error: %v\n", err)
		os.Exit(1)
	}

	// Output JSON array of results
	if prettyJSON {
		b, err := json.MarshalIndent(nextRecords, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(b))
		return
	}
	if err := json.NewEncoder(os.Stdout).Encode(nextRecords); err != nil {
		fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
		os.Exit(1)
	}
}

// prettyIfJSON tries to pretty-print a JSON object/array contained in s.
// Returns (pretty, true) if successful, otherwise ("", false).
func prettyIfJSON(s string) (string, bool) {
	t := strings.TrimSpace(s)
	var v any
	// First attempt: direct parse as object/array
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		if json.Unmarshal([]byte(t), &v) == nil {
			// Only pretty-print maps/arrays; strings/numbers fall through
			switch v.(type) {
			case map[string]any, []any:
				b, err := json.MarshalIndent(v, "", "  ")
				if err == nil {
					return string(b), true
				}
			}
		}
	}
	// Second attempt: if the whole message is a quoted JSON string, unquote and retry
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		if unq, err := strconv.Unquote(t); err == nil {
			unqTrim := strings.TrimSpace(unq)
			if len(unqTrim) > 0 && (unqTrim[0] == '{' || unqTrim[0] == '[') {
				if json.Unmarshal([]byte(unqTrim), &v) == nil {
					switch v.(type) {
					case map[string]any, []any:
						b, err := json.MarshalIndent(v, "", "  ")
						if err == nil {
							return string(b), true
						}
					}
				}
			}
		}
	}
	return "", false
}

// parseExtractSpec parses "name=path" into (name, path).
func parseExtractSpec(spec string) (string, string, error) {
	i := strings.Index(spec, "=")
	if i <= 0 || i == len(spec)-1 {
		return "", "", fmt.Errorf("invalid --extract format; expected name=path")
	}
	name := strings.TrimSpace(spec[:i])
	path := strings.TrimSpace(spec[i+1:])
	if name == "" || path == "" {
		return "", "", fmt.Errorf("invalid --extract format; empty name or path")
	}
	return name, path, nil
}

// countFlagOccurrences counts how many times a long flag (e.g., "--extract") appears
// considering both "--flag value" and "--flag=value" forms.
func countFlagOccurrences(flagName string) int {
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
