package logadapter_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"testing"

	"github.com/gofrs/uuid"

	logadapter "github.com/Mattel/logrus-stackdriver-formatter"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace"
)

func TestFormatter(t *testing.T) {
	for i := range formatterTests {
		tt := formatterTests[i]
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer

			logger := logrus.New()
			logger.Out = &out
			logger.Formatter = logadapter.NewFormatter(
				logadapter.WithProjectID("test-project"),
				logadapter.WithService("test"),
				logadapter.WithVersion("0.1"),
				logadapter.WithSkipTimestamp(),
				logadapter.WithSourceReference(
					"https://github.com/Mattel/test.git",
					"v1.2.3",
				),
				logadapter.WithGlobalTraceID(TraceID),
			)
			tt.run(logger)

			got, err := json.Marshal(tt.out)
			if err != nil {
				t.Error(err)
			}
			assert.JSONEq(t, string(got), out.String())
		})
	}
}

var (
	TraceFlags  = trace.FlagsSampled
	TraceID     = uuid.Must(uuid.FromString("105445aa7843bc8bf206b12000100000"))
	SpanID      = [8]byte{0, 0, 0, 0, 0, 0, 0, 1}
	SpanContext = trace.SpanContext{}.WithSpanID(SpanID).
		WithTraceID(trace.TraceID(TraceID)).
		WithTraceFlags(TraceFlags)
	LineNumber = platformLine()
)

var formatterTests = []struct {
	run  func(*logrus.Logger)
	out  map[string]interface{}
	name string
}{
	{
		name: "With Field",
		run: func(logger *logrus.Logger) {
			logger.
				WithField("foo", "bar").
				WithField("span_context", SpanContext).
				Info("my log entry")
		},
		out: map[string]interface{}{
			"severity": "INFO",
			"message":  "my log entry",
			"logName":  "projects/test-project/logs/test",

			"logging.googleapis.com/trace":         "projects/test-project/traces/105445aa7843bc8bf206b12000100000",
			"logging.googleapis.com/spanId":        "0000000000000001",
			"logging.googleapis.com/trace_sampled": true,
			"context": map[string]interface{}{
				"data": map[string]interface{}{
					"foo": "bar",
				},
			},
			"logging.googleapis.com/sourceLocation": map[string]interface{}{
				"file":     "testing/testing.go",
				"function": "tRunner",
				"line":     LineNumber,
			},
		},
	},
	{
		name: "WithField and Error",
		run: func(logger *logrus.Logger) {
			logger.
				WithField("foo", "bar").
				WithField("span_context", SpanContext).
				Error("my log entry")
		},
		out: map[string]interface{}{
			"@type":    "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent",
			"severity": "ERROR",
			"message":  "my log entry",
			"logName":  "projects/test-project/logs/test",

			"logging.googleapis.com/trace":         "projects/test-project/traces/105445aa7843bc8bf206b12000100000",
			"logging.googleapis.com/spanId":        "0000000000000001",
			"logging.googleapis.com/trace_sampled": true,
			"serviceContext": map[string]interface{}{
				"service": "test",
				"version": "0.1",
			},
			"context": map[string]interface{}{
				"data": map[string]interface{}{
					"foo": "bar",
				},
				"sourceReferences": []map[string]interface{}{
					{
						"repository": "https://github.com/Mattel/test.git",
						"revisionId": "v1.2.3",
					},
				},
				"reportLocation": map[string]interface{}{
					"filePath":     "testing/testing.go",
					"lineNumber":   LineNumber,
					"functionName": "tRunner",
				},
			},
			"logging.googleapis.com/sourceLocation": map[string]interface{}{
				"file":     "testing/testing.go",
				"line":     LineNumber,
				"function": "tRunner",
			},
		},
	},
	{
		name: "WithField, WithError and Error",
		run: func(logger *logrus.Logger) {
			logger.
				WithField("foo", "bar").
				WithField("span_context", SpanContext).
				WithError(errors.New("test error")).
				Error("my log entry")
		},
		out: map[string]interface{}{
			"@type":    "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent",
			"severity": "ERROR",
			"message":  "my log entry\ntest error",
			"logName":  "projects/test-project/logs/test",

			"logging.googleapis.com/trace":         "projects/test-project/traces/105445aa7843bc8bf206b12000100000",
			"logging.googleapis.com/spanId":        "0000000000000001",
			"logging.googleapis.com/trace_sampled": true,
			"serviceContext": map[string]interface{}{
				"service": "test",
				"version": "0.1",
			},
			"context": map[string]interface{}{
				"data": map[string]interface{}{
					"foo":   "bar",
					"error": "test error",
				},
				"sourceReferences": []map[string]interface{}{
					{
						"repository": "https://github.com/Mattel/test.git",
						"revisionId": "v1.2.3",
					},
				},
				"reportLocation": map[string]interface{}{
					"filePath":     "testing/testing.go",
					"lineNumber":   LineNumber,
					"functionName": "tRunner",
				},
			},
			"logging.googleapis.com/sourceLocation": map[string]interface{}{
				"file":     "testing/testing.go",
				"line":     LineNumber,
				"function": "tRunner",
			},
		},
	},
	{
		name: "WithField, HTTPRequest and Error",
		run: func(logger *logrus.Logger) {
			logger.
				WithFields(logrus.Fields{
					"foo":          "bar",
					"span_context": SpanContext,
					"httpRequest": map[string]interface{}{
						"requestMethod": "GET",
					},
				}).
				Error("my log entry")
		},
		out: map[string]interface{}{
			"@type":    "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent",
			"severity": "ERROR",
			"message":  "my log entry",
			"logName":  "projects/test-project/logs/test",

			"logging.googleapis.com/trace":         "projects/test-project/traces/105445aa7843bc8bf206b12000100000",
			"logging.googleapis.com/spanId":        "0000000000000001",
			"logging.googleapis.com/trace_sampled": true,
			"serviceContext": map[string]interface{}{
				"service": "test",
				"version": "0.1",
			},
			"context": map[string]interface{}{
				"data": map[string]interface{}{
					"foo": "bar",
					"httpRequest": map[string]interface{}{
						"requestMethod": "GET",
					},
				},
				"reportLocation": map[string]interface{}{
					"filePath":     "testing/testing.go",
					"lineNumber":   LineNumber,
					"functionName": "tRunner",
				},
				"sourceReferences": []map[string]interface{}{
					{
						"repository": "https://github.com/Mattel/test.git",
						"revisionId": "v1.2.3",
					},
				},
			},
			"logging.googleapis.com/sourceLocation": map[string]interface{}{
				"file":     "testing/testing.go",
				"line":     LineNumber,
				"function": "tRunner",
			},
		},
	},
}

func platformLine() float64 {
	switch runtime.GOOS {
	case "darwin":
		return 1193.0
	case "linux":
		return 1439.0
	default: // does anyone really use windows?
		return 0.0
	}
}
