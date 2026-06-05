package main

import (
	"flag"
	"fmt"

	"github.com/maomeng/aim/app/friend-service/internal/config"
	"github.com/maomeng/aim/app/friend-service/internal/server"
	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/interceptor"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/friend.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	configcenter.InitConfigCenter(c.Name, c.Etcd.Hosts, &c)
	ctx := svc.NewServiceContext(c)

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		friend.RegisterFriendServiceServer(grpcServer, server.NewFriendServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryUserIDInterceptor(), interceptor.UnaryErrorInterceptor())
	defer s.Stop()

	fmt.Printf("Starting friend rpc server at %s...\n", c.ListenOn)
	s.Start()
}
