import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Checkbox, Divider, Form, Input, Modal, Space, Spin, Tag, Upload, message } from 'antd';
import { CloudUploadOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { skillApi } from '@/services/skill';
import type { SkillCandidate, SkillPackage } from '@/types/model';
import { SourceCandidateList } from '@/components/skills/CandidateGovernance';
import { SkillVersionGovernance } from '@/components/skills/SkillVersionGovernance';
import { submitSkillIntent } from '@/utils/skillSubmission';
import { useAuthStore } from '@/stores/auth';

const STATUS_LABEL: Record<string, { text: string; color: string }> = {
  pending_validation: { text: '待验证', color: 'default' },
  evaluated: { text: '验证通过 · 待批准', color: 'blue' },
  validation_failed: { text: '验证失败', color: 'red' },
  approved: { text: '已批准 · 待发布', color: 'gold' },
  rejected: { text: '已拒绝', color: 'default' },
  published: { text: '已发布', color: 'green' },
  expired: { text: '已过期', color: 'default' },
};

const EVALUATION_LABEL: Record<string, string> = {
  passed: '通过',
  failed: '不通过',
  insufficient: '证据不足',
  error: '执行故障（非技能结论）',
};

async function fileBase64(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let binary = '';
  const chunk = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunk) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunk));
  }
  return btoa(binary);
}

function candidateEvidence(candidate: SkillCandidate) {
  const evaluation = candidate.evaluation;
  if (!evaluation) return <span style={{ color: 'var(--aim-text-tertiary)' }}>尚未验证</span>;
  return <div style={{ fontSize: 12, color: 'var(--aim-text-secondary)', lineHeight: 1.6 }}>
    <div>验证结论：{EVALUATION_LABEL[evaluation.status] ?? evaluation.status} · {evaluation.reason}</div>
    <div>候选摘要 {candidate.digest.slice(0, 12)} · 验证 {evaluation.digest.slice(0, 12)} · 模型 {evaluation.model_version || '未记录'}</div>
    {candidate.status === 'validation_failed' && <div>需修改内容后重新导入并验证；旧证据不能授权新内容。</div>}
  </div>;
}

function CandidateRow({ botId, pkg, candidate }: { botId: string; pkg: SkillPackage; candidate: SkillCandidate }) {
  const queryClient = useQueryClient();
  const userId = useAuthStore((state) => state.user?.id);
  const [confirmed, setConfirmed] = useState(false);
  const actionRequest = (action: 'evaluate' | 'approve' | 'publish') => {
    const payload = { digest: candidate.digest };
    return submitSkillIntent(`${userId}:${botId}:${candidate.id}:${action}`, payload,
      async (submission_key) => skillApi[action](botId, candidate.id, { ...payload, submission_key }));
  };
  const invalidate = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: ['bot-skills', botId] }),
    queryClient.invalidateQueries({ queryKey: ['skill-versions', botId] }),
  ]);
  const onError = (err: unknown) => {
    const value = err as { response?: { data?: { message?: string } } };
    message.error(`${value?.response?.data?.message || '操作失败'}；相同意图保留原提交键，过时摘要不会自动替换，请手动刷新后重新审阅。`);
  };
  const evaluate = useMutation({ mutationFn: () => actionRequest('evaluate'), retry: false, onSuccess: invalidate, onError });
  const approve = useMutation({ mutationFn: () => actionRequest('approve'), retry: false, onSuccess: invalidate, onError });
  const publish = useMutation({ mutationFn: () => actionRequest('publish'), retry: false, onSuccess: invalidate, onError });
  const published = pkg.versions.find((version) => version.version_id === candidate.published_version_id);
  const status = STATUS_LABEL[candidate.status] ?? { text: candidate.status, color: 'default' };
  return <div style={{ border: '1px solid var(--aim-border)', borderRadius: 8, padding: 12, background: 'var(--aim-surface)' }}>
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      <strong style={{ fontSize: 13 }}>{candidate.name}</strong>
      <Tag color={status.color} style={{ margin: 0 }}>{status.text}</Tag>
      <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>{candidate.license} · {candidate.digest.slice(0, 12)}</span>
      {published && <Tag color="green" style={{ margin: 0 }}>v{published.version}</Tag>}
    </div>
    <div style={{ fontSize: 12, color: 'var(--aim-text-secondary)', margin: '6px 0' }}>{candidate.description}</div>
    {candidateEvidence(candidate)}
    {candidate.failure && <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>{candidate.failure}</div>}
    {candidate.status === 'evaluated' && <Checkbox checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)}>我已核对当前确切内容与验证证据，确认发布批准（不是执行前代码审查）</Checkbox>}
    <Space style={{ marginTop: 8 }} wrap>
      {['pending_validation', 'validation_failed'].includes(candidate.status)
        && <Button size="small" loading={evaluate.isPending} onClick={() => evaluate.mutate()}>运行代表任务验证</Button>}
      {candidate.status === 'evaluated'
        && <Button size="small" type="primary" disabled={!confirmed || candidate.evaluation?.status !== 'passed'} loading={approve.isPending} onClick={() => approve.mutate()}>批准确切内容与证据</Button>}
      {candidate.status === 'approved'
        && <Button size="small" type="primary" loading={publish.isPending} onClick={() => publish.mutate()}>发布不可变版本</Button>}
    </Space>
  </div>;
}

