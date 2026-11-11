// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package opensearchexporter

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/opensearchexporter/internal/metadata"
)

// Exercise the HTTP bulk decoder, failure callbacks and exporterhelper partial
// retry together. A permanent sibling must neither suppress retryable documents
// nor cause already accepted documents to be sent again.
func TestStudioMixedBulkFailuresRetryOnlyTransientDocuments(t *testing.T) {
	for _, signal := range []string{"logs", "traces"} {
		t.Run(signal, func(t *testing.T) {
			statuses := []int{200, 400, 429, 500, 408}
			logs := plog.NewLogs()
			traces := ptrace.NewTraces()
			records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
			spans := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans()
			ids := make([]string, 0, len(statuses))
			for i := range statuses {
				span := spans.AppendEmpty()
				span.SetTraceID(pcommon.TraceID{1})
				span.SetSpanID(pcommon.SpanID{byte(i + 1)})
				span.SetStartTimestamp(pcommon.Timestamp(1800000000123456789))
				span.SetEndTimestamp(span.StartTimestamp() + 1)
				id := span.TraceID().String() + "-" + span.SpanID().String()
				ids = append(ids, id)
				record := records.AppendEmpty()
				record.SetTimestamp(span.StartTimestamp())
				record.Attributes().PutStr("event_id", id)
				record.Body().SetStr("mixed bulk regression")
			}
			var mu sync.Mutex
			var requests [][]string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				decoder := json.NewDecoder(r.Body)
				var batch []string
				var items []map[string]any
				mu.Lock()
				first := len(requests) == 0
				mu.Unlock()
				for {
					var action map[string]map[string]any
					if err := decoder.Decode(&action); err == io.EOF {
						break
					} else if err != nil {
						t.Error(err)
						http.Error(w, "invalid action", http.StatusBadRequest)
						return
					}
					var document map[string]any
					if err := decoder.Decode(&document); err != nil {
						t.Error(err)
						http.Error(w, "missing document", http.StatusBadRequest)
						return
					}
					id, _ := action["index"]["_id"].(string)
					batch = append(batch, id)
					status := http.StatusOK
					if first {
						for i, expectedID := range ids {
							if expectedID == id {
								status = statuses[i]
							}
						}
					}
					item := map[string]any{"_index": action["index"]["_index"], "_id": id, "status": status}
					if status != http.StatusOK {
						item["error"] = map[string]any{"type": "synthetic_failure", "reason": "mixed response regression"}
					}
					items = append(items, map[string]any{"index": item})
				}
				mu.Lock()
				requests = append(requests, batch)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": first, "items": items})
			}))
			defer server.Close()
			cfg := withDefaultConfig(func(c *Config) {
				c.Endpoint = server.URL
				c.Mode = "studio"
				c.BulkAction = "index"
				c.LogsIndex = "studio-event-ingest-v1"
				c.TracesIndex = "studio-traces-ingest-v1"
				c.QueueConfig = configoptional.None[exporterhelper.QueueBatchConfig]()
				c.InitialInterval = 10 * time.Millisecond
				c.MaxInterval = 20 * time.Millisecond
				c.MaxElapsedTime = time.Second
			})
			core, observed := observer.New(zap.ErrorLevel)
			settings := exportertest.NewNopSettings(metadata.Type)
			settings.Logger = zap.New(core)
			if signal == "logs" {
				exporter, err := createLogsExporter(t.Context(), settings, cfg)
				require.NoError(t, err)
				require.NoError(t, exporter.Start(t.Context(), componenttest.NewNopHost()))
				require.NoError(t, exporter.ConsumeLogs(t.Context(), logs))
				require.NoError(t, exporter.Shutdown(t.Context()))
			} else {
				exporter, err := createTracesExporter(t.Context(), settings, cfg)
				require.NoError(t, err)
				require.NoError(t, exporter.Start(t.Context(), componenttest.NewNopHost()))
				require.NoError(t, exporter.ConsumeTraces(t.Context(), traces))
				require.NoError(t, exporter.Shutdown(t.Context()))
			}
			mu.Lock()
			defer mu.Unlock()
			require.Len(t, requests, 2)
			require.ElementsMatch(t, ids, requests[0])
			require.ElementsMatch(t, ids[2:], requests[1])
			require.Equal(t, 1, observed.FilterMessage("OpenSearch rejected a document; it will not be retried").Len())
		})
	}
}
