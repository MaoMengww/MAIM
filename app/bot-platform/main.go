package main

import (
	"flag"
	"fmt"

	"github.com/maomeng/aim/app/bot-platform/internal/config"
	"github.com/maomeng/aim/app/bot-platform/internal/server"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	"github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/interceptor"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/bot-platform.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	configcenter.InitConfigCenter(c.Name, c.Etcd.Hosts, &c)

	svcCtx := svc.NewServiceContext(c)

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		botplatform.RegisterBotPlatformServer(grpcServer, server.NewBotPlatformServer(svcCtx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryErrorInterceptor())
	defer s.Stop()

	fmt.Printf("Starting bot-platform rpc server at %s...\n", c.ListenOn)
	s.Start()
}
