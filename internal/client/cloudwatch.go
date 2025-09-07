package client

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Nao-Mk2/aws-multi-log-inspector/internal/model"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// LogsAPI is the subset of CloudWatch Logs API we use.
type LogsAPI interface {
	FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

// AuthOptions provides the necessary authentication and configuration details
// extracted from the command-line or environment, without creating a direct
// dependency from the client package to the cmd package.
type AuthOptions struct {
	Region  string
	Profile string
}

type CloudWatchClient struct {
	client LogsAPI
}

type CloudWatchOption func(*cloudWatchCfg)

type cloudWatchCfg struct {
	region      string
	profile     string
	staticCreds *credentials.StaticCredentialsProvider
}

// WithRegion sets an explicit AWS region.
func WithRegion(region string) CloudWatchOption {
	return func(c *cloudWatchCfg) { c.region = region }
}

// WithProfile picks credentials/config from a shared config profile.
func WithProfile(profile string) CloudWatchOption {
	return func(c *cloudWatchCfg) { c.profile = profile }
}

// WithStaticCredentials uses the provided static credentials.
func WithStaticCredentials(accessKey, secretKey, sessionToken string) CloudWatchOption {
	prov := credentials.NewStaticCredentialsProvider(accessKey, secretKey, sessionToken)
	return func(c *cloudWatchCfg) { c.staticCreds = &prov }
}

// NewCloudWatchClient builds a CloudWatch Logs client using functional options.
// Precedence:
//   - If profile is set via WithProfile, use it with optional WithRegion.
//   - Else if static credentials are provided via WithStaticCredentials, use them with optional WithRegion.
//   - Else use the default AWS config chain (env/instance/shared), honoring WithRegion if present.
func NewCloudWatchClient(ctx context.Context, opts ...CloudWatchOption) (*CloudWatchClient, error) {
	// Defaults
	cfgState := &cloudWatchCfg{}
	for _, o := range opts {
		o(cfgState)
	}

	var loadOpts []func(*config.LoadOptions) error
	if cfgState.region != "" {
		loadOpts = append(loadOpts, config.WithRegion(cfgState.region))
	}

	switch {
	case cfgState.profile != "":
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(cfgState.profile))
	case cfgState.staticCreds != nil:
		loadOpts = append(loadOpts, config.WithCredentialsProvider(*cfgState.staticCreds))
	default:
		// default chain only, region already appended if provided
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	return &CloudWatchClient{client: cloudwatchlogs.NewFromConfig(cfg)}, nil
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

// NewCloudWatchOptions creates a slice of CloudWatchOption from AuthOptions and environment variables.
func NewCloudWatchOptions(authOpts AuthOptions) []CloudWatchOption {
	var opts []CloudWatchOption
	if authOpts.Region != "" {
		opts = append(opts, WithRegion(authOpts.Region))
	}

	resolvedProfile := resolveProfile(authOpts.Profile)
	if resolvedProfile != "" {
		opts = append(opts, WithProfile(resolvedProfile))
	} else {
		// Fallback to static credentials if profile is not set
		ak := os.Getenv("AWS_ACCESS_KEY_ID")
		sk := os.Getenv("AWS_SECRET_ACCESS_KEY")
		st := os.Getenv("AWS_SESSION_TOKEN")
		if ak != "" && sk != "" {
			opts = append(opts, WithStaticCredentials(ak, sk, st))
		}
	}
	return opts
}

// resolveProfile returns the profile from flag or AWS_PROFILE env, or empty.
func resolveProfile(flagProfile string) string {
	if flagProfile != "" {
		return flagProfile
	}
	return os.Getenv("AWS_PROFILE")
}
