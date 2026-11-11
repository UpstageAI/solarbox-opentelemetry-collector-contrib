# Build from the repository root. No image is published by this Dockerfile.
FROM golang:1.26.11 AS build
WORKDIR /src
COPY exporter/opensearchexporter ./exporter/opensearchexporter
COPY cmd/studio-otelcol/builder-config.yaml ./cmd/studio-otelcol/builder-config.yaml
RUN mkdir -p dist && go run go.opentelemetry.io/collector/cmd/builder@v0.148.0 --config=cmd/studio-otelcol/builder-config.yaml --skip-compilation
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN cd dist/studio-otelcol && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /studio-otelcol .

# Keep a shell for AI Pack's configuration/volume entrypoint and trust roots for HTTPS.
FROM alpine:3.23.3@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659
RUN test -s /etc/ssl/cert.pem && mkdir -p /var/lib/otelcol && chown 1000:1000 /var/lib/otelcol
COPY --from=build /studio-otelcol /otelcol-contrib
USER 1000:1000
EXPOSE 4317 4318
ENTRYPOINT ["/otelcol-contrib"]
CMD ["--config=/etc/otel/config.yaml"]
