package logadapter

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/gofrs/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/Mattel/logrus-stackdriver-formatter/test"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
)

var (
	TraceFlags  = trace.FlagsSampled
	TraceID     = uuid.Must(uuid.FromString("105445aa7843bc8bf206b12000100000"))
	SpanID      = [8]byte{0, 0, 0, 0, 0, 0, 0, 1}
	SpanContext = trace.SpanContext{}.WithSpanID(SpanID).
			WithTraceID(trace.TraceID(TraceID)).
			WithTraceFlags(TraceFlags)
	LineNumber = platformLine()
)

func TestStackSkip(t *testing.T) {
	var out bytes.Buffer

	logger := logrus.New()
	logger.Out = &out
	logger.Formatter = NewFormatter(
		WithProjectID("test-project"),
		WithService("test"),
		WithVersion("0.1"),
		WithStackSkip("github.com/Mattel/logrus-stackdriver-formatter"),
		WithSkipTimestamp(),
		WithGlobalTraceID(TraceID),
	)

	mylog := test.LogWrapper{
		Logger: logger,
	}
	mylog.Logger.
		WithField("span_context", SpanContext).
		Error("my log entry")

	want := map[string]interface{}{
		"@type":                                reportedErrorEventType,
		"severity":                             "ERROR",
		"message":                              "my log entry",
		"logName":                              "projects/test-project/logs/test",
		"logging.googleapis.com/trace":         "projects/test-project/traces/105445aa7843bc8bf206b12000100000",
		"logging.googleapis.com/spanId":        "0000000000000001",
		"logging.googleapis.com/trace_sampled": true,
		"serviceContext": map[string]interface{}{
			"service": "test",
			"version": "0.1",
		},
		"context": map[string]interface{}{
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
	}
	var got map[string]interface{}
	err := json.Unmarshal(out.Bytes(), &got)
	if err != nil {
		t.Error(err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Unexpected output (-want +got):\n%s", diff)
	}
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
