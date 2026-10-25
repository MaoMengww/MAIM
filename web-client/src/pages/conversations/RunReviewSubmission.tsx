import { useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Input } from 'antd';
import { reviewMetadata, runReviewApi, type SubmitRunReviewPayload } from '@/services/runReview';
import type { BotRun } from '@/types/model';
import { submitSkillIntent } from '@/utils/skillSubmission';
import { clearReviewMaterials, isReviewPermissionError, reviewListKey, reviewPolicyKey, useReviewIdentity, useReviewPolicy, useReviewSubmissionIntent } from './RunReviewContext';
import { reviewError } from './RunReviewPolicy';

export function RunReviewSubmission({ run }: { run: BotRun }) {
  const queryClient = useQueryClient();
  const { userId, revision } = useReviewIdentity();
  const scope = { bot_id: run.bot_id, conv_id: run.conv_id, visibility: run.visibility };
  const policy = useReviewPolicy(scope);
  const intent = useReviewSubmissionIntent(run.id);
  const [feedback, setFeedback] = useState(() => intent.current?.request.trigger_kind === 'feedback' ? intent.current.request.feedback : '');
  const [validation, setValidation] = useState('');
  const locked = useRef(false);
  const bytes = new TextEncoder().encode(feedback).byteLength;
  const available = !!userId && !policy.isError && policy.data?.enabled === true;
  const mutation = useMutation({
    gcTime: 0,
    retry: false,
    mutationFn: (request: SubmitRunReviewPayload) => submitSkillIntent(`${userId}:${run.bot_id}:${run.id}:review-submit`, request,
      async (submission_key) => reviewMetadata(await runReviewApi.submit(run.bot_id, run.id, { ...request, submission_key }))),
    onSuccess: async (_data, request) => {
      intent.current = undefined;
      if (request.trigger_kind === 'feedback') setFeedback('');
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: reviewListKey(userId, revision, scope) }),
        queryClient.invalidateQueries({ queryKey: ['run-review', userId, revision, run.bot_id] }),
      ]);
    },
    onError: async (error) => {
      if (isReviewPermissionError(error)) {
        await clearReviewMaterials(queryClient, userId, revision, run.bot_id);
        await queryClient.invalidateQueries({ queryKey: reviewPolicyKey(userId, revision, scope) });
      }
    },
  });
  const submit = async (trigger: 'feedback' | 'historical') => {
    if (locked.current || !available) return;
    if (trigger === 'feedback' && (!feedback.trim() || bytes > 8192)) {
      setValidation(!feedback.trim() ? '请填写非空反馈，说明需要纠正或补充的事实。' : '反馈不得超过 8192 UTF-8 字节，请缩短后提交。');
      return;
    }
    setValidation('');
    const signature = JSON.stringify({ trigger_kind: trigger, feedback: trigger === 'feedback' ? feedback : undefined });
    if (intent.current?.signature !== signature) {
      intent.current = { signature, request: trigger === 'feedback'
        ? { trigger_kind: trigger, feedback }
        : { trigger_kind: trigger } };
    }
    locked.current = true;
    try { await mutation.mutateAsync(intent.current.request); }
    catch { /* Keep the same intent and submission key until success or an explicit edit. */ }
    finally { locked.current = false; }
  };
  const feedbackId = `review-feedback-${run.id}`;
  return <section className="chat-review-submission" aria-label="向原 Run 提交反馈或历史复盘">
    <strong>针对原 Run 的新复盘修订</strong>
    <p className="chat-run-notice">关联 Run：<a href={`#run-${run.id}`}>{run.id}</a> · {run.visibility === 'personal' ? '我的个人范围' : '会话共享范围'}。每次明确提交独立修订，由服务端关联上一条复盘；原结果、先前评估及候选保留。</p>
    <form onSubmit={(event) => { event.preventDefault(); void submit('feedback'); }} aria-busy={mutation.isPending}>
      <label htmlFor={feedbackId}>后续纠错或补充反馈</label>
      <Input.TextArea id={feedbackId} value={feedback} rows={4} disabled={!available || mutation.isPending}
        aria-describedby={`${feedbackId}-help ${feedbackId}-validation`} aria-invalid={!!validation || bytes > 8192}
        placeholder="描述原结果中需要纠正的事实，以及可核对的依据。反馈将先评估，不会直接成为已验证规则。"
        onChange={(event) => {
          setFeedback(event.target.value);
          intent.current = undefined;
          setValidation('');
          mutation.reset();
        }} />
      <p id={`${feedbackId}-help`} className="chat-run-notice">{bytes} / 8192 UTF-8 字节。反馈不代表事实已验证；可不生成候选或要求补证。</p>
      <p id={`${feedbackId}-validation`} className="chat-run-error" role={validation || bytes > 8192 ? 'alert' : undefined}>
        {validation || (bytes > 8192 ? '反馈超过 8192 UTF-8 字节，请缩短后提交。' : '')}
      </p>
      <div className="chat-review-submission-actions">
        <Button htmlType="submit" type="primary" disabled={!available || bytes > 8192 || mutation.isPending} loading={mutation.isPending && mutation.variables?.trigger_kind === 'feedback'}>提交反馈并复盘</Button>
        <Button htmlType="button" disabled={!available || mutation.isPending} loading={mutation.isPending && mutation.variables?.trigger_kind === 'historical'} onClick={() => void submit('historical')}>显式复盘此历史 Run</Button>
      </div>
    </form>
    <p className="chat-run-notice">历史复盘按钮仅提交此 Run，不携带输入框反馈；不会扫描、批量回填历史，也不会重新执行原任务。读取和提炼时仍核对当前材料许可。</p>
    <div className="chat-review-policy-heading">
      {policy.isPending ? <span role="status">正在核验当前范围策略…</span>
        : policy.isError ? <span className="chat-run-error" role="alert">{reviewError(policy.error)} 暂不可提交。</span>
          : !policy.data?.enabled ? <span role="status">当前范围复盘已关闭，不接受新反馈或历史复盘；可在会话复盘策略中管理。</span> : null}
      <Button size="small" disabled={mutation.isPending} loading={policy.isFetching} onClick={() => void policy.refetch()}>刷新提交资格</Button>
    </div>
    {mutation.isError && <p className="chat-run-error" role="alert">{reviewError(mutation.error)} 未确认结果时可重试同一操作；不修改内容则保留原提交键，避免重复修订。</p>}
    {mutation.isSuccess && <p className="chat-run-notice" role="status">已提交{mutation.data.trigger_kind === 'feedback' ? '反馈' : '显式历史复盘'}：修订 #{mutation.data.revision} · <a href={`#review-${mutation.data.id}`}>{mutation.data.id}</a>。原 Run 结果未改变，评估结果与候选请在下方审阅。</p>}
  </section>;
}
