//go:build integration

package integration

import (
	"testing"

	"github.com/maomeng/aim/app/user-service/internal/config"
	"github.com/maomeng/aim/app/user-service/internal/svc"
	pkgconfig "github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/core/conf"
)

func newUserSvcCtx(t *testing.T) *svc.ServiceContext {
	t.Helper()
	var c config.Config
	pkgconfig.SetLocalDefaults()
	conf.MustLoad("../etc/user.yaml", &c, conf.UseEnv())
	c.Telemetry.Endpoint = ""
	ctx := svc.NewServiceContext(c)
	t.Cleanup(ctx.Close)
	return ctx
}
