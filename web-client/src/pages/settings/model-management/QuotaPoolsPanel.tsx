import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Alert, Button, Empty, Form, Input, Modal, Popconfirm, Space, Table, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { quotaPoolApi } from '@/services/model';
import type { CreateQuotaPoolReq } from '@/types/api';
import type { QuotaPoolResp } from '@/types/model';
import { CapacityFields, completeLimits, LimitsSummary, MutationFailure, QueryFailure } from './shared';
import type { ManagementResources } from './shared';

function PoolEditor({ pool, resources, onClose }: {
  pool: QuotaPoolResp | null;
  resources: ManagementResources;
  onClose: () => void;
}) {
  const [form] = Form.useForm<CreateQuotaPoolReq>();
  const mutation = useMutation({
    mutationFn: (values: CreateQuotaPoolReq) => {
      const payload = { name: values.name.trim(), limits: completeLimits(values.limits) };
      return pool ? quotaPoolApi.update(pool.id, payload) : quotaPoolApi.create(payload);
    },
    onSuccess: () => { message.success(pool ? '配额池已更新' : '配额池已添加'); void resources.invalidate(); onClose(); },
  });
  const references = resources.connections.filter((connection) => connection.quota_pool_id === pool?.id);
  return <Modal open title={pool ? '编辑供应配额池' : '添加供应配额池'} onCancel={onClose}
    onOk={() => form.submit()} confirmLoading={mutation.isPending} okText="保存" cancelText="取消"
    maskClosable={!mutation.isPending} closable={!mutation.isPending} keyboard={!mutation.isPending}
    cancelButtonProps={{ disabled: mutation.isPending }}>
    <MutationFailure error={mutation.error} />
    <Form form={form} name="quota-pool" layout="vertical" disabled={mutation.isPending} preserve={false}
      initialValues={{ name: pool?.name ?? '', limits: completeLimits(pool?.limits) }}
      onFinish={(values) => mutation.mutate(values)}>
      <Form.Item name="name" label="配额池名称" rules={[{ required: true, whitespace: true, message: '请输入配额池名称' }]}>
        <Input autoFocus placeholder="如：个人供应商共享额度" />
      </Form.Item>
      <CapacityFields field="limits" legend="供应额度" />
      <Alert type="info" showIcon message={references.length > 0
        ? `此池已关联 ${references.length} 个自己的连接，修改额度对这些连接下的全部模型共同生效。`
        : '仅在供应商实际共享额度时，将自己的多个连接显式关联到同一池。'}
        description="四项额度独立生效；0 表示该维度不限，而非禁用连接。共享池不会扩大任何模型的使用资格。" />
    </Form>
  </Modal>;
}

export function QuotaPoolsPanel({ resources }: { resources: ManagementResources }) {
  const [editing, setEditing] = useState<QuotaPoolResp | null | undefined>();
  const { pools, poolsQuery, connections, connectionsQuery } = resources;
  const deletion = useMutation({
    mutationFn: quotaPoolApi.delete,
    onSuccess: () => { message.success('配额池已删除'); void resources.invalidate(); },
  });
  const columns: ColumnsType<QuotaPoolResp> = [
    { title: '配额池名称', dataIndex: 'name', key: 'name' },
    { title: '供应额度（0 = 不限）', key: 'limits', render: (_, pool) => <LimitsSummary limits={pool.limits} /> },
    { title: '关联连接', key: 'connections', render: (_, pool) => {
      if (connectionsQuery.isLoading) return '加载中…';
      if (connectionsQuery.isError) return '连接数据不可用';
      const names = connections.filter((connection) => connection.quota_pool_id === pool.id).map((connection) => connection.name);
      return names.length ? names.join('、') : '未关联';
    } },
    { title: '操作', key: 'actions', width: 170, render: (_, pool) => <Space>
      <Button onClick={() => setEditing(pool)} disabled={deletion.isPending}>编辑</Button>
      <Popconfirm title={`删除配额池“${pool.name}”？`} description="仍被连接或模型引用的配额池不能删除，请先解除引用。"
        okText="删除" cancelText="取消" okButtonProps={{ danger: true }} onConfirm={() => deletion.mutate(pool.id)}>
        <Button danger disabled={deletion.isPending} loading={deletion.isPending && deletion.variables === pool.id}>删除</Button>
      </Popconfirm>
    </Space> },
  ];
  return <section aria-label="我的供应配额池">
    <div className="model-management-toolbar">
      <p className="model-management-hint">供应额度按池共同计算，所有维度均显式配置；仅管理自己的池。</p>
      <Button type="primary" onClick={() => setEditing(null)}>添加配额池</Button>
    </div>
    {poolsQuery.isError && <QueryFailure subject="配额池" error={poolsQuery.error} retry={() => { void poolsQuery.refetch(); }} />}
    {connectionsQuery.isError && <QueryFailure subject="关联连接" error={connectionsQuery.error} retry={() => { void connectionsQuery.refetch(); }} />}
    <MutationFailure error={deletion.error} />
    <Table<QuotaPoolResp> rowKey="id" columns={columns} dataSource={pools} loading={poolsQuery.isLoading}
      scroll={{ x: 800 }} pagination={{ pageSize: 10, hideOnSinglePage: true }}
      locale={{ emptyText: poolsQuery.isError ? '配额池数据不可用' : <Empty description="暂无供应配额池。添加连接时可自动创建独立池，也可在此创建共享池。" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} />
    {editing !== undefined && <PoolEditor key={editing?.id ?? 'new'} pool={editing} resources={resources} onClose={() => setEditing(undefined)} />}
  </section>;
}
