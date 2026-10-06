package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	entityidentity "github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// 实体身份只接收规范 UUID 字符串，序号只接收安全整数 JSON number。
type entityID string

func (id *entityID) UnmarshalJSON(raw []byte) error {
	if len(raw) == 0 || raw[0] != '"' {
		return errors.New("实体身份必须为 UUID JSON 字符串")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if err := entityidentity.Validate(value); err != nil {
		return err
	}
	*id = entityID(value)
	return nil
}

func (id entityID) MarshalJSON() ([]byte, error) {
	if err := entityidentity.Validate(string(id)); err != nil {
		return nil, err
	}
	return json.Marshal(string(id))
}

func (id entityID) String() string { return string(id) }

type sequenceNumber int64

func (seq *sequenceNumber) UnmarshalJSON(raw []byte) error {
	value, err := sequence.ParseJSON(raw)
	if err != nil {
		return err
	}
	*seq = sequenceNumber(value)
	return nil
}

func (seq sequenceNumber) MarshalJSON() ([]byte, error) {
	if err := sequence.Validate(int64(seq)); err != nil {
		return nil, err
	}
	return []byte(seq.String()), nil
}

func (seq sequenceNumber) String() string { return strconv.FormatInt(int64(seq), 10) }

func hasEntityID(id *entityID, expected entityID) bool {
	return id != nil && *id == expected
}

type identity struct {
	ID       entityID `json:"id"`
	Username string   `json:"username"`
}

type authResult struct {
	UserID entityID `json:"user_id"`
	User   identity `json:"user"`
	Tokens struct {
		AccessToken string `json:"access_token"`
	} `json:"tokens"`
}

type account struct {
	id       entityID
	username string
	device   string
	token    string
	password string
}

type sentMessage struct {
	SubmissionKey string         `json:"-"`
	MessageID     entityID       `json:"message_id"`
	ConvID        entityID       `json:"conv_id"`
	SenderID      entityID       `json:"from_user_id"`
	Seq           sequenceNumber `json:"seq"`
	Content       struct {
		Text string `json:"text"`
	} `json:"content"`
}

type event struct {
	Type    string    `json:"type"`
	ConvID  *entityID `json:"conv_id"`
	Message struct {
		MessageID entityID       `json:"message_id"`
		ConvID    entityID       `json:"conv_id"`
		SenderID  *entityID      `json:"sender_id"`
		Seq       sequenceNumber `json:"seq"`
		ReplyToID *entityID      `json:"reply_to_msg_id"`
		Content   struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

type driver struct {
	gateway       string
	client        *http.Client
	timeout       time.Duration
	provider      string
	ingestTimeout time.Duration
	queryDeadline time.Duration
	controlDir    string
}

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "E2E FAIL: %v\n", err)
		os.Exit(1)
	}
}

func execute(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: /e2e run [flags] | /e2e probe -kind http|grpc -address URL|host:port [-timeout 3s]")
	}
	switch args[0] {
	case "probe":
		return probe(args[1:])
	case "run":
		return run(args[1:])
	default:
		return errors.New("未知子命令；支持 run 和 probe")
	}
}

