package logadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/Mattel/logrus-stackdriver-formatter/ctxlogrus"
	"github.com/felixge/httpsnoop"
	"github.com/gofrs/uuid"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/sirupsen/logrus"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// WithLogger initializes the log entry in context
func WithLogger(ctx context.Context, logger *logrus.Logger) context.Context {
	// we pack the initial context into the log entry so that hooks
	// needing a request-scoped context may have it.

	entry := logrus.NewEntry(logger).WithContext(ctx)
	return ctxlogrus.ToContext(ctx, entry)
}

// an HTTPRequest wrapped in this will always be logged in the log entry root
// object so that GCP will format it with latency, status, etc. in summary field
type requestDetails struct {
	*HTTPRequest
}

// LoggingMiddleware proivdes a request-scoped log entry into context for HTTP
// requests, writes request logs in a structured format to stackdriver.
func LoggingMiddleware(
	log *logrus.Logger,
	opts ...MiddlewareOption,
) func(http.Handler) http.Handler {
	o := evaluateMiddlewareOptions(opts)

	return func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithLogger(r.Context(), log)
			r = r.WithContext(ctx)

			// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#HttpRequest
			request := &HTTPRequest{
				RequestMethod: r.Method,
				RequestURL:    r.RequestURI,
				RemoteIP:      getRemoteIP(r),
				Referer:       r.Referer(),
				UserAgent:     r.UserAgent(),
				RequestSize:   strconv.FormatInt(r.ContentLength, 10),
				Protocol:      r.Proto,
			}
			ctxlogrus.AddFields(ctx, logrus.Fields{"httpRequest": request})

			m := httpsnoop.CaptureMetrics(handler, w, r)

			request.Status = strconv.Itoa(m.Code)
			request.Latency = fmt.Sprintf("%.5fs", m.Duration.Seconds())
			request.ResponseSize = strconv.FormatInt(m.Written, 10)

			if o.filterHTTP(r) {
				// log the result
				ctxlogrus.Extract(ctx).
					WithField("httpRequest", requestDetails{request}).
					Infof("served HTTP %v %v", r.Method, r.URL)
			}
		})
	}
}

// UnaryLoggingInterceptor provides a request-scoped log entry into context for
// Unary gRPC requests, and logs request details on the response.
// Logging interceptors should be chained at the very top of the request scope.
func UnaryLoggingInterceptor(
	logger *logrus.Logger,
	opts ...MiddlewareOption,
) grpc.UnaryServerInterceptor {
	o := evaluateMiddlewareOptions(opts)
	return loggingInterceptor{logger: logger, middlewareOptions: o}.intercept
}

// StreamLoggingInterceptor provides a request-scoped log entry into context for
// Streaming gRPC requests, and logs request details at the end of the stream.
// Logging interceptors should be chained at the very top of the request scope.
func StreamLoggingInterceptor(
	logger *logrus.Logger,
	opts ...MiddlewareOption,
) grpc.StreamServerInterceptor {
	o := evaluateMiddlewareOptions(opts)
	return loggingInterceptor{logger: logger, middlewareOptions: o}.interceptStream
}

type loggingInterceptor struct {
	logger *logrus.Logger
	*middlewareOptions
}

