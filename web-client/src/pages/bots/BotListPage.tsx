import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Modal, Form, Input, Select, Switch, InputNumber, Tag, Upload, message, Button } from 'antd';
import { botApi } from '@/services/bot';
import { modelApi } from '@/services/model';
import { modelOptionLabel } from '@/utils/provider';
import { UploadOutlined } from '@ant-design/icons';
import { Avatar } from '@/components/common/Avatar';
import type { CreateBotReq } from '@/types/api';

import './BotPage.css';

const BOT_TYPE_OPTIONS = [
  { key: 'official', label: '官方 Bot', desc: '使用平台提供的官方 Bot 模板' },
  { key: 'self_deployed', label: '自部署 Bot', desc: '自定义模型和提示词的自部署 Bot' },
  { key: 'third_party', label: '第三方 Bot', desc: '通过 WebSocket 或 Webhook 接入的外部 Bot' },
];

export function BotListPage() {
  const navigate = useNavigate();
  const [createOpen, setCreateOpen] = useState(false);
  const [selectedType, setSelectedType] = useState<string | null>(null);
  const [step, setStep] = useState<'type' | 'form'>('type');
  const [form] = Form.useForm();
  const [creating, setCreating] = useState(false);
  const [guideOpen, setGuideOpen] = useState(false);
  const [avatarFile, setAvatarFile] = useState<File | null>(null);
  const [avatarPreview, setAvatarPreview] = useState<string>('');

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['bots'],
    queryFn: () => botApi.list(),
  });

  const { data: modelsData } = useQuery({
    queryKey: ['models'],
    queryFn: () => modelApi.list(),
  });

  const models = modelsData?.list ?? [];
  const modelOptions = models
    .filter((m: any) => m.capability === 'chat')
    .map((m: any) => ({ value: m.id, label: modelOptionLabel(m), model_name: m.model_name, owner_id: m.owner_id }));

  const modelMap = Object.fromEntries((models as any[])
    .filter((m: any) => m.capability === 'chat')
    .map((m: any) => [m.id, m.model_name]));

  const bots = data?.list ?? [];

  const subTypeValue = Form.useWatch('sub_type', form);
  const triggersValue = Form.useWatch('response_triggers', form);

  const handleSelectType = (type: string) => {
    setSelectedType(type);
    form.setFieldsValue({ type });
    setStep('form');
  };

  const handleCreate = async () => {
    try {
      const vals = await form.validateFields();
      setCreating(true);

      const basePayload: Record<string, any> = {
        name: vals.name,
        type: selectedType,
      };

      // Build capabilities from switches
      const caps: { builtin_tools?: string[] } = {};
      if (vals.enable_web_search) {
        caps.builtin_tools = ['web_search'];
      }
      if (Object.keys(caps).length > 0) {
        basePayload.capabilities = JSON.stringify(caps);
      }

      // Build settings from extra config
      const settings: Record<string, any> = {};
      const triggers: string[] = [];
      if (vals.response_triggers) {
        for (const t of vals.response_triggers) {
          if (t === 'keyword' && vals.keywords) {
            const kwList = vals.keywords.split(/[,，]/).map((s: string) => s.trim()).filter(Boolean);
            kwList.forEach((kw: string) => triggers.push(`keyword:${kw}`));
          } else {
            triggers.push(t);
          }
        }
      }
      if (triggers.length > 0) {
        settings.response_triggers = triggers;
      }
      if (Object.keys(settings).length > 0) {
        basePayload.settings = JSON.stringify(settings);
      }

      const selectedModelName = vals.model_id ? (modelMap[vals.model_id] || '') : (vals.model_name || '');
      const selectedMemoryModelName = vals.memory_model_id ? (modelMap[vals.memory_model_id] || '') : '';
      if (selectedType === 'official') {
        Object.assign(basePayload, {
          template_id: vals.template_id,
          model_name: selectedModelName,
          model_id: vals.model_id,
          use_platform_model: true,
          max_context_messages: vals.max_context_messages ?? 15,
          streaming_enabled: vals.streaming_enabled ?? true,
        });
      } else if (selectedType === 'self_deployed') {
        Object.assign(basePayload, {
          model_name: selectedModelName,
          model_id: vals.model_id,
          system_prompt: vals.system_prompt || '',
          enable_knowledge: vals.enable_knowledge ?? true,
          temperature: vals.temperature ?? 0.7,
          max_context_messages: vals.max_context_messages ?? 15,
          streaming_enabled: vals.streaming_enabled ?? true,
          memory_model_id: vals.memory_model_id,
          memory_model_name: selectedMemoryModelName,
        });
      } else if (selectedType === 'third_party') {
        Object.assign(basePayload, {
          sub_type: vals.sub_type || 'webhook',
          use_platform_model: false,
        });
        if (vals.sub_type === 'webhook') {
          basePayload.callback_url = vals.callback_url || '';
        }
      }

      const bot = await botApi.create(basePayload as CreateBotReq);
      if (!bot) throw new Error('创建返回为空');

      // Upload avatar if a file was selected
      if (avatarFile && bot?.id) {
        try {
          await botApi.uploadAvatar(bot.id, avatarFile);
        } catch {
          message.warning('Bot 已创建，但头像上传失败');
        }
      }

      message.success('Bot 创建成功');
      setCreateOpen(false);
      setStep('type');
      setSelectedType(null);
      setAvatarFile(null);
      setAvatarPreview('');
      form.resetFields();
      refetch();
      navigate(`/bots/${bot.id}`);
    } catch (err: any) {
      if (err.errorFields) return;
      message.error(err?.response?.data?.message || '创建失败');
    } finally {
      setCreating(false);
    }
  };

  const handleStartChat = async (bot: any) => {
    try {
      if (!bot.id) {
        message.error('Bot 不可用于聊天');
        return;
      }
      const conv = await botApi.createConversation(bot.id);
      navigate(`/conversations/${conv.id}`);
    } catch (err: any) {
      message.error(err?.response?.data?.message || '创建会话失败');
    }
  };

  const handleCancel = () => {
    setCreateOpen(false);
    setStep('type');
    setSelectedType(null);
    setAvatarFile(null);
    setAvatarPreview('');
    form.resetFields();
  };

  const typeLabel = selectedType
    ? BOT_TYPE_OPTIONS.find((o) => o.key === selectedType)?.label || selectedType
    : '';

  return (
    <div className="bot-page">
      <div className="bot-panel">
        <div className="bot-header">
          <h2>Bot</h2>
          <button className="bot-create-btn" onClick={() => setCreateOpen(true)}>+ 创建 Bot</button>
        </div>

        <div className="bot-list">
          {isLoading && <div className="bot-empty">加载中...</div>}

          {!isLoading && bots.length === 0 && (
            <div className="bot-empty">
              <p>暂无 Bot</p>
              <p style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', marginTop: 4 }}>创建 AI Bot 来增强你的聊天体验</p>
            </div>
          )}

          {bots.map((bot: any) => (
            <div
              key={bot.id}
              className="bot-card"
              onClick={() => navigate(`/bots/${bot.id}`)}
            >
              <div className="bot-card-header">
                <Avatar name={bot.name} src={bot.avatar} size={44} />
                <div className="bot-card-info">
                  <div className="bot-card-name">{bot.name}</div>
                  <div className="bot-card-model">{bot.model_name || '默认模型'}</div>
                </div>
                <button className="bot-chat-btn" onClick={(e) => {
                  e.stopPropagation();
                  handleStartChat(bot);
                }}>发消息</button>
                <div className={`bot-card-status ${bot.status === 'active' ? 'active' : ''}`}>
                  {bot.status === 'active' ? '运行中' : '已停用'}
                </div>
              </div>
              {bot.persona && (
                <div className="bot-card-desc">{bot.persona}</div>
              )}
              <div className="bot-card-tags">
                <span className="bot-tag">{bot.type}</span>
                {bot.use_platform_model && <span className="bot-tag">平台模型</span>}
                {bot.enable_knowledge && <span className="bot-tag">知识库</span>}
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Create Bot Modal */}
      <Modal
        title={step === 'type' ? '选择 Bot 类型' : `创建 ${typeLabel}`}
        open={createOpen}
        onOk={step === 'form' ? handleCreate : undefined}
        onCancel={handleCancel}
        confirmLoading={creating}
        okText="创建"
        cancelText={step === 'type' ? '取消' : '返回'}
        okButtonProps={{ style: step === 'type' ? { display: 'none' } : undefined }}
      >
        {step === 'type' ? (
          <div className="bot-type-grid">
            {BOT_TYPE_OPTIONS.map((opt) => (
              <div key={opt.key} className="bot-type-card" onClick={() => handleSelectType(opt.key)}>
                <div className="bot-type-card-title">{opt.label}</div>
                <div className="bot-type-card-desc">{opt.desc}</div>
              </div>
            ))}
          </div>
        ) : (
          <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
            <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
              <Input placeholder="Bot 名称" />
            </Form.Item>
            <Form.Item label="头像">
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <Avatar name={form.getFieldValue('name') || '?'} src={avatarPreview} size={56} />
                <Upload
                  showUploadList={false}
                  beforeUpload={(file) => {
                    const reader = new FileReader();
                    reader.onload = (e) => setAvatarPreview(e.target?.result as string);
                    reader.readAsDataURL(file);
                    setAvatarFile(file);
                    return false;
                  }}
                >
                  <Button icon={<UploadOutlined />}>选择头像</Button>
                </Upload>
              </div>
            </Form.Item>

            {selectedType === 'official' && (
              <>
                <Form.Item name="template_id" label="模板" rules={[{ required: true, message: '请选择模板' }]}>
                  <Select placeholder="选择模板">
                    <Select.Option value="qa">普通问答</Select.Option>
                    <Select.Option value="knowledge">知识库回答</Select.Option>
                  </Select>
                </Form.Item>
                <Form.Item name="model_id" label="模型" rules={[{ required: true, message: '请选择模型' }]}>
                  <Select
                    placeholder="选择模型"
                    allowClear
                    options={modelOptions}
                    showSearch
                    filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}

                  />
                </Form.Item>
                <Form.Item name="streaming_enabled" label="启用流式" valuePropName="checked" initialValue={true}>
                  <Switch />
                </Form.Item>
                <Form.Item name="enable_web_search" label="启用网络搜索" valuePropName="checked" initialValue={false}>
                  <Switch />
                </Form.Item>
                <Form.Item name="response_triggers" label="触发回复方式" initialValue={['mention']}>
                  <Select mode="multiple" placeholder="选择触发方式" maxCount={3}>
                    <Select.Option value="mention">@提及时回复</Select.Option>
                    <Select.Option value="keyword">关键词匹配回复</Select.Option>
                    <Select.Option value="all">自动回复全部消息</Select.Option>
                  </Select>
                </Form.Item>
                {triggersValue?.includes('keyword') && (
                  <Form.Item name="keywords" label="关键词列表" extra="多个关键词用逗号分隔">
                    <Input.TextArea rows={2} placeholder="关键词1, 关键词2, ..." />
                  </Form.Item>
                )}
              </>
            )}

            {selectedType === 'self_deployed' && (
              <>
                <Form.Item name="model_id" label="模型" rules={[{ required: true, message: '请选择模型' }]}>
                  <Select
                    placeholder="选择模型"
                    allowClear
                    options={modelOptions}
                    showSearch
                    filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}

                  />
                </Form.Item>
                <Form.Item name="system_prompt" label="系统提示词">
                  <Input.TextArea rows={3} placeholder="Bot 的角色设定..." />
                </Form.Item>
                <Form.Item name="temperature" label="温度" initialValue={0.7}>
                  <InputNumber min={0} max={2} step={0.1} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item name="max_context_messages" label="最大上下文消息数" initialValue={15}>
                  <InputNumber min={1} max={100} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item name="streaming_enabled" label="启用流式" valuePropName="checked" initialValue={true}>
                  <Switch />
                </Form.Item>
                <Form.Item name="enable_web_search" label="启用网络搜索" valuePropName="checked" initialValue={false}>
                  <Switch />
                </Form.Item>
                <Form.Item name="enable_knowledge" label="启用知识库" valuePropName="checked" initialValue={true}>
                  <Switch />
                </Form.Item>
                <Form.Item name="response_triggers" label="触发回复方式" initialValue={['mention']}>
                  <Select mode="multiple" placeholder="选择触发方式" maxCount={3}>
                    <Select.Option value="mention">@提及时回复</Select.Option>
                    <Select.Option value="keyword">关键词匹配回复</Select.Option>
                    <Select.Option value="all">自动回复全部消息</Select.Option>
                  </Select>
                </Form.Item>
                {triggersValue?.includes('keyword') && (
                  <Form.Item name="keywords" label="关键词列表" extra="多个关键词用逗号分隔">
                    <Input.TextArea rows={2} placeholder="关键词1, 关键词2, ..." />
                  </Form.Item>
                )}
                <Form.Item name="memory_model_id" label="记忆模型">
                  <Select
                    placeholder="选择记忆模型"
                    allowClear
                    options={modelOptions}
                    showSearch
                    filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}

                  />
                </Form.Item>
              </>
            )}

            {selectedType === 'third_party' && (
              <>
                <div style={{ marginBottom: 12 }}>
                  <a
                    style={{ fontSize: 13, cursor: 'pointer', userSelect: 'none', color: 'var(--aim-text-secondary)' }}
                    onClick={() => setGuideOpen(!guideOpen)}
                  >
                    {guideOpen ? '▼' : '▶'} 接入指南 — 消息格式、签名验证、凭证说明
                  </a>

                  {guideOpen && (
                    <div className="bot-guide">
                      <div className="bot-guide-section">
                        <p className="bot-guide-desc">
                          第三方 Bot 是在<strong>你自己的服务器</strong>上运行的服务程序。AIM 只负责将用户消息转发给你的服务，消息的处理和响应完全由你的服务决定。
                        </p>
                      </div>

                      {/* WebSocket */}
                      <div className="bot-guide-mode">
                        <div className="bot-guide-mode-title">🔌 WebSocket 模式 — 实时双向通信</div>
                        <p className="bot-guide-mode-desc">你的服务以 WebSocket 客户端身份持久连接 AIM。</p>
                        <div className="bot-guide-steps">
                          <div className="bot-guide-step"><span className="bot-guide-step-num">1</span><span>调用 <code>POST /api/v1/bots/&#123;id&#125;/token</code> 获取 JWT Token</span></div>
                          <div className="bot-guide-step"><span className="bot-guide-step-num">2</span><span>连接 <code>ws://host:8081/ws/bot?token=&lt;jwt&gt;&device_id=&lt;id&gt;</code></span></div>
                          <div className="bot-guide-step"><span className="bot-guide-step-num">3</span><span>接收推送事件 → 处理 → 通过 WebSocket 发送 <code>message.send</code> 回复消息</span></div>
                        </div>
                        <div className="bot-guide-note">WS 心跳：每 30s 发送 <code>&#123;"type":"ping"&#125;</code>，收到 <code>&#123;"type":"pong"&#125;</code>。Token 过期后需重新获取。</div>
                      </div>

                      {/* Webhook */}
                      <div className="bot-guide-mode">
                        <div className="bot-guide-mode-title">🔗 Webhook 模式 — 异步回调</div>
                        <p className="bot-guide-mode-desc">你的服务提供一个 HTTP 端点，AIM 通过 POST 请求推送事件。</p>
                        <div className="bot-guide-steps">
                          <div className="bot-guide-step"><span className="bot-guide-step-num">1</span><span>在下方填写你的回调地址（<code>callback_url</code>），AIM 会 POST 事件到此地址</span></div>
                          <div className="bot-guide-step"><span className="bot-guide-step-num">2</span><span>用 <code>webhook_secret</code> 验证 Headers: <code>X-AIM-Signature</code>（HMAC-SHA256）、<code>X-AIM-Timestamp</code>（Unix秒）</span></div>
                          <div className="bot-guide-step"><span className="bot-guide-step-num">3</span><span>处理事件 → 通过 AIM Webhook 接口回复消息（同样需要 HMAC 签名）</span></div>
                        </div>
                        <div className="bot-guide-code">
                          <div className="bot-guide-code-title">验签示例 (Go)</div>
                          <pre>{`func verify(msg []byte, sig string, ts int64, secret string) bool {
  mac := hmac.New(sha256.New, []byte(secret))
  mac.Write(msg)
  exp := hex.EncodeToString(mac.Sum(nil))
  return hmac.Equal([]byte(exp), []byte(sig))
}`}</pre>
                        </div>
                      </div>

                      {/* AIM → Bot 消息格式（两种模式一致） */}
                      <div className="bot-guide-mode">
                        <div className="bot-guide-mode-title">📥 AIM 推送给你的事件格式</div>
                        <p className="bot-guide-mode-desc">无论 WebSocket 还是 Webhook，你收到的事件格式相同。所有 ID 字段已转为 string 以避免 JS 精度丢失。</p>
                        <div className="bot-guide-code">
                          <pre>{`{
  "type": "message.created",
  "conv_id": "123456789",
  "bot_id": "987654321",
  "event": { "ts": 1717370000, "version": "1.0" },
  "message": {
    "message_id": "...",
    "msg_type": 1,
    "content": { "text": "你好" },
    "seq": "42",
    "reply_to_msg_id": "...",
    "created_at": "1717370000000"
  }
}`}</pre>
                        </div>
                        <div className="bot-guide-note">msg_type: 1=文本 2=图片 3=文件 4=视频 5=语音 6=位置 9=Bot消息</div>
                      </div>

                      {/* Bot → AIM: WS 模式 */}
                      <div className="bot-guide-mode">
                        <div className="bot-guide-mode-title">📤 你回复 AIM 的请求格式（WebSocket 模式）</div>
                        <p className="bot-guide-mode-desc">通过已建立的 WebSocket 连接直接发送，无需额外认证。</p>
                        <div className="bot-guide-code">
                          <div className="bot-guide-code-title">WS 发送</div>
                          <pre>{`{
  "type": "message.send",
  "conv_id": "123456789",
  "text": "你好，我收到了你的消息！",
  "reply_to_id": "..."
}`}</pre>
                        </div>
                        <div className="bot-guide-code" style={{ marginTop: 8 }}>
                          <div className="bot-guide-code-title">AIM 回复</div>
                          <pre>{`{"type":"message.sent","message_id":"789","seq":10,"created_at":1717500000}`}</pre>
                        </div>
                      </div>

                      {/* Bot → AIM: Webhook 模式 */}
                      <div className="bot-guide-mode">
                        <div className="bot-guide-mode-title">📤 你回复 AIM 的请求格式（Webhook 模式）</div>
                        <p className="bot-guide-mode-desc">向 AIM 发送 <code>POST /api/v1/webhook</code>，带上 HMAC 签名。Bot 身份由签名中的 webhook_secret 自动解析。</p>
                        <div className="bot-guide-code">
                          <div className="bot-guide-code-title">Request Headers</div>
                          <pre>{`X-AIM-Signature: <hex(HMAC-SHA256(body, webhook_secret))>
X-AIM-Timestamp: <unix_seconds>
Content-Type: application/json`}</pre>
                        </div>
                        <div className="bot-guide-code" style={{ marginTop: 8 }}>
                          <div className="bot-guide-code-title">Request Body</div>
                          <pre>{`{
  "type": "message.send",
  "conversation_id": "123456789",
  "text": "你好，我收到了你的消息！",
  "reply_to_id": "..."
}`}</pre>
                        </div>
                      </div>

                      {/* Credentials table */}
                      <div className="bot-guide-section">
                        <div className="bot-guide-mode-title">🔑 凭证说明</div>
                        <table className="bot-guide-table">
                          <thead><tr><th>凭证</th><th>用途</th><th>适用模式</th><th>获取方式</th></tr></thead>
                          <tbody>
                            <tr><td><code>token</code></td><td>WebSocket 连接认证（JWT）</td><td>WebSocket</td><td>Bot 详情页点击"生成 Token"</td></tr>
                            <tr><td><code>webhook_secret</code></td><td>HMAC-SHA256 签名密钥</td><td>Webhook</td><td>创建时自动生成，详情页可轮换</td></tr>
                            <tr><td><code>app_secret</code></td><td>调用 AIM 开放 API</td><td>通用</td><td>创建时自动生成，详情页可轮换</td></tr>
                          </tbody>
                        </table>
                        <div className="bot-guide-note" style={{ marginTop: 8 }}>
                          测试 Bot 工具：<code>go run tools/test-webhook-bot/main.go -mode webhook -secret &lt;webhook_secret&gt;</code>
                        </div>
                      </div>
                    </div>
                  )}
                </div>

                <Form.Item name="sub_type" label="接入方式" initialValue="webhook" rules={[{ required: true, message: '请选择接入方式' }]}>
                  <Select onChange={() => form.setFieldsValue({ callback_url: undefined })}>
                    <Select.Option value="ws">WebSocket — 你的服务主动连接 AIM</Select.Option>
                    <Select.Option value="webhook">Webhook — AIM 回调你的服务</Select.Option>
                  </Select>
                </Form.Item>

                {subTypeValue === 'webhook' && (
                  <Form.Item name="callback_url" label="回调 URL" rules={[{ required: true, message: '请输入回调 URL' }]}>
                    <Input placeholder="https://your-service.com/webhook" />
                  </Form.Item>
                )}

                {subTypeValue !== 'webhook' && (
                  <Form.Item name="callback_url" label=" " colon={false}>
                    <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', lineHeight: 1.6 }}>
                      创建完成后，在 Bot 详情页可生成连接 Token 和查看 app_secret。
                    </div>
                  </Form.Item>
                )}
              </>
            )}
          </Form>
        )}
      </Modal>
    </div>
  );
}
