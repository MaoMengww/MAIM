import { useLayoutEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Button, Checkbox, Input } from 'antd';
import { candidateEvidenceExpired, skillCandidateApi, type CandidateAction, type CandidateActionRequest, type GovernedCandidate } from '@/services/skillCandidate';
import { submitSkillIntent } from '@/utils/skillSubmission';
import { CandidateRevisionForm } from './CandidateRevisionForm';

export function CandidateActions({ botId, candidate, identity, onChanged }: {
  botId: string;
  candidate: GovernedCandidate;
  identity: string;
  onChanged: () => Promise<unknown>;
}) {
  const [reason, setReason] = useState('');
  const [reviewConfirmed, setReviewConfirmed] = useState(false);
  const [approvalConfirmed, setApprovalConfirmed] = useState(false);
  const [notice, setNotice] = useState('');
  const mutation = useMutation({
    retry: false,
    gcTime: 0,
    meta: { evidence_expires_at: candidate.expires_at },
    mutationFn: async ({ action, payload }: { action: CandidateAction; payload: Omit<CandidateActionRequest, 'submission_key'> }) => {
      return submitSkillIntent(`${identity}:${botId}:${candidate.id}:${action}`, payload,
        (submission_key) => skillCandidateApi.act(botId, candidate.id, action, { ...payload, submission_key }));
    },
    onSuccess: async (_data, { action }) => {
      mutation.reset();
      setReviewConfirmed(false);
      setApprovalConfirmed(false);
      setNotice(action === 'publish' ? '不可变版本已发布。发布不等于启用；原 Run 结果保持不变。' : '操作已保存，请按当前内容与证据继续审阅。');
      await onChanged();
    },
    onError: async () => { await onChanged(); },
  });
  useLayoutEffect(() => {
    if (mutation.isSuccess) mutation.reset();
  }, [mutation.isSuccess, mutation.reset]);
  const submit = (action: CandidateAction, extra: Partial<CandidateActionRequest> = {}) => {
    setNotice('');
    if (candidateEvidenceExpired(candidate) && action !== 'revise') {
      setNotice('材料已过期，必须提交新获准证据并重新验证批准，不能继续此操作。');
      return;
    }
    if (extra.reason !== undefined && new TextEncoder().encode(extra.reason).length > 1024) {
      setNotice('补证要求 / 拒绝原因不能超过 1024 UTF-8 字节。');
      return;
    }
    mutation.mutate({ action, payload: { ...extra, digest: candidate.digest } });
  };
  const hasScripts = Object.keys(candidate.scripts).length > 0 || candidate.code_review_required;
  const expired = candidateEvidenceExpired(candidate);
  const immutable = ['published', 'rejected'].includes(candidate.status);
  const canMaintain = candidate.can_maintain && candidate.can_view_materials && !immutable && !expired;
  const canRenew = candidate.can_maintain && !immutable && (candidate.can_view_materials || expired);
  const verified = candidate.evaluation?.status === 'passed' && candidate.evaluation.passed === true;
  const busy = mutation.isPending;
  const error = mutation.error as { message?: string; response?: { data?: { message?: string } } } | null;
  return <section aria-label="候选治理操作" aria-busy={busy}>
    <p className="skill-candidate-notice">执行前代码审查仅允许验证被冻结的确切脚本身份，不是发布批准。无可信执行节点人工准入策略时必须明确拒绝执行；界面确认不能授予准入。</p>
    <p className="skill-candidate-notice">脚本验证每次使用新的加固容器；共享宿主内核的容器不是恶意代码强隔离。只应运行获准且经可信人工审查的代码。</p>
    {!canRenew && <p role="note">当前无维护及材料查看权限，或候选已终结；不能补证、修订、验证或批准。Bot 所有权和脱敏不是材料授权。</p>}
    {expired && <>
      <div className="skill-candidate-actions" aria-label="证据已过期，操作禁用">
        <Button disabled>验证（须先补证）</Button><Button disabled>批准（须重新验证）</Button><Button disabled>发布（须重新批准）</Button>
      </div>
      {canRenew && <p>请在下方补证入口提交完整包和新的获准样例、具体预期、许可确认及截止时间；不可只延长旧期限。</p>}
    </>}
    {candidate.reason && <p role="note">已保存的补证要求 / 处理原因：{candidate.reason}</p>}
    {canMaintain && <>
      {hasScripts && <fieldset disabled={busy || !candidate.can_review_code}>
        <legend>执行前人工代码审查</legend>
        <p>先展开上方完整脚本、依赖镜像摘要和固定入口。当前仅支持可信节点已安装准入的完整 worker.py 与固定入口 python3 -I -B /opt/aim/worker.py；其余身份拒绝执行。</p>
        <Checkbox checked={reviewConfirmed} onChange={(event) => setReviewConfirmed(event.target.checked)}>
          我已逐一审阅全部脚本全文、依赖镜像及固定入口，确认冻结当前确切身份；这不是批准发布。
        </Checkbox>
        <div className="skill-candidate-actions"><Button disabled={!reviewConfirmed || !candidate.can_review_code} loading={busy && mutation.variables?.action === 'code-review'}
          onClick={() => submit('code-review', { confirmed: true })}>确认执行前代码审查</Button></div>
        {!candidate.can_review_code && <p>当前没有可信人工代码审查权限。</p>}
      </fieldset>}
      <div className="skill-candidate-actions">
        <Button disabled={busy || candidate.status === 'awaiting_evidence' || candidate.status === 'evaluating' && !hasScripts || hasScripts && !candidate.code_review_digest}
          loading={busy && mutation.variables?.action === 'evaluate'} onClick={() => submit('evaluate')}>
          {hasScripts ? candidate.status === 'evaluating' ? '按原执行身份对账恢复' : '在新容器验证精确 CSV 产物' : '运行代表任务与基线回归验证'}
        </Button>
      </div>
      {hasScripts && !candidate.code_review_digest && <p>尚无有效可信代码审查身份，验证已禁用；先完成可信准入与执行前审查。</p>}
      {candidate.status === 'awaiting_evidence' && <p>正在等待补证；请在修订中提交获准样例，再重新验证。</p>}
      {candidate.status === 'evaluating' && <p role="status">{hasScripts ? '验证仍在执行或结果未知；使用“按原执行身份对账恢复”查询并回收已有任务，不会换身份另起执行。' : '模型验证仍在执行或结果未知；不能盲目重放。请刷新读取已保存的结果。'}</p>}
      <label className="skill-candidate-file-label">补证要求 / 拒绝原因（可选）
        <Input.TextArea value={reason} maxLength={1024} rows={2} disabled={busy} onChange={(event) => setReason(event.target.value)} />
      </label>
      <p>最多 1024 UTF-8 字节，汉字通常占 3 字节。</p>
      <div className="skill-candidate-actions">
        <Button disabled={busy} onClick={() => submit('request-evidence', { reason })}>要求补证并撤销旧验证 / 批准</Button>
        <Button danger disabled={busy} onClick={() => submit('reject', { reason })}>拒绝此候选</Button>
      </div>
      <fieldset disabled={busy}>
        <legend>发布批准（与执行前审查分开）</legend>
        <Checkbox checked={approvalConfirmed} onChange={(event) => setApprovalConfirmed(event.target.checked)}>
          我已核对当前内容摘要、来源许可、基线和有效验证证据，批准此确切内容用于发布。
        </Checkbox>
        <div className="skill-candidate-actions">
          <Button type="primary" disabled={busy || !verified || candidate.status !== 'evaluated' || !approvalConfirmed}
            onClick={() => submit('approve')}>批准确切内容与有效证据</Button>
          <Button type="primary" disabled={busy || !candidate.can_publish || !verified || candidate.status !== 'approved'}
            onClick={() => submit('publish')}>发布不可变版本</Button>
        </div>
        {!verified && <p>未验证、不通过、证据不足或环境故障均不能批准发布；退出码与单次反馈不是通过证据。</p>}
        {!candidate.can_publish && <p>当前没有发布权限；维护权限不代替发布权限。</p>}
      </fieldset>
    </>}
    {canRenew && <CandidateRevisionForm key={`${candidate.evidence_id}:${expired ? 'new-evidence' : 'live'}`} candidate={candidate} pending={busy}
      onSubmit={(revision) => submit('revise', revision)} />}
    {busy && <p role="status">操作处理中，请勿重复发起。验证有独立模型 / 沙箱 / 时间 / 资源 / 日志预算，且不递归复盘。</p>}
    {mutation.isError && <div className="skill-candidate-error" role="alert">
      <p>{error?.response?.data?.message || error?.message || '操作失败或结果未知。'}</p>
      <p>不会自动换键、替换过时摘要或重跑脚本。相同意图重试保留原 submission_key；若内容 / 基线已变，请先手动刷新并重新审阅，不能覆盖现版。</p>
    </div>}
    {notice && <p role="status">{notice}</p>}
  </section>;
}
