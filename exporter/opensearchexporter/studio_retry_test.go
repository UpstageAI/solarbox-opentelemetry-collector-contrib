// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package opensearchexporter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exportertest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/opensearchexporter/internal/metadata"
)

func TestStudioExporterRetriesWholeBulkFailures(t *testing.T) {
	for _, signal := range []string{"logs", "traces"} {
		for _, status := range []int{500, 429, 403} {
			t.Run(fmt.Sprintf("%s/%d", signal, status), func(t *testing.T) {
				var requests atomic.Int32
				var mu sync.Mutex
				var ids []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if requests.Add(1) == 1 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "error": map[string]any{"type": "synthetic", "reason": "failure"}})
						return
					}
					scanner := bufio.NewScanner(r.Body)
					scanner.Buffer(make([]byte, 65536), 4*1024*1024)
					var items []map[string]any
					for scanner.Scan() {
						var action map[string]map[string]any
						if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
							t.Error(err)
							return
						}
						id, ok := action["index"]["_id"].(string)
						if !ok || id == "" {
							t.Error("missing stable document ID")
							return
						}
						mu.Lock()
						ids = append(ids, id)
						mu.Unlock()
						if !scanner.Scan() {
							t.Error("missing document")
							return
						}
						items = append(items, map[string]any{"index": map[string]any{"_index": action["index"]["_index"], "_id": id, "status": 200}})
					}
					if err := scanner.Err(); err != nil {
						t.Error(err)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"errors": false, "items": items})
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
				settings := exportertest.NewNopSettings(metadata.Type)
				var err error
				if signal == "logs" {
					exp, e := createLogsExporter(t.Context(), settings, cfg)
					require.NoError(t, e)
					require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
					err = exp.ConsumeLogs(t.Context(), studioTestLogs())
					require.NoError(t, exp.Shutdown(t.Context()))
				} else {
					exp, e := createTracesExporter(t.Context(), settings, cfg)
					require.NoError(t, e)
					require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
					err = exp.ConsumeTraces(t.Context(), studioTestTraces())
					require.NoError(t, exp.Shutdown(t.Context()))
				}
				if status == 403 {
					require.Error(t, err)
					require.Equal(t, int32(1), requests.Load())
				} else {
					require.NoError(t, err)
					require.Equal(t, int32(2), requests.Load())
					mu.Lock()
					defer mu.Unlock()
					if signal == "logs" {
						require.Equal(t, []string{"event-1", "event-2"}, ids)
					} else {
						require.Len(t, ids, 1)
					}
				}
			})
		}
	}
}
