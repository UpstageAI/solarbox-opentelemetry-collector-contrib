// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package opensearchexporter

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func studioTestLogs() plog.Logs {
	logs := plog.NewLogs()
	resource := logs.ResourceLogs().AppendEmpty()
	resource.Resource().Attributes().PutStr("service.name", "studio-pipeline")
	resource.Resource().Attributes().PutInt("typed.resource", 7)
	records := resource.ScopeLogs().AppendEmpty().LogRecords()
	for _, id := range []string{"event-1", "event-2"} {
		r := records.AppendEmpty()
		r.SetTimestamp(pcommon.Timestamp(1800000000123456789))
		r.SetSeverityText("INFO")
		r.SetSeverityNumber(plog.SeverityNumberInfo)
		r.Attributes().PutStr("event_id", id)
		r.Attributes().PutStr("log_type", "llm_call")
		r.Attributes().PutStr("input", strings.Repeat("🚀", 262144))
		r.Attributes().PutInt("input_tokens", 42)
		r.Attributes().PutBool("input_truncated", true)
		r.Attributes().PutEmptyMap("confidence").PutInt("high", 7)
		r.Body().SetStr("unmodified body")
	}
	return logs
}

func studioTestTraces() ptrace.Traces {
	traces := ptrace.NewTraces()
	r := traces.ResourceSpans().AppendEmpty()
	r.Resource().Attributes().PutStr("service.name", "studio-pipeline")
	s := r.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	s.SetTraceID(pcommon.TraceID{1})
	s.SetSpanID(pcommon.SpanID{2})
	s.SetStartTimestamp(pcommon.Timestamp(1800000000123456789))
	s.SetEndTimestamp(s.StartTimestamp() + 1500000000)
	s.SetName("extract")
	s.SetKind(ptrace.SpanKindInternal)
	s.Status().SetCode(ptrace.StatusCodeError)
	s.Attributes().PutStr("org_id", "org-1")
	s.Attributes().PutInt("gen_ai.usage.input_tokens", 42)
	s.Events().AppendEmpty().Attributes().PutStr("preserved", "yes")
	s.Links().AppendEmpty().Attributes().PutInt("preserved", 3)
	return traces
}

func TestStudioEncodingPreservesTheExistingReadContract(t *testing.T) {
	model := &studioMappingModel{}
	logs := studioTestLogs().ResourceLogs().At(0)
	record := logs.ScopeLogs().At(0).LogRecords().At(0)
	payload, err := model.encodeLog(logs.Resource(), pcommon.NewInstrumentationScope(), "", record)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(payload, &doc))
	require.Equal(t, "2027-01-15T08:00:00.123456789Z", doc["time"])
	require.Equal(t, "INFO", doc["severityText"])
	require.Equal(t, float64(9), doc["severityNumber"])
	attrs := doc["attributes"].(map[string]any)
	require.Equal(t, strings.Repeat("🚀", 262144), attrs["input"])
	require.Equal(t, float64(42), attrs["input_tokens"])
	require.Equal(t, true, attrs["input_truncated"])
	require.Equal(t, map[string]any{"high": float64(7)}, attrs["confidence"])
	resource := doc["resource"].(map[string]any)["attributes"].(map[string]any)
	require.Equal(t, "studio-pipeline", resource["service.name"])
	require.Equal(t, float64(7), resource["typed.resource"])
	require.Equal(t, "event-1", model.logDocumentID(record))
	spans := studioTestTraces().ResourceSpans().At(0)
	span := spans.ScopeSpans().At(0).Spans().At(0)
	payload, err = model.encodeTrace(spans.Resource(), pcommon.NewInstrumentationScope(), "", span)
	require.NoError(t, err)
	doc = nil
	require.NoError(t, json.Unmarshal(payload, &doc))
	require.Equal(t, float64(1500000000), doc["durationInNanos"])
	require.Equal(t, float64(2), doc["status"].(map[string]any)["code"])
	require.Equal(t, "SPAN_KIND_INTERNAL", doc["kind"])
	require.Equal(t, "2027-01-15T08:00:00.123456789Z", doc["startTime"])
	require.Equal(t, span.TraceID().String()+"-"+span.SpanID().String(), model.traceDocumentID(span))
	require.Len(t, doc["events"], 1)
	require.Len(t, doc["links"], 1)
}

func TestStudioRejectsUnstableIdentityAndRouting(t *testing.T) {
	model := &studioMappingModel{}
	_, err := model.encodeLog(pcommon.NewResource(), pcommon.NewInstrumentationScope(), "", plog.NewLogRecord())
	require.Error(t, err)
	_, err = model.encodeTrace(pcommon.NewResource(), pcommon.NewInstrumentationScope(), "", ptrace.NewSpan())
	require.Error(t, err)
	cfg := withDefaultConfig(func(c *Config) {
		c.Endpoint = "https://search.example"
		c.Mode = "studio"
		c.LogsIndex = "studio-event-ingest-v1"
	})
	require.ErrorContains(t, cfg.Validate(), "bulk_action")
	cfg.BulkAction = "index"
	require.NoError(t, cfg.Validate())
	cfg.LogsIndexTimeFormat = "yyyy.MM.dd"
	require.ErrorContains(t, cfg.Validate(), "time suffixes")
}

func TestMixedFailuresKeepEveryRetryableDocument(t *testing.T) {
	bad := consumererror.NewPermanent(errors.New("mapping rejected"))
	logs := studioTestLogs()
	err := joinLogsErrors([]error{bad, consumererror.NewLogs(errors.New("429"), logs), consumererror.NewLogs(errors.New("500"), logs)})
	require.False(t, consumererror.IsPermanent(err))
	var partial consumererror.Logs
	require.ErrorAs(t, err, &partial)
	require.Equal(t, 4, partial.Data().LogRecordCount())
	traces := studioTestTraces()
	err = joinTracesErrors([]error{bad, consumererror.NewTraces(errors.New("429"), traces), consumererror.NewTraces(errors.New("500"), traces)})
	require.False(t, consumererror.IsPermanent(err))
	var failed consumererror.Traces
	require.ErrorAs(t, err, &failed)
	require.Equal(t, 2, failed.Data().SpanCount())
	err = joinLogsErrors([]error{consumererror.NewLogs(errors.New("partial"), logs), errors.New("whole request failed")})
	require.False(t, consumererror.IsPermanent(err))
	require.NotErrorAs(t, err, &partial)
}

func TestPermanentDocumentErrorsRemainVisibleWhenSiblingsRetry(t *testing.T) {
	core, observed := observer.New(zap.ErrorLevel)
	logger := zap.New(core)
	logs := newLogBulkIndexer("index", &studioMappingModel{})
	logs.logger = logger
	logs.appendPermanentError(errors.New("mapping rejected"))
	logs.appendRetryLogError(errors.New("busy"), studioTestLogs())
	require.False(t, consumererror.IsPermanent(logs.joinedError()))
	traces := newTraceBulkIndexer("index", &studioMappingModel{})
	traces.logger = logger
	traces.appendPermanentError(errors.New("mapping rejected"))
	traces.appendRetryTraceError(errors.New("busy"), studioTestTraces())
	require.False(t, consumererror.IsPermanent(traces.joinedError()))
	require.Equal(t, 2, observed.Len())
	for _, entry := range observed.All() {
		require.Equal(t, "OpenSearch rejected a document; it will not be retried", entry.Message)
		require.Equal(t, "mapping rejected", entry.ContextMap()["error"])
	}
}
