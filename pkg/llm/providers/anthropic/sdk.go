package anthropic

import (
	"context"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

type sdkStream interface {
	Next() bool
	Current() sdk.MessageStreamEventUnion
	Err() error
	Close() error
}

type messageStreamClient interface {
	newStreaming(ctx context.Context, params sdk.MessageNewParams) sdkStream
}

type sdkClient struct {
	client sdk.Client
}

func newSDKClient(opts Options) messageStreamClient {
	requestOptions := []option.RequestOption{option.WithoutEnvironmentDefaults()}
	if opts.AuthToken != "" {
		requestOptions = append(requestOptions, option.WithAuthToken(opts.AuthToken))
	} else if opts.APIKey != "" {
		requestOptions = append(requestOptions, option.WithAPIKey(opts.APIKey))
	}
	if opts.BaseURL != "" {
		requestOptions = append(requestOptions, option.WithBaseURL(opts.BaseURL))
	}
	return &sdkClient{client: sdk.NewClient(requestOptions...)}
}

func (c *sdkClient) newStreaming(ctx context.Context, params sdk.MessageNewParams) sdkStream {
	return c.client.Messages.NewStreaming(ctx, params)
}

var _ sdkStream = (*ssestream.Stream[sdk.MessageStreamEventUnion])(nil)
