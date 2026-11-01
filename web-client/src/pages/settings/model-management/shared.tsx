import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import { Alert, Button, Form, InputNumber, Space } from 'antd';
import { modelConnectionApi, quotaPoolApi } from '@/services/model';
import type { ModelManagementList } from '@/services/model';
import { useAuthStore } from '@/stores/auth';
import type { CapacityLimits, ModelConnectionResp, QuotaPoolResp } from '@/types/model';

export const unlimitedLimits: CapacityLimits = {
  requests_per_minute: 0,
  requests_per_second: 0,
  tokens_per_second: 0,
  concurrency: 0,
};

export function completeLimits(limits?: CapacityLimits): CapacityLimits {
  return { ...unlimitedLimits, ...limits };
}

export function managementError(error: unknown, fallback: string): string {
  if (error && typeof error === 'object' && 'response' in error) {
    const response = error.response;
    if (response && typeof response === 'object' && 'data' in response) {
      const data = response.data;
      if (data && typeof data === 'object') {
        if ('message' in data && typeof data.message === 'string' && data.message) return data.message;
        if ('msg' in data && typeof data.msg === 'string' && data.msg) return data.msg;
      }
    }
  }
  return fallback;
}

export function QueryFailure({ error, retry, subject }: { error: unknown; retry: () => void; subject: string }) {
  return <Alert className="model-management-alert" type="error" showIcon role="alert"
    message={`${subject}加载失败`} description={managementError(error, '请检查网络后重试。')}
    action={<Button onClick={retry}>重试</Button>} />;
}

export function MutationFailure({ error }: { error: unknown }) {
  return error ? <Alert className="model-management-alert" type="error" showIcon role="alert"
    message={managementError(error, '操作失败，请重试。')} /> : null;
}

const limitDimensions: { key: keyof CapacityLimits; label: string }[] = [
  { key: 'requests_per_minute', label: '每分钟请求数 (RPM)' },
  { key: 'requests_per_second', label: '每秒请求数 (RPS)' },
  { key: 'tokens_per_second', label: '每秒输入 Token 预算 (TPS)' },
  { key: 'concurrency', label: '并发调用数' },
];

export function CapacityFields({ field, legend }: { field: 'limits' | 'model_limits'; legend: string }) {
  return <fieldset className="model-management-limits">
    <legend>{legend}</legend>
    <p className="model-management-hint">每项均为供应商明确的额度，0 表示不限；不配置任何默认供应限额。</p>
    <p className="model-management-hint">TPS 按请求发送前的保守输入估算准入，不是计费用量；单次估算超过一秒预算会明确拒绝。</p>
    <div className="model-management-form-grid">
      {limitDimensions.map(({ key, label }) => <Form.Item key={key} name={[field, key]} label={label}
        rules={[{ required: true, type: 'integer', min: 0, max: 2147483647, message: '请输入 0 至 2147483647 的整数，0 表示不限' }]}>
        <InputNumber min={0} max={2147483647} precision={0} className="model-management-number" />
      </Form.Item>)}
    </div>
  </fieldset>;
}

export function LimitsSummary({ limits }: { limits?: CapacityLimits }) {
  const values = completeLimits(limits);
  return <Space size={[12, 4]} wrap className="model-management-limit-summary">
    <span>RPM {values.requests_per_minute || '不限'}</span>
    <span>RPS {values.requests_per_second || '不限'}</span>
    <span>TPS {values.tokens_per_second || '不限'}</span>
    <span>并发 {values.concurrency || '不限'}</span>
  </Space>;
}

export function useModelManagementResources(): ManagementResources {
  const currentUserId = useAuthStore((s) => s.user?.id);
  const queryClient = useQueryClient();
  const connectionsQuery = useQuery({
    queryKey: ['model-connections', currentUserId],
    queryFn: modelConnectionApi.list,
    enabled: !!currentUserId,
  });
  const poolsQuery = useQuery({
    queryKey: ['quota-pools', currentUserId],
    queryFn: quotaPoolApi.list,
    enabled: !!currentUserId,
  });
  // The server is authoritative; also keep all management choices explicitly own-only.
  const connections = (connectionsQuery.data?.list ?? []).filter((item) =>
    item.owner_type === 'user' && item.owner_id === currentUserId);
  const pools = (poolsQuery.data?.list ?? []).filter((item) =>
    item.owner_type === 'user' && item.owner_id === currentUserId);
  const invalidate = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: ['models'] }),
    queryClient.invalidateQueries({ queryKey: ['model-connections'] }),
    queryClient.invalidateQueries({ queryKey: ['quota-pools'] }),
  ]);
  return { currentUserId, connectionsQuery, poolsQuery, connections, pools, invalidate };
}

export interface ManagementResources {
  currentUserId: string | undefined;
  connectionsQuery: UseQueryResult<ModelManagementList<ModelConnectionResp>, Error>;
  poolsQuery: UseQueryResult<ModelManagementList<QuotaPoolResp>, Error>;
  connections: ModelConnectionResp[];
  pools: QuotaPoolResp[];
  invalidate: () => Promise<unknown>;
}
