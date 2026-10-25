import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Tag } from 'antd';
import { reviewMetadata, runReviewApi, type Review, type ReviewStatus, type ReviewTrigger } from '@/services/runReview';
import type { BotRun } from '@/types/model';
import { clearReviewMaterials, isReviewPermissionError, reviewDetailKey, reviewListKey, useReviewIdentity, useReviewPolicy, useRunReviews } from './RunReviewContext';
import { ReviewCandidateDetails } from './ReviewCandidateDetails';
import { reviewError } from './RunReviewPolicy';
import { RunReviewSubmission } from './RunReviewSubmission';
import './RunReview.css';

const STATUS: Record<ReviewStatus, { label: string; color: string }> = {
  queued: { label: '排队中', color: 'default' },
  assessing: { label: '评估经验价值', color: 'processing' },
  generating: { label: '提炼待验证候选', color: 'processing' },
  no_candidate: { label: '不沉淀候选', color: 'default' },
  candidate_generated: { label: '已生成待验证候选', color: 'success' },
  failed: { label: '复盘失败', color: 'error' },
  over_budget: { label: '超出复盘预算', color: 'warning' },
  cancelled: { label: '复盘已取消', color: 'default' },
};
const ACTIVE: Partial<Record<ReviewStatus, true>> = { queued: true, assessing: true, generating: true };
const TRIGGER: Record<ReviewTrigger, string> = { automatic: '终态自动复盘', feedback: '用户后续反馈', historical: '显式历史复盘' };

function ReviewDetails({ review, run, canCancel }: { review: Review; run: BotRun; canCancel: boolean }) {
  const queryClient = useQueryClient();
  const { userId, revision } = useReviewIdentity();
  const [submissionKey] = useState(() => crypto.randomUUID());
  const queryKey = reviewDetailKey(userId, revision, run.bot_id, review.id);
  const detail = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      try {
        const data = await runReviewApi.get(run.bot_id, review.id, signal);
        return data.expires_at > 0 && data.expires_at * 1000 <= Date.now() ? reviewMetadata(data) : data;
      } catch (error) {
        if (isReviewPermissionError(error)) await clearReviewMaterials(queryClient, userId, revision, run.bot_id, review.id);
        throw error;
      }
    },
    enabled: !!userId,
    retry: false,
    staleTime: 3000,
    gcTime: 0,
    refetchInterval: (query) => query.state.status === 'error' ? false : query.state.data && (ACTIVE[query.state.data.status] || query.state.data.candidate || query.state.data.feedback || query.state.data.assessment) ? 5000 : false,
  });
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const expires = detail.data?.expires_at;
    if (!expires) return;
    if (expires * 1000 <= now) {
      queryClient.setQueryData<Review>(queryKey, (current) => current ? reviewMetadata(current) : current);
      return;
    }
    const delay = Math.min(Math.max(expires * 1000 - Date.now(), 0), 2147483647);
    const timer = window.setTimeout(() => {
      setNow(Date.now());
      if (expires * 1000 <= Date.now()) {
        queryClient.setQueryData<Review>(queryKey, (current) => current ? reviewMetadata(current) : current);
      }
    }, delay);
    return () => window.clearTimeout(timer);
  }, [detail.data?.expires_at, now, queryClient, userId, revision, run.bot_id, review.id]);
  const cancel = useMutation({
    mutationFn: () => runReviewApi.cancel(run.bot_id, review.id, { submission_key: submissionKey }),
    onSuccess: (data) => {
      queryClient.setQueryData(queryKey, data);
      const listKey = reviewListKey(userId, revision, { bot_id: run.bot_id, conv_id: run.conv_id, visibility: run.visibility });
      queryClient.setQueryData<Review[]>(listKey, (current) => current?.map((entry) => entry.id === data.id ? reviewMetadata(data) : entry));
      void queryClient.invalidateQueries({ queryKey: listKey });
    },
    onError: async (error) => {
      if (isReviewPermissionError(error)) await clearReviewMaterials(queryClient, userId, revision, run.bot_id);
      void queryClient.invalidateQueries({ queryKey });
    },
  });
  const data = detail.isError ? review : detail.data ?? review;
  const status = STATUS[data.status];
  const expired = data.expires_at > 0 && data.expires_at * 1000 <= Math.max(now, Date.now());
  const materialsAvailable = !!detail.data && !detail.isError && !expired;
  return <article id={`review-${data.id}`} className="chat-review-detail" aria-label={`复盘修订 ${data.revision} · ${TRIGGER[data.trigger_kind]}`}>
    <div className="chat-review-policy-heading" role="status">
      <strong>修订 #{data.revision}</strong>
      <Tag>{TRIGGER[data.trigger_kind]}</Tag>
      <Tag color={status.color}>{status.label}</Tag>
      {expired && <Tag>材料期限已到</Tag>}
      <Button size="small" loading={detail.isFetching} disabled={cancel.isPending} onClick={() => { cancel.reset(); void detail.refetch(); }}>刷新复盘</Button>
      {ACTIVE[data.status] && canCancel && !expired && !detail.isError && <Button size="small" danger loading={cancel.isPending} onClick={() => cancel.mutate()}>取消此复盘</Button>}
    </div>
    <dl className="chat-review-meta">
      <dt>复盘 ID</dt><dd>{data.id}</dd>
      <dt>提交来源</dt><dd>{TRIGGER[data.trigger_kind]} · {data.submitted_by || '系统自动触发'}</dd>
      <dt>父修订</dt><dd>{data.parent_review_id ? <a href={`#review-${data.parent_review_id}`}>{data.parent_review_id}</a> : '首条复盘，无父修订'}</dd>
      <dt>候选关联</dt><dd>{data.candidate_id || '尚无候选'}</dd>
      <dt>来源 Run</dt><dd><a href={`#run-${data.run_id}`}>{data.run_id}</a></dd>
      <dt>范围</dt><dd>{run.visibility === 'personal' ? '我的个人范围' : '会话共享范围'} · {data.scope_key}</dd>
      <dt>复盘说明</dt><dd>{materialsAvailable ? data.reason || '尚未给出说明。' : '材料当前不可用；保留可审查的修订元数据。'}</dd>
      <dt>模型调用</dt><dd>{data.model_calls} / 2 · {data.model_version || '尚未记录模型版本'}</dd>
      <dt>创建</dt><dd>{new Date(data.created_at * 1000).toLocaleString('zh-CN')}</dd>
      <dt>更新</dt><dd>{new Date(data.updated_at * 1000).toLocaleString('zh-CN')}</dd>
      <dt>材料期限</dt><dd>{new Date(data.expires_at * 1000).toLocaleString('zh-CN')}</dd>
    </dl>
    {detail.isPending && <p className="chat-run-notice" role="status">正在读取此修订的当前授权材料…</p>}
    {detail.isError && <div className="chat-run-error" role="alert">
      {reviewError(detail.error)} 不能显示缓存中的反馈、评估或候选材料。
      <Button size="small" loading={detail.isFetching} onClick={() => void detail.refetch()}>重试复盘详情</Button>
    </div>}
    {materialsAvailable && data.trigger_kind === 'feedback' && <section className="chat-review-material" aria-label="此修订的反馈原文">
      <strong>反馈原文 · 尚待核验的用户陈述</strong>
      <p>{data.feedback || '反馈材料当前不可读；不从列表或其他修订推断内容。'}</p>
    </section>}
    {materialsAvailable && data.assessment && <section className="chat-review-material" aria-label="此修订的经验价值评估">
      <strong>经验价值评估 · 不等于验证或发布批准</strong>
      <dl className="chat-review-meta">
        <dt>价值判断</dt><dd>{data.assessment.valuable ? '值得提炼待验证候选' : '不建议沉淀候选'}</dd>
        <dt>评估原因</dt><dd>{data.assessment.reason}</dd>
        <dt>目标版本</dt><dd>{data.assessment.target_version_id || '未指定已有版本'}</dd>
        <dt>目标</dt><dd>{data.assessment.target_goal || '未给出目标'}</dd>
        <dt>适用边界</dt><dd>{data.assessment.applicability || '未给出适用边界'}</dd>
      </dl>
    </section>}
    {ACTIVE[data.status] && !canCancel && <p className="chat-run-notice">当前没有取消权限；仅合资格请求者、控制者、显式获权成员或会话管理员可取消。</p>}
    {cancel.isError && <p className="chat-run-error" role="alert">{reviewError(cancel.error)} 请刷新确认状态；重试使用原提交键。</p>}
    {cancel.isSuccess && data.status === 'cancelled' && <p className="chat-run-notice" role="status">复盘已取消，原 Run 结果保持不变。</p>}
    {materialsAvailable && data.candidate && <ReviewCandidateDetails botId={run.bot_id} candidate={data.candidate} />}
    {data.status === 'candidate_generated' && (expired || !data.candidate) && <p className="chat-run-notice" role="status">候选材料当前不可用，可能已过期或来源许可已变化；可刷新详情确认。</p>}
  </article>;
}

