package main

import (
	"flag"
	"fmt"

	"github.com/maomeng/aim/app/llm-gateway/internal/config"
	"github.com/maomeng/aim/app/llm-gateway/internal/handler"
	"github.com/maomeng/aim/app/llm-gateway/internal/svc"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/interceptor"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/llm-gateway.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	ctx := svc.NewServiceContext(c)
	defer ctx.Close()

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		pb.RegisterLLMGatewayServer(grpcServer, handler.NewLLMGatewayHandler(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryUserIDInterceptor(), interceptor.UnaryErrorInterceptor())
	s.AddStreamInterceptors(interceptor.StreamErrorInterceptor())
	defer s.Stop()

	fmt.Printf("Starting llm-gateway rpc server at %s...\n", c.ListenOn)
	s.Start()
}
