package inspector

import (
	"aws-multi-log-inspector/internal/model"
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

type CloudWatchLogsRetriever interface {
	SearchGroup(ctx context.Context, group, filterPattern string, startMs, endMs int64) ([]model.LogRecord, error)
}

// Inspector searches CloudWatch Logs across multiple groups.
type Inspector struct {
	client    CloudWatchLogsRetriever
	groups    []string
	startTime time.Time
	endTime   time.Time
}

// New creates an Inspector.
func New(client CloudWatchLogsRetriever, groups []string, startTime, endTime time.Time) *Inspector {
	return &Inspector{client: client, groups: groups, startTime: startTime, endTime: endTime}
}

// Search finds logs matching the given filter pattern across configured groups.
func (in *Inspector) Search(ctx context.Context, filterPattern string) ([]model.LogRecord, error) {
	if len(in.groups) == 0 {
		return nil, errors.New("no log groups configured")
	}
	if filterPattern == "" {
		return nil, errors.New("empty filter pattern")
	}

	startMs := in.startTime.UnixMilli()
	endMs := in.endTime.UnixMilli()

	const numWorkers = 4
	groupChan := make(chan string, len(in.groups))
	resultChan := make(chan []model.LogRecord, len(in.groups))
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
				records, err := in.client.SearchGroup(ctx, group, filterPattern, startMs, endMs)
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
	var allRecords []model.LogRecord
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
