package anthropic

import (
	"errors"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/wt68/runcode/engine/llm"
)

// classifyError normalizes an SDK/transport error into a neutral *llm.Error so
// upper layers can classify Anthropic failures the same way they classify any
// other provider's. A nil error stays nil.
//
// The SDK exhausts its own retries (option.WithMaxRetries) before surfacing an
// error, so a result here is the terminal outcome; the Retryable flag is set
// from the status purely as information for callers/logging, not to request a
// further retry at this layer.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		kind, retryable := llm.ClassifyHTTPStatus(apiErr.StatusCode)
		return &llm.Error{
			Kind:       kind,
			Retryable:  retryable,
			StatusCode: apiErr.StatusCode,
			Provider:   providerName,
			Message:    apiErr.Error(),
			Err:        err,
		}
	}
	return &llm.Error{
		Kind:     llm.ErrorKindUnknown,
		Provider: providerName,
		Message:  err.Error(),
		Err:      err,
	}
}
