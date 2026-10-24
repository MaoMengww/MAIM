import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Tag } from 'antd';
import { runReviewApi, type Review, type ReviewStatus } from '@/services/runReview';
import type { BotRun } from '@/types/model';
import { reviewDetailKey, reviewListKey, useReviewIdentity, useReviewPolicy, useRunReviews } from './RunReviewContext';
import { ReviewCandidateDetails } from './ReviewCandidateDetails';
import { reviewError } from './RunReviewPolicy';
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

function ReviewDetails({ review, run, canCancel }: { review: Review; run: BotRun; canCancel: boolean }) {
  const queryClient = useQueryClient();
  const { userId, revision } = useReviewIdentity();
  const [submissionKey] = useState(() => crypto.randomUUID());
  const queryKey = reviewDetailKey(userId, revision, run.bot_id, review.id);
  const detail = useQuery({
    queryKey,
    queryFn: ({ signal }) => runReviewApi.get(run.bot_id, review.id, signal),
    enabled: !!userId,
    retry: false,
    staleTime: 3000,
    refetchInterval: (query) => query.state.status === 'error' ? false : query.state.data && (ACTIVE[query.state.data.status] || query.state.data.candidate) ? 5000 : false,
  });
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const expires = detail.data?.expires_at;
    if (!expires || expires * 1000 <= now) return;
    const delay = Math.min(Math.max(expires * 1000 - Date.now(), 0), 2147483647);
    const timer = window.setTimeout(() => {
      setNow(Date.now());
      if (expires * 1000 <= Date.now()) {
        queryClient.setQueryData<Review>(queryKey, (current) => current ? { ...current, candidate: undefined, candidate_id: undefined } : current);
      }
    }, delay);
    return () => window.clearTimeout(timer);
  }, [detail.data?.expires_at, now, queryClient, userId, revision, run.bot_id, review.id]);
  const cancel = useMutation({
    mutationFn: () => runReviewApi.cancel(run.bot_id, review.id, { submission_key: submissionKey }),
    onSuccess: (data) => {
      queryClient.setQueryData(queryKey, data);
      const listKey = reviewListKey(userId, revision, { bot_id: run.bot_id, conv_id: run.conv_id, visibility: run.visibility });
      queryClient.setQueryData<Review[]>(listKey, (current) => current?.map((entry) => entry.id === data.id ? data : entry));
      void queryClient.invalidateQueries({ queryKey: listKey });
    },
    onError: () => { void queryClient.invalidateQueries({ queryKey }); },
  });
  if (detail.isPending) return <p className="chat-run-notice" role="status">正在读取复盘详情…</p>;
  if (detail.isError) return <div className="chat-run-error" role="alert">
    {reviewError(detail.error)} 不能显示缓存中的候选材料。
    <Button size="small" loading={detail.isFetching} onClick={() => void detail.refetch()}>重试复盘详情</Button>
  </div>;
  const data = detail.data;
  if (!data) return <p className="chat-run-notice" role="status">复盘详情为空。</p>;
  const status = STATUS[data.status];
  const expired = data.expires_at > 0 && data.expires_at * 1000 <= now;
  return <article className="chat-review-detail" aria-label={`复盘 ${data.id}`}>
    <div className="chat-review-policy-heading" role="status">
      <Tag color={status.color}>{status.label}</Tag>
      {expired && <Tag>材料期限已到</Tag>}
      <Button size="small" loading={detail.isFetching} disabled={cancel.isPending} onClick={() => { cancel.reset(); void detail.refetch(); }}>刷新复盘</Button>
      {ACTIVE[data.status] && canCancel && !expired && <Button size="small" danger loading={cancel.isPending} onClick={() => cancel.mutate()}>取消此复盘</Button>}
    </div>
    <dl className="chat-review-meta">
      <dt>来源 Run</dt><dd><a href={`#run-${data.run_id}`}>{data.run_id}</a></dd>
      <dt>范围</dt><dd>{run.visibility === 'personal' ? '我的个人范围' : '会话共享范围'} · {data.scope_key}</dd>
      <dt>评估原因</dt><dd>{data.reason || '评估尚未给出原因。'}</dd>
      <dt>模型调用</dt><dd>{data.model_calls} / 2 · {data.model_version || '尚未记录模型版本'}</dd>
      <dt>创建</dt><dd>{new Date(data.created_at * 1000).toLocaleString('zh-CN')}</dd>
      <dt>更新</dt><dd>{new Date(data.updated_at * 1000).toLocaleString('zh-CN')}</dd>
      <dt>材料期限</dt><dd>{new Date(data.expires_at * 1000).toLocaleString('zh-CN')}</dd>
    </dl>
    {ACTIVE[data.status] && !canCancel && <p className="chat-run-notice">当前没有取消权限；仅合资格请求者、控制者、显式获权成员或会话管理员可取消。</p>}
    {cancel.isError && <p className="chat-run-error" role="alert">{reviewError(cancel.error)} 请刷新确认状态；重试使用原提交键。</p>}
    {cancel.isSuccess && data.status === 'cancelled' && <p className="chat-run-notice" role="status">复盘已取消，原 Run 结果保持不变。</p>}
    {!expired && data.candidate && <ReviewCandidateDetails botId={run.bot_id} candidate={data.candidate} />}
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
  const reviews = (list.data ?? []).filter((review) => review.run_id === run.id);
  const terminal = run.status === 'succeeded' || run.status === 'failed' || run.status === 'stopped';
  const canCancel = run.can_control === true || (!policy.isError && policy.data?.can_manage === true);
  return <>
    <div className="chat-review-policy-heading">
      <span className="chat-run-notice">仅展示当前有权读取的来源材料；复盘不改变原执行结局。</span>
      <Button size="small" loading={list.isFetching} onClick={() => void list.refetch()}>刷新复盘列表</Button>
    </div>
    {reviews.length === 0 && <p className="chat-run-notice" role="status">{terminal
      ? '此 Run 暂无复盘记录。仅新进入成功、失败、已停止终态且范围策略允许的 Run 会异步入队，不回填历史。'
      : '此 Run 尚未进入成功、失败或已停止终态；等待输入、等待授权、停止待确认均不会触发复盘。'}</p>}
    {reviews.map((review) => <ReviewDetails key={review.id} review={review} run={run} canCancel={canCancel} />)}
  </>;
}

export function RunReviewSection({ run }: { run: BotRun }) {
  const [open, setOpen] = useState(false);
  return <details className="chat-run-details chat-review-section" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>复盘进展与经验候选</summary>
    {open && <RunReviewList run={run} />}
  </details>;
}
