// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package opensearchexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/opensearchexporter"

import (
	"errors"

	"github.com/opensearch-project/opensearch-go/v4"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Whole-request errors have no per-item status. Preserve transient failures so
// exporterhelper can retry instead of acknowledging and deleting queued data.
func classifyBulkError(err error) error {
	var structured *opensearch.StructError
	var text *opensearch.StringError
	status := 0
	if errors.As(err, &structured) {
		status = structured.Status
	} else if errors.As(err, &text) {
		status = text.Status
	}
	if status >= 400 && status < 500 && status != 408 && status != 429 {
		return consumererror.NewPermanent(err)
	}
	return err
}

// Returning a join containing a permanent error discards retryable siblings.
// Combine all failed items into one partial-data error instead of letting
// errors.As expose only the first retryable document.
func joinLogsErrors(errs []error) error {
	retry := plog.NewLogs()
	var retryErrors []error
	wholeBatch := false
	for _, err := range errs {
		if err == nil || consumererror.IsPermanent(err) {
			continue
		}
		retryErrors = append(retryErrors, err)
		var partial consumererror.Logs
		if errors.As(err, &partial) {
			items := partial.Data().ResourceLogs()
			for i := 0; i < items.Len(); i++ {
				items.At(i).CopyTo(retry.ResourceLogs().AppendEmpty())
			}
		} else {
			wholeBatch = true
		}
	}
	if len(retryErrors) == 0 {
		return errors.Join(errs...)
	}
	err := errors.Join(retryErrors...)
	if wholeBatch {
		// A whole-request failure must not expose only the first partial-data wrapper.
		return errors.New(err.Error())
	}
	return consumererror.NewLogs(err, retry)
}

func joinTracesErrors(errs []error) error {
	retry := ptrace.NewTraces()
	var retryErrors []error
	wholeBatch := false
	for _, err := range errs {
		if err == nil || consumererror.IsPermanent(err) {
			continue
		}
		retryErrors = append(retryErrors, err)
		var partial consumererror.Traces
		if errors.As(err, &partial) {
			items := partial.Data().ResourceSpans()
			for i := 0; i < items.Len(); i++ {
				items.At(i).CopyTo(retry.ResourceSpans().AppendEmpty())
			}
		} else {
			wholeBatch = true
		}
	}
	if len(retryErrors) == 0 {
		return errors.Join(errs...)
	}
	err := errors.Join(retryErrors...)
	if wholeBatch {
		// A whole-request failure must not expose only the first partial-data wrapper.
		return errors.New(err.Error())
	}
	return consumererror.NewTraces(err, retry)
}
