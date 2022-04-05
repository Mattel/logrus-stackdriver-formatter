package logadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	logadapter "github.com/Mattel/logrus-stackdriver-formatter"
	"github.com/Mattel/logrus-stackdriver-formatter/ctxlogrus"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	grpc_testing "github.com/grpc-ecosystem/go-grpc-middleware/testing"
	pb_testproto "github.com/grpc-ecosystem/go-grpc-middleware/testing/testproto"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)

type grpcTestSuite struct {
	*grpc_testing.InterceptorTestSuite
	mutexBuffer *grpc_testing.MutexReadWriter
	buffer      *bytes.Buffer
	logger      *logrus.Logger
}

func newGRPCTestSuite(t *testing.T) *grpcTestSuite {
	b := &bytes.Buffer{}
	muB := grpc_testing.NewMutexReadWriter(b)
	logger := logrus.New()
	logger.Formatter = logadapter.NewFormatter(
		logadapter.WithProjectID("test-project"),
		logadapter.WithService("logging-test"),
		logadapter.WithVersion("v1.0.0"),
		logadapter.WithStackTraceStyle(logadapter.TraceInPayload),
		logadapter.WithSourceReference(
			"github.com/Mattel/logrus-stackdriver-formatter",
			"v1.0.0",
		),
		logadapter.WithPrettyPrint(),
	)
	var out io.Writer
	out = muB
	if testing.Verbose() {
		out = io.MultiWriter(os.Stdout, muB)
	}
	logger.Out = out

	return &grpcTestSuite{
		logger:      logger,
		buffer:      b,
		mutexBuffer: muB,
		InterceptorTestSuite: &grpc_testing.InterceptorTestSuite{
			TestService: &loggingPingService{&grpc_testing.TestPingService{T: t}},
		},
	}
}

func (s *grpcTestSuite) SetupTest() {
	s.mutexBuffer.Lock()
	s.buffer.Reset()
	s.mutexBuffer.Unlock()
}

var goodPing = &pb_testproto.PingRequest{Value: "something", SleepTimeMs: 9999}

type loggingPingService struct {
	pb_testproto.TestServiceServer
}

func (s *loggingPingService) Ping(
	ctx context.Context,
	ping *pb_testproto.PingRequest,
) (*pb_testproto.PingResponse, error) {
	grpc_ctxtags.Extract(ctx).Set("custom_tags.string", "something").Set("custom_tags.int", 1337)
	ctxlogrus.AddFields(ctx, logrus.Fields{"custom_field": "custom_value"})
	ctxlogrus.Extract(ctx).Info("some ping")
	if ping.Value == "pls panic" {
		panic("test panic RPC")
	}
	return s.TestServiceServer.Ping(ctx, ping)
}

func (s *loggingPingService) PingError(
	ctx context.Context,
	ping *pb_testproto.PingRequest,
) (*pb_testproto.Empty, error) {
	empty, err := s.TestServiceServer.PingError(ctx, ping)

	if ping.Value == "stack" {
		st := errors.WithStack(err)
		ctxlogrus.Extract(ctx).WithError(st).Error("error with stack")
	}

	return empty, err
}

func (s *loggingPingService) PingList(
	ping *pb_testproto.PingRequest,
	stream pb_testproto.TestService_PingListServer,
) error {
	grpc_ctxtags.Extract(stream.Context()).
		Set("custom_tags.string", "something").
		Set("custom_tags.int", 1337)
	ctxlogrus.AddFields(stream.Context(), logrus.Fields{"custom_field": "custom_value"})
	ctxlogrus.Extract(stream.Context()).Info("some pinglist")
	return s.TestServiceServer.PingList(ping, stream)
}

func (s *loggingPingService) PingEmpty(
	ctx context.Context,
	empty *pb_testproto.Empty,
) (*pb_testproto.PingResponse, error) {
	return s.TestServiceServer.PingEmpty(ctx, empty)
}

func (s *grpcTestSuite) getOutputJSONs() []map[string]interface{} {
	ret := make([]map[string]interface{}, 0)
	dec := json.NewDecoder(s.mutexBuffer)

	for {
		var val map[string]interface{}
		err := dec.Decode(&val)
		if err == io.EOF {
			break
		}
		if err != nil {
			s.T().Fatalf("failed decoding output from Logrus JSON: %v", err)
		}

		ret = append(ret, val)
	}

	return ret
}

type httpTestSuite struct {
	suite.Suite

	server *httptest.Server
	mux    *http.ServeMux
	Client *http.Client

	mutexBuffer *grpc_testing.MutexReadWriter
	buffer      *bytes.Buffer
	logger      *logrus.Logger
}

func newHTTPTestSuite(t *testing.T) *httpTestSuite {
	b := &bytes.Buffer{}
	muB := grpc_testing.NewMutexReadWriter(b)
	logger := logrus.New()
	logger.Formatter = logadapter.NewFormatter(
		logadapter.WithProjectID("test-project"),
		logadapter.WithService("logging-test"),
		logadapter.WithVersion("v1.0.0"),
		logadapter.WithStackTraceStyle(logadapter.TraceInPayload),
		logadapter.WithSourceReference(
			"github.com/Mattel/logrus-stackdriver-formatter",
			"v1.0.0",
		),
		logadapter.WithPrettyPrint(),
	)
	var out io.Writer
	out = muB
	if testing.Verbose() {
		out = io.MultiWriter(os.Stdout, muB)
	}
	logger.Out = out

	return &httpTestSuite{
		logger:      logger,
		buffer:      b,
		mutexBuffer: muB,
		Suite:       suite.Suite{},
	}
}

func (s *httpTestSuite) SetupSuite() {
	s.mux = http.NewServeMux()

	apiHandler := http.NewServeMux()
	apiHandler.Handle("/", s.mux)

	recoveryHandler := logadapter.RecoveryMiddleware(apiHandler)
	loggingHandler := logadapter.LoggingMiddleware(s.logger)(recoveryHandler)

	s.server = httptest.NewTLSServer(loggingHandler)
	s.Client = s.server.Client()

	s.mux.HandleFunc("/panic", s.ServePanic)
	s.mux.HandleFunc("/logging", s.ServeLogging)
}

func (s *httpTestSuite) TearDownSuite() {
	s.server.Close()
}

func (s *httpTestSuite) ServePanic(w http.ResponseWriter, r *http.Request) {
	panic("surprise panic attack!")
}

func (s *httpTestSuite) ServeLogging(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctxlogrus.AddFields(ctx, logrus.Fields{"testField": "testValue"})

	ctxlogrus.Extract(ctx).Info("served from http request")
}
