package logadapter

import (
	"context"
	"net/http"
	"strings"
)

var defaultLogOptions = &middlewareOptions{
	filterRPC:        DefaultFilterRPC,
	filterHTTP:       DefaultFilterHTTP,
	customErrHandler: DefaultErrorHandler,
}

type MiddlewareOption func(*middlewareOptions)

// Options
type middlewareOptions struct {
	filterRPC        FilterRPC
	filterHTTP       FilterHTTP
	customErrHandler ErrorHandler
}

func evaluateMiddlewareOptions(opts []MiddlewareOption) *middlewareOptions {
	optCopy := &middlewareOptions{}
	*optCopy = *defaultLogOptions
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

// WithRPCFilter provides a filter to the logging middleware that determines
// whether or not to log individual messages
func WithRPCFilter(f FilterRPC) MiddlewareOption {
	return func(o *middlewareOptions) {
		o.filterRPC = f
	}
}

// WithHTTPFilter provides a filter to the logging middleware that determines
// whether or not to log individual messages
func WithHTTPFilter(f FilterHTTP) MiddlewareOption {
	return func(o *middlewareOptions) {
		o.filterHTTP = f
	}
}

func WithErrorHandler(h ErrorHandler) MiddlewareOption {
	return func(o *middlewareOptions) {
		o.customErrHandler = h
	}
}

// Logging filters
type (
	FilterRPC  func(ctx context.Context, fullMethod string, err error) bool
	FilterHTTP func(r *http.Request) bool

	// ErrorHandler should return true if the error provided has already been logged
	ErrorHandler func(ctx context.Context, err error, method string) (handled bool)
)

// DefaultFilterRPC filters gRPC standard health check and gRPC reflection requests.
func DefaultFilterRPC(_ context.Context, fullMethod string, _ error) bool {
	switch {
	case strings.HasPrefix(fullMethod, "/grpc.health"):
		return false
	case strings.HasPrefix(fullMethod, "/grpc.reflection"):
		return false
	default:
		return true
	}
}

// DefaultFilterHTTP filters health checks and monitoring canaries from some well known user agents
// or URL paths.
func DefaultFilterHTTP(r *http.Request) bool {
	userAgent := r.Header.Get("User-Agent")
	switch {
	case userAgent == "Envoy/HC", // Envoy Proxy healthchecker
		strings.HasPrefix(userAgent, "kube-probe/"),                 // kubernetes probes
		strings.HasPrefix(userAgent, "GoogleHC/"),                   // GCP load balancer
		strings.HasPrefix(userAgent, "GoogleStackdriverMonitoring"), // GCP Operations Monitoring
		strings.HasPrefix(r.URL.Path, "/health"):
		return false
	default:
		return true
	}
}

// DefaultErrorHandler does nothing.
func DefaultErrorHandler(ctx context.Context, err error, method string) (handled bool) {
	return false
}
