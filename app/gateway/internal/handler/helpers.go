package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/protocol"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func bindJSON(c *gin.Context, dst any) error {
	if msg, ok := dst.(proto.Message); ok {
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return err
		}
		return protocol.Unmarshal(raw, msg)
	}
	return c.ShouldBindJSON(dst)
}

func requirePathIdentities(c *gin.Context, names ...string) bool {
	for _, name := range names {
		if err := identity.Validate(c.Param(name)); err != nil {
			response.BadRequest(c, "invalid "+name)
			return false
		}
	}
	return true
}

// 所有权来自鉴权身份，公开创建入口不能由客户端提升为平台对象。
func bindUserOwnedJSON(c *gin.Context, dst proto.Message) error {
	ownerID := c.GetString(middleware.CtxKeyUserID)
	if err := identity.Validate(ownerID); err != nil {
		return err
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("request must be a JSON object")
	}
	for _, names := range [][2]string{{"owner_id", "ownerId"}, {"owner_type", "ownerType"}} {
		if _, snake := fields[names[0]]; snake {
			if _, camel := fields[names[1]]; camel {
				return errors.New("duplicate ownership field")
			}
		}
	}
	for _, key := range []string{"owner_id", "ownerId"} {
		if value, supplied := fields[key]; supplied {
			var suppliedID string
			if err := json.Unmarshal(value, &suppliedID); err != nil {
				return err
			}
			if err := identity.Validate(suppliedID); err != nil {
				return err
			}
			if suppliedID != ownerID {
				return errors.New("owner must be authenticated user")
			}
		}
	}
	for _, key := range []string{"owner_type", "ownerType"} {
		if value, supplied := fields[key]; supplied {
			var suppliedType string
			if err := json.Unmarshal(value, &suppliedType); err != nil {
				return err
			}
			if suppliedType != "user" {
				return errors.New("user-owned creation requires user ownership")
			}
		}
	}
	delete(fields, "ownerId")
	delete(fields, "ownerType")
	fields["owner_type"] = json.RawMessage(`"user"`)
	fields["owner_id"], err = json.Marshal(ownerID)
	if err != nil {
		return err
	}
	raw, err = json.Marshal(fields)
	if err != nil {
		return err
	}
	return protocol.Unmarshal(raw, dst)
}

func requireRequestIdentities(c *gin.Context, msg proto.Message) bool {
	if err := validateRequestIdentities(msg.ProtoReflect()); err != nil {
		response.BadRequest(c, err.Error())
		return false
	}
	return true
}

func validateRequestIdentities(msg protoreflect.Message) error {
	fields := msg.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if field.HasPresence() && !msg.Has(field) {
			continue
		}
		value := msg.Get(field)
		entity, _ := proto.GetExtension(field.Options(), common.E_EntityId).(bool)
		submission, _ := proto.GetExtension(field.Options(), common.E_SubmissionKey).(bool)
		if entity || submission {
			validate := identity.Validate
			if submission {
				validate = identity.ValidateSubmissionKey
			}
			if field.IsList() {
				list := value.List()
				for j := range list.Len() {
					if err := validate(list.Get(j).String()); err != nil {
						return fmt.Errorf("%s: %w", field.Name(), err)
					}
				}
			} else if !field.IsMap() {
				if err := validate(value.String()); err != nil {
					return fmt.Errorf("%s: %w", field.Name(), err)
				}
			}
		}
		if field.Message() == nil || field.IsMap() {
			continue
		}
		if field.IsList() {
			list := value.List()
			for j := range list.Len() {
				if err := validateRequestIdentities(list.Get(j).Message()); err != nil {
					return err
				}
			}
		} else if err := validateRequestIdentities(value.Message()); err != nil {
			return err
		}
	}
	return nil
}
