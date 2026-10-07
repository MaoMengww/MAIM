package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// REST oneofs use protojson's int64 strings; Kafka-backed WS content uses JSON
// numbers. IDs at both boundaries use the existing strict UUID decoder.
type attachmentPart struct {
	FileID       entityID `json:"file_id"`
	Size         int64    `json:"size,string"`
	URL          string   `json:"url"`
	ThumbnailURL string   `json:"thumbnail_url"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mime_type"`
}

type attachmentInfo struct {
	FileID     entityID `json:"file_id"`
	UploaderID entityID `json:"uploader_id"`
	Name       string   `json:"name"`
	Key        string   `json:"key"`
	Bucket     string   `json:"bucket"`
	MimeType   string   `json:"mime_type"`
	Size       int64    `json:"size,string"`
	MD5        string   `json:"md5"`
	Access     int32    `json:"access"`
	Purpose    int32    `json:"purpose"`
}

type uploadedAttachment struct {
	info      attachmentInfo
	data      []byte
	kind      int32
	objectURL string
}

type attachmentACK struct {
	MessageID entityID       `json:"message_id"`
	ConvID    entityID       `json:"conv_id"`
	SenderID  entityID       `json:"from_user_id"`
	Seq       sequenceNumber `json:"seq"`
	CreatedAt int64          `json:"created_at,string"`
	Type      int32          `json:"type"`
	Content   struct {
		Files []struct {
			FileID   entityID `json:"file_id"`
			Size     int64    `json:"size"`
			URL      string   `json:"url"`
			Name     string   `json:"file_name"`
			MimeType string   `json:"mime_type"`
		} `json:"files"`
	} `json:"content"`
}

type attachmentConfirmation struct {
	ack  attachmentACK
	file uploadedAttachment
	key  string
}

