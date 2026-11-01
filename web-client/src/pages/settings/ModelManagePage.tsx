import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Table, Button, Modal, Form, Input, Select, InputNumber, message, Tag } from 'antd';
import { CrownOutlined, EditOutlined } from '@ant-design/icons';
import { modelApi } from '@/services/model';
import type { ModelCapability, ModelResp } from '@/types/model';
import { displayProvider, providerOptions } from '@/utils/provider';
import { useAuthStore } from '@/stores/auth';
import type { CreateModelReq, UpdateModelReq } from '@/types/api';
import { capabilityOptions, displayCapability } from '@/utils/capability';

type ModelFormValues = Omit<CreateModelReq, 'owner_type' | 'owner_id'>;

function formatPrice(value: number | undefined): string {
  if (value == null) return '-';
  return value === 0 ? '免费 (¥0)' : `¥${value.toLocaleString('zh-CN', { maximumFractionDigits: 6 })}`;
}

function getModelError(error: unknown): string | undefined {
  if (!error || typeof error !== 'object' || !('response' in error)) return undefined;
  const response = error.response;
  if (!response || typeof response !== 'object' || !('data' in response)) return undefined;
  const data = response.data;
  if (!data || typeof data !== 'object') return undefined;
  if ('message' in data && typeof data.message === 'string') return data.message;
  if ('msg' in data && typeof data.msg === 'string') return data.msg;
  return undefined;
}