func options(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

func run(args []string) error {
	flags := options("run")
	gateway := flags.String("gateway", "http://gateway:8080", "gateway HTTP 根地址")
	realtimeA := flags.String("realtime-a", "ws://realtime-service:8081/ws", "realtime A WebSocket 地址")
	realtimeB := flags.String("realtime-b", "ws://realtime-b:8081/ws", "realtime B WebSocket 地址")
	cross := flags.Bool("cross-instance", false, "额外验收两个用户分别连接 A/B 的双向投递；失败返回非零")
	selected := flags.String("scenario", "all", "选择 all|stage-p3|stage-p4|stage-p5|stage-p6|bot-runtime|knowledge-ingest|relationships|conversations|conversation-unread|broadcasts|user-sync|same-instance-a|same-instance-b|cross-instance")
	timeout := flags.Duration("timeout", 20*time.Second, "每次 HTTP/WS 操作的超时时间")
	provider := flags.String("provider", "http://e2e-provider:8099", "外部 OpenAI/MCP fixture HTTP 根地址")
	ingestTimeout := flags.Duration("ingest-timeout", 10*time.Minute, "异步入库完成的总截止时间")
	queryDeadline := flags.Duration("query-deadline", 5*time.Second, "入库负载下每次检索的硬截止时间")
	controlDir := flags.String("control-dir", "", "stage-p6 runner 生命周期检查点目录")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 || *ingestTimeout <= 0 || *queryDeadline <= 0 {
		return errors.New("run 不接受位置参数，所有 timeout/deadline 必须大于零")
	}
	if _, err := endpoint(*gateway, "http", "https"); err != nil {
		return fmt.Errorf("配置 gateway: %w", err)
	}
	type scenarioSpec struct{ name, a, b string }
	var scenarios []scenarioSpec
	switch *selected {
	case "all":
		scenarios = []scenarioSpec{
			{"relationships", "", ""},
			{"conversations", *realtimeA, *realtimeA},
			{"broadcasts", *realtimeA, *realtimeB},
			{"user-sync", *realtimeA, *realtimeB},
			{"same-instance-a", *realtimeA, *realtimeA},
			{"same-instance-b", *realtimeB, *realtimeB},
			{"bot-runtime", *realtimeA, *realtimeA},
			{"knowledge-ingest", "", ""},
			{"cross-instance", *realtimeA, *realtimeB},
		}
	case "stage-p3":
		scenarios = []scenarioSpec{{"relationships", "", ""}, {"same-instance-a", *realtimeA, *realtimeA}}
	case "stage-p4":
		scenarios = []scenarioSpec{{"relationships", "", ""}, {"same-instance-a", *realtimeA, *realtimeA}, {"conversation-unread", *realtimeA, *realtimeA}}
	case "stage-p5":
		scenarios = []scenarioSpec{{"relationships", "", ""}, {"same-instance-a", *realtimeA, *realtimeA}, {"conversation-unread", *realtimeA, *realtimeA}, {"bot-runtime", *realtimeA, *realtimeA}, {"knowledge-ingest", "", ""}}
	case "stage-p6":
		if *controlDir == "" {
			return errors.New("stage-p6 需要 run.py 生命周期控制与 -control-dir")
		}
		scenarios = []scenarioSpec{{"stage-p6", *realtimeA, *realtimeB}}
	case "bot-runtime":
		scenarios = []scenarioSpec{{"bot-runtime", *realtimeA, *realtimeA}}
	case "knowledge-ingest":
		scenarios = []scenarioSpec{{"knowledge-ingest", "", ""}}
	case "conversations", "conversation-unread":
		scenarios = []scenarioSpec{{*selected, *realtimeA, *realtimeA}}
	case "broadcasts":
		scenarios = []scenarioSpec{{"broadcasts", *realtimeA, *realtimeB}}
	case "relationships":
		scenarios = []scenarioSpec{{"relationships", "", ""}}
	case "user-sync":
		scenarios = []scenarioSpec{{"user-sync", *realtimeA, *realtimeB}}
	case "same-instance-a":
		scenarios = []scenarioSpec{{"same-instance-a", *realtimeA, *realtimeA}}
	case "same-instance-b":
		scenarios = []scenarioSpec{{"same-instance-b", *realtimeB, *realtimeB}}
	case "cross-instance":
		scenarios = []scenarioSpec{{"cross-instance", *realtimeA, *realtimeB}}
	default:
		return errors.New("scenario 必须为 all|stage-p3|stage-p4|stage-p5|stage-p6|bot-runtime|knowledge-ingest|relationships|conversations|conversation-unread|broadcasts|user-sync|same-instance-a|same-instance-b|cross-instance")
	}
	if *cross && *selected != "all" && *selected != "cross-instance" && *selected != "stage-p6" {
		scenarios = append(scenarios, scenarioSpec{"cross-instance", *realtimeA, *realtimeB})
	}
	for _, scenario := range scenarios {
		if scenario.name == "bot-runtime" || scenario.name == "knowledge-ingest" || scenario.name == "stage-p6" {
			if _, err := endpoint(*provider, "http", "https"); err != nil {
				return fmt.Errorf("配置外部 provider: %w", err)
			}
		}
		if scenario.name == "relationships" || scenario.name == "knowledge-ingest" {
			continue
		}
		for _, address := range []string{scenario.a, scenario.b} {
			if _, err := endpoint(address, "ws", "wss"); err != nil {
				return fmt.Errorf("配置 realtime 场景 %s: %w", scenario.name, err)
			}
		}
	}
	if (*selected == "all" || *selected == "user-sync" || *selected == "cross-instance" || *selected == "broadcasts" || *selected == "stage-p6" || *cross) && *realtimeA == *realtimeB {
		return errors.New("realtime A/B 必须使用不同地址，不能将单实例冒充两实例")
	}
	d := driver{gateway: strings.TrimRight(*gateway, "/"), client: newHTTPClient(*timeout), timeout: *timeout,
		provider: strings.TrimRight(*provider, "/"), ingestTimeout: *ingestTimeout, queryDeadline: *queryDeadline, controlDir: *controlDir}
	defer d.client.CloseIdleConnections()
	var failures []error
	for _, scenario := range scenarios {
		var err error
		switch scenario.name {
		case "relationships":
			err = d.relationships()
		case "user-sync":
			err = d.userSync(scenario.a, scenario.b)
			if err == nil {
				err = d.inboxChanges(scenario.a, scenario.b)
			}
		case "conversations", "conversation-unread":
			err = d.conversations(scenario.a, scenario.name == "conversation-unread")
		case "broadcasts":
			err = d.broadcasts(scenario.a, scenario.b)
		case "bot-runtime":
			err = d.botRuntime(scenario.a, scenario.b)
		case "knowledge-ingest":
			err = d.knowledgeIngest()
		case "stage-p6":
			err = d.realtimeP6(scenario.a, scenario.b)
		default:
			err = d.scenario(scenario.a, scenario.b)
		}
		if err != nil {
			failure := fmt.Errorf("场景 %s: %w", scenario.name, err)
			fmt.Fprintln(os.Stderr, failure)
			failures = append(failures, failure)
		} else if scenario.name == "relationships" {
			fmt.Println("E2E PASS: relationships 请求 → 接受/拒绝/取消 → 双向好友 → 备注/分组 → 删除 → 拉黑/解除")
		} else if scenario.name == "user-sync" {
			fmt.Println("E2E PASS: user-sync 双账号注册/登录 → 空流正位点重建 → 单位点跨单聊/群聊limit=1分页正文/无遗漏/隔离 → 最近2条历史与置顶免打扰重建 → 续增量 → 未知/负位点显式重建 → 非法参数HTTP400")
			fmt.Println("E2E PASS: inbox-changes 双realtime离线编辑两次/撤回/全删 → 完整状态重放/重复读取 → 在线变更提示 → 会话元数据/私有设置 → 自己已读合并/边界/列表详情回执一致 → 他人已读不入流 → 移除后无消息/重加入/解散")
			fmt.Println("E2E PASS: personal-deletion 他人消息个人删除 → 同账号双设备跨实例tombstone/删除前位点重放 → 原发送者不受影响 → byID/around历史/search计数与摘要隔离 → 会话预览回退 → 新设备/未知位点重建不复活 → 旧会话同步URL HTTP404")
		} else if scenario.name == "broadcasts" {
			fmt.Println("E2E PASS: broadcasts 并发首播唯一系统会话 → user/group/all范围 → 跨实例普通message.new → 会话复用/seq → 离线同步/重建/history → 非成员拒读")
		} else if scenario.name == "bot-runtime" {
			fmt.Println("E2E PASS: bot-runtime CRUD/配置 → 网络 MCP 发现/调用 → token 验证 → Kafka Bot 精确回复 → 同实例 WS/REST读取 → 删除")
		} else if scenario.name == "knowledge-ingest" {
			fmt.Printf("E2E PASS: knowledge-ingest multipart上传 → 异步ready → 精确内容检索 → 大文档并发入库时每次查询 <=%s → 失败入库不伤查询\n", d.queryDeadline)
		} else if scenario.name == "conversations" || scenario.name == "conversation-unread" {
			fmt.Printf("E2E PASS: %s 建群 → 邀请/列成员 → 权限 → 群聊 WS → 精确 ID 读取/补拉 → 会话列表 → 已读回执 → 移除后拒绝读取\n", scenario.name)
			if scenario.name == "conversation-unread" {
				fmt.Println("E2E PASS: conversation-unread 列表未读数 → mark read 归零 → 已读位点不能回退")
			}
		} else if scenario.name == "stage-p6" {
			fmt.Println("E2E PASS: stage-p6 定向跨实例/多端/权限/群消息变更/已读未读/Bot流式 → 连接登记心跳与重连 → SIGKILL/TTL/用户位点补拉/恢复 → readiness503/平滑drain/恢复")
		} else {
			fmt.Printf("E2E PASS: %s 注册 → 登录 → 身份 → 私聊 → 双向 WS 投递\n", scenario.name)
		}
	}
	return errors.Join(failures...)
}

func endpoint(address string, schemes ...string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("地址必须有 host，不得包含凭据、query 或 fragment")
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			return u, nil
		}
	}
	return nil, errors.New("地址协议不受支持")
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// apiError marks a gateway envelope rejection (non-2xx or business code != 0) as
// opposed to a transport or contract-JSON failure. The friend handlers report every
// RPC error as the gateway's internal code, so a business rejection and a service-side
// RPC failure look identical here; negative steps therefore only claim the operation
// did not succeed, and reachability comes from the positive call on the same RPC later
// in the scenario.
type apiError struct {
	method string
	path   string
	status int
	code   int
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d，业务 code=%d", e.method, e.path, e.status, e.code)
}