func (d *driver) attachments(addressA, addressB string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	a, err := d.register("attachment_a", suffix)
	if err != nil {
		return err
	}
	b, err := d.register("attachment_b", suffix)
	if err != nil {
		return err
	}
	outsider, err := d.register("attachment_outsider", suffix)
	if err != nil {
		return err
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", a.token, map[string]any{"type": "single", "peer_user_id": b.id}, &conv); err != nil {
		return err
	}
	connA, err := d.connect(addressA, a)
	if err != nil {
		return err
	}
	defer connA.Close()
	connB, err := d.connect(addressB, b)
	if err != nil {
		return err
	}
	defer connB.Close()
	for _, socket := range []*websocket.Conn{connA, connB} {
		if err := d.ready(socket); err != nil {
			return err
		}
	}
	initial, err := d.inboxRead("attachments-initial", b, 0, 50)
	if err != nil {
		return err
	}

	imageBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jxioAAAAASUVORK5CYII=")
	if err != nil {
		return err
	}
	// A real two-frame H.264/MP4 fixture, rather than a MIME-labelled placeholder.
	videoBytes, err := base64.StdEncoding.DecodeString("AAAAIGZ0eXBpc29tAAACAGlzb21pc28yYXZjMW1wNDEAAAMxbW9vdgAAAGxtdmhkAAAAAAAAAAAAAAAAAAAD6AAAAFAAAQAAAQAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAlt0cmFrAAAAXHRraGQAAAADAAAAAAAAAAAAAAABAAAAAAAAAFAAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAABAAAAAQAAAAAAAkZWR0cwAAABxlbHN0AAAAAAAAAAEAAABQAAAAAAABAAAAAAHTbWRpYQAAACBtZGhkAAAAAAAAAAAAAAAAAAAyAAAABABVxAAAAAAALWhkbHIAAAAAAAAAAHZpZGUAAAAAAAAAAAAAAABWaWRlb0hhbmRsZXIAAAABfm1pbmYAAAAUdm1oZAAAAAEAAAAAAAAAAAAAACRkaW5mAAAAHGRyZWYAAAAAAAAAAQAAAAx1cmwgAAAAAQAAAT5zdGJsAAAAvnN0c2QAAAAAAAAAAQAAAK5hdmMxAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAABAAEABIAAAASAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGP//AAAANGF2Y0MBZAAK/+EAF2dkAAqs2V7ARAAAAwAEAAADAMg8SJZYAQAGaOvjyyLA/fj4AAAAABBwYXNwAAAAAQAAAAEAAAAUYnRydAAAAAAAARuYAAEbmAAAABhzdHRzAAAAAAAAAAEAAAACAAACAAAAABRzdHNzAAAAAAAAAAEAAAABAAAAHHN0c2MAAAAAAAAAAQAAAAEAAAACAAAAAQAAABxzdHN6AAAAAAAAAAAAAAACAAACygAAAAwAAAAUc3RjbwAAAAAAAAABAAADYQAAAGJ1ZHRhAAAAWm1ldGEAAAAAAAAAIWhkbHIAAAAAAAAAAG1kaXJhcHBsAAAAAAAAAAAAAAAALWlsc3QAAAAlqXRvbwAAAB1kYXRhAAAAAQAAAABMYXZmNTguNzYuMTAwAAAACGZyZWUAAALebWRhdAAAAq4GBf//qtxF6b3m2Ui3lizYINkj7u94MjY0IC0gY29yZSAxNjMgcjMwNjAgNWRiNmFhNiAtIEguMjY0L01QRUctNCBBVkMgY29kZWMgLSBDb3B5bGVmdCAyMDAzLTIwMjEgLSBodHRwOi8vd3d3LnZpZGVvbGFuLm9yZy94MjY0Lmh0bWwgLSBvcHRpb25zOiBjYWJhYz0xIHJlZj0zIGRlYmxvY2s9MTowOjAgYW5hbHlzZT0weDM6MHgxMTMgbWU9aGV4IHN1Ym1lPTcgcHN5PTEgcHN5X3JkPTEuMDA6MC4wMCBtaXhlZF9yZWY9MSBtZV9yYW5nZT0xNiBjaHJvbWFfbWU9MSB0cmVsbGlzPTEgOHg4ZGN0PTEgY3FtPTAgZGVhZHpvbmU9MjEsMTEgZmFzdF9wc2tpcD0xIGNocm9tYV9xcF9vZmZzZXQ9LTIgdGhyZWFkcz0xIGxvb2thaGVhZF90aHJlYWRzPTEgc2xpY2VkX3RocmVhZHM9MCBucj0wIGRlY2ltYXRlPTEgaW50ZXJsYWNlZD0wIGJsdXJheV9jb21wYXQ9MCBjb25zdHJhaW5lZF9pbnRyYT0wIGJmcmFtZXM9MyBiX3B5cmFtaWQ9MiBiX2FkYXB0PTEgYl9iaWFzPTAgZGlyZWN0PTEgd2VpZ2h0Yj0xIG9wZW5fZ29wPTAgd2VpZ2h0cD0yIGtleWludD0yNTAga2V5aW50X21pbj0yNSBzY2VuZWN1dD00MCBpbnRyYV9yZWZyZXNoPTAgcmNfbG9va2FoZWFkPTQwIHJjPWNyZiBtYnRyZWU9MSBjcmY9MjMuMCBxY29tcD0wLjYwIHFwbWluPTAgcXBtYXg9NjkgcXBzdGVwPTQgaXBfcmF0aW89MS40MCBhcT0xOjEuMDAAgAAAABRliIQAL//+21vzLKpSiq0ORvownQAAAAhBmiFsQr/+wA==")
	if err != nil {
		return err
	}
	fixtures := []struct {
		name, mime string
		kind       int32
		data       []byte
	}{
		{"image.png", "image/png", 2, imageBytes},
		{"audio.wav", "audio/wav", 5, []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x80\x3e\x00\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x00\x00")},
		{"video.mp4", "video/mp4", 4, videoBytes},
		{"file.txt", "text/plain", 3, []byte("真实附件对象字节\n" + suffix + "\x00\xff")},
	}
	var files []uploadedAttachment
	var confirmed []attachmentConfirmation
	var after sequenceNumber
	for _, fixture := range fixtures {
		file, err := d.attachmentUpload(a, suffix+"_"+fixture.name, fixture.mime, fixture.data, 3)
		if err != nil {
			return err
		}
		file.kind = fixture.kind
		files = append(files, file)
		// A valid user outside this conversation can access public entities too.
		if err := d.attachmentRead(outsider, file); err != nil {
			return err
		}
		key, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		body := attachmentBody(conv.ID, key.String(), file.info.FileID, "https://obsolete.invalid/signed-before")
		ack, err := d.attachmentSend(a, body, conv.ID, file, after)
		if err != nil {
			return err
		}
		current := attachmentConfirmation{ack: ack, file: file, key: key.String()}
		if err := d.attachmentWS(connB, current); err != nil {
			return err
		}
		if err := d.attachmentPersisted(b, current); err != nil {
			return err
		}
		// Changing only an ephemeral URL must recover the committed UUID/seq.
		retry, err := d.attachmentSend(a, attachmentBody(conv.ID, key.String(), file.info.FileID, "https://obsolete.invalid/signed-after"), conv.ID, file, after)
		if err != nil {
			return err
		}
		if retry.MessageID != ack.MessageID || retry.Seq != ack.Seq || retry.CreatedAt != ack.CreatedAt {
			return errors.New("attachments.retry: 同键重试改变原消息UUID/seq/创建时间")
		}
		confirmed = append(confirmed, current)
		after = ack.Seq
	}
	// The same type with a different file entity is a semantic conflict, even
	// when its bytes are identical; it must not append a second history row.
	other, err := d.attachmentUpload(a, suffix+"_other.png", "image/png", imageBytes, 3)
	if err != nil {
		return err
	}
	other.kind = 2
	conflictKey, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	original, err := d.attachmentSend(a, attachmentBody(conv.ID, conflictKey.String(), files[0].info.FileID, ""), conv.ID, files[0], after)
	if err != nil {
		return err
	}
	originalConfirmation := attachmentConfirmation{ack: original, file: files[0], key: conflictKey.String()}
	if err := d.attachmentWS(connB, originalConfirmation); err != nil {
		return err
	}
	if err := d.submissionConflict(a, attachmentBody(conv.ID, conflictKey.String(), other.info.FileID, "")); err != nil {
		return err
	}
	if err := d.attachmentPersisted(b, originalConfirmation); err != nil {
		return err
	}
	confirmed = append(confirmed, originalConfirmation)
	after = original.Seq
	if err := d.attachmentHistory(b, confirmed); err != nil {
		return err
	}
	position, err := d.attachmentInbox(b, initial.NextPosition, confirmed)
	if err != nil {
		return err
	}

	// Disconnect before submitting, then consume the real persisted account
	// inbox: all four oneofs must survive offline replay with downloadable IDs.
	if err := connB.Close(); err != nil {
		return err
	}
	var offline []attachmentConfirmation
	// The initial five messages have now been fenced in the recipient inbox.
	for _, file := range files {
		key, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		ack, err := d.attachmentSend(a, attachmentBody(conv.ID, key.String(), file.info.FileID, ""), conv.ID, file, after)
		if err != nil {
			return err
		}
		current := attachmentConfirmation{ack: ack, file: file, key: key.String()}
		offline = append(offline, current)
		after = ack.Seq
	}
	if _, err := d.attachmentInbox(b, position, offline); err != nil {
		return err
	}
	for _, current := range offline {
		if err := d.attachmentPersisted(b, current); err != nil {
			return err
		}
	}
	if err := d.attachmentReplySearch(a, b, connA, confirmed, suffix); err != nil {
		return err
	}
	if err := d.attachmentFailures(a, b, conv.ID, suffix); err != nil {
		return err
	}

	// Deleting a message must not garbage-collect a reused or unrelated file.
	if err := d.request(http.MethodDelete, "/messages/"+original.MessageID.String(), a.token, map[string]bool{"delete_for_all": true}, nil); err != nil {
		return err
	}
	if err := d.inboxMessageAbsent("attachments-message-delete", b, original.MessageID); err != nil {
		return err
	}
	for _, file := range append(files, other) {
		if err := d.attachmentRead(b, file); err != nil {
			return err
		}
	}
	if err := d.attachmentPersisted(b, confirmed[0]); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, "/files/"+other.info.FileID.String(), a.token, nil, nil); err != nil {
		return err
	}
	if err := d.attachmentDeleted(a, other); err != nil {
		return err
	}
	for _, file := range files {
		if err := d.attachmentRead(b, file); err != nil {
			return err
		}
	}
	if err := d.attachmentBatchDelete(a, b, files); err != nil {
		return err
	}
	for _, submitted := range confirmed[:4] {
		retry, err := d.attachmentSend(a, attachmentBody(conv.ID, submitted.key, submitted.file.info.FileID, ""), conv.ID, submitted.file, submitted.ack.Seq-1)
		if err != nil {
			return fmt.Errorf("attachments.deleted-file-replay: %w", err)
		}
		if retry.MessageID != submitted.ack.MessageID || retry.Seq != submitted.ack.Seq || retry.CreatedAt != submitted.ack.CreatedAt {
			return errors.New("attachments.deleted-file-replay: 文件删除后原提交身份改变")
		}
		if err := d.submissionConflict(a, attachmentBody(conv.ID, submitted.key, other.info.FileID, "")); err != nil {
			return err
		}
	}
	return nil
}

