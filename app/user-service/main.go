package main

import (
	"flag"
	"fmt"

	"github.com/maomeng/aim/app/user-service/internal/config"
	"github.com/maomeng/aim/app/user-service/internal/svc"
	"github.com/maomeng/aim/app/user-service/pb/user"
	userServer "github.com/maomeng/aim/app/user-service/server"
	"github.com/maomeng/aim/pkg/interceptor"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/user.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	// Keep RPC timing/metrics without serializing authentication credentials.
	c.Middlewares.StatConf.IgnoreContentMethods = append(c.Middlewares.StatConf.IgnoreContentMethods,
		user.UserService_Register_FullMethodName,
		user.UserService_Login_FullMethodName,
		user.UserService_OAuthLogin_FullMethodName,
		user.UserService_RefreshToken_FullMethodName,
		user.UserService_ValidateToken_FullMethodName,
		user.UserService_Logout_FullMethodName,
		user.UserService_UpdatePassword_FullMethodName,
		user.UserService_UpdateProfile_FullMethodName,
		user.UserService_BindPhone_FullMethodName,
		user.UserService_BindEmail_FullMethodName,
	)
	ctx := svc.NewServiceContext(c)
	defer ctx.Close()

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		srv := userServer.NewUserServer(&userServer.UserServerContext{
			AuthLogic:     ctx.AuthLogic,
			UserLogic:     ctx.UserLogic,
			StatusLogic:   ctx.StatusLogic,
			FriendContext: ctx.FriendContext,
		}, ctx.Log)
		user.RegisterUserServiceServer(grpcServer, srv)

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	s.AddUnaryInterceptors(interceptor.UnaryErrorInterceptor(), interceptor.UnaryRequestIDInterceptor(), interceptor.UnaryUserIDInterceptor())
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
