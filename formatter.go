package logadapter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-stack/stack"
	"github.com/gofrs/uuid"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

type severity string

// LogSeverity as understood by GCP
// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#logseverity
const (
	severityDebug    severity = "DEBUG"
	severityInfo     severity = "INFO"
	severityWarning  severity = "WARNING"
	severityError    severity = "ERROR"
	severityCritical severity = "CRITICAL"
	severityAlert    severity = "ALERT"
)

var levelsToSeverity = map[logrus.Level]severity{
	logrus.DebugLevel: severityDebug,
	logrus.InfoLevel:  severityInfo,
	logrus.WarnLevel:  severityWarning,
	logrus.ErrorLevel: severityError,
	logrus.FatalLevel: severityCritical,
	logrus.PanicLevel: severityAlert,
}

// log entries containing this type are evaluated as long entries as though all
// required fields are present, and captures the error event
const reportedErrorEventType = "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent"

// ServiceContext provides the data about the service we are sending to Google.
type ServiceContext struct {
	Service string `json:"service,omitempty"`
	Version string `json:"version,omitempty"`
}

// SourceLocation is the information about where a log entry was produced.
// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#LogEntrySourceLocation
type SourceLocation struct {
	FilePath     string `json:"file,omitempty"`
	LineNumber   int    `json:"line,omitempty"`
	FunctionName string `json:"function,omitempty"`
}

// ReportLocation is the information about where an error occurred.
// https://cloud.google.com/error-reporting/reference/rest/v1beta1/ErrorContext#SourceLocation
type ReportLocation struct {
	FilePath     string `json:"filePath,omitempty"`
	LineNumber   int    `json:"lineNumber,omitempty"`
	FunctionName string `json:"functionName,omitempty"`
}

// Context is sent with every message to stackdriver.
type Context struct {
	Data             map[string]interface{} `json:"data,omitempty"`
	User             string                 `json:"user,omitempty"`
	ReportLocation   *ReportLocation        `json:"reportLocation,omitempty"`
	HTTPRequest      *HTTPRequest           `json:"httpRequest,omitempty"`
	PubSubRequest    map[string]interface{} `json:"pubSubRequest,omitempty"`
	GRPCRequest      *GRPCRequest           `json:"grpcRequest,omitempty"`
	GRPCStatus       json.RawMessage        `json:"grpcStatus,omitempty"`
	SourceReferences []SourceReference      `json:"sourceReferences,omitempty"`
}

// HTTPRequest defines details of a request and response to append to a log.
// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#httprequest
type HTTPRequest struct {
	RequestMethod                  string `json:"requestMethod,omitempty"`
	RequestURL                     string `json:"requestUrl,omitempty"`
	RequestSize                    string `json:"requestSize,omitempty"`
	Status                         string `json:"status,omitempty"`
	ResponseSize                   string `json:"responseSize,omitempty"`
	UserAgent                      string `json:"userAgent,omitempty"`
	RemoteIP                       string `json:"remoteIp,omitempty"`
	ServerIP                       string `json:"serverIp,omitempty"`
	Referer                        string `json:"referer,omitempty"`
	Latency                        string `json:"latency,omitempty"`
	CacheLookup                    bool   `json:"cacheLookup,omitempty"`
	CacheHit                       bool   `json:"cacheHit,omitempty"`
	CacheValidatedWithOriginServer bool   `json:"cacheValidatedWithOriginServer,omitempty"`
	CacheFillBytes                 string `json:"cacheFillBytes,omitempty"`
	Protocol                       string `json:"protocol,omitempty"`
}

// Entry stores a log entry.
type Entry struct {
	Type           string          `json:"@type,omitempty"`
	LogName        string          `json:"logName,omitempty"`
	Timestamp      string          `json:"timestamp,omitempty"`
	ServiceContext *ServiceContext `json:"serviceContext,omitempty"`
	Message        string          `json:"message,omitempty"`
	Severity       severity        `json:"severity,omitempty"`
	Context        *Context        `json:"context,omitempty"`
	SourceLocation *SourceLocation `json:"logging.googleapis.com/sourceLocation,omitempty"`
	StackTrace     string          `json:"stack_trace,omitempty"`
	Trace          string          `json:"logging.googleapis.com/trace,omitempty"`
	SpanID         string          `json:"logging.googleapis.com/spanId,omitempty"`
	TraceSampled   bool            `json:"logging.googleapis.com/trace_sampled,omitempty"`
	HTTPRequest    *HTTPRequest    `json:"httpRequest,omitempty"`
}

// SourceReference is a reference to a particular snapshot of the source tree
// used to build and deploy an application
type SourceReference struct {
	Repository string `json:"repository,omitempty"`
	RevisionID string `json:"revisionId,omitempty"`
}

// Formatter implements Stackdriver formatting for logrus.
type Formatter struct {
	Service         string
	Version         string
	SourceReference []SourceReference
	ProjectID       string
	StackSkip       []string
	StackStyle      StackTraceStyle
	SkipTimestamp   bool
	RegexSkip       string
	PrettyPrint     bool
	GlobalTraceID   string
}

