package inspector

import (
    "context"
    "errors"
    "sort"
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

// Search finds logs containing the given requestID across configured groups.
func (in *Inspector) Search(ctx context.Context, requestID string) ([]LogRecord, error) {
    if len(in.groups) == 0 {
        return nil, errors.New("no log groups configured")
    }
    if requestID == "" {
        return nil, errors.New("empty request id")
    }
    startMs := in.startTime.UnixMilli()
    endMs := in.endTime.UnixMilli()

    records := make([]LogRecord, 0)
    for _, g := range in.groups {
        var next *string
        for {
            out, err := in.client.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
                LogGroupName: aws.String(g),
                FilterPattern: aws.String(requestID),
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
                    LogGroup:  g,
                    LogStream: aws.ToString(e.LogStreamName),
                    Message:   aws.ToString(e.Message),
                })
            }
            if out.NextToken == nil || (next != nil && aws.ToString(out.NextToken) == aws.ToString(next)) {
                break
            }
            next = out.NextToken
        }
    }

    sort.Slice(records, func(i, j int) bool { return records[i].Timestamp.Before(records[j].Timestamp) })
    return records, nil
}