// GRPCRequest represents details of a gRPC request and response appended to a log.
type GRPCRequest struct {
	Method    string `json:"method,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	PeerAddr  string `json:"peer,omitempty"`
	Deadline  string `json:"deadline,omitempty"`
	Duration  string `json:"duration,omitempty"`
}

func (l loggingInterceptor) intercept(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	startTime := time.Now()
	ctx = WithLogger(ctx, l.logger)

	request := l.requestFromContext(ctx, info.FullMethod)

	resp, err := handler(ctx, req)

	request.Duration = fmt.Sprintf("%.5fs", time.Since(startTime).Seconds())

	l.log(ctx, err, info.FullMethod, request)

	return resp, err
}

func (l loggingInterceptor) interceptStream(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	startTime := time.Now()
	ctx := WithLogger(ss.Context(), l.logger)

	request := l.requestFromContext(ctx, info.FullMethod)

	wrapped := grpc_middleware.WrapServerStream(ss)
	wrapped.WrappedContext = ctx

	err := handler(srv, wrapped)

	request.Duration = fmt.Sprintf("%.5fs", time.Since(startTime).Seconds())

	l.log(ctx, err, info.FullMethod, request)

	return err
}

// requestFromContext creates gRPC request details with information extracted from the request
// context
func (l *loggingInterceptor) requestFromContext(ctx context.Context, method string) *GRPCRequest {
	request := &GRPCRequest{Method: method}

	if d, ok := ctx.Deadline(); ok {
		request.Deadline = d.UTC().Format(time.RFC3339Nano)
	}

	if p, ok := peer.FromContext(ctx); ok && p != nil {
		u := &url.URL{
			Scheme: p.Addr.Network(),
			Host:   p.Addr.String(),
		}
		request.PeerAddr = u.String()
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok && md != nil {
		request.UserAgent = strings.Join(md.Get("user-agent"), "")
	}

	ctxlogrus.AddFields(ctx, logrus.Fields{"grpcRequest": request})

	return request
}

// logStatus adds the gRPC Status to the log context.
// If the response is an internal server error, log that as an Error
// returns true if the logging was handled (e.g. internal server error)
func (l *loggingInterceptor) log(
	ctx context.Context,
	err error,
	method string,
	request *GRPCRequest,
) {
	if !l.filterRPC(ctx, method, err) {
		return
	}

	if handled := l.handleError(ctx, err, method); handled {
		return
	}

	// write a simulacrum of the HTTPRequest as defined on LogEntry spec:
	// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#HttpRequest
	// This allows log lines to be formatted with special little widgets in GCP
	// logs view just like the Load Balancer logs
	httpReq := requestDetails{
		&HTTPRequest{
			RequestMethod: http.MethodPost,
			RequestURL:    request.Method,
			UserAgent:     request.UserAgent,
			Latency:       request.Duration,
			RemoteIP:      request.PeerAddr,
			Protocol:      "gRPC",
			// TODO:
			// ResponseSize: "",
			Status: strconv.Itoa(statusRPCToHTTP(err)),
		},
	}

	// if we reach here, the response either wasn't a bad error worth handling (e.g. NotFound and
	// its ilk)
	ctxlogrus.Extract(ctx).WithField("httpRequest", httpReq).Infof("served RPC %v", method)
}

// handleError adds grpcStatus to logentry, and can handle our most egregious errors
// returns true if the default Info logger should be skipped
func (l *loggingInterceptor) handleError(
	ctx context.Context,
	err error,
	method string,
) (handled bool) {
	if err == nil {
		return false
	}
	st := status.Convert(err)

	// add grpcStatus to log entry, if available
	jsonStatus, merr := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(st.Proto())
	if merr != nil {
		// this should never actually happen, so we log it to help identify
		// why our gRPC status error isn't included in logs
		ctxlogrus.Extract(ctx).WithError(merr).Warnf("error marshalling error status into log")
		return false
	}

	ctxlogrus.AddFields(ctx, logrus.Fields{
		"grpcStatus": json.RawMessage(jsonStatus),
	})
	// if we're about to return an internal server error to the client, always log as Error level.
	if st.Code() == codes.Internal {
		ctxlogrus.Extract(ctx).WithError(err).Errorf("internal error response on RPC %s", method)
		return true
	}

	// opportunity to log or transform the error with a custom error handler
	// If the error handler indicates logging has been handled already, we
	// return early and do not log as Info down below
	return l.customErrHandler(ctx, err, method)
}

// RecoveryMiddleware recovers from panics in the HTTP handler chain, logging
// an error for Error Reporting.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			var err error
			e := recover()
			if e == nil {
				return
			}

			switch t := e.(type) {
			case string:
				err = errors.New(t)
			case error:
				err = t
			default:
				err = fmt.Errorf("unknown panic value: (%T) %v", t, t)
			}

			ctx := r.Context()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)

			stErr := errWithStack(ctx, err)
			entry := ctxlogrus.Extract(ctx)

			// write error back to client
			jsonStatus, merr := protojson.MarshalOptions{
				EmitUnpopulated: true,
			}.Marshal(
				stErr.Proto(),
			)
			if merr != nil {
				entry.WithError(merr).Errorf("error marshalling error status into log")
				fmt.Fprint(w, `{"error": "server_error"}`)
				return
			}

			if _, err := w.Write(jsonStatus); err != nil {
				entry.WithError(err).Warn("error writing json server_error to ResponseWriter")
				return
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// UnaryRecoveryInterceptor is an interceptor that recovers panics and turns them
// into nicer GRPC errors.
func UnaryRecoveryInterceptor(
	ctx context.Context,
	req interface{},
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (resp interface{}, err error) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}

		switch t := e.(type) {
		case string:
			err = errors.New(t)
		case error:
			err = t
		default:
			err = fmt.Errorf("unknown panic value: (%T) %v", t, t)
		}

		stErr := errWithStack(ctx, err)
		err = stErr.Err()
		resp = nil
	}()

	return handler(ctx, req)
}

// StreamRecoveryInterceptor is an interceptor that recovers panics from
// Streaming services and turns them into nicer gRPC errors.
func StreamRecoveryInterceptor(
	srv interface{},
	ss grpc.ServerStream,
	_ *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) (err error) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}

		switch t := e.(type) {
		case string:
			err = errors.New(t)
		case error:
			err = t
		default:
			err = fmt.Errorf("unknown panic value: (%T) %v", t, t)
		}

		stErr := errWithStack(ss.Context(), err)
		err = stErr.Err()
	}()

	return handler(srv, ss)
}

// errWithStack generates a stack trace, logs it, and provides an internal
// server error response back to return to the client
func errWithStack(ctx context.Context, err error) *status.Status {
	stack := debug.Stack()
	ctxlogrus.Extract(ctx).
		WithError(err).
		WithField("stackTrace", string(stack)).
		Error("panic handling request")

	serverError := status.New(codes.Internal, "server error")
	reqID, _ := uuid.NewV4()
	// generate a shared UUID we can find this log entry from client-provided response body
	stErr, _ := serverError.WithDetails(&errdetails.RequestInfo{
		RequestId: reqID.String(),
	})
	return stErr
}

// getRemoteIP extracts the remote IP from X-Forwarded-For header, if applicable
// https://cloud.google.com/load-balancing/docs/https#x-forwarded-for_header
func getRemoteIP(r *http.Request) string {
	ctx := r.Context()

	fwdHeader := r.Header.Get("X-Forwarded-For")
	forwarded := strings.Split(fwdHeader, ",")
	ctxlogrus.AddFields(ctx, logrus.Fields{
		"forwardIP": fwdHeader,
	})

	// x-Forwarded-For directly from GCP LB is assumed to be sanitized
	// format: `<unverified IP(s)>, <client IP>, <global fw rule ext. IP>, <other proxies IP>`
	// only second and third entries are added for requests through GCP
	if len(forwarded) >= 2 {
		return strings.TrimSpace(forwarded[len(forwarded)-2])
	}

	// fallback to peer IP
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "0.0.0.0"
	}
	return ip
}

// Convert server-sent RPC status codes to HTTP-equivalent.
// ONLY FOR USE IN LOG.
func statusRPCToHTTP(err error) int {
	if err == nil {
		return http.StatusOK
	}

	st := status.Convert(err)
	switch st.Code() {
	case codes.Canceled:
		return http.StatusRequestTimeout // ESP converts this to nginx status 499, which isn't real
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.FailedPrecondition:
		return http.StatusBadRequest
	case codes.Aborted:
		return http.StatusConflict
	case codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Internal:
		return http.StatusInternalServerError
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}
