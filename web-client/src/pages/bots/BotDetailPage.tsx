import { useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Descriptions, Tag, Button, Space, Input, Switch, InputNumber, Modal, Form, Select, Upload, message, Spin, Divider } from 'antd';
import { EditOutlined, UploadOutlined, BookOutlined, ApiOutlined, PoweroffOutlined, ToolOutlined, DeleteOutlined, ReloadOutlined } from '@ant-design/icons';
import { botApi } from '@/services/bot';
import { mcpApi } from '@/services/mcp';
import { kbApi } from '@/services/knowledge';
import { modelApi } from '@/services/model';
import { Avatar } from '@/components/common/Avatar';
import { modelOptionLabel } from '@/utils/provider';

function botTypeLabel(type: string): string {
  switch (type) {
    case 'official': return '官方 Bot';
    case 'self_deployed': return '自部署 Bot';
    case 'third_party': return '第三方 Bot';
    default: return type;
  }
}

export function BotDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [editOpen, setEditOpen] = useState(false);
  const [form] = Form.useForm();

  const { data: bot, isLoading } = useQuery({
    queryKey: ['bot', id],
    queryFn: () => botApi.get(id as any),
    enabled: !!id,
  });

  const { data: modelsData } = useQuery({
    queryKey: ['models'],
    queryFn: () => modelApi.list(),
  });

  const modelOptions = (modelsData?.list ?? [])
    .filter((m: any) => m.capability === 'chat')
    .map((m: any) => ({ value: m.id, label: modelOptionLabel(m), model_name: m.model_name, owner_id: m.owner_id }));

  const updateMutation = useMutation({
    mutationFn: (vals: any) => botApi.update(id as any, vals),
    onSuccess: () => {
      message.success('Bot 已更新');
      setEditOpen(false);
      queryClient.invalidateQueries({ queryKey: ['bot', id] });
      queryClient.invalidateQueries({ queryKey: ['bots'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '更新失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: () => botApi.delete(id as any),
    onSuccess: () => {
      message.success('Bot 已删除');
      queryClient.setQueryData(['bots'], (old: any) => {
        if (!old?.list) return old;
        return { ...old, list: old.list.filter((b: any) => b.id !== Number(id)) };
      });
      queryClient.invalidateQueries({ queryKey: ['bots'] });
      navigate('/bots');
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '删除失败'),
  });

  const handleDelete = () => {
    Modal.confirm({
      title: '确认删除',
      content: `确定要删除 Bot「${bot?.name}」吗？此操作不可恢复。`,
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: () => deleteMutation.mutate(),
    });
  };

  const handleEdit = () => {
    if (!bot) return;
    const caps = bot.capabilities ? JSON.parse(bot.capabilities) : {};
    const settings = bot.settings ? JSON.parse(bot.settings) : {};
    // Parse response_triggers from bot-level field: separate keyword:xxx entries
    const rawTriggers: string[] = bot.response_triggers || [];
    const triggers: string[] = [];
    const keywords: string[] = [];
    for (const t of rawTriggers) {
      if (t.startsWith('keyword:')) {
        keywords.push(t.slice(8));
        if (!triggers.includes('keyword')) triggers.push('keyword');
      } else {
        triggers.push(t);
      }
    }
    form.setFieldsValue({
      ...bot,
      enable_web_search: caps.builtin_tools?.includes('web_search') ?? false,
      response_triggers: triggers.length > 0 ? triggers : ['mention'],
      keywords: keywords.join(', '),
    });
    setEditOpen(true);
  };

  const handleStartChat = async () => {
    if (!bot) return;
    try {
      const conv = await botApi.createConversation(bot.id);
      navigate(`/conversations/${conv.id}`);
    } catch (err: any) {
      message.error(err?.response?.data?.message || '创建会话失败');
    }
  };

  // ─── Knowledge Base Bindings ───
  const [kbBindOpen, setKbBindOpen] = useState(false);

  const { data: boundKbs, isLoading: boundKbsLoading } = useQuery({
    queryKey: ['bot-kb-bindings', id],
    queryFn: () => kbApi.listBotBindings(id as any),
    enabled: !!id,
  });

  const { data: allKbsData } = useQuery({
    queryKey: ['kb-list'],
    queryFn: () => kbApi.list(),
    enabled: kbBindOpen,
  });

  const allKbs = allKbsData?.list ?? [];
  const [selectedKbId, setSelectedKbId] = useState<number | undefined>();
  const boundKbIds = new Set((boundKbs ?? []).map((b: any) => b.kb_id));
  const availableKbs = allKbs.filter((kb: any) => !boundKbIds.has(kb.id));

  const bindKbMutation = useMutation({
    mutationFn: (kbId: number) => kbApi.bindToBot(id as any, kbId),
    onSuccess: () => {
      message.success('绑定成功');
      queryClient.invalidateQueries({ queryKey: ['bot-kb-bindings', id] });
      setSelectedKbId(undefined);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '绑定失败'),
  });

  const unbindKbMutation = useMutation({
    mutationFn: (kbId: number) => kbApi.unbindFromBot(id as any, kbId),
    onSuccess: () => {
      message.success('已解绑');
      queryClient.invalidateQueries({ queryKey: ['bot-kb-bindings', id] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '解绑失败'),
  });

  if (isLoading) {
    return <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}><Spin /></div>;
  }

  if (!bot) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', gap: 12 }}>
        <p style={{ color: 'var(--aim-text-tertiary)' }}>Bot 不存在</p>
        <Button onClick={() => navigate('/bots')}>返回列表</Button>
      </div>
    );
  }

  return (
    <div style={{ padding: 32, width: '100%', overflowY: 'auto' }}>
      <Space style={{ marginBottom: 24 }}>
        <Button onClick={() => navigate('/bots')}>← 返回</Button>
      </Space>

      <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 32 }}>
        <Avatar name={bot.name} src={bot.avatar} size={56} />
        <div style={{ flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <h2 style={{ color: 'var(--aim-text)', margin: 0, fontSize: 22 }}>{bot.name}</h2>
            <Tag color={bot.status === 'active' ? 'green' : 'default'}>
              {bot.status === 'active' ? '运行中' : '已停用'}
            </Tag>
          </div>
          {bot.persona && <p style={{ color: 'var(--aim-text-secondary)', margin: '4px 0 0', fontSize: 13 }}>{bot.persona}</p>}
        </div>
        <Button icon={<EditOutlined />} onClick={handleEdit}>编辑</Button>
        <Button danger loading={deleteMutation.isPending} onClick={handleDelete}>删除</Button>
        <Button type="primary" onClick={handleStartChat}>发消息</Button>
      </div>

      <Descriptions column={2} bordered size="small" style={{ marginBottom: 24 }}
        styles={{
          label: { color: 'var(--aim-text-secondary)', background: 'var(--aim-surface)' },
          content: { color: 'var(--aim-text)' },
        }}
      >
        <Descriptions.Item label="类型">{bot.type}</Descriptions.Item>
        <Descriptions.Item label="模型">{bot.model_name || '默认'}</Descriptions.Item>
        <Descriptions.Item label="使用平台模型">{bot.use_platform_model ? '是' : '否'}</Descriptions.Item>
        <Descriptions.Item label="温度">{bot.temperature ?? '-'}</Descriptions.Item>
        <Descriptions.Item label="最大上下文">{bot.max_context_messages ?? '-'} 条</Descriptions.Item>
        <Descriptions.Item label="启用流式">{bot.streaming_enabled ? '是' : '否'}</Descriptions.Item>
        <Descriptions.Item label="启用知识库">{bot.enable_knowledge ? '是' : '否'}</Descriptions.Item>
        <Descriptions.Item label="启用网络搜索">
          {bot.capabilities ? (JSON.parse(bot.capabilities).builtin_tools?.includes('web_search') ? '是' : '否') : '否'}
        </Descriptions.Item>
        <Descriptions.Item label="连接方式">{bot.conn_mode || '-'}</Descriptions.Item>
        <Descriptions.Item label="标签" span={2}>
          {bot.bot_tags?.length ? bot.bot_tags.map((t: string) => <Tag key={t}>{t}</Tag>) : '-'}
        </Descriptions.Item>
      </Descriptions>

      {/* ── Knowledge Base Bindings ── */}
      <div style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
          <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--aim-text)', display: 'flex', alignItems: 'center', gap: 8 }}>
            <BookOutlined /> 知识库
          </span>
          <Button size="small" icon={<BookOutlined />} onClick={() => setKbBindOpen(true)}>
            管理绑定
          </Button>
        </div>
        {bot.enable_knowledge ? (
          boundKbs && boundKbs.length > 0 ? (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
              {boundKbs.map((b: any) => (
                <Tag key={b.kb_id} color="blue" style={{ padding: '2px 10px', fontSize: 13 }}>
                  {b.kb_name || `知识库 #${b.kb_id}`}
                </Tag>
              ))}
            </div>
          ) : (
            <div style={{ fontSize: 13, color: 'var(--aim-text-tertiary)', padding: '8px 0' }}>
              暂未绑定知识库
            </div>
          )
        ) : (
          <div style={{ fontSize: 13, color: 'var(--aim-text-tertiary)', padding: '8px 0' }}>
            知识库功能已关闭，可在编辑中开启
          </div>
        )}
      </div>

      <Modal
        title="管理知识库绑定"
        open={kbBindOpen}
        onCancel={() => setKbBindOpen(false)}
        footer={null}
        width={480}
      >
        {boundKbsLoading ? (
          <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div>
        ) : (
          <>
            <div style={{ marginBottom: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8, color: 'var(--aim-text-secondary)' }}>已绑定的知识库</div>
              {boundKbs && boundKbs.length > 0 ? (
                boundKbs.map((b: any) => (
                  <div key={b.kb_id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid var(--aim-border)' }}>
                    <span>
                      <Tag color={b.mode === 'wiki' ? 'purple' : 'blue'} style={{ marginRight: 6 }}>
                        {b.mode === 'wiki' ? 'WIKI' : 'RAG'}
                      </Tag>
                      {b.kb_name || `知识库 #${b.kb_id}`}
                    </span>
                    <Button size="small" danger loading={unbindKbMutation.isPending} onClick={() => unbindKbMutation.mutate(b.kb_id)}>
                      解绑
                    </Button>
                  </div>
                ))
              ) : (
                <div style={{ color: 'var(--aim-text-tertiary)', fontSize: 13 }}>暂未绑定任何知识库</div>
              )}
            </div>

            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <Select
                style={{ flex: 1 }}
                placeholder="选择知识库..."
                value={selectedKbId}
                onChange={setSelectedKbId}
                options={availableKbs.map((kb: any) => ({ value: kb.id, label: `${kb.name} (${kb.mode === 'wiki' ? 'WIKI' : 'RAG'})` }))}
                showSearch
                filterOption={(input, option) => (option?.label ?? '').toLowerCase().includes(input.toLowerCase())}
              />
              <Button type="primary" onClick={() => bindKbMutation.mutate(selectedKbId!)} loading={bindKbMutation.isPending} disabled={!selectedKbId}>
                绑定
              </Button>
            </div>
          </>
        )}
      </Modal>

      {/* ── MCP Tool Bindings ── */}
      <BotMcpSection botId={id as string} />

      {/* ── Memory Management ── */}
      <Divider orientation="left" style={{ color: 'var(--aim-text-secondary)', fontSize: 13 }}>记忆</Divider>
      <BotMemorySection botId={id as string} />

      {bot.system_prompt && (
        <>
          <Divider orientation="left" style={{ color: 'var(--aim-text-secondary)', fontSize: 13 }}>系统提示词</Divider>
          <div style={{ background: 'var(--aim-surface)', borderRadius: 8, padding: 16, marginBottom: 24, whiteSpace: 'pre-wrap', fontSize: 13, color: 'var(--aim-text-secondary)', lineHeight: 1.6 }}>
            {bot.system_prompt}
          </div>
        </>
      )}

      <Divider orientation="left" style={{ color: 'var(--aim-text-secondary)', fontSize: 13 }}>Bot 信息</Divider>
      <Descriptions column={1} bordered size="small" style={{ marginBottom: 24 }}
        styles={{
          label: { color: 'var(--aim-text-secondary)', background: 'var(--aim-surface)' },
          content: { color: 'var(--aim-text)' },
        }}
      >
        <Descriptions.Item label="类型">{botTypeLabel(bot.type)}</Descriptions.Item>
        {bot.template_id && (
          <Descriptions.Item label="模板">
            {bot.template_id === 'qa' ? '普通问答' : '知识库回答'}
          </Descriptions.Item>
        )}
        <Descriptions.Item label="模型">{bot.model_name || '默认'}</Descriptions.Item>
        {bot.type === 'third_party' && (
          <Descriptions.Item label="连接方式">{bot.sub_type === 'webhook' ? 'Webhook' : bot.sub_type === 'ws' ? 'WebSocket' : bot.conn_mode || '-'}</Descriptions.Item>
        )}
      </Descriptions>

      <div style={{ color: 'var(--aim-text-tertiary)', fontSize: 12 }}>
        创建时间: {bot.created_at ? new Date(bot.created_at * 1000).toLocaleString('zh-CN') : '-'}
      </div>

      <Modal
        title="编辑 Bot"
        open={editOpen}
        onOk={() => form.validateFields().then((vals) => {
          const caps = bot.capabilities ? JSON.parse(bot.capabilities) : { builtin_tools: [] };
          if (!caps.builtin_tools) caps.builtin_tools = [];
          if (vals.enable_web_search) {
            if (!caps.builtin_tools.includes('web_search')) {
              caps.builtin_tools.push('web_search');
            }
          } else {
            caps.builtin_tools = caps.builtin_tools.filter((t: string) => t !== 'web_search');
          }
          delete vals.enable_web_search;
          // Build response_triggers as top-level field (keyword → keyword:xxx format)
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
            vals.response_triggers = triggers;
          }
          delete vals.keywords;
          // Ensure numeric fields are numbers, not strings
          if (vals.temperature != null) vals.temperature = Number(vals.temperature);
          if (vals.max_context_messages != null) vals.max_context_messages = Number(vals.max_context_messages);
          if (vals.memory_limit != null) vals.memory_limit = Number(vals.memory_limit);
          vals.capabilities = JSON.stringify(caps);
          updateMutation.mutate(vals);
        })}
        onCancel={() => setEditOpen(false)}
        confirmLoading={updateMutation.isPending}
        okText="保存"
        cancelText="取消"
      >
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="name" label="名称">
            <Input />
          </Form.Item>
          <Form.Item name="avatar" label="头像">
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <Avatar name={form.getFieldValue('name')} src={form.getFieldValue('avatar')} size={56} />
              <Upload
                showUploadList={false}
                customRequest={({ file }) => {
                  const f = file as File;
                  botApi.uploadAvatar(bot.id, f).then((data: any) => {
                    const url = data?.url || '';
                    if (url) {
                      form.setFieldsValue({ avatar: url });
                    }
                    message.success('头像更新成功');
                    queryClient.invalidateQueries({ queryKey: ['bot', id] });
                  }).catch(() => message.error('上传失败'));
                }}
              >
                <Button icon={<UploadOutlined />}>上传头像</Button>
              </Upload>
            </div>
          </Form.Item>

          {bot.type === 'official' && (
            <>
              <Form.Item name="model_id" label="模型">
                <Select placeholder="选择模型" allowClear options={modelOptions} showSearch
                  filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}

                />
              </Form.Item>
              {bot.template_id === 'knowledge' && (
                <Form.Item name="enable_knowledge" label="启用知识库" valuePropName="checked">
                  <Switch />
                </Form.Item>
              )}
              <Form.Item name="streaming_enabled" label="启用流式" valuePropName="checked">
                <Switch />
              </Form.Item>
              <Form.Item name="enable_web_search" label="启用网络搜索" valuePropName="checked">
                <Switch />
              </Form.Item>
              <Form.Item name="response_triggers" label="触发回复方式">
                <Select mode="multiple" placeholder="选择触发方式" maxCount={3}>
                  <Select.Option value="mention">@提及时回复</Select.Option>
                  <Select.Option value="keyword">关键词匹配回复</Select.Option>
                  <Select.Option value="all">自动回复全部消息</Select.Option>
                </Select>
              </Form.Item>
              <Form.Item noStyle shouldUpdate={(prev, cur) => prev.response_triggers !== cur.response_triggers}>
                {({ getFieldValue }) => {
                  const triggers = getFieldValue('response_triggers') || [];
                  return triggers.includes('keyword') ? (
                    <Form.Item name="keywords" label="关键词列表" extra="多个关键词用逗号分隔">
                      <Input.TextArea rows={2} placeholder="关键词1, 关键词2, ..." />
                    </Form.Item>
                  ) : null;
                }}
              </Form.Item>
              <div style={{ color: 'var(--aim-text-tertiary)', fontSize: 12, padding: '8px 0' }}>
                系统提示词、温度等为模板固定配置，不可修改
              </div>
            </>
          )}

          {bot.type === 'self_deployed' && (
            <>
              <Form.Item name="model_id" label="模型">
                <Select placeholder="选择模型" allowClear options={modelOptions} showSearch
                  filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}

                />
              </Form.Item>
              <Form.Item name="system_prompt" label="系统提示词">
                <Input.TextArea rows={3} />
              </Form.Item>
              <Form.Item name="persona" label="个性设定">
                <Input.TextArea rows={2} />
              </Form.Item>
              <Form.Item name="temperature" label="温度">
                <InputNumber min={0} max={2} step={0.1} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item name="max_context_messages" label="最大上下文消息数">
                <InputNumber min={1} max={100} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item name="streaming_enabled" label="启用流式" valuePropName="checked">
                <Switch />
              </Form.Item>
              <Form.Item name="enable_web_search" label="启用网络搜索" valuePropName="checked">
                <Switch />
              </Form.Item>
              <Form.Item name="enable_knowledge" label="启用知识库" valuePropName="checked">
                <Switch />
              </Form.Item>
              <Form.Item name="response_triggers" label="触发回复方式">
                <Select mode="multiple" placeholder="选择触发方式" maxCount={3}>
                  <Select.Option value="mention">@提及时回复</Select.Option>
                  <Select.Option value="keyword">关键词匹配回复</Select.Option>
                  <Select.Option value="all">自动回复全部消息</Select.Option>
                </Select>
              </Form.Item>
              <Form.Item noStyle shouldUpdate={(prev, cur) => prev.response_triggers !== cur.response_triggers}>
                {({ getFieldValue }) => {
                  const triggers = getFieldValue('response_triggers') || [];
                  return triggers.includes('keyword') ? (
                    <Form.Item name="keywords" label="关键词列表" extra="多个关键词用逗号分隔">
                      <Input.TextArea rows={2} placeholder="关键词1, 关键词2, ..." />
                    </Form.Item>
                  ) : null;
                }}
              </Form.Item>
              <Form.Item name="memory_model_id" label="记忆模型">
                <Select placeholder="选择记忆模型" allowClear options={modelOptions} showSearch
                  filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}

                />
              </Form.Item>
              <Form.Item name="memory_limit" label="记忆条数">
                <InputNumber min={0} max={50} placeholder="默认 5 条" style={{ width: '100%' }} />
              </Form.Item>
            </>
          )}

          {bot.type === 'third_party' && (
            <>
              <div className="bot-guide-mode" style={{ marginBottom: 16 }}>
                <div className="bot-guide-mode-title">
                  连接方式：{bot.sub_type === 'webhook' ? '🔗 Webhook' : '🔌 WebSocket'}
                </div>
                <div style={{ fontSize: 12, color: 'var(--aim-text-secondary)', lineHeight: 1.6 }}>
                  {bot.sub_type === 'webhook'
                    ? `回调地址：${bot.callback_url || '未设置'}`
                    : 'WebSocket 模式，连接凭证通过下方按钮生成'}
                </div>
              </div>

              {bot.sub_type === 'webhook' && (
                <Form.Item name="callback_url" label="回调地址">
                  <Input placeholder="https://your-service.com/webhook" />
                </Form.Item>
              )}

              {/* 回复消息到 AIM */}
              <div className="bot-guide-mode" style={{ marginBottom: 12 }}>
                <div className="bot-guide-mode-title">📤 回复消息到 AIM</div>
                {bot.sub_type === 'ws' ? (
                  <>
                    <div style={{ fontSize: 12, color: 'var(--aim-text-secondary)', lineHeight: 1.6, marginTop: 4 }}>
                      通过已建立的 WebSocket 连接直接发送，无需额外认证：
                    </div>
                    <div className="bot-guide-code" style={{ marginTop: 6 }}>
                      <pre style={{ fontSize: 11, margin: 0 }}>{`{"type":"message.send","conv_id":"...","text":"回复内容","reply_to_id":"..."}`}</pre>
                    </div>
                    <div className="bot-guide-note" style={{ marginTop: 6, fontSize: 11 }}>
                      AIM 回复：{`{"type":"message.sent","message_id":"...","seq":...,"created_at":...}`}
                    </div>
                  </>
                ) : (
                  <>
                    <div style={{ fontSize: 12, color: 'var(--aim-text-secondary)', lineHeight: 1.6, marginTop: 4 }}>
                      <code style={{ background: 'var(--aim-border)', padding: '2px 6px', borderRadius: 3 }}>
                        POST /api/v1/webhook
                      </code>
                    </div>
                    <div style={{ fontSize: 11, color: 'var(--aim-text-tertiary)', marginTop: 6 }}>
                      Headers: <code>X-AIM-Signature</code>（HMAC-SHA256(body, webhook_secret)）、<code>X-AIM-Timestamp</code>（Unix秒）
                    </div>
                    <div className="bot-guide-note" style={{ marginTop: 6, fontSize: 11 }}>
                      消息体：{`{"type":"message.send","conversation_id":"...","text":"...","reply_to_id":"..."}`} — Bot 身份由签名自动解析
                    </div>
                  </>
                )}
              </div>

              <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 16 }}>
                {bot.sub_type === 'webhook' && (
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 12px', background: 'var(--aim-bg)', borderRadius: 6 }}>
                    <div>
                      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--aim-text)' }}>Webhook Secret</div>
                      <div style={{ fontSize: 11, color: 'var(--aim-text-tertiary)' }}>
                        {bot.has_webhook_secret ? '已配置（创建时自动生成）' : '未配置'} — 用于验证 Webhook 请求签名
                      </div>
                    </div>
                    <Button size="small" onClick={async () => {
                      try {
                        const r = await botApi.rotateSecret(id as any);
                        message.success('Webhook Secret 已轮换，新值：' + r?.webhook_secret);
                        queryClient.invalidateQueries({ queryKey: ['bot', id] });
                      } catch (e: any) {
                        message.error('轮换失败');
                      }
                    }}>轮换密钥</Button>
                  </div>
                )}

                {bot.sub_type === 'ws' && (
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 12px', background: 'var(--aim-bg)', borderRadius: 6 }}>
                    <div>
                      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--aim-text)' }}>连接 Token</div>
                      <div style={{ fontSize: 11, color: 'var(--aim-text-tertiary)' }}>
                        连接到 <code style={{ fontSize: 10 }}>ws://host:8081/ws/bot?token=&lt;jwt&gt;&device_id=&lt;id&gt;</code>
                      </div>
                    </div>
                    <Button size="small" onClick={async () => {
                      try {
                        const r = await botApi.issueToken(id as any);
                        message.success('Token 已生成：' + r?.token);
                      } catch (e: any) {
                        message.error('生成失败');
                      }
                    }}>生成 Token</Button>
                  </div>
                )}

                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 12px', background: 'var(--aim-bg)', borderRadius: 6 }}>
                  <div>
                    <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--aim-text)' }}>App Secret</div>
                    <div style={{ fontSize: 11, color: 'var(--aim-text-tertiary)' }}>
                      {bot.has_app_secret ? '已配置' : '未配置'} — 用于调用 AIM 开放 API
                    </div>
                  </div>
                  <Button size="small" onClick={async () => {
                    try {
                      const r = await botApi.rotateSecret(id as any);
                      message.success('App Secret 已轮换');
                      queryClient.invalidateQueries({ queryKey: ['bot', id] });
                    } catch (e: any) {
                      message.error('轮换失败');
                    }
                  }}>轮换密钥</Button>
                </div>
              </div>

              <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', lineHeight: 1.6, padding: '8px 0' }}>
                连接方式在创建后不可修改。如需切换，请删除后重新创建。
              </div>
            </>
          )}

          {bot.type !== 'third_party' && (
            <Form.Item name="status" label="状态">
              <Select>
                <Select.Option value="active">运行中</Select.Option>
                <Select.Option value="disabled">已停用</Select.Option>
              </Select>
            </Form.Item>
          )}
        </Form>
      </Modal>
    </div>
  );
}

