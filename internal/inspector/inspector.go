package inspector

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// LogsClient is the subset of CloudWatch Logs API we use.
type LogsClient interface {
	FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

// LogRecord represents a single log entry matched across groups.
type LogRecord struct {
	Timestamp time.Time
	LogGroup  string
	LogStream string
	Message   string
}

// Inspector searches CloudWatch Logs across multiple groups.
type Inspector struct {
	client    LogsClient
	groups    []string
	startTime time.Time
	endTime   time.Time
}

// New creates an Inspector.
func New(client LogsClient, groups []string, startTime, endTime time.Time) *Inspector {
	return &Inspector{client: client, groups: groups, startTime: startTime, endTime: endTime}
}

// Search finds logs matching the given filter pattern across configured groups.
func (in *Inspector) Search(ctx context.Context, filterPattern string) ([]LogRecord, error) {
	if len(in.groups) == 0 {
		return nil, errors.New("no log groups configured")
	}
	if filterPattern == "" {
		return nil, errors.New("empty filter pattern")
	}
	// CloudWatch Logs filter pattern treats special characters as token separators
	// unless the term is quoted. Quote the string to match the literal sequence.
	fp := filterPattern
	if !(len(fp) >= 2 && fp[0] == '"' && fp[len(fp)-1] == '"') {
		fp = "\"" + fp + "\""
	}
	startMs := in.startTime.UnixMilli()
	endMs := in.endTime.UnixMilli()

	const numWorkers = 4
	groupChan := make(chan string, len(in.groups))
	resultChan := make(chan []LogRecord, len(in.groups))
	errorChan := make(chan error, len(in.groups))

	// Send all groups to the channel
	for _, g := range in.groups {
		groupChan <- g
	}
	close(groupChan)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range groupChan {
				records, err := in.searchGroup(ctx, group, fp, startMs, endMs)
				if err != nil {
					errorChan <- err
					return
				}
				resultChan <- records
			}
		}()
	}

	// Wait for all workers to complete and close result channels
	go func() {
		wg.Wait()
		close(resultChan)
		close(errorChan)
	}()

	// Collect results
	var allRecords []LogRecord
	for {
		select {
		case err := <-errorChan:
			if err != nil {
				return nil, err
			}
		case records, ok := <-resultChan:
			if !ok {
				goto done
			}
			allRecords = append(allRecords, records...)
		}
	}

done:
	// Check for any remaining errors
	select {
	case err := <-errorChan:
		if err != nil {
			return nil, err
		}
	default:
	}

	sort.Slice(allRecords, func(i, j int) bool {
		if allRecords[i].Timestamp.Equal(allRecords[j].Timestamp) {
			if allRecords[i].LogGroup == allRecords[j].LogGroup {
				if allRecords[i].LogStream == allRecords[j].LogStream {
					return allRecords[i].Message < allRecords[j].Message
				}
				return allRecords[i].LogStream < allRecords[j].LogStream
			}
			return allRecords[i].LogGroup < allRecords[j].LogGroup
		}
		return allRecords[i].Timestamp.Before(allRecords[j].Timestamp)
	})
	return allRecords, nil
}

// searchGroup searches logs in a single log group
func (in *Inspector) searchGroup(ctx context.Context, group, filterPattern string, startMs, endMs int64) ([]LogRecord, error) {
	var records []LogRecord
	var next *string
	for {
		out, err := in.client.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:  aws.String(group),
			FilterPattern: aws.String(filterPattern),
			StartTime:     aws.Int64(startMs),
			EndTime:       aws.Int64(endMs),
			NextToken:     next,
			Interleaved:   aws.Bool(true),
		})
		if err != nil {
			return nil, err
		}
		for _, e := range out.Events {
			ts := time.Unix(0, aws.ToInt64(e.Timestamp)*int64(time.Millisecond))
			records = append(records, LogRecord{
				Timestamp: ts,
				LogGroup:  group,
				LogStream: aws.ToString(e.LogStreamName),
				Message:   aws.ToString(e.Message),
			})
		}
		if out.NextToken == nil || (next != nil && aws.ToString(out.NextToken) == aws.ToString(next)) {
			break
		}
		next = out.NextToken
	}
	return records, nil
}