func (d *driver) request(method, path, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return errors.New("无法编码请求 JSON")
		}
		body = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, d.gateway+"/api/v1"+path, body)
	if err != nil {
		return errors.New("无法创建 HTTP 请求")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %s", method, path, transportFailure(err))
	}
	defer resp.Body.Close()
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("%s %s: HTTP %d，响应不是合法契约 JSON", method, path, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code == nil || *envelope.Code != 0 {
		if envelope.Code == nil {
			return fmt.Errorf("%s %s: HTTP %d，业务 code=missing", method, path, resp.StatusCode)
		}
		// Never print response bodies: an auth error may echo credentials.
		return &apiError{method: method, path: path, status: resp.StatusCode, code: *envelope.Code}
	}
	if output != nil {
		if err := json.Unmarshal(envelope.Data, output); err != nil {
			return fmt.Errorf("%s %s: data 与公开契约不匹配（UUID身份与安全整数序号）", method, path)
		}
	}
	return nil
}

func randomSuffix() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("随机账号生成失败")
	}
	return hex.EncodeToString(raw[:]), nil
}

func (d *driver) register(label, suffix string) (account, error) {
	a := account{username: "e2e_" + suffix + "_" + label, device: "e2e_" + suffix + "_" + label}
	passwordSuffix, err := randomSuffix()
	if err != nil {
		return a, err
	}
	password := "E2e!" + passwordSuffix
	a.password = password
	var registered authResult
	if err := d.request(http.MethodPost, "/auth/register", "", map[string]string{
		"username": a.username, "password": password, "device_id": a.device, "platform": "web",
	}, &registered); err != nil {
		return a, fmt.Errorf("gateway.registration: %w", err)
	}
	if registered.UserID == "" || registered.User.ID != registered.UserID || registered.User.Username != a.username {
		return a, errors.New("gateway.registration: 注册返回的用户身份不匹配")
	}
	var loggedIn authResult
	if err := d.request(http.MethodPost, "/auth/login", "", map[string]string{
		"account": a.username, "password": password, "device_id": a.device, "platform": "web",
	}, &loggedIn); err != nil {
		return a, fmt.Errorf("gateway.login: %w", err)
	}
	if loggedIn.UserID != registered.UserID || loggedIn.User.ID != registered.UserID || loggedIn.User.Username != a.username || loggedIn.Tokens.AccessToken == "" {
		return a, errors.New("gateway.login: 登录用户身份或 access_token 无效")
	}
	a.id, a.token = loggedIn.UserID, loggedIn.Tokens.AccessToken
	var current identity
	if err := d.request(http.MethodGet, "/users/me", a.token, nil, &current); err != nil {
		return a, fmt.Errorf("gateway.identity: %w", err)
	}
	if current.ID != a.id || current.Username != a.username {
		return a, errors.New("gateway.identity: 当前用户与注册、登录身份不一致")
	}
	return a, nil
}

