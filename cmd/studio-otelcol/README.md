# Studio OpenSearch Collector

This optional distribution fixes Studio's direct OpenSearch ingestion without
changing the existing ClickHouse catalog or its Collector image. It includes
OTLP, batch/filter, OpenSearch, Basic authentication, file storage and health checks.

Build from the repository root with Go 1.25 or newer and Collector Builder 0.148.0:

    mkdir -p dist
    ocb --config=cmd/studio-otelcol/builder-config.yaml
    ./dist/studio-otelcol/studio-otelcol --config=/path/to/collector.yaml
    docker build -f cmd/studio-otelcol/Dockerfile -t studio-opensearch-collector:0.148.0-studio.1 .

The runtime image includes /bin/sh, CA roots, and /otelcol-contrib, and runs as
UID/GID 1000. Queue directories must be writable by that user. Image publication
is separate from building and testing this source.

Use mapping.mode: studio, bulk_action: index, explicit logs_index or
traces_index, and no index time suffix. Route event/audit/LLM logs to their
respective studio-*-ingest-v1 routing indices. Keep LLM batches at one record,
and use separate persistent exporter queues. Existing Studio ingest pipelines
perform validation and event-date routing; this Collector does not initialize
schemas or change query settings.

Studio mode preserves typed OTLP attributes and the existing Data Prepper OTLP
document shape. Log IDs come from attributes.event_id; span IDs are traceId-spanId.
Retries overwrite the same document instead of double-counting.
HTTP request/connection failures remain retryable; authentication and invalid
document errors remain permanent. The default SS4O output remains unchanged.
