import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { CrownOutlined } from '@ant-design/icons';
import { Alert, Button, Empty, Popconfirm, Select, Space, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { modelApi } from '@/services/model';
import type { ModelCapability, ModelResp } from '@/types/model';
import { capabilityOptions, displayCapability } from '@/utils/capability';
import { displayProvider } from '@/utils/provider';
import { ModelEditor } from './ModelEditor';
import { completeLimits, LimitsSummary, MutationFailure, QueryFailure } from './shared';
import type { ManagementResources } from './shared';

function formatPrice(value: number | undefined): string {
  if (value == null) return '-';
  return value === 0 ? '免费 (¥0)' : `¥${value.toLocaleString('zh-CN', { maximumFractionDigits: 6 })}`;
}

export function ModelsPanel({ resources, onManageConnections }: {
  resources: ManagementResources;
  onManageConnections: () => void;
}) {
  const [editing, setEditing] = useState<ModelResp | null | undefined>();
  const [capability, setCapability] = useState<ModelCapability>();
  const { currentUserId, connections, connectionsQuery, pools } = resources;
  const modelsQuery = useQuery({
    queryKey: ['models', 'manage', currentUserId, capability],
    queryFn: () => modelApi.list(capability),
    enabled: !!currentUserId,
  });
  const deletion = useMutation({
    mutationFn: modelApi.delete,
    onSuccess: () => { message.success('模型已删除'); void resources.invalidate(); },
  });
  const columns: ColumnsType<ModelResp> = [
    { title: '模型名称', dataIndex: 'model_name', key: 'name', render: (name: string, model) => <Space wrap>
      <span>{name}</span>{model.owner_type === 'platform' && <Tag icon={<CrownOutlined />} color="gold">官方</Tag>}
    </Space> },
    { title: '接入连接', key: 'connection', render: (_, model) => <div>
      {model.owner_type === 'platform' ? '平台连接' : connections.find((connection) => connection.id === model.connection_id)?.name ?? '连接名称暂不可用'}
      <div className="model-management-hint">{displayProvider(model.provider)}</div>
    </div> },
    { title: '能力', dataIndex: 'capability', key: 'capability', render: (value: string) =>
      <Tag color={value === 'judge' ? 'cyan' : 'default'}>{displayCapability(value)}</Tag> },
    { title: '供应配额', key: 'limits', render: (_, model) => {
      const pool = pools.find((item) => item.id === model.quota_pool_id);
      const sublimits = completeLimits(model.model_limits);
      return <div><span>{pool?.name ?? (model.owner_type === 'platform' ? '平台配额池' : '供应配额池')}</span><br />
        <LimitsSummary limits={model.pool_limits} />
        {Object.values(sublimits).some((value) => value > 0)
          ? <div className="model-management-sublimits">模型子限额：<LimitsSummary limits={sublimits} /></div>
          : <div className="model-management-hint">无额外模型子限额</div>}
      </div>;
    } },
    { title: '状态', dataIndex: 'status', key: 'status', render: (value: string) => <Tag color={value === 'active' ? 'green' : 'default'}>{value}</Tag> },
    { title: '输入价格 (¥/MTok)', dataIndex: 'input_price_per_mtok', key: 'input_price', render: formatPrice },
    { title: '输出价格 (¥/MTok)', dataIndex: 'output_price_per_mtok', key: 'output_price', render: formatPrice },
    { title: '操作', key: 'actions', width: 170, render: (_, model) =>
      model.owner_type === 'user' && model.owner_id === currentUserId ? <Space>
        <Button onClick={() => setEditing(model)} disabled={deletion.isPending}>编辑</Button>
        <Popconfirm title={`删除模型“${model.model_name}”？`} description="删除后将不能再选择此模型。"
          okText="删除" cancelText="取消" okButtonProps={{ danger: true }} onConfirm={() => deletion.mutate(model.id)}>
          <Button danger disabled={deletion.isPending} loading={deletion.isPending && deletion.variables === model.id}>删除</Button>
        </Popconfirm>
      </Space> : <span className="model-management-hint">平台管理</span> },
  ];
  const noConnections = !connectionsQuery.isLoading && !connectionsQuery.isError && connections.length === 0;
  return <section aria-label="可用模型管理">
    <div className="model-management-toolbar">
      <p className="model-management-hint">模型选择自己的接入连接；平台模型仍可按既有资格使用，不能在此修改。</p>
      <Space wrap>
        <Select<ModelCapability> aria-label="按模型能力筛选" placeholder="全部能力" allowClear value={capability}
          options={capabilityOptions} onChange={setCapability} className="model-management-filter" />
        <Button type="primary" onClick={() => setEditing(null)} disabled={connectionsQuery.isLoading || connectionsQuery.isError || noConnections}>添加模型</Button>
      </Space>
    </div>
    {connectionsQuery.isError && <QueryFailure subject="接入连接" error={connectionsQuery.error} retry={() => { void connectionsQuery.refetch(); }} />}
    {noConnections && <Alert className="model-management-alert" type="info" showIcon message="添加模型前，请先配置自己的接入连接。"
      action={<Button onClick={onManageConnections}>管理连接</Button>} />}
    {modelsQuery.isError && <QueryFailure subject="模型" error={modelsQuery.error} retry={() => { void modelsQuery.refetch(); }} />}
    <MutationFailure error={deletion.error} />
    <Table<ModelResp> rowKey="id" dataSource={modelsQuery.data?.list ?? []} columns={columns} loading={modelsQuery.isLoading}
      scroll={{ x: 1250 }} pagination={{ pageSize: 10, hideOnSinglePage: true }}
      locale={{ emptyText: modelsQuery.isError ? '模型数据不可用' : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={capability ? `暂无 ${displayCapability(capability)} 模型，可切换能力筛选或添加模型。` : '暂无可用模型。配置接入连接后即可添加自己的模型。'} /> }} />
    {editing !== undefined && <ModelEditor key={editing?.id ?? 'new'} model={editing} resources={resources}
      onClose={() => setEditing(undefined)} onManageConnections={() => { setEditing(undefined); onManageConnections(); }} />}
  </section>;
}
