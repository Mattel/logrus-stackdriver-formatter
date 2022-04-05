package logadapterexample

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofrs/uuid"
	"go.opentelemetry.io/otel/trace"

	stackdriver "github.com/Mattel/logrus-stackdriver-formatter"
	"github.com/sirupsen/logrus"
)

var (
	TraceFlags  = trace.FlagsSampled
	TraceID     = uuid.Must(uuid.FromString("105445aa7843bc8bf206b12000100000"))
	SpanID      = [8]byte{0, 0, 0, 0, 0, 0, 0, 1}
	SpanContext = trace.SpanContext{}.WithSpanID(SpanID).
			WithTraceID(trace.TraceID(TraceID)).
			WithTraceFlags(TraceFlags)
)

func ExampleWithError() {
	logger := logrus.New()
	var b bytes.Buffer
	foo := bufio.NewWriter(&b)
	logger.Out = foo
	logger.Formatter = stackdriver.NewFormatter(
		stackdriver.WithService("test-service"),
		stackdriver.WithVersion("v0.1.0"),
		stackdriver.WithSkipTimestamp(),
		stackdriver.WithGlobalTraceID(TraceID),
	)

	logger.WithField("span_context", SpanContext).Info("application up and running")

	_, err := strconv.ParseInt("text", 10, 64)
	if err != nil {
		logger.WithField("span_context", SpanContext).WithError(err).
			Errorln("unable to parse integer")
	}
	foo.Flush()

	fmt.Println(PrettyString(b.String()))

	// Output:
	// {
	//     "logName": "projects//logs/test-service",
	//     "message": "application up and running",
	//     "severity": "INFO",
	//     "context": {},
	//     "logging.googleapis.com/sourceLocation": {
	//         "file": "testing/run_example.go",
	//         "line": 63,
	//         "function": "runExample"
	//     },
	//     "logging.googleapis.com/trace": "projects//traces/105445aa7843bc8bf206b12000100000",
	//     "logging.googleapis.com/spanId": "0000000000000001",
	//     "logging.googleapis.com/trace_sampled": true
	// }
	// {
	//     "@type": "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent",
	//     "logName": "projects//logs/test-service",
	//     "serviceContext": {
	//         "service": "test-service",
	//         "version": "v0.1.0"
	//     },
	//     "message": "unable to parse integer\nstrconv.ParseInt: parsing \"text\": invalid syntax",
	//     "severity": "ERROR",
	//     "context": {
	//         "data": {
	//             "error": "strconv.ParseInt: parsing \"text\": invalid syntax"
	//         },
	//         "reportLocation": {
	//             "filePath": "testing/run_example.go",
	//             "lineNumber": 63,
	//             "functionName": "runExample"
	//         }
	//     },
	//     "logging.googleapis.com/sourceLocation": {
	//         "file": "testing/run_example.go",
	//         "line": 63,
	//         "function": "runExample"
	//     },
	//     "logging.googleapis.com/trace": "projects//traces/105445aa7843bc8bf206b12000100000",
	//     "logging.googleapis.com/spanId": "0000000000000001",
	//     "logging.googleapis.com/trace_sampled": true
	// }
}

func PrettyString(str string) string {
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, []byte(str), "", "    "); err != nil {
		if strings.Contains(str, "\n") {
			var pieces []string
			for _, split := range strings.Split(str, "\n") {
				pieces = append(pieces, PrettyString(split))
			}
			return strings.Join(pieces, "\n")
		}
		return str
	}
	return prettyJSON.String()
}
