package client_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/Nao-Mk2/aws-multi-log-inspector/internal/client"
	"github.com/Nao-Mk2/aws-multi-log-inspector/internal/model"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// mockLogsAPI implements client.LogsAPI for testing.
type mockLogsAPI struct {
	responses []*cloudwatchlogs.FilterLogEventsOutput
	inputs    []*cloudwatchlogs.FilterLogEventsInput
	err       error
	call      int
}

func (m *mockLogsAPI) FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
	m.inputs = append(m.inputs, params)
	if m.err != nil {
		return nil, m.err
	}
	if m.call < len(m.responses) {
		r := m.responses[m.call]
		m.call++
		return r, nil
	}
	// Default empty page if not enough responses provided
	m.call++
	return &cloudwatchlogs.FilterLogEventsOutput{}, nil
}

// setPrivateClient sets the unexported client field on CloudWatchClient via unsafe.
func setPrivateClient(cwc *client.CloudWatchClient, api client.LogsAPI) {
	v := reflect.ValueOf(cwc).Elem().FieldByName("client")
	// Create a writable reflect.Value for the unexported field
	rv := reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	rv.Set(reflect.ValueOf(api))
}

func TestSearchGroup(t *testing.T) {
	ts1 := int64(1700000000123) // milliseconds
	ts2 := int64(1700000000456)

	tests := []struct {
		name           string
		group          string
		filter         string
		startMs        int64
		endMs          int64
		mock           *mockLogsAPI
		wantRecords    []model.LogRecord
		wantCalls      int
		wantErr        bool
		assertInputIdx []int // which recorded inputs to assert basic params on
	}{
		{
			name:    "single page returns records",
			group:   "/aws/lambda/foo",
			filter:  "ERROR",
			startMs: 0,
			endMs:   2000000000000,
			mock: &mockLogsAPI{responses: []*cloudwatchlogs.FilterLogEventsOutput{
				{
					Events: []types.FilteredLogEvent{
						{Timestamp: aws.Int64(ts1), LogStreamName: aws.String("s1"), Message: aws.String("hello")},
						{Timestamp: aws.Int64(ts2), LogStreamName: aws.String("s2"), Message: aws.String("world")},
					},
					NextToken: nil,
				},
			}},
			wantRecords: []model.LogRecord{
				{Timestamp: time.Unix(0, ts1*int64(time.Millisecond)), LogGroup: "/aws/lambda/foo", LogStream: "s1", Message: "hello"},
				{Timestamp: time.Unix(0, ts2*int64(time.Millisecond)), LogGroup: "/aws/lambda/foo", LogStream: "s2", Message: "world"},
			},
			wantCalls:      1,
			wantErr:        false,
			assertInputIdx: []int{0},
		},
		{
			name:    "paginates until token repeats",
			group:   "/aws/ecs/bar",
			filter:  "WARN",
			startMs: 1000,
			endMs:   9999,
			mock: &mockLogsAPI{responses: []*cloudwatchlogs.FilterLogEventsOutput{
				{
					Events: []types.FilteredLogEvent{
						{Timestamp: aws.Int64(ts1), LogStreamName: aws.String("a"), Message: aws.String("m1")},
					},
					NextToken: aws.String("A"),
				},
				{
					Events: []types.FilteredLogEvent{
						{Timestamp: aws.Int64(ts2), LogStreamName: aws.String("b"), Message: aws.String("m2")},
					},
					// Same token as previous -> stop
					NextToken: aws.String("A"),
				},
			}},
			wantRecords: []model.LogRecord{
				{Timestamp: time.Unix(0, ts1*int64(time.Millisecond)), LogGroup: "/aws/ecs/bar", LogStream: "a", Message: "m1"},
				{Timestamp: time.Unix(0, ts2*int64(time.Millisecond)), LogGroup: "/aws/ecs/bar", LogStream: "b", Message: "m2"},
			},
			wantCalls:      2,
			wantErr:        false,
			assertInputIdx: []int{0, 1},
		},
		{
			name:    "propagates api error",
			group:   "group-x",
			filter:  "INFO",
			startMs: 1,
			endMs:   2,
			mock:    &mockLogsAPI{err: errors.New("boom")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwc := &client.CloudWatchClient{}
			setPrivateClient(cwc, tt.mock)

			got, err := cwc.SearchGroup(context.Background(), tt.group, tt.filter, tt.startMs, tt.endMs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if tt.wantCalls != 0 && tt.mock.call != tt.wantCalls {
				t.Fatalf("FilterLogEvents calls = %d, want %d", tt.mock.call, tt.wantCalls)
			}

			if len(got) != len(tt.wantRecords) {
				t.Fatalf("records len = %d, want %d", len(got), len(tt.wantRecords))
			}
			for i := range tt.wantRecords {
				if !got[i].Timestamp.Equal(tt.wantRecords[i].Timestamp) ||
					got[i].LogGroup != tt.wantRecords[i].LogGroup ||
					got[i].LogStream != tt.wantRecords[i].LogStream ||
					got[i].Message != tt.wantRecords[i].Message {
					t.Fatalf("record[%d] = %+v, want %+v", i, got[i], tt.wantRecords[i])
				}
			}

			for _, idx := range tt.assertInputIdx {
				if idx >= len(tt.mock.inputs) {
					t.Fatalf("missing recorded input at idx %d", idx)
				}
				in := tt.mock.inputs[idx]
				if aws.ToString(in.LogGroupName) != tt.group {
					t.Fatalf("LogGroupName = %q, want %q", aws.ToString(in.LogGroupName), tt.group)
				}
				if aws.ToString(in.FilterPattern) != tt.filter {
					t.Fatalf("FilterPattern = %q, want %q", aws.ToString(in.FilterPattern), tt.filter)
				}
				if aws.ToInt64(in.StartTime) != tt.startMs || aws.ToInt64(in.EndTime) != tt.endMs {
					t.Fatalf("Start/End = (%d,%d), want (%d,%d)", aws.ToInt64(in.StartTime), aws.ToInt64(in.EndTime), tt.startMs, tt.endMs)
				}
			}
		})
	}
}

// helper to temporarily set env var
func withEnv(key, val string, fn func()) {
	old, had := os.LookupEnv(key)
	_ = os.Setenv(key, val)
	defer func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}()
	fn()
}

