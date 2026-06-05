package knowledgebase

import (
	"github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

type Client interface {
	knowledgebase.KnowledgeBaseClient
}

type defaultClient struct {
	knowledgebase.KnowledgeBaseClient
}

func NewClient(c zrpc.Client) Client {
	return &defaultClient{
		KnowledgeBaseClient: knowledgebase.NewKnowledgeBaseClient(c.Conn()),
	}
}

func NewClientFromConn(conn *grpc.ClientConn) Client {
	return &defaultClient{
		KnowledgeBaseClient: knowledgebase.NewKnowledgeBaseClient(conn),
	}
}