func attachmentBody(conv entityID, key string, file entityID, temporaryURL string) map[string]any {
	return map[string]any{"conversation_id": conv, "client_msg_id": key, "content": map[string]any{"files": []map[string]any{{"file_id": file, "url": temporaryURL}}}}
}

func (d *driver) attachmentObject(method, address, mime string, data []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(data))
	if err != nil {
		return 0, nil, errors.New("attachments.object: 无效对象URL")
	}
	if mime != "" {
		req.Header.Set("Content-Type", mime)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("attachments.object: %s", transportFailure(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("attachments.object: %s", transportFailure(err))
	}
	return resp.StatusCode, raw, nil
}

func (d *driver) attachmentUpload(caller account, name, mime string, data []byte, access int32) (uploadedAttachment, error) {
	var upload struct {
		FileID    entityID `json:"file_id"`
		URL       string   `json:"upload_url"`
		Key       string   `json:"key"`
		ExpiresAt int64    `json:"expires_at,string"`
	}
	if err := d.request(http.MethodPost, "/files/upload_url", caller.token, map[string]any{"name": name, "mime_type": mime, "size": len(data), "purpose": 1, "access": access}, &upload); err != nil {
		return uploadedAttachment{}, err
	}
	if upload.FileID == "" || uuid.MustParse(upload.FileID.String()).Version() != 7 || upload.ExpiresAt <= time.Now().Unix() {
		return uploadedAttachment{}, errors.New("attachments.upload: 未分配UUIDv7或上传能力已过期")
	}
	statusCode, _, err := d.attachmentObject(http.MethodPut, upload.URL, mime, data)
	if err != nil {
		return uploadedAttachment{}, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return uploadedAttachment{}, fmt.Errorf("attachments.PUT: HTTP%d", statusCode)
	}
	digest := md5.Sum(data)
	var result struct {
		File attachmentInfo `json:"file"`
	}
	if err := d.request(http.MethodPost, "/files/confirm", caller.token, map[string]any{"file_id": upload.FileID, "md5": hex.EncodeToString(digest[:])}, &result); err != nil {
		return uploadedAttachment{}, err
	}
	file := uploadedAttachment{info: result.File, data: data}
	if file.info.FileID != upload.FileID || file.info.Key != upload.Key || file.info.UploaderID != caller.id || file.info.Size != int64(len(data)) || file.info.Name != name || file.info.MimeType != mime || file.info.Access != access || file.info.Purpose != 1 || file.info.MD5 != hex.EncodeToString(digest[:]) {
		return file, errors.New("attachments.confirm: 上传实体身份/内容元数据/权限与PUT不一致")
	}
	if access == 3 && !strings.HasPrefix(file.info.Key, "public/") {
		return file, errors.New("attachments.public: 公有对象不在public前缀")
	}
	if access == 1 && strings.HasPrefix(file.info.Key, "public/") {
		return file, errors.New("attachments.private: 私有对象落入匿名公有前缀")
	}
	var download struct {
		URL string `json:"download_url"`
	}
	if err := d.request(http.MethodGet, "/files/"+file.info.FileID.String()+"/download", caller.token, nil, &download); err != nil {
		return file, err
	}
	file.objectURL = download.URL
	return file, d.attachmentRead(caller, file)
}

func (d *driver) attachmentRead(caller account, file uploadedAttachment) error {
	var info struct {
		File attachmentInfo `json:"file"`
	}
	path := "/files/" + file.info.FileID.String()
	if err := d.request(http.MethodGet, path+"/info", caller.token, nil, &info); err != nil {
		return err
	}
	if info.File != file.info {
		return errors.New("attachments.info: 同一UUID元数据与confirm不同")
	}
	var download struct {
		URL       string         `json:"download_url"`
		ExpiresAt int64          `json:"expires_at,string"`
		File      attachmentInfo `json:"file"`
	}
	if err := d.request(http.MethodGet, path+"/download", caller.token, nil, &download); err != nil {
		return err
	}
	if download.File != file.info {
		return errors.New("attachments.download: 下载能力关联了其他文件实体")
	}
	if file.info.Access == 1 && download.ExpiresAt <= time.Now().Unix() {
		return errors.New("attachments.private: 缺少有效signed下载能力")
	}
	code, raw, err := d.attachmentObject(http.MethodGet, download.URL, "", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK || !bytes.Equal(raw, file.data) {
		return errors.New("attachments.download: 读取对象字节不等于真实PUT字节")
	}
	if file.info.Access == 1 {
		u, err := url.Parse(download.URL)
		if err != nil {
			return errors.New("attachments.private: 无效signed URL")
		}
		if u.RawQuery == "" {
			return errors.New("attachments.private: 下载不是signed URL")
		}
		u.RawQuery = ""
		code, _, err := d.attachmentObject(http.MethodGet, u.String(), "", nil)
		if err != nil {
			return err
		}
		if code != http.StatusForbidden {
			return fmt.Errorf("attachments.private: 匿名对象GET期望403，实际%d", code)
		}
	}
	return nil
}

func (d *driver) attachmentSend(sender account, body any, conv entityID, file uploadedAttachment, after sequenceNumber) (attachmentACK, error) {
	var ack attachmentACK
	if err := d.request(http.MethodPost, "/messages/send", sender.token, body, &ack); err != nil {
		return ack, err
	}
	if ack.MessageID == "" || uuid.MustParse(ack.MessageID.String()).Version() != 7 || ack.ConvID != conv || ack.SenderID != sender.id || ack.Seq != after+1 || ack.CreatedAt <= 0 || ack.Type != file.kind || len(ack.Content.Files) != 1 {
		return ack, errors.New("attachments.ACK: 消息UUID/seq/类型/主体不符合提交")
	}
	part := ack.Content.Files[0]
	if part.FileID != file.info.FileID || part.Size != file.info.Size || part.Name != file.info.Name || part.MimeType != file.info.MimeType || part.URL != "" {
		return ack, errors.New("attachments.ACK: 文件引用未解析真实实体或保留临时URL")
	}
	return ack, nil
}

func (d *driver) attachmentWS(socket *websocket.Conn, expected attachmentConfirmation) error {
	if err := socket.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return err
	}
	for {
		_, raw, err := socket.ReadMessage()
		if err != nil {
			return fmt.Errorf("attachments.WS: %s", transportFailure(err))
		}
		var evt struct {
			Type    string    `json:"type"`
			ConvID  *entityID `json:"conv_id"`
			Message struct {
				MessageID entityID       `json:"message_id"`
				ConvID    entityID       `json:"conv_id"`
				SenderID  *entityID      `json:"sender_id"`
				Seq       sequenceNumber `json:"seq"`
				Type      int32          `json:"msg_type"`
				Content   struct {
					FileID entityID `json:"file_id"`
					Size   int64    `json:"size"`
					URL    string   `json:"url"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(raw, &evt); err != nil {
			return errors.New("attachments.WS: UUID/序号/附件合同错误")
		}
		if evt.Type == "error" {
			return errors.New("attachments.WS: realtime返回error")
		}
		if evt.Type != "message.new" || evt.Message.MessageID != expected.ack.MessageID {
			continue
		}
		msg := evt.Message
		if !hasEntityID(evt.ConvID, expected.ack.ConvID) || msg.ConvID != expected.ack.ConvID || !hasEntityID(msg.SenderID, expected.ack.SenderID) || msg.Seq != expected.ack.Seq || msg.Type != expected.file.kind || msg.Content.FileID != expected.file.info.FileID || msg.Content.Size != expected.file.info.Size || msg.Content.URL != "" {
			return errors.New("attachments.WS: 跨实例投递身份/类型/附件引用与ACK不一致")
		}
		return nil
	}
}

func checkAttachmentMessage(actual storedMessage, expected attachmentConfirmation) error {
	if actual.MessageID != expected.ack.MessageID || actual.ConvID != expected.ack.ConvID || !hasEntityID(actual.SenderID, expected.ack.SenderID) || actual.Seq != expected.ack.Seq || actual.CreatedAt != expected.ack.CreatedAt || actual.Type != expected.file.kind {
		return errors.New("attachments.persisted: 消息UUID/seq/类型与ACK不一致")
	}
	var part *attachmentPart
	count := 0
	for i, value := range []*attachmentPart{actual.Image, actual.File, actual.Video, actual.Audio} {
		if value == nil {
			continue
		}
		count++
		if int32(i+2) == expected.file.kind {
			part = value
		}
	}
	if count != 1 || part == nil || part.FileID != expected.file.info.FileID || part.Size != expected.file.info.Size || part.URL != "" || part.ThumbnailURL != "" {
		return errors.New("attachments.persisted: 正确oneof/file_id/size或持久化URL不符合合同")
	}
	if expected.file.kind == 3 && (part.Name != expected.file.info.Name || part.MimeType != expected.file.info.MimeType) {
		return errors.New("attachments.persisted: 文件实体名称/MIME丢失")
	}
	return nil
}

func (d *driver) attachmentPersisted(caller account, expected attachmentConfirmation) error {
	var direct struct {
		Message storedMessage `json:"message"`
	}
	if err := d.request(http.MethodGet, "/messages/"+expected.ack.MessageID.String(), caller.token, nil, &direct); err != nil {
		return err
	}
	if err := checkAttachmentMessage(direct.Message, expected); err != nil {
		return err
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err := d.request(http.MethodGet, "/messages/"+expected.ack.ConvID.String()+"/around/"+expected.ack.Seq.String(), caller.token, nil, &history); err != nil {
		return err
	}
	count := 0
	for _, message := range history.Messages {
		if message.Seq != expected.ack.Seq && message.MessageID != expected.ack.MessageID {
			continue
		}
		if err := checkAttachmentMessage(message, expected); err != nil {
			return err
		}
		count++
	}
	if count != 1 {
		return errors.New("attachments.history: 原始seq消息不是唯一历史实体")
	}
	return d.attachmentRead(caller, expected.file)
}

func (d *driver) attachmentInbox(caller account, position sequenceNumber, expected []attachmentConfirmation) (sequenceNumber, error) {
	pending := make(map[entityID]attachmentConfirmation, len(expected))
	for _, current := range expected {
		pending[current.ack.MessageID] = current
	}
	seen := make(map[entityID]bool, len(expected))
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		bounded := *d
		bounded.timeout = time.Until(deadline)
		page, err := bounded.inboxRead("attachments-offline", caller, position, 1)
		if err != nil {
			return position, err
		}
		if page.RebuildRequired {
			return position, errors.New("attachments.inbox: 有效位点触发重建")
		}
		for _, change := range page.Changes {
			if change.Kind == "conversation.upsert" {
				continue
			}
			current, ok := pending[change.Message.MessageID]
			if !ok || seen[change.Message.MessageID] || change.Kind != "message.new" || change.ConversationID != current.ack.ConvID {
				return position, errors.New("attachments.inbox: 重复/未知消息或错误变化类型")
			}
			if err := checkAttachmentMessage(change.Message, current); err != nil {
				return position, err
			}
			if err := d.attachmentRead(caller, current.file); err != nil {
				return position, err
			}
			seen[change.Message.MessageID] = true
			delete(pending, change.Message.MessageID)
		}
		position = page.NextPosition
		if page.HasMore {
			continue
		}
		if len(pending) == 0 {
			return position, nil
		}
		time.Sleep(min(100*time.Millisecond, max(0, time.Until(deadline))))
	}
	return position, fmt.Errorf("attachments.inbox: 截止时间内缺失%d条确认附件消息", len(pending))
}

func (d *driver) attachmentReplySearch(sender, receiver account, socket *websocket.Conn, confirmed []attachmentConfirmation, suffix string) error {
	for i, target := range confirmed[:4] {
		key, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		text := fmt.Sprintf("附件回复%s%d", suffix, i)
		var reply sentMessage
		if err := d.request(http.MethodPost, "/messages/send", receiver.token, map[string]any{"conversation_id": target.ack.ConvID, "client_msg_id": key.String(), "reply_to_msg_id": target.ack.MessageID, "content": map[string]string{"text": text}}, &reply); err != nil {
			return err
		}
		if err := d.receiveMessage(socket, reply); err != nil {
			return err
		}
		var direct struct {
			Message storedMessage `json:"message"`
		}
		if err := d.request(http.MethodGet, "/messages/"+reply.MessageID.String(), sender.token, nil, &direct); err != nil {
			return err
		}
		if err := checkStoredMessage("attachments.reply-byID", direct.Message, reply); err != nil {
			return err
		}
		check := func(message storedMessage) error {
			if !hasEntityID(message.ReplyToID, target.ack.MessageID) || message.ReplyTo == nil || message.ReplyTo.MessageID != target.ack.MessageID || !hasEntityID(message.ReplyTo.SenderID, sender.id) || message.ReplyTo.Type != target.file.kind || message.ReplyTo.Deleted || message.ReplyTo.Preview != "[消息]" {
				return errors.New("attachments.reply: 附件引用摘要未保留原消息身份/类型/预览")
			}
			return nil
		}
		if err := check(direct.Message); err != nil {
			return err
		}
		// Search's current contract indexes text, not attachment filenames. A
		// searchable text reply must expose its real attachment-message reference.
		searched, err := d.inboxSearchVisible(sender, reply)
		if err != nil {
			return err
		}
		if err := check(searched); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) attachmentReject(method, path string, caller account, input any, expected int) error {
	err := d.request(method, path, caller.token, input, nil)
	var rejected *apiError
	if !errors.As(err, &rejected) || rejected.status != expected || rejected.code == 0 {
		return fmt.Errorf("attachments.reject %s %s: 期望HTTP%d，实际%v", method, path, expected, err)
	}
	return nil
}

func (d *driver) attachmentFailures(owner, other account, conv entityID, suffix string) error {
	unknown, err := uuid.NewV7()
	if err != nil {
		return err
	}
	for _, candidate := range []struct {
		id   any
		code int
	}{{unknown.String(), http.StatusNotFound}, {"42", http.StatusBadRequest}, {"not-a-uuid", http.StatusBadRequest}, {42, http.StatusBadRequest}} {
		if err := d.attachmentReject(http.MethodPost, "/files/confirm", owner, map[string]any{"file_id": candidate.id}, candidate.code); err != nil {
			return err
		}
		if id, ok := candidate.id.(string); ok {
			for _, tail := range []string{"/info", "/download"} {
				if err := d.attachmentReject(http.MethodGet, "/files/"+id+tail, owner, nil, candidate.code); err != nil {
					return err
				}
			}
			if err := d.attachmentReject(http.MethodDelete, "/files/"+id, owner, nil, candidate.code); err != nil {
				return err
			}
		}
		key, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		body := map[string]any{"conversation_id": conv, "client_msg_id": key.String(), "content": map[string]any{"files": []map[string]any{{"file_id": candidate.id}}}}
		if err := d.attachmentReject(http.MethodPost, "/messages/send", owner, body, candidate.code); err != nil {
			return err
		}
	}
	var absent struct {
		FileID entityID `json:"file_id"`
	}
	if err := d.request(http.MethodPost, "/files/upload_url", owner.token, map[string]any{"name": suffix + "_absent.txt", "mime_type": "text/plain", "size": 4, "purpose": 1, "access": 3}, &absent); err != nil {
		return err
	}
	if err := d.attachmentReject(http.MethodPost, "/files/confirm", owner, map[string]any{"file_id": absent.FileID}, http.StatusNotFound); err != nil {
		return err
	}
	if err := d.attachmentReject(http.MethodGet, "/files/"+absent.FileID.String()+"/download", owner, nil, http.StatusNotFound); err != nil {
		return err
	}
	key, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	if err := d.attachmentReject(http.MethodPost, "/messages/send", owner, attachmentBody(conv, key.String(), absent.FileID, ""), http.StatusNotFound); err != nil {
		return err
	}
	private, err := d.attachmentUpload(owner, suffix+"_private.txt", "text/plain", []byte("private object "+suffix), 1)
	if err != nil {
		return err
	}
	for _, operation := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/files/" + private.info.FileID.String() + "/info", nil},
		{http.MethodGet, "/files/" + private.info.FileID.String() + "/download", nil},
		{http.MethodDelete, "/files/" + private.info.FileID.String(), nil},
		{http.MethodPost, "/files/confirm", map[string]any{"file_id": private.info.FileID}},
		{http.MethodPost, "/messages/send", attachmentBody(conv, key.String(), private.info.FileID, "")},
	} {
		if err := d.attachmentReject(operation.method, operation.path, other, operation.body, http.StatusForbidden); err != nil {
			return err
		}
	}
	if err := d.attachmentRead(owner, private); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, "/files/"+private.info.FileID.String(), owner.token, nil, nil); err != nil {
		return err
	}
	if err := d.attachmentDeleted(owner, private); err != nil {
		return err
	}
	return d.request(http.MethodDelete, "/files/"+absent.FileID.String(), owner.token, nil, nil)
}

func (d *driver) attachmentDeleted(owner account, file uploadedAttachment) error {
	for _, tail := range []string{"/info", "/download"} {
		if err := d.attachmentReject(http.MethodGet, "/files/"+file.info.FileID.String()+tail, owner, nil, http.StatusNotFound); err != nil {
			return err
		}
	}
	if err := d.attachmentReject(http.MethodPost, "/files/confirm", owner, map[string]any{"file_id": file.info.FileID}, http.StatusNotFound); err != nil {
		return err
	}
	// A new download API rejection alone is not deletion proof: check the
	// actual previously-addressable object, without an API metadata lookup.
	return d.attachmentObjectAbsent(file)
}

func (d *driver) attachmentObjectAbsent(file uploadedAttachment) error {
	code, _, err := d.attachmentObject(http.MethodGet, file.objectURL, "", nil)
	if err != nil {
		return err
	}
	// GetObject-only public policy may hide missing objects as403. A retained
	// private signed capability must distinguish actual deletion as404.
	if code != http.StatusNotFound && !(file.info.Access == 3 && code == http.StatusForbidden) {
		return fmt.Errorf("attachments.delete: 原下载能力未观察到对象删除，HTTP%d", code)
	}
	return nil
}

func (d *driver) attachmentBatchDelete(owner, other account, files []uploadedAttachment) error {
	conn, err := grpc.NewClient(d.fileRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return errors.New("attachments.batch: 无法连接真实file RPC")
	}
	defer conn.Close()
	client := filepb.NewFileServiceClient(conn)
	ids := make([]string, 0, len(files))
	for _, file := range files {
		ids = append(ids, file.info.FileID.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	if _, err := client.BatchDeleteFiles(ctx, &filepb.BatchDeleteFilesReq{FileIds: ids, UserId: owner.id.String()}); status.Code(err) != codes.Unauthenticated {
		return fmt.Errorf("attachments.batch: 无caller期望Unauthenticated，实际%s", status.Code(err))
	}
	forged := metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", other.id.String()))
	if _, err := client.BatchDeleteFiles(forged, &filepb.BatchDeleteFilesReq{FileIds: ids, UserId: owner.id.String()}); status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("attachments.batch: 伪造owner期望PermissionDenied，实际%s", status.Code(err))
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	survivor, err := d.attachmentUpload(other, suffix+"_batch_survivor.txt", "text/plain", []byte("foreign-owned survivor "+suffix), 3)
	if err != nil {
		return err
	}
	invoke := func(caller account) error {
		ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
		defer cancel()
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", caller.id.String(), "device-id", caller.device))
		response, err := client.BatchDeleteFiles(ctx, &filepb.BatchDeleteFilesReq{FileIds: ids, UserId: caller.id.String()})
		if err != nil {
			return fmt.Errorf("attachments.batch: RPC %s", status.Code(err))
		}
		if response.GetCode() != 0 {
			return fmt.Errorf("attachments.batch: 业务code=%d", response.GetCode())
		}
		return nil
	}
	// Existing bulk deletion is best effort: a non-owner batch is a no-op,
	// not permission to erase public objects that they are allowed to read.
	if err := invoke(other); err != nil {
		return err
	}
	for _, file := range files {
		if err := d.attachmentRead(other, file); err != nil {
			return err
		}
	}
	// Include a readable foreign-owned object in the owner's real batch. Only
	// owned UUIDs may be removed; unrelated object bytes must remain intact.
	ids = append(ids, survivor.info.FileID.String())
	if err := invoke(owner); err != nil {
		return err
	}
	for _, file := range files {
		if err := d.attachmentDeleted(owner, file); err != nil {
			return err
		}
	}
	if err := d.attachmentRead(owner, survivor); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, "/files/"+survivor.info.FileID.String(), other.token, nil, nil); err != nil {
		return err
	}
	return d.attachmentDeleted(other, survivor)
}

func (d *driver) attachmentHistory(caller account, expected []attachmentConfirmation) error {
	var page struct {
		Messages []storedMessage `json:"messages"`
	}
	if err := d.request(http.MethodGet, "/convs/"+expected[0].ack.ConvID.String()+"/messages?cursor=0&limit=50", caller.token, nil, &page); err != nil {
		return err
	}
	if len(page.Messages) != len(expected) {
		return errors.New("attachments.history: 重试/冲突追加了额外历史消息")
	}
	pending := make(map[entityID]attachmentConfirmation, len(expected))
	for _, current := range expected {
		pending[current.ack.MessageID] = current
	}
	for _, actual := range page.Messages {
		current, ok := pending[actual.MessageID]
		if !ok {
			return errors.New("attachments.history: 出现重复或未确认消息UUID")
		}
		if err := checkAttachmentMessage(actual, current); err != nil {
			return err
		}
		delete(pending, actual.MessageID)
	}
	if len(pending) != 0 {
		return errors.New("attachments.history: 缺失原提交历史")
	}
	return nil
}