func (d *driver) scenario(addressA, addressB string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	a, err := d.register("a", suffix)
	if err != nil {
		return err
	}
	b, err := d.register("b", suffix)
	if err != nil {
		return err
	}
	if a.id == b.id {
		return errors.New("gateway.identity: 不同账号返回相同 user_id")
	}
	// Private conversations and sends require membership, not friendship.
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", a.token, map[string]any{
		"type": "single", "peer_user_id": b.id.String(),
	}, &conv); err != nil {
		return fmt.Errorf("messaging.conversation: %w", err)
	}
	if conv.ID == "" {
		return errors.New("messaging.conversation: 没有有效 conversation_id")
	}
	connA, err := d.connect(addressA, a)
	if err != nil {
		return fmt.Errorf("realtime.connect 用户 A: %w", err)
	}
	defer connA.Close()
	connB, err := d.connect(addressB, b)
	if err != nil {
		return fmt.Errorf("realtime.connect 用户 B: %w", err)
	}
	defer connB.Close()
	// An application-level pong is emitted only after registration and OnConnect,
	// so a successful HTTP upgrade alone cannot race message delivery.
	for i, conn := range []*websocket.Conn{connA, connB} {
		if err := d.ready(conn); err != nil {
			return fmt.Errorf("realtime.ready 用户 %d: %w", i+1, err)
		}
	}
	forward, forwardErr := d.sendAndReceive(a, connB, conv.ID, suffix+"_a_to_b", 0)
	_, backwardErr := d.sendAndReceive(b, connA, conv.ID, suffix+"_b_to_a", forward)
	return errors.Join(forwardErr, backwardErr)
}

