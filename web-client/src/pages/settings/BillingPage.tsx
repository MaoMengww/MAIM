import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Card, Col, Row, Statistic, Table, Progress, Empty, Flex, Tag, Divider, Button, Modal, Input, message } from 'antd';
import { PlusOutlined, WalletOutlined } from '@ant-design/icons';
import { modelApi } from '@/services/model';
import { authApi } from '@/services/auth';
import { useAuthStore } from '@/stores/auth';
import type { BillingModelStat, BillingRecordItem } from '@/types/model';

const PRESET_AMOUNTS = [10, 50, 100, 500];

function toNum(v: unknown): number {
  if (typeof v === 'number') return v;
  if (typeof v === 'string') return parseFloat(v);
  return NaN;
}

function fmtTokens(v: unknown): string {
  if (v == null) return '-';
  const n = toNum(v);
  if (isNaN(n)) return '-';
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
  return n.toLocaleString();
}

function fmtCost(v: unknown): string {
  if (v == null) return '-';
  const n = toNum(v);
  if (isNaN(n)) return '-';
  if (n >= 100) return '¥' + n.toFixed(2);
  if (n >= 0.01) return '¥' + n.toFixed(4);
  return '¥' + n.toFixed(6);
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

const capabilityColor: Record<string, string> = {
  chat: '#1677ff',
  embed: '#52c41a',
  rerank: '#fa8c16',
  vlm: '#eb2f96',
  tts: '#722ed1',
};

export function BillingPage() {
  const [page, setPage] = useState(1);
  const pageSize = 15;
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);
  const queryClient = useQueryClient();

  const [rechargeOpen, setRechargeOpen] = useState(false);
  const [rechargeAmount, setRechargeAmount] = useState<number>(0);
  const [customAmount, setCustomAmount] = useState<string>('');

  const { data, isLoading } = useQuery({
    queryKey: ['billing-stats'],
    queryFn: () => modelApi.billingStats(),
  });

  const { data: recordsData, isLoading: recordsLoading } = useQuery({
    queryKey: ['billing-records', page],
    queryFn: () => modelApi.billingRecords(page, pageSize),
  });

  const rechargeMut = useMutation({
    mutationFn: (amount: number) => authApi.recharge(amount),
    onSuccess: (resp) => {
      message.success(`充值成功，当前余额 ¥${resp.new_balance.toFixed(2)}`);
      if (user) {
        setUser({ ...user, balance: resp.new_balance });
      }
      setRechargeOpen(false);
      setCustomAmount('');
      setRechargeAmount(0);
    },
    onError: (err: any) => {
      message.error(err?.response?.data?.message || '充值失败');
    },
  });

  const handlePresetClick = (amount: number) => {
    setRechargeAmount(amount);
    setCustomAmount('');
  };

  const handleCustomChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = e.target.value;
    setCustomAmount(val);
    const num = parseFloat(val);
    if (!isNaN(num) && num > 0) {
      setRechargeAmount(num);
    } else {
      setRechargeAmount(0);
    }
  };

  const handleRecharge = () => {
    if (rechargeAmount <= 0) {
      message.warning('请输入充值金额');
      return;
    }
    rechargeMut.mutate(rechargeAmount);
  };

  const models = data?.by_model ?? [];
  const maxCost = Math.max(...models.map((m) => m.total_cost), 0);
  const records = recordsData?.items ?? [];
  const total = recordsData?.total ?? 0;

  const summaryColumns = [
    {
      title: '模型', dataIndex: 'model_name', key: 'model_name',
      render: (name: string, r: BillingModelStat) => (
        <Flex align="center" gap={8}>
          {name}
          <Tag color={capabilityColor[r.capability] || '#666'} style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
            {r.capability}
          </Tag>
        </Flex>
      ),
    },
    {
      title: '输入 Tokens', dataIndex: 'input_tokens', key: 'input_tokens', width: 130,
      render: (v: number) => fmtTokens(v),
    },
    {
      title: '输出 Tokens', dataIndex: 'output_tokens', key: 'output_tokens', width: 130,
      render: (v: number) => fmtTokens(v),
    },
    {
      title: '费用占比', key: 'cost_bar', width: 200,
      render: (_: any, r: BillingModelStat) => (
        <Progress
          percent={maxCost > 0 ? Math.round((r.total_cost / maxCost) * 100) : 0}
          size="small"
          format={() => fmtCost(r.total_cost)}
          style={{ margin: 0 }}
        />
      ),
    },
  ];

  const recordColumns = [
    {
      title: '扣费时间', dataIndex: 'created_at', key: 'created_at', width: 150,
      render: (v: string) => fmtTime(v),
    },
    {
      title: '模型', dataIndex: 'model_name', key: 'model_name',
      render: (name: string, r: BillingRecordItem) => (
        <Flex align="center" gap={6}>
          {name}
          <Tag color={capabilityColor[r.capability] || '#666'} style={{ margin: 0, fontSize: 10, lineHeight: '16px' }}>
            {r.capability}
          </Tag>
        </Flex>
      ),
    },
    {
      title: '输入 Tokens', dataIndex: 'input_tokens', key: 'input_tokens', width: 110,
      render: (v: number) => fmtTokens(v),
    },
    {
      title: '输出 Tokens', dataIndex: 'output_tokens', key: 'output_tokens', width: 110,
      render: (v: number) => fmtTokens(v),
    },
    {
      title: '输入费用', dataIndex: 'input_cost', key: 'input_cost', width: 100,
      render: (v: number) => fmtCost(v),
    },
    {
      title: '输出费用', dataIndex: 'output_cost', key: 'output_cost', width: 100,
      render: (v: number) => fmtCost(v),
    },
    {
      title: '合计', dataIndex: 'total_cost', key: 'total_cost', width: 100,
      render: (v: number) => <span style={{ fontWeight: 500 }}>{fmtCost(v)}</span>,
    },
    {
      title: '提供商', dataIndex: 'provider', key: 'provider', width: 100,
    },
  ];

  return (
    <div style={{ flex: 1, padding: 32, overflowY: 'auto' }}>
      <Flex align="center" justify="space-between" style={{ marginBottom: 24 }}>
        <h2 style={{ color: 'var(--aim-text)', margin: 0, fontSize: 20 }}>账单管理</h2>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setRechargeOpen(true)}>
          充值
        </Button>
      </Flex>

      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col span={6}>
          <Card>
            <Statistic
              title={<Flex align="center" gap={6}><WalletOutlined /> 当前余额</Flex>}
              value={user?.balance ?? 0}
              precision={2}
              prefix="¥"
              valueStyle={{ color: '#52c41a' }}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="总费用"
              value={data?.total_cost ?? 0}
              precision={4}
              prefix="¥"
              loading={isLoading}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="输入 Tokens"
              value={data?.total_input_tokens ?? 0}
              formatter={(v) => fmtTokens(Number(v))}
              loading={isLoading}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="输出 Tokens"
              value={data?.total_output_tokens ?? 0}
              formatter={(v) => fmtTokens(Number(v))}
              loading={isLoading}
            />
          </Card>
        </Col>
      </Row>

      {models.length > 0 && (
        <Table
          dataSource={models}
          columns={summaryColumns}
          rowKey={(r: BillingModelStat) => `${r.model_name}-${r.capability}`}
          loading={isLoading}
          pagination={false}
          size="small"
          style={{ marginBottom: 32 }}
        />
      )}

      <Divider />

      <Flex align="center" justify="space-between" style={{ marginBottom: 16 }}>
        <h3 style={{ color: 'var(--aim-text)', margin: 0, fontSize: 16 }}>逐笔流水</h3>
      </Flex>

      {records.length === 0 && models.length === 0 ? (
        <Empty description="暂无数据" />
      ) : (
        <Table
          dataSource={records}
          columns={recordColumns}
          rowKey="id"
          loading={recordsLoading}
          size="small"
          pagination={{
            current: page,
            pageSize,
            total,
            onChange: (p) => setPage(p),
            showTotal: (t) => `共 ${t} 条`,
            showSizeChanger: false,
          }}
        />
      )}

      <Modal
        title="充值"
        open={rechargeOpen}
        onOk={handleRecharge}
        onCancel={() => { setRechargeOpen(false); setCustomAmount(''); setRechargeAmount(0); }}
        confirmLoading={rechargeMut.isPending}
        okText="确认充值"
        destroyOnClose
      >
        <div style={{ padding: '8px 0' }}>
          <p style={{ color: 'var(--aim-text-secondary)', marginBottom: 16, fontSize: 14 }}>
            选择充值金额
          </p>
          <Flex gap={12} wrap="wrap" style={{ marginBottom: 20 }}>
            {PRESET_AMOUNTS.map((amt) => (
              <Button
                key={amt}
                type={rechargeAmount === amt && !customAmount ? 'primary' : 'default'}
                size="large"
                style={{ width: 80, height: 56, fontSize: 16 }}
                onClick={() => handlePresetClick(amt)}
              >
                ¥{amt}
              </Button>
            ))}
          </Flex>
          <div style={{ marginTop: 8 }}>
            <span style={{ color: 'var(--aim-text-secondary)', fontSize: 13, marginRight: 12 }}>自定义金额</span>
            <Input
              style={{ width: 200 }}
              placeholder="请输入金额"
              prefix="¥"
              value={customAmount}
              onChange={handleCustomChange}
              type="number"
              min={0}
              step={0.01}
            />
          </div>
          {rechargeAmount > 0 && (
            <div style={{ marginTop: 16, padding: '12px 16px', background: 'var(--aim-surface)', borderRadius: 8 }}>
              <span style={{ color: 'var(--aim-text-secondary)' }}>充值金额：</span>
              <span style={{ fontSize: 20, fontWeight: 600, color: '#52c41a' }}>¥{rechargeAmount.toFixed(2)}</span>
            </div>
          )}
        </div>
      </Modal>
    </div>
  );
}
