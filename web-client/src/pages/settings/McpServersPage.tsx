import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Table, Button, Modal, Form, Input, Select, Switch, message, Tooltip, Tag, Collapse, Descriptions, Spin } from 'antd';
import { mcpApi } from '@/services/mcp';
import type { McpServerInfo, McpToolInfo } from '@/types/model';

export function McpServersPage() {
  const queryClient = useQueryClient();
  const [modalOpen, setModalOpen] = useState(false);
  const [editingServer, setEditingServer] = useState<McpServerInfo | null>(null);
  const [form] = Form.useForm();
  const [toolsModalOpen, setToolsModalOpen] = useState(false);
  const [toolsServer, setToolsServer] = useState<McpServerInfo | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ['mcp-servers'],
    queryFn: () => mcpApi.list(),
  });

  const servers = data?.list ?? [];

  const saveMutation = useMutation({
    mutationFn: (vals: any) => {
      const body = buildServerPayload(vals);
      return editingServer
        ? mcpApi.update(editingServer.id, body)
        : mcpApi.create(body);
    },
    onSuccess: () => {
      message.success(editingServer ? 'MCP 服务器已更新' : 'MCP 服务器添加成功');
      setModalOpen(false);
      setEditingServer(null);
      form.resetFields();
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => mcpApi.delete(id),
    onSuccess: (_data, id) => {
      message.success('MCP 服务器已删除');
      queryClient.setQueryData(['mcp-servers'], (old: any) => {
        if (!old?.list) return old;
        return { ...old, list: old.list.filter((s: any) => s.id !== id) };
      });
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] });
    },
  });

  const discoverMutation = useMutation({
    mutationFn: (id: number) => mcpApi.discoverTools(id),
    onSuccess: (data: any) => {
      const count = data?.tools?.length ?? 0;
      message.success(`工具发现完成，共 ${count} 个工具`);
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '工具发现失败，请检查 MCP 服务器是否可访问'),
  });

  const { data: toolsData, isLoading: toolsLoading, refetch: refetchTools } = useQuery({
    queryKey: ['mcp-tools', toolsServer?.id],
    queryFn: () => mcpApi.listTools(toolsServer!.id),
    enabled: false,
  });

  const openEdit = (server: McpServerInfo) => {
    setEditingServer(server);
    const auth = safeParseJSON(server.auth_config);
    const adv = safeParseJSON(server.advanced_config);
    form.setFieldsValue({
      name: server.name,
      description: server.description,
      transport: server.transport,
      url: server.url,
      enabled: server.enabled,
      api_key: auth?.api_key ?? '',
      token: auth?.token ?? '',
      timeout: adv?.timeout ?? 30,
      retry_count: adv?.retry_count ?? 3,
      retry_delay: adv?.retry_delay ?? 1,
    });
    setModalOpen(true);
  };

  const openCreate = () => {
    setEditingServer(null);
    form.resetFields();
    form.setFieldsValue({ transport: 'sse', enabled: true, timeout: 30, retry_count: 3, retry_delay: 1 });
    setModalOpen(true);
  };

  const openTools = (server: McpServerInfo) => {
    setToolsServer(server);
    setToolsModalOpen(true);
    setTimeout(() => refetchTools(), 100);
  };

  const columns = [
    { title: '名称', dataIndex: 'name', key: 'name' },
    { title: '传输方式', dataIndex: 'transport', key: 'transport', width: 120,
      render: (v: string) => v === 'sse' ? 'SSE' : v === 'http-streamable' ? 'HTTP Streamable' : v,
    },
    { title: 'URL', dataIndex: 'url', key: 'url', ellipsis: true },
    { title: '状态', dataIndex: 'status', key: 'status', width: 70,
      render: (v: string) => <Tag color={v === 'active' ? 'green' : 'default'}>{v === 'active' ? '启用' : '停用'}</Tag>,
    },
    {
      title: '操作', key: 'actions', width: 200,
      render: (_: any, r: McpServerInfo) => (
        <span style={{ display: 'flex', gap: 4 }}>
          <Button type="link" size="small" onClick={() => openEdit(r)}>编辑</Button>
          <Button type="link" size="small" onClick={() => openTools(r)}>工具</Button>
          <Tooltip title="发现并更新工具列表">
            <Button type="link" size="small" loading={discoverMutation.isPending}
              onClick={() => discoverMutation.mutate(r.id)}>刷新</Button>
          </Tooltip>
          <Button type="link" size="small" danger onClick={() => {
            Modal.confirm({
              title: '确认删除',
              content: `确定要删除 MCP 服务器「${r.name}」吗？`,
              onOk: () => deleteMutation.mutate(r.id),
            });
          }}>删除</Button>
        </span>
      ),
    },
  ];

  return (
    <div style={{ flex: 1, padding: 32, overflowY: 'auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
        <h2 style={{ color: 'var(--aim-text)', margin: 0, fontSize: 20 }}>MCP 服务器</h2>
        <Button type="primary" onClick={openCreate}>添加服务器</Button>
      </div>

      <Table
        dataSource={servers}
        columns={columns}
        rowKey="id"
        loading={isLoading}
        pagination={false}
        size="small"
      />

      {/* Create/Edit Modal */}
      <Modal
        title={editingServer ? '编辑 MCP 服务器' : '添加 MCP 服务器'}
        open={modalOpen}
        onOk={() => form.validateFields().then((vals) => saveMutation.mutate(vals))}
        onCancel={() => { setModalOpen(false); setEditingServer(null); form.resetFields(); }}
        confirmLoading={saveMutation.isPending}
        okText={editingServer ? '保存' : '添加'}
        cancelText="取消"
        width={560}
      >
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="name" label="服务名称" rules={[{ required: true, message: '请输入服务名称' }]}>
            <Input placeholder="请输入服务名称" />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input.TextArea rows={2} placeholder="请输入服务描述" />
          </Form.Item>
          <Form.Item name="transport" label="传输类型" rules={[{ required: true }]}>
            <Select>
              <Select.Option value="sse">SSE (Server-Sent Events)</Select.Option>
              <Select.Option value="http-streamable">HTTP Streamable</Select.Option>
            </Select>
          </Form.Item>
          <Form.Item name="url" label="服务 URL" rules={[{ required: true, message: '请输入服务 URL' }]}>
            <Input placeholder="https://example.com/mcp" />
          </Form.Item>
          <Form.Item name="enabled" label="启用服务" valuePropName="checked" initialValue={true}>
            <Switch />
          </Form.Item>

          <div style={{ fontWeight: 600, marginBottom: 8, color: 'var(--aim-text)' }}>认证配置</div>
          <Form.Item name="api_key" label="API Key">
            <Input.Password placeholder="可选" />
          </Form.Item>
          <Form.Item name="token" label="Bearer Token">
            <Input.Password placeholder="可选" />
          </Form.Item>

          <div style={{ fontWeight: 600, marginBottom: 8, color: 'var(--aim-text)' }}>高级配置</div>
          <div style={{ display: 'flex', gap: 12 }}>
            <Form.Item name="timeout" label="超时时间(秒)" initialValue={30} style={{ flex: 1 }}>
              <Input type="number" min={1} />
            </Form.Item>
            <Form.Item name="retry_count" label="重试次数" initialValue={3} style={{ flex: 1 }}>
              <Input type="number" min={0} />
            </Form.Item>
            <Form.Item name="retry_delay" label="重试延迟(秒)" initialValue={1} style={{ flex: 1 }}>
              <Input type="number" min={0} step={0.5} />
            </Form.Item>
          </div>
        </Form>
      </Modal>

      {/* Tools Modal */}
      <Modal
        title={toolsServer ? `工具列表 - ${toolsServer.name}` : '工具列表'}
        open={toolsModalOpen}
        onCancel={() => { setToolsModalOpen(false); setToolsServer(null); }}
        footer={null}
        width={640}
      >
        {toolsLoading ? <Spin style={{ display: 'block', margin: '24px auto' }} /> : null}
        {toolsData && toolsData.length === 0 && <div style={{ textAlign: 'center', padding: 24, color: '#999' }}>暂无工具，请点击"刷新"按钮发现工具</div>}
        {toolsData && toolsData.length > 0 && (
          <Collapse
            items={toolsData.map((tool: McpToolInfo) => ({
              key: tool.id,
              label: (
                <span>
                  <code style={{ fontSize: 13 }}>{tool.name}</code>
                  {tool.description && (
                    <span style={{ marginLeft: 8, fontSize: 12, color: '#888' }}>{tool.description}</span>
                  )}
                </span>
              ),
              children: (
                <Descriptions column={1} size="small">
                  {tool.description && <Descriptions.Item label="描述">{tool.description}</Descriptions.Item>}
                  {tool.input_schema && (
                    <Descriptions.Item label="参数 Schema">
                      <pre style={{ fontSize: 12, maxHeight: 300, overflow: 'auto', background: '#f5f5f5', padding: 8, borderRadius: 4, margin: 0 }}>
                        {JSON.stringify(JSON.parse(tool.input_schema), null, 2)}
                      </pre>
                    </Descriptions.Item>
                  )}
                  {!tool.input_schema && <Descriptions.Item label="参数">无</Descriptions.Item>}
                </Descriptions>
              ),
            }))}
          />
        )}
      </Modal>
    </div>
  );
}

function buildServerPayload(vals: any) {
  const authConfig: Record<string, string> = {};
  if (vals.api_key) authConfig.api_key = vals.api_key;
  if (vals.token) authConfig.token = vals.token;
  const advancedConfig: Record<string, number> = {
    timeout: Number(vals.timeout) || 30,
    retry_count: Number(vals.retry_count) || 3,
    retry_delay: Number(vals.retry_delay) || 1,
  };
  return {
    name: vals.name,
    description: vals.description ?? '',
    transport: vals.transport,
    url: vals.url,
    enabled: vals.enabled ?? true,
    auth_config: JSON.stringify(authConfig),
    advanced_config: JSON.stringify(advancedConfig),
  };
}

function safeParseJSON(str: string | undefined | null): any {
  if (!str) return null;
  try { return JSON.parse(str); } catch { return null; }
}