func (d *driver) connect(address string, a account) (*websocket.Conn, error) {
	u, err := endpoint(address, "ws", "wss")
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("token", a.token)
	query.Set("device_id", a.device)
	query.Set("platform", "web")
	u.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	dialer := websocket.Dialer{HandshakeTimeout: d.timeout}
	// Browsers always send Origin on a WebSocket handshake, and the web client's
	// origin (vite dev server) differs from the realtime host. Dial the way the
	// real client does so a same-origin-only server is rejected here, not in prod.
	headers := http.Header{"Origin": []string{"http://localhost:3000"}}
	conn, response, err := dialer.DialContext(ctx, u.String(), headers)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("WebSocket 握手 HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("WebSocket 握手: %s", transportFailure(err))
	}
	conn.SetReadLimit(1 << 20)
	// Control writes may run concurrently with application writes. All scenarios
	// must maintain the connection lease, including the older P3/P4 clients.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(d.timeout)); err != nil {
				return
			}
		}
	}()
	return conn, nil
}

func (d *driver) ready(conn *websocket.Conn) error {
	if err := conn.SetWriteDeadline(time.Now().Add(d.timeout)); err != nil {
		return errors.New("无法设置 ping 写超时")
	}
	if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		return fmt.Errorf("应用 ping 发送失败: %s", transportFailure(err))
	}
	if err := conn.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return errors.New("无法设置 pong 读超时")
	}
	for {
		evt, err := readEvent(conn)
		if err != nil {
			return fmt.Errorf("等待应用 pong: %w", err)
		}
		if evt.Type == "pong" {
			return nil
		}
		if evt.Type == "error" {
			return errors.New("ping 后 realtime 返回 error 事件")
		}
	}
}

func readEvent(conn *websocket.Conn) (event, error) {
	var evt event
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return evt, fmt.Errorf("WS 读取失败: %s", transportFailure(err))
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return evt, errors.New("WS 事件与公开 JSON 契约不匹配（UUID身份与安全整数序号）")
	}
	return evt, nil
}

func (d *driver) sendMessage(sender account, convID entityID, text string, after sequenceNumber) (sentMessage, error) {
	submissionKey, err := uuid.NewRandom()
	if err != nil {
		return sentMessage{}, fmt.Errorf("messaging.submission-key: %w", err)
	}
	return d.sendMessageWithKey(sender, convID, text, after, submissionKey.String())
}

// 重试沿用第一次返回的 SubmissionKey，不生成另一个发送身份。
func (d *driver) sendMessageWithKey(sender account, convID entityID, text string, after sequenceNumber, submissionKey string) (sentMessage, error) {
	sent := sentMessage{SubmissionKey: submissionKey}
	if err := entityidentity.ValidateSubmissionKey(submissionKey); err != nil {
		return sent, fmt.Errorf("messaging.submission-key: %w", err)
	}
	if err := d.request(http.MethodPost, "/messages/send", sender.token, map[string]any{
		"conversation_id": convID.String(), "client_msg_id": submissionKey,
		"content": map[string]string{"text": text},
	}, &sent); err != nil {
		return sent, fmt.Errorf("messaging.send 发送方 %s: %w", sender.id, err)
	}
	if sent.MessageID == "" || sent.Seq <= after || sent.ConvID != convID || sent.SenderID != sender.id || sent.Content.Text != text {
		return sent, errors.New("messaging.send: HTTP 确认的 message_id/conv_id/seq/发送方/内容不符合发送请求")
	}
	return sent, nil
}