export function ModelManagePage() {
  const queryClient = useQueryClient();
  const currentUserId = useAuthStore((s) => s.user?.id);
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editingModel, setEditingModel] = useState<ModelResp | null>(null);
  const [capability, setCapability] = useState<ModelCapability>();
  const [createForm] = Form.useForm<ModelFormValues>();
  const [editForm] = Form.useForm<ModelFormValues>();

  const { data, isLoading } = useQuery({
    queryKey: ['models', 'manage', currentUserId, capability],
    queryFn: () => modelApi.list(capability),
  });

  const models = data?.list ?? [];

  const createMutation = useMutation({
    mutationFn: (vals: ModelFormValues) => {
      if (!currentUserId) throw new Error('请先登录');
      return modelApi.create({ ...vals, owner_type: 'user', owner_id: currentUserId });
    },
    onSuccess: () => {
      message.success('模型添加成功');
      setCreateOpen(false);
      createForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['models'] });
    },
    onError: (err: unknown) => message.error(getModelError(err) || '添加失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => modelApi.delete(id),
    onSuccess: () => {
      message.success('模型已删除');
      queryClient.invalidateQueries({ queryKey: ['models'] });
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateModelReq }) => modelApi.update(id, data),
    onSuccess: () => {
      message.success('模型更新成功');
      setEditOpen(false);
      setEditingModel(null);
      editForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['models'] });
    },
    onError: (err: unknown) => message.error(getModelError(err) || '更新失败'),
  });

  const handleEdit = (r: ModelResp) => {
    setEditingModel(r);
    editForm.setFieldsValue({
      model_name: r.model_name,
      provider: r.provider,
      capability: r.capability,
      base_url: r.base_url,
      input_price_per_mtok: r.input_price_per_mtok,
      output_price_per_mtok: r.output_price_per_mtok,
    });
    setEditOpen(true);
  };

  const columns = [
    {
      title: '名称', dataIndex: 'model_name', key: 'name',
      render: (name: string, r: ModelResp) => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
          {name}
          {r.owner_type === 'platform' && (
            <Tag icon={<CrownOutlined />} color="gold" style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>官方</Tag>
          )}
        </span>
      ),
    },
    { title: '供应商', dataIndex: 'provider', key: 'provider', width: 120,
      render: (p: string) => displayProvider(p),
    },
    { title: '能力', dataIndex: 'capability', key: 'capability', width: 170,
      render: (value: string) => <Tag color={value === 'judge' ? 'cyan' : 'default'}>{displayCapability(value)}</Tag>,
    },
    { title: 'Base URL', dataIndex: 'base_url', key: 'base_url', ellipsis: true },
    {
      title: '状态', dataIndex: 'status', key: 'status', width: 80,
      render: (s: string) => <Tag color={s === 'active' ? 'green' : 'default'}>{s}</Tag>,
    },
    {
      title: '输入价格 (¥/MTok)',
      dataIndex: 'input_price_per_mtok',
      key: 'input_price',
      width: 160,
      render: formatPrice,
    },
    {
      title: '输出价格 (¥/MTok)',
      dataIndex: 'output_price_per_mtok',
      key: 'output_price',
      width: 160,
      render: formatPrice,
    },
    {
      title: '操作', key: 'actions', width: 120,
      render: (_: unknown, r: ModelResp) => r.owner_type === 'user' && r.owner_id === currentUserId ? (
        <span style={{ display: 'inline-flex', gap: 4 }}>
          <Button type="link" icon={<EditOutlined />} onClick={() => handleEdit(r)}>编辑</Button>
          <Button type="link" danger onClick={() => deleteMutation.mutate(r.id)}>删除</Button>
        </span>
      ) : null,
    },
  ];

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 16, marginBottom: 16 }}>
        <h2 style={{ color: 'var(--aim-text)', margin: 0, fontSize: 20 }}>模型管理</h2>
        <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <Select<ModelCapability>
            aria-label="按模型能力筛选"
            placeholder="全部能力"
            allowClear
            value={capability}
            options={capabilityOptions}
            onChange={setCapability}
            style={{ width: 200 }}
          />
          <Button type="primary" onClick={() => setCreateOpen(true)}>添加模型</Button>
        </div>
      </div>
      <Table
        dataSource={models}
        columns={columns}
        rowKey="id"
        loading={isLoading}
        pagination={false}
        size="small"
        scroll={{ x: 1100 }}
      />

      {/* Create Modal */}
      <Modal
        title="添加模型"
        open={createOpen}
        onOk={() => createForm.validateFields().then((vals) => createMutation.mutate(vals))}
        onCancel={() => { setCreateOpen(false); createForm.resetFields(); }}
        confirmLoading={createMutation.isPending}
        okText="添加"
        cancelText="取消"
      >
        <Form name="create-model" form={createForm} layout="vertical" initialValues={{ input_price_per_mtok: 0, output_price_per_mtok: 0 }} style={{ marginTop: 16 }}>
          <Form.Item name="model_name" label="模型名称" rules={[{ required: true }]} extra="TypeSafe AI 请填写实际可用的固定 Jev 模型版本。">
            <Input placeholder="如 gpt-4o" />
          </Form.Item>
          <Form.Item name="provider" label="供应商" rules={[{ required: true }]}>
            <Select placeholder="选择供应商">
              {providerOptions.map((o) => (
                <Select.Option key={o.value} value={o.value}>
                  {o.label}
                </Select.Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item name="capability" label="能力" rules={[{ required: true }]} extra="结构化判断使用 TypeSafe AI 的 System One 文本接口，支持 Noul、Choice、Score。">
            <Select options={capabilityOptions} />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL" rules={[{ required: true }]} extra="TypeSafe AI：https://api.typesafe.ai；不包含 /v1/systemone。">
            <Input placeholder="https://api.openai.com/v1" />
          </Form.Item>
          <Form.Item name="input_price_per_mtok" label="输入价格 (¥/MTok)" rules={[{ required: true, type: 'number', min: 0 }]}>
            <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="output_price_per_mtok" label="输出价格 (¥/MTok)" rules={[{ required: true, type: 'number', min: 0 }]} extra="供应商输出免费时设为 0；真实输出 token 仍会记录。">
            <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="api_key" label="API Key">
            <Input.Password placeholder="选填" />
          </Form.Item>
        </Form>
      </Modal>

      {/* Edit Modal */}
      <Modal
        title="编辑模型"
        open={editOpen}
        onOk={() => editForm.validateFields().then((vals) => {
          if (!editingModel) return;
          const payload: UpdateModelReq = {};
          if (vals.model_name !== editingModel.model_name) payload.model_name = vals.model_name;
          if (vals.provider !== editingModel.provider) payload.provider = vals.provider;
          if (vals.capability !== editingModel.capability) payload.capability = vals.capability;
          if (vals.base_url !== editingModel.base_url) payload.base_url = vals.base_url;
          if (vals.input_price_per_mtok !== editingModel.input_price_per_mtok) payload.input_price_per_mtok = vals.input_price_per_mtok;
          if (vals.output_price_per_mtok !== editingModel.output_price_per_mtok) payload.output_price_per_mtok = vals.output_price_per_mtok;
          if (vals.api_key) payload.api_key = vals.api_key;
          updateMutation.mutate({ id: editingModel.id, data: payload });
        })}
        onCancel={() => { setEditOpen(false); setEditingModel(null); editForm.resetFields(); }}
        confirmLoading={updateMutation.isPending}
        okText="保存"
        cancelText="取消"
      >
        <Form name="edit-model" form={editForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="model_name" label="模型名称" rules={[{ required: true }]} extra="TypeSafe AI 请填写实际可用的固定 Jev 模型版本。">
            <Input placeholder="如 gpt-4o" />
          </Form.Item>
          <Form.Item name="provider" label="供应商" rules={[{ required: true }]}>
            <Select placeholder="选择供应商">
              {providerOptions.map((o) => (
                <Select.Option key={o.value} value={o.value}>
                  {o.label}
                </Select.Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item name="capability" label="能力" rules={[{ required: true }]} extra="结构化判断使用 TypeSafe AI 的 System One 文本接口，支持 Noul、Choice、Score。">
            <Select options={capabilityOptions} />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL" rules={[{ required: true }]} extra="TypeSafe AI：https://api.typesafe.ai；不包含 /v1/systemone。">
            <Input placeholder="https://api.openai.com/v1" />
          </Form.Item>
          <Form.Item name="input_price_per_mtok" label="输入价格 (¥/MTok)" rules={[{ required: true, type: 'number', min: 0 }]}>
            <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="output_price_per_mtok" label="输出价格 (¥/MTok)" rules={[{ required: true, type: 'number', min: 0 }]} extra="供应商输出免费时设为 0；真实输出 token 仍会记录。">
            <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="api_key" label="API Key">
            <Input.Password placeholder="不修改则留空" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
