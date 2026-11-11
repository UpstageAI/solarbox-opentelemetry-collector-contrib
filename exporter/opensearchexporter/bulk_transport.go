// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package opensearchexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/opensearchexporter"

import (
	"bytes"
	"io"
	"net/http"
)

// The bulk indexer reuses its buffer as soon as RoundTrip returns. HTTP servers
// may respond before the transport finishes reading a large request body. Give
// the transport its own immutable copy so an early error cannot race with buffer
// reuse or corrupt the next bulk request.
type bulkTransport struct {
	base http.RoundTripper
}

func (t bulkTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return t.base.RoundTrip(request)
	}
	body, err := io.ReadAll(request.Body)
	closeErr := request.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	clone := request.Clone(request.Context())
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	clone.Body, _ = clone.GetBody()
	return t.base.RoundTrip(clone)
}
