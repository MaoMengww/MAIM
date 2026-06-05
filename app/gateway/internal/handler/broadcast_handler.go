package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	msgclient "github.com/maomeng/aim/app/message-service/client/messageservice"
	"github.com/zeromicro/go-zero/zrpc"
)

type BroadcastHandler struct {
	msgClient msgclient.MessageService
}

func NewBroadcastHandler(cli zrpc.Client) *BroadcastHandler {
	return &BroadcastHandler{
		msgClient: msgclient.NewMessageService(cli),
	}
}

func (h *BroadcastHandler) CreateBroadcast(c *gin.Context) {
	var req msgclient.SendBroadcastReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.msgClient.SendBroadcast(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Created(c, resp)
}

func (h *BroadcastHandler) ListBroadcasts(c *gin.Context) {
	c.String(200, `{"data":[]}`)
}

func (h *BroadcastHandler) GetBroadcast(c *gin.Context) {
	c.String(200, `{"data":{}}`)
}

func (h *BroadcastHandler) GetMyBroadcasts(c *gin.Context) {
	c.String(200, `{"data":[]}`)
}
