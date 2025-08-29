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

	"aws-multi-log-inspector/internal/aws"
	"aws-multi-log-inspector/internal/inspector"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [--groups g1,g2] [--region us-east-1] <X-Request-Id>\n", os.Args[0])
	fmt.Fprintln(os.Stderr, "Environment: LOG_GROUP_NAMES can provide comma-separated groups; AWS credentials from default sources.")
	os.Exit(2)
}

func main() {
	var groupsCSV string
	var region string
	var profileFlag string

	if v := os.Getenv("LOG_GROUP_NAMES"); v != "" {
		groupsCSV = v
	}

	flag.StringVar(&groupsCSV, "groups", groupsCSV, "Comma-separated CloudWatch log group names")
	flag.StringVar(&region, "region", os.Getenv("AWS_REGION"), "AWS region (optional; falls back to AWS defaults)")
	flag.StringVar(&profileFlag, "profile", "", "AWS shared config profile (or set AWS_PROFILE)")
	flag.Parse()

	if flag.NArg() < 1 {
		usage()
	}
	searchStr := flag.Arg(0)

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
	cw, err := aws.NewCloudWatchClient(ctx, region, resolvedProfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create CloudWatch client: %v\n", err)
		os.Exit(1)
	}

	insp := inspector.New(cw, groups, start, end)
	records, err := insp.Search(ctx, searchStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search error: %v\n", err)
		os.Exit(1)
	}

	if len(records) == 0 {
		fmt.Printf("No logs found for the given string `%s` in the last 24h.\n", searchStr)
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
