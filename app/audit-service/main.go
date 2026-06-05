package main

import (
	"flag"
	"fmt"

	"github.com/maomeng/aim/app/audit-service/internal/config"
	"github.com/maomeng/aim/app/audit-service/internal/server"
	"github.com/maomeng/aim/app/audit-service/internal/svc"
	"github.com/maomeng/aim/app/audit-service/pb/audit"

	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/audit.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	configcenter.InitConfigCenter(c.Name, c.Etcd.Hosts, &c)
	ctx := svc.NewServiceContext(c)

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		audit.RegisterAuditServiceServer(grpcServer, server.NewAuditServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryErrorInterceptor())
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