// ─── Memory Management Section ───
function BotMemorySection({ botId }: { botId: string }) {
  const queryClient = useQueryClient();
  const [memOpen, setMemOpen] = useState(false);

  const { data: memories = [], isLoading } = useQuery({
    queryKey: ['bot-memories', botId],
    queryFn: () => botApi.getMemory(botId),
  });

  const forgetMutation = useMutation({
    mutationFn: (memoryId: number) => botApi.forgetMemory(botId, memoryId),
    onSuccess: (_data, memoryId) => {
      message.success('记忆已删除');
      queryClient.setQueryData(['bot-memories', botId], (old: any[]) => {
        if (!old) return old;
        return old.filter((m: any) => m.id !== memoryId);
      });
      queryClient.invalidateQueries({ queryKey: ['bot-memories', botId] });
    },
    onError: () => message.error('删除失败'),
  });

  const clearMutation = useMutation({
    mutationFn: () => botApi.clearMemory(botId),
    onSuccess: () => {
      message.success('记忆已清空');
      queryClient.setQueryData(['bot-memories', botId], []);
      queryClient.invalidateQueries({ queryKey: ['bot-memories', botId] });
    },
    onError: () => message.error('清空失败'),
  });

  return (
    <div style={{ marginBottom: 24 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
        <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--aim-text)', display: 'flex', alignItems: 'center', gap: 8 }}>
          我的记忆
        </span>
        <Space size={8}>
          {!isLoading && memories.length > 0 && (
            <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
              共 {memories.length} 条
            </span>
          )}
          <Button size="small" onClick={() => setMemOpen(true)}>
            管理记忆
          </Button>
        </Space>
      </div>

      {isLoading ? (
        <div style={{ textAlign: 'center', padding: 20 }}><Spin /></div>
      ) : memories.length === 0 ? (
        <div style={{ fontSize: 13, color: 'var(--aim-text-tertiary)', padding: '8px 0' }}>
          暂无记忆 — 与 Bot 对话后会自动记录
        </div>
      ) : (
        <div>
          {memories.slice(0, 5).map((mem) => (
            <div
              key={mem.id}
              style={{
                display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start',
                padding: '10px 12px', marginBottom: 6, borderRadius: 6,
                background: 'var(--aim-surface)', border: '1px solid var(--aim-border)',
              }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                  <Tag color={mem.memory_type === 'episode' ? 'purple' : 'blue'} style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
                    {mem.memory_type === 'episode' ? '事件' : '事实'}
                  </Tag>
                  {mem.category && (
                    <Tag color="cyan" style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
                      {mem.category}
                    </Tag>
                  )}
                  {mem.importance > 0.7 && (
                    <Tag color="red" style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>重要</Tag>
                  )}
                  <Tag color="geekblue" style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
                    {Number(mem.final_score ?? 0).toFixed(2)}
                  </Tag>
                </div>
                <div style={{ fontSize: 13, color: 'var(--aim-text)', lineHeight: 1.5, wordBreak: 'break-word' }}>
                  {mem.content}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      <Modal
        title="Bot 记忆管理"
        open={memOpen}
        onCancel={() => setMemOpen(false)}
        footer={null}
        width={560}
      >
        {isLoading ? (
          <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div>
        ) : memories.length === 0 ? (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--aim-text-tertiary)' }}>
            <p>暂无记忆</p>
            <p style={{ fontSize: 12, marginTop: 4 }}>与 Bot 对话后，Bot 会自动记录相关记忆</p>
          </div>
        ) : (
          <>
            <div style={{ marginBottom: 12, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)' }}>共 {memories.length} 条记忆</span>
              <Button
                size="small"
                danger
                loading={clearMutation.isPending}
                onClick={() => Modal.confirm({
                  title: '确认清空',
                  content: '确定要清空所有记忆吗？此操作不可恢复。',
                  okText: '清空',
                  okType: 'danger',
                  cancelText: '取消',
                  onOk: () => clearMutation.mutate(),
                })}
              >
                清空全部
              </Button>
            </div>
            <div style={{ maxHeight: 400, overflowY: 'auto' }}>
              {memories.map((mem) => (
                <div
                  key={mem.id}
                  style={{
                    display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start',
                    padding: '10px 12px', marginBottom: 8, borderRadius: 6,
                    background: 'var(--aim-surface)', border: '1px solid var(--aim-border)',
                  }}
                >
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                      <Tag color={mem.memory_type === 'episode' ? 'purple' : 'blue'} style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
                        {mem.memory_type === 'episode' ? '事件' : '事实'}
                      </Tag>
                      {mem.category && (
                        <Tag color="cyan" style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
                          {mem.category}
                        </Tag>
                      )}
                      {mem.importance > 0.7 && (
                        <Tag color="red" style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>重要</Tag>
                      )}
                      <Tag color="geekblue" style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
                        {Number(mem.final_score ?? 0).toFixed(2)}
                      </Tag>
                      <span style={{ fontSize: 11, color: 'var(--aim-text-tertiary)', marginLeft: 'auto' }}>
                        {mem.created_at ? new Date(mem.created_at).toLocaleString('zh-CN') : ''}
                      </span>
                    </div>
                    <div style={{ fontSize: 13, color: 'var(--aim-text)', lineHeight: 1.5, wordBreak: 'break-word' }}>
                      {mem.content}
                    </div>
                  </div>
                  <Button
                    type="text"
                    size="small"
                    danger
                    loading={forgetMutation.isPending}
                    icon={<DeleteOutlined />}
                    onClick={() => forgetMutation.mutate(mem.id)}
                    style={{ marginLeft: 8, flexShrink: 0 }}
                  />
                </div>
              ))}
            </div>
          </>
        )}
      </Modal>
    </div>
  );
}

// ─── MCP Tool Binding Section ───
function BotMcpSection({ botId }: { botId: string }) {
  const queryClient = useQueryClient();
  const [mcpOpen, setMcpOpen] = useState(false);
  const [selectedMcpId, setSelectedMcpId] = useState<number | undefined>();

  const { data: boundServers = [], isLoading } = useQuery({
    queryKey: ['bot-mcp-servers', botId],
    queryFn: () => mcpApi.listBotMcpServers(botId),
  });

  const { data: allMcpData } = useQuery({
    queryKey: ['mcp-servers'],
    queryFn: () => mcpApi.list(),
    enabled: mcpOpen,
  });

  const allServers = allMcpData?.list ?? [];
  const boundMcpIds = new Set(boundServers.map((s: any) => s.mcp_server_id));
  const availableServers = allServers.filter((s: any) => !boundMcpIds.has(s.id));

  const assignMutation = useMutation({
    mutationFn: (mcpId: number) => mcpApi.assignToBot(botId, mcpId),
    onSuccess: () => {
      message.success('绑定成功');
      queryClient.invalidateQueries({ queryKey: ['bot-mcp-servers', botId] });
      setSelectedMcpId(undefined);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '绑定失败'),
  });

  const unassignMutation = useMutation({
    mutationFn: (mcpId: number) => mcpApi.unassignFromBot(botId, mcpId),
    onSuccess: () => {
      message.success('已解绑');
      queryClient.invalidateQueries({ queryKey: ['bot-mcp-servers', botId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '解绑失败'),
  });

  const toggleMutation = useMutation({
    mutationFn: ({ mcpId, enabled }: { mcpId: number; enabled: boolean }) =>
      mcpApi.updateBotMcpServer(botId, mcpId, enabled),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['bot-mcp-servers', botId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  return (
    <div style={{ marginBottom: 24 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
        <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--aim-text)', display: 'flex', alignItems: 'center', gap: 8 }}>
          <ApiOutlined /> MCP 工具
        </span>
        <Button size="small" icon={<ApiOutlined />} onClick={() => setMcpOpen(true)}>
          管理绑定
        </Button>
      </div>

      {isLoading ? (
        <div style={{ textAlign: 'center', padding: 20 }}><Spin /></div>
      ) : boundServers.length === 0 ? (
        <div style={{ fontSize: 13, color: 'var(--aim-text-tertiary)', padding: '8px 0' }}>
          暂未绑定 MCP 服务器 — 绑定后 Bot 可获得额外工具能力
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {boundServers.map((s: any) => (
            <div
              key={s.mcp_server_id}
              style={{
                display: 'flex', alignItems: 'center', gap: 12,
                padding: '10px 14px', borderRadius: 8,
                background: 'var(--aim-surface)', border: '1px solid var(--aim-border)',
                opacity: s.enabled ? 1 : 0.5,
                transition: 'opacity 0.2s',
              }}
            >
              <div style={{
                width: 32, height: 32, borderRadius: 8,
                background: s.enabled ? 'linear-gradient(135deg, #667eea, #764ba2)' : 'var(--aim-border)',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontSize: 15, color: '#fff', flexShrink: 0,
              }}>
                <ToolOutlined />
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--aim-text)' }}>{s.name}</span>
                  <Tag color={s.enabled ? 'green' : 'default'} style={{ margin: 0, fontSize: 10, lineHeight: '16px' }}>
                    {s.enabled ? '启用' : '停用'}
                  </Tag>
                  {s.transport && (
                    <Tag color="geekblue" style={{ margin: 0, fontSize: 10, lineHeight: '16px' }}>
                      {s.transport === 'sse' ? 'SSE' : 'HTTP'}
                    </Tag>
                  )}
                </div>
              </div>
              <Switch
                size="small"
                checked={s.enabled}
                loading={toggleMutation.isPending}
                onChange={(checked) => toggleMutation.mutate({ mcpId: s.mcp_server_id, enabled: checked })}
              />
            </div>
          ))}
        </div>
      )}

      <Modal
        title="管理 MCP 工具绑定"
        open={mcpOpen}
        onCancel={() => setMcpOpen(false)}
        footer={null}
        width={560}
      >
        {isLoading ? (
          <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div>
        ) : (
          <>
            <div style={{ marginBottom: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8, color: 'var(--aim-text-secondary)' }}>
                已绑定的 MCP 服务器
              </div>
              {boundServers.length > 0 ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {boundServers.map((s: any) => (
                    <div key={s.mcp_server_id} style={{
                      display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                      padding: '10px 12px', borderRadius: 6,
                      background: 'var(--aim-surface)', border: '1px solid var(--aim-border)',
                    }}>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--aim-text)' }}>{s.name}</div>
                        {s.url && (
                          <div style={{
                            fontSize: 11, color: 'var(--aim-text-tertiary)',
                            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                          }}>
                            {s.url}
                          </div>
                        )}
                      </div>
                      <Space size={8}>
                        <Switch
                          size="small"
                          checked={s.enabled}
                          loading={toggleMutation.isPending}
                          onChange={(checked) => toggleMutation.mutate({ mcpId: s.mcp_server_id, enabled: checked })}
                        />
                        <Button
                          size="small"
                          danger
                          loading={unassignMutation.isPending}
                          icon={<DeleteOutlined />}
                          onClick={() => Modal.confirm({
                            title: '确认解绑',
                            content: `确定要解绑 MCP 服务器「${s.name}」吗？`,
                            okText: '解绑',
                            okType: 'danger',
                            cancelText: '取消',
                            onOk: () => unassignMutation.mutate(s.mcp_server_id),
                          })}
                        />
                      </Space>
                    </div>
                  ))}
                </div>
              ) : (
                <div style={{ color: 'var(--aim-text-tertiary)', fontSize: 13, padding: '16px 0', textAlign: 'center' }}>
                  暂未绑定任何 MCP 服务器
                </div>
              )}
            </div>

            <div style={{ display: 'flex', gap: 8, alignItems: 'center', borderTop: '1px solid var(--aim-border)', paddingTop: 16 }}>
              <Select
                style={{ flex: 1 }}
                placeholder="选择 MCP 服务器..."
                value={selectedMcpId}
                onChange={setSelectedMcpId}
                options={availableServers.map((s: any) => ({ value: s.id, label: s.name }))}
                showSearch
                filterOption={(input, option) => (option?.label ?? '').toLowerCase().includes(input.toLowerCase())}
              />
              <Button
                type="primary"
                onClick={() => assignMutation.mutate(selectedMcpId!)}
                loading={assignMutation.isPending}
                disabled={!selectedMcpId}
              >
                绑定
              </Button>
            </div>
          </>
        )}
      </Modal>
    </div>
  );
}
