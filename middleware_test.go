package logadapter_test

import (
	"context"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	logadapter "github.com/Mattel/logrus-stackdriver-formatter"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	pb_testproto "github.com/grpc-ecosystem/go-grpc-middleware/testing/testproto"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	pbstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestServerSuite(t *testing.T) {
	s := newGRPCTestSuite(t)
	s.InterceptorTestSuite.ServerOpts = []grpc.ServerOption{
		grpc_middleware.WithStreamServerChain(
			logadapter.StreamLoggingInterceptor(s.logger),
			logadapter.StreamRecoveryInterceptor,
		),
		grpc_middleware.WithUnaryServerChain(
			logadapter.UnaryLoggingInterceptor(s.logger),
			logadapter.UnaryRecoveryInterceptor,
		),
	}

	suite.Run(t, &logFormatterSuite{s})
}

type logFormatterSuite struct {
	*grpcTestSuite
}

func (s *logFormatterSuite) TestPanic() {
	deadline := time.Now().Add(3 * time.Second)
	panicPing := &pb_testproto.PingRequest{Value: "pls panic", SleepTimeMs: 9999}
	_, err := s.Client.Ping(s.DeadlineCtx(deadline), panicPing)

	require.Error(s.T(), err, "panic in an RPC returns error to client")

	st := status.Convert(err)
	reqID := ""
	for _, detail := range st.Details() {
		if ri, ok := detail.(*errdetails.RequestInfo); ok {
			reqID = ri.RequestId
		}
	}
	assert.NotEmpty(
		s.T(),
		reqID,
		"panic in RPC returns a requestID to correlate logs back to client-reported error",
	)

	_ = s.getOutputJSONs()
}

func (s *logFormatterSuite) TestGood() {
	deadline := time.Now().Add(3 * time.Second)
	ctx := s.DeadlineCtx(deadline)

	md := metadata.Pairs("X-Cloud-Trace-Context", "105445aa7843bc8bf206b12000100000/1;o=1")
	ctx = metadata.NewOutgoingContext(ctx, md)

	_, err := s.Client.Ping(ctx, goodPing)

	require.NoError(s.T(), err, "can't error on successful call")

	msgs := s.getOutputJSONs()
	require.Len(s.T(), msgs, 2, "two messages should be logged")
}

func (s *logFormatterSuite) TestError() {
	for _, tcase := range []struct {
		code     codes.Code
		level    logrus.Level
		msg      string
		logError bool
	}{
		{
			code:     codes.Internal,
			msg:      "Internal errors returned to client will be logged",
			logError: true,
		},
		{
			code:     codes.NotFound,
			logError: false,
		},
	} {
		// TODO: validate Error log format
		s.buffer.Reset()
		_, err := s.Client.PingError(s.SimpleCtx(), &pb_testproto.PingRequest{
			Value:             "anything",
			ErrorCodeReturned: uint32(tcase.code),
		})
		require.Error(s.T(), err, "each call returns an error")

		msgs := s.getOutputJSONs()
		require.Len(s.T(), msgs, 1, "only logging interceptor printed in PingErr")

		if tcase.logError {
			assert.Equal(s.T(), "ERROR", msgs[0]["severity"], "error is logged as error")
			assert.Equal(
				s.T(),
				"type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent",
				msgs[0]["@type"],
				"errors are typed to force Error Reporting parsing",
			)
		}
	}
}

func (s *logFormatterSuite) TestWithStack() {
	_, err := s.Client.PingError(s.SimpleCtx(), &pb_testproto.PingRequest{
		Value:             "stack",
		ErrorCodeReturned: uint32(codes.Aborted),
	})

	require.Error(s.T(), err, "call returns error")
}

func TestHTTPMiddleware(t *testing.T) {
	s := newHTTPTestSuite(t)

	suite.Run(t, &httpMiddlewareSuite{s})
}

type httpMiddlewareSuite struct {
	*httpTestSuite
}

func (s *httpMiddlewareSuite) TestPanic() {
	t := s.T()

	req, err := http.NewRequest("GET", s.server.URL+"/panic", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	req.WithContext(ctx)

	res, err := s.Client.Do(req)
	require.NoError(t, err, "can't error on successful call")
	defer res.Body.Close()

	assert.Equal(t, "application/json", res.Header.Get("Content-Type"), "responds as JSON")

	body, err := ioutil.ReadAll(res.Body)
	require.NoError(t, err, "can read body")

	pbs := &pbstatus.Status{}
	if err := protojson.Unmarshal(body, pbs); err != nil {
		t.Fatal(err)
	}
	st := status.FromProto(pbs)
	assert.Equal(t, codes.Internal, st.Code(), "status code is internal server error")
	assert.Equal(t, "server error", st.Message(), "non-descript server error")

	if got, want := res.StatusCode, http.StatusInternalServerError; got != want {
		t.Errorf("wrong status recieved; got %d, wanted %d", got, want)
	}
}

func (s *httpMiddlewareSuite) TestLogging() {
	t := s.T()

	req, err := http.NewRequest("GET", s.server.URL+"/logging", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	req.WithContext(ctx)

	req.Header.Set("X-Cloud-Trace-Context", "105445aa7843bc8bf206b12000100000/1;o=1")

	res, err := s.Client.Do(req)
	require.NoError(t, err, "can't error on successful call")

	if got, want := res.StatusCode, http.StatusOK; got != want {
		t.Errorf("wrong status recieved; got %d, wanted %d", got, want)
	}
}

// TODO: X-Cloud-Trace header
