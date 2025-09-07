package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Nao-Mk2/aws-multi-log-inspector/internal/client"
	"github.com/Nao-Mk2/aws-multi-log-inspector/internal/inspector"
	"github.com/Nao-Mk2/aws-multi-log-inspector/internal/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/Nao-Mk2/aws-multi-log-inspector/cmd"
)

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: aws-multi-log-inspector --filter-pattern <pattern> [--groups g1,g2] [--region us-east-1] [--start RFC3339] [--end RFC3339]")
	fmt.Fprintln(os.Stderr, "Environment: LOG_GROUP_NAMES can provide comma-separated groups; AWS credentials from default sources.")
	os.Exit(2)
}

func main() {
	// Parse flags/env and validate relationships
	opts := cmd.CollectOptions()
	if msg, code := opts.Validate(); code != 0 {
		if opts.FilterPattern == "" {
			usage()
		}
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(code)
	}

	// Parse groups
	groups := cmd.ParseGroupsCSV(opts.GroupsCSV)
	if len(groups) == 0 {
		fmt.Fprintln(os.Stderr, "error: no log groups provided (use --groups or LOG_GROUP_NAMES)")
		os.Exit(1)
	}

	// Resolve search window: RFC3339 flags or last 24h by default
	start, end, err := cmd.ResolveTimeWindow(opts.StartRFC3339, opts.EndRFC3339, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid time window: %v\n", err)
		os.Exit(2)
	}

	ctx := context.Background()
	cwOpts := opts.BuildCloudWatchOptions()
	cw, err := client.NewCloudWatchClient(ctx, cwOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create CloudWatch client: %v\n", err)
		os.Exit(1)
	}

	insp := inspector.New(cw, groups, start, end)
	// Configure concurrency (bounded by number of groups, minimum 1)
	workers := opts.Concurrency
	if workers <= 0 {
		workers = 1
	}
	if workers > len(groups) {
		workers = len(groups)
	}
	insp.SetWorkers(workers)
	records, err := insp.Search(ctx, opts.FilterPattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search error: %v\n", err)
		os.Exit(1)
	}
	if len(records) == 0 {
		// Report accurate time window in the message
		windowMsg := "in the last 24h."
		if opts.StartRFC3339 != "" || opts.EndRFC3339 != "" {
			windowMsg = fmt.Sprintf("between %s and %s.", start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
		}
		fmt.Printf("No logs found for the given pattern `%s` %s\n", opts.FilterPattern, windowMsg)
		return
	}

	// If --extract is not used, print first search results
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if opts.Extract == "" {
		// Align --pretty output format with --next-filter: emit JSON array
		if opts.PrettyJSON {
			if err := enc.Encode(records); err != nil {
				fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		w := bufio.NewWriter(os.Stdout)
		for _, r := range records {
			ts := r.Timestamp.UTC().Format(time.RFC3339)
			prefix := fmt.Sprintf("%s %s/%s", ts, r.LogGroup, r.LogStream)
			fmt.Fprintf(w, "%s %s\n", prefix, r.Message)
		}
		_ = w.Flush()
		return
	}

	// Extract flow
	// Parse extract flag: name=path
	extractName, extractPath, err := opts.ParseExtractSpec()
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
	if opts.NextFilter == "" {
		out := map[string]string{"value": extracted}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Build next filter pattern
	replaced := util.ReplacePlaceholder(opts.NextFilter, extractName, extracted)
	nextPattern, err := util.BuildNextFilter(replaced, extracted)
	if err != nil {
		fmt.Fprintf(os.Stderr, "next-filter build error: %v\n", err)
		os.Exit(1)
	}

	// Second search using the nextPattern (exactly as given), across groups
	nextInspector := inspector.New(cw, groups, start, end)
	nextInspector.SetWorkers(workers)
	nextRecords, err := nextInspector.Search(ctx, nextPattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "second search error: %v\n", err)
		os.Exit(1)
	}

	// Output JSON array of results
	if opts.PrettyJSON {
		if err := enc.Encode(nextRecords); err != nil {
			fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := json.NewEncoder(os.Stdout).Encode(nextRecords); err != nil {
		fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
		os.Exit(1)
	}
}