// NewFormatter returns a new Formatter.
func NewFormatter(options ...Option) *Formatter {
	fmtr := Formatter{
		StackSkip: []string{
			"github.com/sirupsen/logrus",
			"github.com/Mattel/logrus-stackdriver-formatter",
			"github.com/grpc-ecosystem/go-grpc-middleware",
			"go.opentelemetry.io",
		},
		StackStyle: TraceInMessage,
	}
	for _, option := range options {
		option(&fmtr)
	}

	// GlobalTraceID groups logs from runtime log entry
	if fmtr.GlobalTraceID == "" {
		id := uuid.Must(uuid.NewV4())
		opt := WithGlobalTraceID(id)
		opt(&fmtr)
	}
	return &fmtr
}

// errorOrigin Extracts the report location from call stack.
func (f *Formatter) errorOrigin() stack.Call {
	// skip will skip packages and sub-packages from a list
	skip := func(pkg string) bool {
		for _, skip := range f.StackSkip {
			if strings.Contains(pkg, skip) {
				return true
			}
		}
		return false
	}

	var r *regexp.Regexp
	if f.RegexSkip != "" {
		r = regexp.MustCompile(f.RegexSkip)
	}
	// We could start at 2 to skip this call and our caller's call, but they are filtered by package
	for i := 0; ; i++ {
		c := stack.Caller(i)
		// ErrNoFunc indicates we're over traversing the stack.
		if _, err := c.MarshalText(); err != nil {
			return stack.Call{}
		}
		pkg := fmt.Sprintf("%+k", c)
		// Remove vendoring from package path.
		parts := strings.SplitN(pkg, "/vendor/", 2)
		pkg = parts[len(parts)-1]
		if !skip(pkg) && (r == nil || !r.MatchString(c.Frame().Function)) {
			return c
		}
	}
}

// taken from https://github.com/sirupsen/logrus/blob/master/json_formatter.go#L51
func replaceErrors(source logrus.Fields) logrus.Fields {
	data := make(logrus.Fields, len(source))
	for k, v := range source {
		switch v := v.(type) {
		case error:
			// Otherwise errors are ignored by `encoding/json`
			// https://github.com/sirupsen/logrus/issues/137
			data[k] = v.Error()
		default:
			data[k] = v
		}
	}
	return data
}

