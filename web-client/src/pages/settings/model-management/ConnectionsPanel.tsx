import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Alert, Button, Checkbox, Empty, Form, Input, Modal, Popconfirm, Select, Space, Table, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { modelConnectionApi } from '@/services/model';
import type { CreateModelConnectionReq } from '@/types/api';
import type { ModelConnectionResp } from '@/types/model';
import { displayProvider, providerOptions } from '@/utils/provider';
import { LimitsSummary, MutationFailure, QueryFailure } from './shared';
import type { ManagementResources } from './shared';

interface ConnectionFormValues extends CreateModelConnectionReq {
  rotate_api_key?: boolean;
}

function ConnectionEditor({ connection, resources, onClose }: {
  connection: ModelConnectionResp | null;
  resources: ManagementResources;
  onClose: () => void;
}) {
  const [form] = Form.useForm<ConnectionFormValues>();
  const rotateKey = Form.useWatch('rotate_api_key', form);
  const { pools, poolsQuery } = resources;
  const mutation = useMutation({
    // A write-only key must not linger in the mutation cache after this editor closes.
    gcTime: 0,
    mutationFn: (values: ConnectionFormValues) => {
      const payload: CreateModelConnectionReq = {
        name: values.name.trim(), provider: values.provider, base_url: values.base_url.trim(),
        quota_pool_id: values.quota_pool_id,
      };
      if (!connection || values.rotate_api_key) payload.api_key = values.api_key ?? '';
      return connection ? modelConnectionApi.update(connection.id, payload) : modelConnectionApi.create(payload);
    },
    onSuccess: () => {
      message.success(connection ? '连接已更新，引用此连接的模型将使用新配置' : '连接已添加');
      void resources.invalidate();
      form.resetFields();
      onClose();
    },
  });
  const poolOptions = pools.map((pool) => ({ value: pool.id, label: pool.name }));
  const poolMissing = !!connection && !pools.some((pool) => pool.id === connection.quota_pool_id);
  return <Modal open title={connection ? '编辑接入连接' : '添加接入连接'} onCancel={onClose}
    onOk={() => form.submit()} confirmLoading={mutation.isPending} okText="保存" cancelText="取消"
    maskClosable={!mutation.isPending} closable={!mutation.isPending}
    cancelButtonProps={{ disabled: mutation.isPending }} keyboard={!mutation.isPending}>
    <MutationFailure error={mutation.error} />
    <Form form={form} name="model-connection" layout="vertical" preserve={false} disabled={mutation.isPending}
      initialValues={connection ? { ...connection, rotate_api_key: false } : { rotate_api_key: false }}
      onFinish={(values) => mutation.mutate(values)}>
      <Form.Item name="name" label="连接名称" rules={[{ required: true, whitespace: true, message: '请输入连接名称' }]}>
        <Input autoFocus placeholder="如：个人 OpenAI 接入" />
      </Form.Item>
      <Form.Item name="provider" label="供应商 / 协议" rules={[{ required: true, message: '请选择供应商' }]}>
        <Select showSearch optionFilterProp="label" options={providerOptions} placeholder="选择供应商" />
      </Form.Item>
      <Form.Item name="base_url" label="Base URL" rules={[
        { required: true, whitespace: true, message: '请输入服务地址' },
        { validator: (_, value: string) => {
          if (!value) return Promise.resolve();
          try {
            const url = new URL(value);
            if (url.protocol === 'http:' || url.protocol === 'https:') return Promise.resolve();
          } catch { /* The field below explains malformed URLs. */ }
          return Promise.reject(new Error('请输入有效的 HTTP 或 HTTPS 地址'));
        } },
      ]} extra="TypeSafe AI 使用 https://api.typesafe.ai，不包含 /v1/systemone。">
        <Input placeholder="https://api.openai.com/v1" />
      </Form.Item>
      {connection && <Form.Item name="rotate_api_key" valuePropName="checked">
        <Checkbox>轮换 API Key（仅写入，不回显）</Checkbox>
      </Form.Item>}
      {(!connection || rotateKey) && <Form.Item name="api_key" label={connection ? '新的 API Key' : 'API Key'}
        extra={connection ? '保存将替换现有凭据；留空将清除 API Key。' : '可选。密钥仅写入，之后不会展示或返回。'}>
        <Input.Password autoComplete="new-password" placeholder={connection ? '输入新密钥，或留空清除' : '无需认证时可留空'} />
      </Form.Item>}
      <Form.Item name="quota_pool_id" label="供应配额池"
        rules={connection ? [{ required: true, message: '请选择自己的配额池' }] : []}
        extra={connection ? '切换后，此连接下所有模型都使用新池的供应额度。' : '不选择时自动创建独立配额池，四项额度均为 0（不限）。'}>
        <Select showSearch optionFilterProp="label" options={poolOptions} allowClear={!connection}
          loading={poolsQuery.isFetching} disabled={mutation.isPending || poolsQuery.isError || poolsQuery.isLoading}
          placeholder={connection ? '选择自己的配额池' : '自动创建独立配额池（不限）'} />
      </Form.Item>
      {poolsQuery.isError && <QueryFailure subject="配额池" error={poolsQuery.error} retry={() => { void poolsQuery.refetch(); }} />}
      {poolMissing && !poolsQuery.isLoading && !poolsQuery.isError &&
        <Alert type="warning" showIcon message="原配额池不可选，请选择一个自己的配额池。" />}
      <Alert type="info" showIcon message="仅可使用自己的连接与配额池。共享池只合并供应额度，不扩大模型使用资格。" />
    </Form>
  </Modal>;
}