func (d *driver) sendAndReceive(sender account, receiver *websocket.Conn, convID entityID, text string, after sequenceNumber) (sequenceNumber, error) {
	sent, err := d.sendMessage(sender, convID, text, after)
	if err != nil {
		return sent.Seq, err
	}
	return sent.Seq, d.receiveMessage(receiver, sent)
}

func (d *driver) receiveMessage(receiver *websocket.Conn, sent sentMessage) error {
	if err := receiver.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return errors.New("realtime.delivery: 无法设置消息读取超时")
	}
	for {
		evt, err := readEvent(receiver)
		if err != nil {
			return fmt.Errorf("realtime.delivery: HTTP 已确认 conv_id=%s message_id=%s seq=%s，但对方 WS 未收到匹配消息: %w", sent.ConvID, sent.MessageID, sent.Seq, err)
		}
		if evt.Type == "error" {
			return errors.New("realtime.delivery: realtime 返回 error 事件")
		}
		if evt.Type != "message.new" {
			continue
		}
		msg := evt.Message
		// Group membership changes can emit unrelated system messages while the
		// confirmed text is in flight. Match by exact ID, then check its contract.
		if msg.MessageID != sent.MessageID {
			continue
		}
		if !hasEntityID(evt.ConvID, sent.ConvID) || msg.ConvID != sent.ConvID || msg.MessageID != sent.MessageID || msg.Seq != sent.Seq || !hasEntityID(msg.SenderID, sent.SenderID) || msg.Content.Text != sent.Content.Text {
			return fmt.Errorf("realtime.contract: message.new 与 HTTP 确认不一致，期望 conv_id=%s message_id=%s seq=%s sender_id=%s", sent.ConvID, sent.MessageID, sent.Seq, sent.SenderID)
		}
		return nil
	}
}

// Error strings from HTTP or WS dialers may contain a URL with a token. Only
// classify them; never print their text, WS close reasons, or server bodies.
func transportFailure(err error) string {
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "连接/读取超时"
	}
	var closed *websocket.CloseError
	if errors.As(err, &closed) {
		return fmt.Sprintf("连接已关闭（WS code=%d）", closed.Code)
	}
	return "网络 I/O 失败"
}

func probe(args []string) error {
	flags := options("probe")
	kind := flags.String("kind", "", "http 或 grpc")
	address := flags.String("address", "", "HTTP URL 或 host:port")
	timeout := flags.Duration("timeout", 3*time.Second, "健康检查超时时间")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 || *address == "" {
		return errors.New("readiness: 必须指定 address，timeout 必须大于零，不接受位置参数")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	switch *kind {
	case "http":
		location := *address
		if !strings.Contains(location, "://") {
			location = "http://" + location + "/health"
		}
		u, err := endpoint(location, "http", "https")
		if err != nil {
			return fmt.Errorf("readiness.http: %w", err)
		}
		if u.Path == "" {
			u.Path = "/health"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return errors.New("readiness.http: 请求地址无效")
		}
		client := newHTTPClient(*timeout)
		defer client.CloseIdleConnections()
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("readiness.http: %s", transportFailure(err))
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("readiness.http: HTTP %d，期望 200", resp.StatusCode)
		}
	case "grpc":
		if _, _, err := net.SplitHostPort(*address); err != nil {
			return errors.New("readiness.grpc: address 必须是 host:port")
		}
		conn, err := grpc.NewClient(*address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return errors.New("readiness.grpc: 无法创建标准 health 客户端")
		}
		defer conn.Close()
		resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
		if err != nil {
			return fmt.Errorf("readiness.grpc: 标准 health Check 失败（RPC code=%s）", status.Code(err))
		}
		if resp.Status != healthpb.HealthCheckResponse_SERVING {
			return fmt.Errorf("readiness.grpc: 标准 health 状态 %s，不是 SERVING", resp.Status)
		}
	default:
		return errors.New("readiness: kind 必须是 http 或 grpc")
	}
	return nil
}
