package client

import (
	"context"
	"fmt"
	"time"

	"github.com/Nao-Mk2/aws-multi-log-inspector/internal/model"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// LogsAPI is the subset of CloudWatch Logs API we use.
type LogsAPI interface {
	FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

type CloudWatchClient struct {
	client LogsAPI
}

// NewCloudWatchClient loads AWS configuration using the provided region and
// shared profile, and returns a CloudWatch Logs client. region may be empty
// to use default resolution. profile is required and should match the shared
// config profile name.
func NewCloudWatchClient(ctx context.Context, region, profile string) (*CloudWatchClient, error) {
	if profile == "" {
		return nil, fmt.Errorf("profile required")
	}
	var cfgOpts []func(*config.LoadOptions) error
	if region != "" {
		cfgOpts = append(cfgOpts, config.WithRegion(region))
	}
	cfgOpts = append(cfgOpts, config.WithSharedConfigProfile(profile))
	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, err
	}
	return &CloudWatchClient{
		client: cloudwatchlogs.NewFromConfig(cfg),
	}, nil
}

// SearchGroup searches logs in a single log group
func (cwc *CloudWatchClient) SearchGroup(ctx context.Context, group, filterPattern string, startMs, endMs int64) ([]model.LogRecord, error) {
	var records []model.LogRecord
	var next *string
	for {
		out, err := cwc.client.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
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
			records = append(records, model.LogRecord{
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
