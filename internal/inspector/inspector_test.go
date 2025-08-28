package inspector

import (
    "context"
    "errors"
    "sort"
    "testing"
    "time"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
    "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

type fakeLogsClient struct {
    responses  map[string][]types.FilteredLogEvent
    errByGroup map[string]error
    calls      []string
}

func (f *fakeLogsClient) FilterLogEvents(ctx context.Context, in *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
    g := aws.ToString(in.LogGroupName)
    f.calls = append(f.calls, g)
    if err := f.errByGroup[g]; err != nil {
        return nil, err
    }
    events := f.responses[g]
    // Ensure events are within the provided time window for realism
    startMs := aws.ToInt64(in.StartTime)
    endMs := aws.ToInt64(in.EndTime)
    var filtered []types.FilteredLogEvent
    for _, e := range events {
        ts := aws.ToInt64(e.Timestamp)
        if (startMs == 0 || ts >= startMs) && (endMs == 0 || ts <= endMs) {
            filtered = append(filtered, e)
        }
    }
    // Return in a single page for simplicity
    return &cloudwatchlogs.FilterLogEventsOutput{Events: filtered}, nil
}

func TestSearchAggregatesAndSortsAcrossGroups(t *testing.T) {
    now := time.Unix(1_700_000_000, 0)
    g1 := "/aws/app/one"
    g2 := "/aws/app/two"
    f := &fakeLogsClient{
        responses: map[string][]types.FilteredLogEvent{
            g1: {
                {Timestamp: aws.Int64(now.Add(2 * time.Minute).UnixMilli()), Message: aws.String("msg2"), LogStreamName: aws.String("s1")},
            },
            g2: {
                {Timestamp: aws.Int64(now.Add(1 * time.Minute).UnixMilli()), Message: aws.String("msg1"), LogStreamName: aws.String("s2")},
                {Timestamp: aws.Int64(now.Add(3 * time.Minute).UnixMilli()), Message: aws.String("msg3"), LogStreamName: aws.String("s3")},
            },
        },
        errByGroup: map[string]error{},
    }

    insp := New(f, []string{g1, g2}, now.Add(-time.Hour), now.Add(4*time.Minute))
    records, err := insp.Search(context.Background(), "req-123")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(records) != 3 {
        t.Fatalf("expected 3 records, got %d", len(records))
    }
    // Ensure ascending order by timestamp
    if !sort.SliceIsSorted(records, func(i, j int) bool { return records[i].Timestamp.Before(records[j].Timestamp) }) {
        t.Fatalf("records are not sorted by timestamp ascending: %+v", records)
    }
    // Spot check fields
    if records[0].Message != "msg1" || records[0].LogGroup != g2 {
        t.Fatalf("unexpected first record: %+v", records[0])
    }
}

func TestSearchErrorWhenNoGroups(t *testing.T) {
    insp := New(&fakeLogsClient{}, nil, time.Now().Add(-time.Hour), time.Now())
    if _, err := insp.Search(context.Background(), "abc"); err == nil {
        t.Fatalf("expected error when no groups configured")
    }
}

func TestSearchPropagatesAPIError(t *testing.T) {
    g1 := "/aws/app/one"
    f := &fakeLogsClient{responses: map[string][]types.FilteredLogEvent{}, errByGroup: map[string]error{g1: errors.New("boom")}}
    insp := New(f, []string{g1}, time.Now().Add(-time.Hour), time.Now())
    if _, err := insp.Search(context.Background(), "abc"); err == nil {
        t.Fatalf("expected API error to propagate")
    }
}

