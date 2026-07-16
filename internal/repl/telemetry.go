package repl

import (
	"context"
	"time"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/internal/telemetry"
)

type turnObserver struct {
	session *Session
	id      string
	started time.Time
}

func (s *Session) startTurn(ctx context.Context, userText string) turnObserver {
	observer := turnObserver{session: s, id: telemetry.NewTurnID(), started: time.Now()}
	s.record(ctx, telemetry.Event{
		Time:    observer.started.UTC(),
		Name:    telemetry.EventTurnStart,
		TraceID: s.traceID,
		TurnID:  observer.id,
		Attributes: telemetry.Attrs{
			string(telemetry.AttrModel):            s.model,
			string(telemetry.AttrToolCount):        len(s.tools),
			string(telemetry.AttrMaxIterations):    s.maxIterations,
			string(telemetry.AttrReasoningEnabled): s.reasoning.Enabled,
			string(telemetry.AttrUserTextBytes):    len([]byte(userText)),
		},
	})
	return observer
}

func (o turnObserver) end(ctx context.Context, result TurnResult) error {
	o.session.record(ctx, telemetry.Event{
		Time:    time.Now().UTC(),
		Name:    telemetry.EventTurnEnd,
		TraceID: o.session.traceID,
		TurnID:  o.id,
		Attributes: telemetry.MergeAttrs(telemetry.Attrs{
			string(telemetry.AttrIterations):            result.Iterations,
			string(telemetry.AttrStopReason):            string(result.FinalStopReason),
			string(telemetry.AttrToolResultCount):       len(result.ToolResults),
			string(telemetry.AttrAssistantMessageCount): len(result.AssistantMessages),
			string(telemetry.AttrDurationMS):            telemetry.DurationMS(time.Since(o.started)),
		}, telemetry.UsageAttrs(result.FinalUsage)),
	})
	return nil
}

func (o turnObserver) error(ctx context.Context, result TurnResult, err error) error {
	if err == nil {
		return nil
	}
	o.session.record(ctx, telemetry.Event{
		Time:    time.Now().UTC(),
		Name:    telemetry.EventTurnError,
		TraceID: o.session.traceID,
		TurnID:  o.id,
		Attributes: telemetry.Attrs{
			string(telemetry.AttrError):      err.Error(),
			string(telemetry.AttrIterations): result.Iterations,
			string(telemetry.AttrDurationMS): telemetry.DurationMS(time.Since(o.started)),
		},
	})
	return err
}

func (s *Session) llmErrorEvent(requestID string, turnID string, purpose string, req llm.Request, started time.Time, err error) telemetry.Event {
	return telemetry.Event{
		Time:      time.Now().UTC(),
		Name:      telemetry.EventLLMRequestError,
		TraceID:   s.traceID,
		TurnID:    turnID,
		RequestID: requestID,
		Attributes: telemetry.Attrs{
			string(telemetry.AttrProvider):     s.provider.Name(),
			string(telemetry.AttrModel):        req.Model,
			string(telemetry.AttrPurpose):      purpose,
			string(telemetry.AttrError):        err.Error(),
			string(telemetry.AttrDurationMS):   telemetry.DurationMS(time.Since(started)),
			string(telemetry.AttrMessageCount): len(req.Messages),
			string(telemetry.AttrToolCount):    len(req.Tools),
		},
	}
}

func (s *Session) record(ctx context.Context, event telemetry.Event) {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	s.telemetry.Record(ctx, event)
}