function RunReviewList({ run }: { run: BotRun }) {
  const list = useRunReviews(run.bot_id, run.conv_id, run.visibility);
  const policy = useReviewPolicy({ bot_id: run.bot_id, conv_id: run.conv_id, visibility: run.visibility });
  if (!list || list.isPending) return <p className="chat-run-notice" role="status">正在读取此范围复盘记录…</p>;
  if (list.isError) return <div className="chat-run-error" role="alert">
    {reviewError(list.error)}
    <Button size="small" loading={list.isFetching} onClick={() => void list.refetch()}>重试复盘列表</Button>
  </div>;
  const reviews = (list.data ?? []).filter((review) => review.run_id === run.id).sort((a, b) => b.revision - a.revision);
  const terminal = run.status === 'succeeded' || run.status === 'failed' || run.status === 'stopped';
  const canCancel = run.can_control === true || (!policy.isError && policy.data?.can_manage === true);
  return <>
    <div className="chat-review-policy-heading">
      <span className="chat-run-notice">仅展示当前有权读取的来源材料；复盘不改变原执行结局。</span>
      <Button size="small" loading={list.isFetching} onClick={() => void list.refetch()}>刷新复盘列表</Button>
    </div>
    {reviews.length === 0 && <p className="chat-run-notice" role="status">{terminal
      ? '此 Run 暂无复盘记录。可在上方明确提交反馈或仅复盘此历史 Run；不会默认回填历史。'
      : '此 Run 尚未进入成功、失败或已停止终态；等待输入、等待授权、停止待确认均不会触发复盘。'}</p>}
    {reviews.map((review) => <ReviewDetails key={review.id} review={review} run={run} canCancel={canCancel} />)}
  </>;
}

export function RunReviewSection({ run }: { run: BotRun }) {
  const [open, setOpen] = useState(false);
  const { userId, revision } = useReviewIdentity();
  const terminal = run.status === 'succeeded' || run.status === 'failed' || run.status === 'stopped';
  return <details className="chat-run-details chat-review-section" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>复盘进展与经验候选</summary>
    {terminal && <RunReviewSubmission key={`${userId}:${revision}:${run.id}`} run={run} />}
    {open && <RunReviewList run={run} />}
  </details>;
}
