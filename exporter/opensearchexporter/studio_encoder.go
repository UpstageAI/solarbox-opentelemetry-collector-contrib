// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package opensearchexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/opensearchexporter"

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// studioMappingModel retains OTLP field types and the existing Studio schema
// written by Data Prepper output_format: otel. Only mapping.mode: studio selects it.
type studioMappingModel struct{}

func (*studioMappingModel) logDocumentID(record plog.LogRecord) string {
	value, _ := record.Attributes().Get("event_id")
	return value.Str()
}

func (*studioMappingModel) traceDocumentID(span ptrace.Span) string {
	return span.TraceID().String() + "-" + span.SpanID().String()
}

func studioResource(resource pcommon.Resource, schemaURL string) map[string]any {
	return map[string]any{"attributes": resource.Attributes().AsRaw(), "droppedAttributesCount": resource.DroppedAttributesCount(), "schemaUrl": schemaURL}
}

func studioScope(scope pcommon.InstrumentationScope) map[string]any {
	return map[string]any{"name": scope.Name(), "version": scope.Version(), "attributes": scope.Attributes().AsRaw(), "droppedAttributesCount": scope.DroppedAttributesCount()}
}

func studioTime(value pcommon.Timestamp) string {
	return value.AsTime().UTC().Format(time.RFC3339Nano)
}

func (model *studioMappingModel) encodeLog(resource pcommon.Resource, scope pcommon.InstrumentationScope, schemaURL string, record plog.LogRecord) ([]byte, error) {
	return model.encodeLogWithResourceSchema(resource, "", scope, schemaURL, record)
}

func (*studioMappingModel) encodeLogWithResourceSchema(resource pcommon.Resource, resourceSchemaURL string, scope pcommon.InstrumentationScope, schemaURL string, record plog.LogRecord) ([]byte, error) {
	id, exists := record.Attributes().Get("event_id")
	if !exists || id.Type() != pcommon.ValueTypeStr || id.Str() == "" || len(id.Str()) > 512 {
		return nil, errors.New("studio log requires a nonempty string event_id of at most 512 bytes")
	}
	if record.Timestamp() == 0 {
		return nil, errors.New("studio log requires an event timestamp")
	}
	document := map[string]any{
		"time": studioTime(record.Timestamp()), "observedTime": studioTime(record.ObservedTimestamp()),
		"attributes": record.Attributes().AsRaw(), "body": record.Body().AsRaw(),
		"resource": studioResource(resource, resourceSchemaURL), "scope": studioScope(scope), "schemaUrl": schemaURL,
		"traceId": record.TraceID().String(), "spanId": record.SpanID().String(),
		"severityNumber": int32(record.SeverityNumber()), "severityText": record.SeverityText(),
		"droppedAttributesCount": record.DroppedAttributesCount(), "flags": uint32(record.Flags()),
	}
	return json.Marshal(document)
}

func (model *studioMappingModel) encodeTrace(resource pcommon.Resource, scope pcommon.InstrumentationScope, schemaURL string, span ptrace.Span) ([]byte, error) {
	return model.encodeTraceWithResourceSchema(resource, "", scope, schemaURL, span)
}

func (*studioMappingModel) encodeTraceWithResourceSchema(resource pcommon.Resource, resourceSchemaURL string, scope pcommon.InstrumentationScope, schemaURL string, span ptrace.Span) ([]byte, error) {
	if span.TraceID().IsEmpty() || span.SpanID().IsEmpty() || span.StartTimestamp() == 0 ||
		span.EndTimestamp() < span.StartTimestamp() || uint64(span.EndTimestamp()-span.StartTimestamp()) > math.MaxInt64 {
		return nil, errors.New("studio span requires valid identity and a nonnegative representable interval")
	}
	events := make([]map[string]any, 0, span.Events().Len())
	for i := 0; i < span.Events().Len(); i++ {
		event := span.Events().At(i)
		events = append(events, map[string]any{"time": studioTime(event.Timestamp()), "name": event.Name(), "attributes": event.Attributes().AsRaw(), "droppedAttributesCount": event.DroppedAttributesCount()})
	}
	links := make([]map[string]any, 0, span.Links().Len())
	for i := 0; i < span.Links().Len(); i++ {
		link := span.Links().At(i)
		links = append(links, map[string]any{"traceId": link.TraceID().String(), "spanId": link.SpanID().String(), "traceState": link.TraceState().AsRaw(), "attributes": link.Attributes().AsRaw(), "droppedAttributesCount": link.DroppedAttributesCount(), "flags": link.Flags()})
	}
	document := map[string]any{
		"traceId": span.TraceID().String(), "spanId": span.SpanID().String(), "parentSpanId": span.ParentSpanID().String(), "traceState": span.TraceState().AsRaw(),
		"name": span.Name(), "kind": "SPAN_KIND_" + strings.ToUpper(span.Kind().String()),
		"startTime": studioTime(span.StartTimestamp()), "endTime": studioTime(span.EndTimestamp()),
		"durationInNanos": int64(span.EndTimestamp() - span.StartTimestamp()),
		"status":          map[string]any{"code": int32(span.Status().Code()), "message": span.Status().Message()},
		"resource":        studioResource(resource, resourceSchemaURL), "scope": studioScope(scope), "schemaUrl": schemaURL,
		"attributes": span.Attributes().AsRaw(), "droppedAttributesCount": span.DroppedAttributesCount(),
		"events": events, "droppedEventsCount": span.DroppedEventsCount(), "links": links, "droppedLinksCount": span.DroppedLinksCount(), "flags": span.Flags(),
	}
	return json.Marshal(document)
}