// ToEntry formats a logrus entry to a stackdriver entry.
func (f *Formatter) ToEntry(e *logrus.Entry) (Entry, error) {
	severity := levelsToSeverity[e.Level]

	message := []string{}

	ee := Entry{
		Severity: severity,
		Context: &Context{
			Data: replaceErrors(e.Data),
		},
	}

	// If provided, format the current active trace and span id's to correlate logs to traces
	if tc, ok := e.Data["span_context"]; ok {
		if spanCtx, ok := tc.(trace.SpanContext); ok && spanCtx.IsValid() {
			ee.Trace = fmt.Sprintf("projects/%s/traces/%s", f.ProjectID, spanCtx.TraceID())
			ee.SpanID = spanCtx.SpanID().String()
			ee.TraceSampled = spanCtx.IsSampled()
		}

		delete(ee.Context.Data, "span_context")
	}

	if ee.Trace == "" {
		ee.Trace = fmt.Sprintf("projects/%s/traces/%s", f.ProjectID, f.GlobalTraceID)
	}

	if val, ok := e.Data["logID"]; ok {
		ee.LogName = "projects/" + f.ProjectID + "/logs/" + f.Service + "%2F" + val.(string)
	} else {
		ee.LogName = "projects/" + f.ProjectID + "/logs/" + f.Service
	}

	if len(e.Message) > 0 {
		message = append(message, e.Message)
	}

	if !f.SkipTimestamp {
		if !e.Time.IsZero() {
			ee.Timestamp = e.Time.UTC().Format(time.RFC3339Nano)
		} else {
			ee.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}

	// annotate where the log entry was produced
	if e.Caller != nil {
		// attempt first to read from logrus if SetReportCaller was configured
		ee.SourceLocation = extractFromCaller(e)
	} else {
		// Extract report location from call stack.
		c := f.errorOrigin()
		lineNumber, _ := strconv.ParseInt(fmt.Sprintf("%d", c), 10, 64)
		ee.SourceLocation = extractFromCallStack(c, lineNumber)
	}

	switch severity {
	case severityError, severityCritical, severityAlert:
		ee.ServiceContext = &ServiceContext{
			Service: f.Service,
			Version: f.Version,
		}

		// annotate build information
		if f.SourceReference != nil {
			ee.Context.SourceReferences = f.SourceReference
		}

		// LogEntry.LogEntrySourceLocation is a different structure than ErrorContext.SourceLocation
		// When reporting an ErrorEvent, copy the same into ReportLocation
		// https://cloud.google.com/error-reporting/reference/rest/v1beta1/ErrorContext#SourceLocation
		// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#LogEntrySourceLocation
		if ee.SourceLocation != nil {
			ee.Context.ReportLocation = &ReportLocation{
				FilePath:     ee.SourceLocation.FilePath,
				LineNumber:   ee.SourceLocation.LineNumber,
				FunctionName: ee.SourceLocation.FunctionName,
			}
		}

		// When using WithError(), the error is sent separately, but Error
		// Reporting expects it to be a part of the message so we append it
		// also.
		if err, ok := e.Data[logrus.ErrorKey]; ok {
			payloadTrace := f.StackStyle == TraceInPayload || f.StackStyle == TraceInBoth
			if verr, ok := err.(error); ok && payloadTrace {
				if stackTrace := extractStackFromError(verr); stackTrace != nil {
					stack := append(message, fmt.Sprintf("%s", stackTrace))
					ee.StackTrace = strings.Join(stack, "\n")
				}
			}

			// errors.WithStack formats the call stack to append to the message with %+v
			// but this is not correctly formatted to be parsed by GCP Error Reporting
			message = append(message, fmt.Sprintf("%v", err))
		}

		// If we supplied a stack trace, we can append it to the message.
		// Stacktrace is assumed to be formatted by debug.Stack()
		// Deliberately overwrites any stacktrace provided from the error
		if st, ok := ee.Context.Data["stackTrace"]; ok {
			// Error Reporting assumes the first line of a stacktrace explains the error encountered
			// Even if it's not in the message itself
			stack := append(message, fmt.Sprintf("%+v", st))

			if f.StackStyle == TraceInMessage || f.StackStyle == TraceInBoth {
				message = stack
			}
			if f.StackStyle == TraceInPayload || f.StackStyle == TraceInBoth {
				ee.StackTrace = strings.Join(stack, "\n")
			}

			delete(ee.Context.Data, "stackTrace")
		}

		// @type as ReportedErrorEvent if all required fields may be provided
		// https://cloud.google.com/error-reporting/docs/formatting-error-messages#json_representation
		if len(message) > 0 && ee.ServiceContext.Service != "" &&
			(ee.StackTrace != "" || ee.SourceLocation != nil) {
			ee.Type = reportedErrorEventType
		}
	}

	// UserID, email, or arbitrary token identifying a user can be provided to an error report
	if userData, ok := ee.Context.Data["user"]; ok {
		if user, ok := userData.(string); ok {
			ee.Context.User = user
			delete(ee.Context.Data, "user")
		}
		if user, ok := userData.(fmt.Stringer); ok {
			ee.Context.User = user.String()
			delete(ee.Context.Data, "user")
		}
	}

	// As a convenience, when using supplying the httpRequest field, it
	// gets special care.
	if req, ok := ee.Context.Data["httpRequest"].(*HTTPRequest); ok {
		ee.Context.HTTPRequest = req
		delete(ee.Context.Data, "httpRequest")
	}

	// Promote the httpRequest details to parent entry so logs may be presented with HTTP request
	// details Only do this when the logging middleware provides special instructions in log entry
	// context to do so, as the resulting log message summary line is specially formatted to ignore
	// the payload message
	if req, ok := ee.Context.Data["httpRequest"].(requestDetails); ok {
		ee.HTTPRequest = req.HTTPRequest
		delete(ee.Context.Data, "httpRequest")
	}

	// As a convenience, when using supplying the grpcRequest field, it
	// gets special care.
	if req, ok := ee.Context.Data["grpcRequest"].(*GRPCRequest); ok {
		ee.Context.GRPCRequest = req
		delete(ee.Context.Data, "grpcRequest")
	}

	// As a convenience, when using supplying the grpcStatus field, it
	// gets special care.
	if req, ok := ee.Context.Data["grpcStatus"].(json.RawMessage); ok {
		ee.Context.GRPCStatus = req
		delete(ee.Context.Data, "grpcStatus")
	}

	// As a convenience, when using supplying the pubSubRequest field, it
	// gets special care.
	if req, ok := ee.Context.Data["pubSubRequest"].(map[string]interface{}); ok {
		ee.Context.PubSubRequest = req
		delete(ee.Context.Data, "pubsubRequest")
	}

	ee.Message = strings.Join(message, "\n")
	return ee, nil
}

func extractFromCaller(e *logrus.Entry) *SourceLocation {
	return &SourceLocation{
		FilePath:     e.Caller.File,
		FunctionName: e.Caller.Function,
		LineNumber:   e.Caller.Line,
	}
}

func extractFromCallStack(c stack.Call, lineNumber int64) *SourceLocation {
	return &SourceLocation{
		FilePath:     fmt.Sprintf("%+s", c),
		LineNumber:   int(lineNumber),
		FunctionName: fmt.Sprintf("%n", c),
	}
}

// Format formats a logrus entry according to the Stackdriver specifications.
func (f *Formatter) Format(e *logrus.Entry) (b []byte, err error) {
	ee, _ := f.ToEntry(e)

	if f.PrettyPrint {
		b, err = json.MarshalIndent(ee, "", "\t")
	} else {
		b, err = json.Marshal(ee)
	}

	b = append(b, '\n')

	return
}
