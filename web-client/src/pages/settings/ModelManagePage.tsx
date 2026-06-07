import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Table, Button, Modal, Form, Input, Select, InputNumber, message, Tag } from 'antd';
import { CrownOutlined, EditOutlined } from '@ant-design/icons';
import { modelApi } from '@/services/model';
import type { ModelResp } from '@/types/model';
import { displayProvider, providerOptions } from '@/utils/provider';

const CAPABILITY_OPTIONS = [
  { value: 'chat', label: 'Chat' },
  { value: 'embedding', label: 'Embedding' },
  { value: 'rerank', label: 'Rerank' },
  { value: 'vlm', label: 'VLM' },
  { value: 'tts', label: 'TTS' },
];

export function ModelManagePage() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editingModel, setEditingModel] = useState<ModelResp | null>(null);
  const [createForm] = Form.useForm();
  const [editForm] = Form.useForm();

  const { data, isLoading } = useQuery({
    queryKey: ['models'],
    queryFn: () => modelApi.list(),
  });

  const models = data?.list ?? [];

  const createMutation = useMutation({
    mutationFn: (vals: any) => modelApi.create(vals),
    onSuccess: () => {
      message.success('模型添加成功');
      setCreateOpen(false);
      createForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['models'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '添加失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => modelApi.delete(id),
    onSuccess: (_data, id) => {
      message.success('模型已删除');
      queryClient.setQueryData(['models'], (old: any) => {
        if (!old?.list) return old;
        return { ...old, list: old.list.filter((m: any) => m.id !== id) };
      });
      queryClient.invalidateQueries({ queryKey: ['models'] });
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Record<string, unknown> }) => modelApi.update(id, data),
    onSuccess: () => {
      message.success('模型更新成功');
      setEditOpen(false);
      setEditingModel(null);
      editForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['models'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '更新失败'),
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
          {Number(r.owner_id) === 0 && (
            <Tag icon={<CrownOutlined />} color="gold" style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>官方</Tag>
          )}
        </span>
      ),
    },
    { title: '供应商', dataIndex: 'provider', key: 'provider', width: 120,
      render: (p: string) => displayProvider(p),
    },
    { title: '能力', dataIndex: 'capability', key: 'capability', width: 100 },
    { title: 'Base URL', dataIndex: 'base_url', key: 'base_url', ellipsis: true },
    {
      title: '状态', dataIndex: 'status', key: 'status', width: 80,
      render: (s: string) => <Tag color={s === 'active' ? 'green' : 'default'}>{s}</Tag>,
    },
    {
      title: '输入价格',
      dataIndex: 'input_price_per_mtok',
      key: 'input_price',
      width: 100,
      render: (v: number) => v ? `¥${v.toFixed(2)}` : '-',
    },
    {
      title: '输出价格',
      dataIndex: 'output_price_per_mtok',
      key: 'output_price',
      width: 100,
      render: (v: number) => v ? `¥${v.toFixed(2)}` : '-',
    },
    {
      title: '操作', key: 'actions', width: 120,
      render: (_: any, r: ModelResp) => Number(r.owner_id) !== 0 ? (
        <span style={{ display: 'inline-flex', gap: 4 }}>
          <Button type="link" icon={<EditOutlined />} onClick={() => handleEdit(r)}>编辑</Button>
          <Button type="link" danger onClick={() => deleteMutation.mutate(r.id)}>删除</Button>
        </span>
      ) : null,
    },
  ];

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
        <h2 style={{ color: 'var(--aim-text)', margin: 0, fontSize: 20 }}>模型管理</h2>
        <Button type="primary" onClick={() => setCreateOpen(true)}>添加模型</Button>
      </div>
      <Table
        dataSource={models}
        columns={columns}
        rowKey="id"
        loading={isLoading}
        pagination={false}
        size="small"
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
        <Form form={createForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="model_name" label="模型名称" rules={[{ required: true }]}>
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
          <Form.Item name="capability" label="能力" rules={[{ required: true }]}>
            <Select options={CAPABILITY_OPTIONS} />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL" rules={[{ required: true }]}>
            <Input placeholder="https://api.openai.com/v1" />
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
          const payload: Record<string, unknown> = {};
          if (vals.model_name !== editingModel.model_name) payload.model_name = vals.model_name;
          if (vals.provider !== editingModel.provider) payload.provider = vals.provider;
          if (vals.capability !== editingModel.capability) payload.capability = vals.capability;
          if (vals.base_url !== editingModel.base_url) payload.base_url = vals.base_url;
          if (vals.input_price_per_mtok != null) vals.input_price_per_mtok = Number(vals.input_price_per_mtok);
          if (vals.output_price_per_mtok != null) vals.output_price_per_mtok = Number(vals.output_price_per_mtok);
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
        <Form form={editForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="model_name" label="模型名称">
            <Input placeholder="如 gpt-4o" />
          </Form.Item>
          <Form.Item name="provider" label="供应商">
            <Select placeholder="选择供应商">
              {providerOptions.map((o) => (
                <Select.Option key={o.value} value={o.value}>
                  {o.label}
                </Select.Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item name="capability" label="能力">
            <Select options={CAPABILITY_OPTIONS} />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL">
            <Input placeholder="https://api.openai.com/v1" />
          </Form.Item>
          <Form.Item name="input_price_per_mtok" label="输入价格 (¥/MTok)">
            <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="output_price_per_mtok" label="输出价格 (¥/MTok)">
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
