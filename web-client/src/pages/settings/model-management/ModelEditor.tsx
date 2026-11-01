import { useMutation } from '@tanstack/react-query';
import { Alert, Button, Form, Input, InputNumber, Modal, Select, message } from 'antd';
import { modelApi } from '@/services/model';
import type { CreateModelReq } from '@/types/api';
import type { ModelResp } from '@/types/model';
import { capabilityOptions } from '@/utils/capability';
import { displayProvider } from '@/utils/provider';
import { CapacityFields, completeLimits, LimitsSummary, MutationFailure, QueryFailure } from './shared';
import type { ManagementResources } from './shared';

export function ModelEditor({ model, resources, onClose, onManageConnections }: {
  model: ModelResp | null;
  resources: ManagementResources;
  onClose: () => void;
  onManageConnections: () => void;
}) {
  const [form] = Form.useForm<CreateModelReq>();
  const connectionId = Form.useWatch('connection_id', form);
  const { connections, connectionsQuery, pools, poolsQuery } = resources;
  const selectedConnection = connections.find((connection) => connection.id === connectionId);
  const selectedPool = pools.find((pool) => pool.id === selectedConnection?.quota_pool_id);
  const mutation = useMutation({
    mutationFn: async (values: CreateModelReq) => {
      const payload: CreateModelReq = {
        model_name: values.model_name.trim(), capability: values.capability, connection_id: values.connection_id,
        input_price_per_mtok: values.input_price_per_mtok, output_price_per_mtok: values.output_price_per_mtok,
        model_limits: completeLimits(values.model_limits),
      };
      return model ? modelApi.update(model.id, payload) : modelApi.create(payload);
    },
    onSuccess: () => { message.success(model ? '模型已更新' : '模型已添加'); void resources.invalidate(); onClose(); },
  });
  const unavailable = connectionsQuery.isLoading || connectionsQuery.isError || connections.length === 0;
  return <Modal open title={model ? '编辑模型' : '添加模型'} onCancel={onClose}
    onOk={() => form.submit()} confirmLoading={mutation.isPending} okText="保存" cancelText="取消"
    okButtonProps={{ disabled: unavailable }} cancelButtonProps={{ disabled: mutation.isPending }}
    maskClosable={!mutation.isPending} closable={!mutation.isPending} keyboard={!mutation.isPending}>
    <MutationFailure error={mutation.error} />
    {connectionsQuery.isError && <QueryFailure subject="接入连接" error={connectionsQuery.error} retry={() => { void connectionsQuery.refetch(); }} />}
    {!connectionsQuery.isLoading && !connectionsQuery.isError && connections.length === 0 &&
      <Alert className="model-management-alert" type="info" showIcon message="请先创建自己的接入连接"
        action={<Button onClick={onManageConnections}>管理连接</Button>} />}
    <Form form={form} name="model-entry" layout="vertical" preserve={false} disabled={mutation.isPending}
      initialValues={{
        model_name: model?.model_name ?? '', capability: model?.capability, connection_id: model?.connection_id,
        input_price_per_mtok: model?.input_price_per_mtok ?? 0,
        output_price_per_mtok: model?.output_price_per_mtok ?? 0,
        model_limits: completeLimits(model?.model_limits),
      }} onFinish={(values) => mutation.mutate(values)}>
      <Form.Item name="model_name" label="模型名称" rules={[{ required: true, whitespace: true, message: '请输入模型名称' }]}
        extra="填写供应商实际可调用的名称；TypeSafe AI 请填写固定的 Jev 模型版本。">
        <Input autoFocus placeholder="如 gpt-4o" />
      </Form.Item>
      <Form.Item name="connection_id" label="接入连接" rules={[
        { required: true, message: '请选择自己的接入连接' },
        { validator: (_, value: string) => !value || connections.some((connection) => connection.id === value)
          ? Promise.resolve() : Promise.reject(new Error('此连接不可选，请选择一个自己的连接')) },
      ]} extra="供应商、服务地址和 API Key 统一在连接中管理，模型不再单独保存凭据。">
        <Select showSearch optionFilterProp="label" placeholder="选择自己的接入连接" disabled={mutation.isPending || unavailable}
          loading={connectionsQuery.isFetching}
          options={connections.map((connection) => ({ value: connection.id, label: `${connection.name} · ${displayProvider(connection.provider)}` }))} />
      </Form.Item>
      {selectedConnection && <div className="model-management-selection" role="status">
        <p>{displayProvider(selectedConnection.provider)} · {selectedConnection.base_url}</p>
        <p>供应配额池：{selectedPool?.name ?? (poolsQuery.isLoading ? '加载中…' : '配额池信息暂不可用')}</p>
        {selectedPool && <LimitsSummary limits={selectedPool.limits} />}
      </div>}
      <Form.Item name="capability" label="模型能力" rules={[{ required: true, message: '请选择模型能力' }]}
        extra="结构化判断使用 TypeSafe AI System One 文本接口，支持 Noul、Choice、Score。">
        <Select options={capabilityOptions} placeholder="选择能力" />
      </Form.Item>
      <div className="model-management-form-grid">
        <Form.Item name="input_price_per_mtok" label="输入价格 (¥/MTok)"
          rules={[{ required: true, type: 'number', min: 0, message: '请输入非负价格' }]}>
          <InputNumber min={0} step={0.01} className="model-management-number" />
        </Form.Item>
        <Form.Item name="output_price_per_mtok" label="输出价格 (¥/MTok)"
          rules={[{ required: true, type: 'number', min: 0, message: '请输入非负价格' }]} extra="免费时填 0，真实 token 用量仍记录。">
          <InputNumber min={0} step={0.01} className="model-management-number" />
        </Form.Item>
      </div>
      <CapacityFields field="model_limits" legend="可选模型子限额" />
      <p className="model-management-hint">仅在供应商有模型级限制时设置。全部填 0 表示无额外子限额；池内所有模型仍共同受供应配额池约束。</p>
    </Form>
  </Modal>;
}
