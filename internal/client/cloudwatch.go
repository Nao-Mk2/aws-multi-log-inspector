package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// NewCloudWatchClient loads AWS configuration using the provided region and
// shared profile, and returns a CloudWatch Logs client. region may be empty
// to use default resolution. profile is required and should match the shared
// config profile name.
func NewCloudWatchClient(ctx context.Context, region, profile string) (*cloudwatchlogs.Client, error) {
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
	return cloudwatchlogs.NewFromConfig(cfg), nil
}