func TestNewCloudWatchOptions(t *testing.T) {
	tests := []struct {
		name    string
		options client.AuthOptions
		env     map[string]string // key -> value, value="" means unset
		wantLen int
	}{
		{
			name:    "no region or profile, no env",
			options: client.AuthOptions{},
			env:     map[string]string{"AWS_PROFILE": "", "AWS_ACCESS_KEY_ID": "", "AWS_SECRET_ACCESS_KEY": ""},
			wantLen: 0,
		},
		{
			name:    "with region",
			options: client.AuthOptions{Region: "us-east-1"},
			env:     map[string]string{"AWS_PROFILE": "", "AWS_ACCESS_KEY_ID": "", "AWS_SECRET_ACCESS_KEY": ""},
			wantLen: 1,
		},
		{
			name:    "with profile flag",
			options: client.AuthOptions{Profile: "my-profile"},
			env:     map[string]string{"AWS_PROFILE": "", "AWS_ACCESS_KEY_ID": "", "AWS_SECRET_ACCESS_KEY": ""},
			wantLen: 1,
		},
		{
			name:    "with AWS_PROFILE env",
			options: client.AuthOptions{},
			env:     map[string]string{"AWS_PROFILE": "env-profile"},
			wantLen: 1,
		},
		{
			name:    "profile flag overrides AWS_PROFILE",
			options: client.AuthOptions{Profile: "flag-profile"},
			env:     map[string]string{"AWS_PROFILE": "env-profile"},
			wantLen: 1,
		},
		{
			name:    "with static creds",
			options: client.AuthOptions{},
			env:     map[string]string{"AWS_ACCESS_KEY_ID": "key", "AWS_SECRET_ACCESS_KEY": "secret"},
			wantLen: 1,
		},
		{
			name:    "profile overrides static creds",
			options: client.AuthOptions{Profile: "my-profile"},
			env:     map[string]string{"AWS_ACCESS_KEY_ID": "key", "AWS_SECRET_ACCESS_KEY": "secret"},
			wantLen: 1,
		},
		{
			name:    "with region and profile",
			options: client.AuthOptions{Region: "us-west-2", Profile: "another-profile"},
			env:     map[string]string{},
			wantLen: 2,
		},
		{
			name:    "with region and static creds",
			options: client.AuthOptions{Region: "us-west-2"},
			env:     map[string]string{"AWS_ACCESS_KEY_ID": "key", "AWS_SECRET_ACCESS_KEY": "secret"},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup env
			for k, v := range tt.env {
				old, had := os.LookupEnv(k)
				if v == "" {
					os.Unsetenv(k)
				} else {
					os.Setenv(k, v)
				}
				defer func(k, old string, had bool) {
					if had {
						os.Setenv(k, old)
					} else {
						os.Unsetenv(k)
					}
				}(k, old, had)
			}

			opts := client.NewCloudWatchOptions(tt.options)
			if len(opts) != tt.wantLen {
				t.Errorf("NewCloudWatchOptions() returned %d options, want %d", len(opts), tt.wantLen)
			}
		})
	}
}
