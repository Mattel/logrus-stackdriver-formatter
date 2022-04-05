package logadapter_test

import (
	"context"
	"io"
	"net"
	"testing"

	logadapter "github.com/Mattel/logrus-stackdriver-formatter"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/interop"
	pb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 2048

func benchmark(b *testing.B, opt ...grpc.ServerOption) {
	l := bufconn.Listen(bufSize)
	defer l.Close()

	s := grpc.NewServer(opt...)
	pb.RegisterTestServiceServer(s, interop.NewTestServer())
	go func() {
		if err := s.Serve(l); err != nil {
			panic(err)
		}
	}()

	defer s.Stop()

	ctx := context.Background()
	dial := func(context.Context, string) (net.Conn, error) { return l.Dial() }
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(dial), grpc.WithInsecure())
	if err != nil {
		b.Fatalf("Failed to dial bufnet: %v", err)
	}
	defer conn.Close()
	client := pb.NewTestServiceClient(conn)

	b.ReportAllocs()
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		interop.DoEmptyUnaryCall(client)
		interop.DoLargeUnaryCall(client)
		interop.DoClientStreaming(client)
		interop.DoServerStreaming(client)
		interop.DoPingPong(client)
		interop.DoEmptyStream(client)
	}

	b.StopTimer()
}

func BenchmarkMiddleware(b *testing.B) {
	logger := logrus.New()
	formatter := logadapter.NewFormatter(
		logadapter.WithProjectID("test-project"),
		logadapter.WithService("benchmark"),
		logadapter.WithVersion("v1.0.0"),
	)
	logger.SetFormatter(formatter)
	logger.SetOutput(io.Discard)
	logger.SetReportCaller(true)

	tcases := []struct {
		Name        string
		Middlewares []grpc.ServerOption
	}{
		{
			Name:        "NoInstrumentation",
			Middlewares: []grpc.ServerOption{},
		},
		{
			Name: "UnaryLoggingInterceptor",
			Middlewares: []grpc.ServerOption{
				grpc.UnaryInterceptor(logadapter.UnaryLoggingInterceptor(logger)),
			},
		},
		{
			Name: "StreamLoggingInterceptor",
			Middlewares: []grpc.ServerOption{
				grpc.StreamInterceptor(logadapter.StreamLoggingInterceptor(logger)),
			},
		},
		{
			Name: "UnaryRecoverInterceptor",
			Middlewares: []grpc.ServerOption{
				grpc.UnaryInterceptor(logadapter.UnaryRecoveryInterceptor),
			},
		},
		{
			Name: "StreamRecoverInterceptor",
			Middlewares: []grpc.ServerOption{
				grpc.StreamInterceptor(logadapter.StreamRecoveryInterceptor),
			},
		},
		{
			Name: "LoggingRecoverInterceptor",
			Middlewares: []grpc.ServerOption{
				grpc.ChainUnaryInterceptor(
					logadapter.UnaryLoggingInterceptor(logger),
					logadapter.UnaryRecoveryInterceptor,
				),
				grpc.ChainStreamInterceptor(
					logadapter.StreamLoggingInterceptor(logger),
					logadapter.StreamRecoveryInterceptor,
				),
			},
		},
	}

	for _, tc := range tcases {
		b.Run(tc.Name, func(b *testing.B) {
			benchmark(b, tc.Middlewares...)
		})
	}
}
