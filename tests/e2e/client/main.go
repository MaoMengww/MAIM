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

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// decimal preserves integer IDs and seq exactly whether the public API sends
// a JSON string or a JSON number. No message identifier passes through float64.
type decimal int64

func (d *decimal) UnmarshalJSON(raw []byte) error {
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("标识字段不是合法 JSON 字符串")
		}
	}
	// Heartbeat/presence envelopes explicitly encode an absent conv_id as "".
	// Required IDs are still checked as positive in their consuming paths.
	if value == "" {
		*d = 0
		return nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return errors.New("标识字段不是精确的 int64 十进制整数")
	}
	*d = decimal(n)
	return nil
}

func (d decimal) String() string { return strconv.FormatInt(int64(d), 10) }

type identity struct {
	ID       decimal `json:"id"`
	Username string  `json:"username"`
}

type authResult struct {
	UserID decimal  `json:"user_id"`
	User   identity `json:"user"`
	Tokens struct {
		AccessToken string `json:"access_token"`
	} `json:"tokens"`
}

type account struct {
	id       decimal
	username string
	device   string
	token    string
}

type sentMessage struct {
	MessageID decimal `json:"message_id"`
	ConvID    decimal `json:"conv_id"`
	SenderID  decimal `json:"from_user_id"`
	Seq       decimal `json:"seq"`
	Content   struct {
		Text string `json:"text"`
	} `json:"content"`
}