export function BotSkillSection({ botId, canManage }: { botId: string; canManage: boolean }) {
  const queryClient = useQueryClient();
  const userId = useAuthStore((state) => state.user?.id);
  const [importOpen, setImportOpen] = useState(false);
  const [file, setFile] = useState<File>();
  const [form] = Form.useForm();
  const { data = [], isLoading, isError, isFetching, refetch } = useQuery({
    queryKey: ['bot-skills', botId, userId],
    queryFn: () => skillApi.list(botId),
    enabled: canManage,
    retry: false, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  const importMutation = useMutation({
    mutationFn: async (values: { representative_task: string; expected_output: string }) => {
      if (!file) throw new Error('请选择 zip 包');
      const payload = { archive: await fileBase64(file), representative_task: values.representative_task, expected_output: values.expected_output };
      return submitSkillIntent(`${userId}:${botId}:import`, payload,
        (submission_key) => skillApi.importPackage(botId, { ...payload, submission_key }));
    },
    onSuccess: () => {
      message.success('包已导入为待验证候选');
      setImportOpen(false);
      setFile(undefined);
      form.resetFields();
      queryClient.invalidateQueries({ queryKey: ['bot-skills', botId] });
    },
    onError: (err: unknown) => {
      const value = err as { response?: { data?: { message?: string } }; message?: string };
      message.error(value?.response?.data?.message || value?.message || '导入失败');
    },
  });
  return <>
    <SourceCandidateList botId={botId} />
    <SkillVersionGovernance botId={botId} />
    {canManage && <div style={{ marginBottom: 24 }}>
    <Divider orientation="left" style={{ color: 'var(--aim-text-secondary)', fontSize: 13 }}>Instruction skills · 已导入包</Divider>
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
      <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)', display: 'flex', alignItems: 'center', gap: 6 }}>
        <SafetyCertificateOutlined /> 纯指令包 · 验证、批准后发布不可变版本
      </span>
      <Button size="small" icon={<CloudUploadOutlined />} onClick={() => setImportOpen(true)}>导入 zip 包</Button>
      <Button loading={isFetching} onClick={() => void refetch()}>刷新已导入包</Button>
    </div>
    {isError ? <p role="alert">技能包列表读取失败或当前权限不足；不展示缓存材料，请刷新重新核验。</p>
      : isLoading ? <div style={{ textAlign: 'center', padding: 16 }}><Spin /></div>
      : data.length === 0 ? <div style={{ fontSize: 13, color: 'var(--aim-text-tertiary)', padding: '8px 0' }}>
        尚无技能包。包必须包含 SKILL.md（name、description、license）与 references/ 或 assets/ 文本资源；不支持脚本、模型覆盖、fork、自动 MCP 或命令扩展。
      </div>
      : <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {data.map((pkg) => <div key={pkg.skill_id}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8, flexWrap: 'wrap' }}>
            <strong style={{ fontSize: 14 }}>{pkg.name}</strong>
            <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>{pkg.license} · 发布者 {pkg.publisher_id.slice(0, 8)}</span>
            {pkg.enabled_version && <Tag color="green" style={{ margin: 0 }}>已启用 v{pkg.enabled_version.version}</Tag>}
          </div>
          <div style={{ fontSize: 12, color: 'var(--aim-text-secondary)', marginBottom: 8 }}>
            已发布：{pkg.versions.length === 0 ? '无' : pkg.versions.map((version) => `v${version.version}${version.enabled ? '（启用）' : ''}`).join('、')}
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {pkg.candidates.length === 0 ? <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>没有候选</div>
              : pkg.candidates.map((candidate) => <CandidateRow key={candidate.id} botId={botId} pkg={pkg} candidate={candidate} />)}
          </div>
        </div>)}
      </div>}
    <Modal title="导入 instruction skill 包" open={importOpen} confirmLoading={importMutation.isPending}
      onCancel={() => setImportOpen(false)} onOk={() => form.submit()} okText="导入为候选">
      <Form form={form} layout="vertical" onFinish={(values) => importMutation.mutate(values)}>
        <Form.Item label="能力包（zip）" required>
          <Upload maxCount={1} accept=".zip,application/zip" beforeUpload={(next) => { setFile(next); return false; }} onRemove={() => setFile(undefined)}>
            <Button>选择 zip 包</Button>
          </Upload>
        </Form.Item>
        <Form.Item name="representative_task" label="代表任务" rules={[{ required: true, message: '填写能区分正确与退化的代表性任务' }]}>
          <Input.TextArea rows={3} maxLength={16384} placeholder="任务输入；验证会用 Bot 当前模型真实执行" />
        </Form.Item>
        <Form.Item name="expected_output" label="预期明确结果" rules={[{ required: true, message: '必须给出可核对的具体结果，而不是“非空”' }]}>
          <Input.TextArea rows={2} maxLength={2048} placeholder="给出具体预期文本，验证将核对明确结果，不接受仅非空或退出码" />
        </Form.Item>
      </Form>
    </Modal>
  </div>}
  </>;
}