export function ConnectionsPanel({ resources, onManagePools }: {
  resources: ManagementResources;
  onManagePools: () => void;
}) {
  const [editing, setEditing] = useState<ModelConnectionResp | null | undefined>();
  const { connections, connectionsQuery, pools } = resources;
  const deletion = useMutation({
    mutationFn: modelConnectionApi.delete,
    onSuccess: () => { message.success('连接已删除'); void resources.invalidate(); },
  });
  const columns: ColumnsType<ModelConnectionResp> = [
    { title: '连接名称', dataIndex: 'name', key: 'name' },
    { title: '供应商', dataIndex: 'provider', key: 'provider', render: displayProvider },
    { title: 'Base URL', dataIndex: 'base_url', key: 'base_url', ellipsis: true },
    { title: '供应配额池', key: 'pool', render: (_, connection) => {
      const pool = pools.find((item) => item.id === connection.quota_pool_id);
      return <div>{pool?.name ?? '配额池名称暂不可用'}<br />{pool && <LimitsSummary limits={pool.limits} />}</div>;
    } },
    { title: '操作', key: 'actions', width: 170, render: (_, connection) => <Space>
      <Button onClick={() => setEditing(connection)} disabled={deletion.isPending}>编辑</Button>
      <Popconfirm title={`删除连接“${connection.name}”？`} description="仅无模型引用的连接可删除；删除模型只会禁用，仍保留引用并阻止连接删除。"
        okText="删除" cancelText="取消" okButtonProps={{ danger: true }} onConfirm={() => deletion.mutate(connection.id)}>
        <Button danger disabled={deletion.isPending} loading={deletion.isPending && deletion.variables === connection.id}>删除</Button>
      </Popconfirm>
    </Space> },
  ];
  return <section aria-label="我的接入连接">
    <div className="model-management-toolbar">
      <p className="model-management-hint">连接集中管理服务地址、供应商与凭据；多个模型可引用同一连接。</p>
      <Space wrap><Button onClick={onManagePools}>管理配额池</Button>
        <Button type="primary" onClick={() => setEditing(null)}>添加连接</Button></Space>
    </div>
    {connectionsQuery.isError && <QueryFailure subject="连接" error={connectionsQuery.error} retry={() => { void connectionsQuery.refetch(); }} />}
    <MutationFailure error={deletion.error} />
    <Table<ModelConnectionResp> rowKey="id" columns={columns} dataSource={connections} loading={connectionsQuery.isLoading}
      scroll={{ x: 850 }} pagination={{ pageSize: 10, hideOnSinglePage: true }}
      locale={{ emptyText: connectionsQuery.isError ? '连接数据不可用' : <Empty description="还没有接入连接。先添加连接，再为模型选择它。" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} />
    {editing !== undefined && <ConnectionEditor key={editing?.id ?? 'new'} connection={editing} resources={resources} onClose={() => setEditing(undefined)} />}
  </section>;
}