type event struct {
	Type    string  `json:"type"`
	ConvID  decimal `json:"conv_id"`
	Message struct {
		MessageID decimal `json:"message_id"`
		ConvID    decimal `json:"conv_id"`
		SenderID  decimal `json:"sender_id"`
		Seq       decimal `json:"seq"`
		Content   struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

type driver struct {
	gateway string
	client  *http.Client
	timeout time.Duration
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
	realtimeA := flags.String("realtime-a", "ws://ws-gateway:8081/ws", "realtime A WebSocket 地址")
	realtimeB := flags.String("realtime-b", "ws://realtime-b:8081/ws", "realtime B WebSocket 地址")
	cross := flags.Bool("cross-instance", false, "额外验收两个用户分别连接 A/B 的双向投递；失败返回非零")
	selected := flags.String("scenario", "all", "选择 all|stage-p3|stage-p4|relationships|conversations|conversation-unread|same-instance-a|same-instance-b|cross-instance；conversation-unread 为 issue09 独立验收")
	timeout := flags.Duration("timeout", 20*time.Second, "每次 HTTP/WS 操作的超时时间")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 {
		return errors.New("run 不接受位置参数，timeout 必须大于零")
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
			{"same-instance-a", *realtimeA, *realtimeA},
			{"same-instance-b", *realtimeB, *realtimeB},
		}
		if *cross {
			scenarios = append(scenarios, scenarioSpec{"cross-instance", *realtimeA, *realtimeB})
		}
	case "stage-p3":
		scenarios = []scenarioSpec{{"relationships", "", ""}, {"same-instance-a", *realtimeA, *realtimeA}}
	case "stage-p4":
		scenarios = []scenarioSpec{{"relationships", "", ""}, {"same-instance-a", *realtimeA, *realtimeA}, {"conversation-unread", *realtimeA, *realtimeA}}
	case "conversations", "conversation-unread":
		scenarios = []scenarioSpec{{*selected, *realtimeA, *realtimeA}}
	case "relationships":
		scenarios = []scenarioSpec{{"relationships", "", ""}}
	case "same-instance-a":
		scenarios = []scenarioSpec{{"same-instance-a", *realtimeA, *realtimeA}}
	case "same-instance-b":
		scenarios = []scenarioSpec{{"same-instance-b", *realtimeB, *realtimeB}}
	case "cross-instance":
		scenarios = []scenarioSpec{{"cross-instance", *realtimeA, *realtimeB}}
	default:
		return errors.New("scenario 必须为 all|stage-p3|stage-p4|relationships|conversations|conversation-unread|same-instance-a|same-instance-b|cross-instance")
	}
	for _, scenario := range scenarios {
		if scenario.name == "relationships" {
			continue
		}
		for _, address := range []string{scenario.a, scenario.b} {
			if _, err := endpoint(address, "ws", "wss"); err != nil {
				return fmt.Errorf("配置 realtime 场景 %s: %w", scenario.name, err)
			}
		}
	}
	if (*selected == "all" || *selected == "cross-instance") && *realtimeA == *realtimeB {
		return errors.New("realtime A/B 必须使用不同地址，不能将单实例冒充两实例")
	}
	d := driver{gateway: strings.TrimRight(*gateway, "/"), client: newHTTPClient(*timeout), timeout: *timeout}
	defer d.client.CloseIdleConnections()
	var failures []error
	for _, scenario := range scenarios {
		var err error
		switch scenario.name {
		case "relationships":
			err = d.relationships()
		case "conversations", "conversation-unread":
			err = d.conversations(scenario.a, scenario.name == "conversation-unread")
		default:
			err = d.scenario(scenario.a, scenario.b)
		}
		if err != nil {
			failure := fmt.Errorf("场景 %s: %w", scenario.name, err)
			fmt.Fprintln(os.Stderr, failure)
			failures = append(failures, failure)
		} else if scenario.name == "relationships" {
			fmt.Println("E2E PASS: relationships 请求 → 接受/拒绝/取消 → 双向好友 → 备注/分组 → 删除 → 拉黑/解除")
		} else if scenario.name == "conversations" || scenario.name == "conversation-unread" {
			fmt.Printf("E2E PASS: %s 建群 → 邀请/列成员 → 权限 → 群聊 WS → 精确 ID 读取/补拉 → 会话列表 → 已读回执 → 移除后拒绝读取\n", scenario.name)
			if scenario.name == "conversation-unread" {
				fmt.Println("E2E PASS: conversation-unread 列表未读数 → mark read 归零 → 已读位点不能回退")
			}
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
			return fmt.Errorf("%s %s: data 与公开契约不匹配（保留精确整数）", method, path)
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
	var registered authResult
	if err := d.request(http.MethodPost, "/auth/register", "", map[string]string{
		"username": a.username, "password": password, "device_id": a.device, "platform": "web",
	}, &registered); err != nil {
		return a, fmt.Errorf("gateway.registration: %w", err)
	}
	if registered.UserID <= 0 || registered.User.ID != registered.UserID || registered.User.Username != a.username {
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
		ID decimal `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", a.token, map[string]any{
		"type": "single", "peer_user_id": b.id.String(),
	}, &conv); err != nil {
		return fmt.Errorf("messaging.conversation: %w", err)
	}
	if conv.ID <= 0 {
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
	conn, response, err := dialer.DialContext(ctx, u.String(), nil)
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
		return evt, errors.New("WS 事件与公开 JSON 契约不匹配（保留精确整数）")
	}
	return evt, nil
}

func (d *driver) sendMessage(sender account, convID decimal, text string, after decimal) (sentMessage, error) {
	var sent sentMessage
	if err := d.request(http.MethodPost, "/messages/send", sender.token, map[string]any{
		"conversation_id": convID.String(), "client_msg_id": "e2e_" + text,
		"content": map[string]string{"text": text},
	}, &sent); err != nil {
		return sent, fmt.Errorf("messaging.send 发送方 %s: %w", sender.id, err)
	}
	if sent.MessageID <= 0 || sent.Seq <= after || sent.ConvID != convID || sent.SenderID != sender.id || sent.Content.Text != text {
		return sent, errors.New("messaging.send: HTTP 确认的 message_id/conv_id/seq/发送方/内容不符合发送请求")
	}
	return sent, nil
}

func (d *driver) sendAndReceive(sender account, receiver *websocket.Conn, convID decimal, text string, after decimal) (decimal, error) {
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
		if evt.ConvID != sent.ConvID || msg.ConvID != sent.ConvID || msg.MessageID != sent.MessageID || msg.Seq != sent.Seq || msg.SenderID != sent.SenderID || msg.Content.Text != sent.Content.Text {
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
