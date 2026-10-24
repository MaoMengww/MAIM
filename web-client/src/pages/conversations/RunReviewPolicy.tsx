import { useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Select, Switch } from 'antd';
import { isAxiosError } from 'axios';
import { runReviewApi, type ReviewVisibility } from '@/services/runReview';
import type { BotInConv } from '@/types/model';
import { reviewListKey, reviewPolicyKey, useReviewIdentity, useReviewPolicy } from './RunReviewContext';

export function reviewError(error: unknown): string {
  if (isAxiosError(error) && (error.response?.status === 401 || error.response?.status === 403)) {
    return '当前无权访问此复盘范围，或来源材料权限已失效。';
  }
  const value = error as { response?: { data?: { message?: string } }; message?: string } | null;
  return value?.response?.data?.message || value?.message || '复盘请求失败，请重试。';
}

function ReviewPolicyControl({ botId, convId, visibility }: { botId: string; convId: string; visibility: ReviewVisibility }) {
  const queryClient = useQueryClient();
  const { userId, revision } = useReviewIdentity();
  const scope = { bot_id: botId, conv_id: convId, visibility };
  const policy = useReviewPolicy(scope);
  const intent = useRef<{ enabled: boolean; submission_key: string }>();
  const title = visibility === 'conversation' ? '会话共享范围' : '我的个人范围';
  const mutation = useMutation({
    mutationFn: async (enabled: boolean) => {
      if (!policy.data?.can_manage || policy.isError) throw new Error('当前无权管理此复盘范围，请刷新权限。');
      if (!intent.current || intent.current.enabled !== enabled) intent.current = { enabled, submission_key: crypto.randomUUID() };
      return runReviewApi.updatePolicy(botId, { conv_id: convId, visibility, ...intent.current });
    },
    onSuccess: (data) => {
      intent.current = undefined;
      queryClient.setQueryData(reviewPolicyKey(userId, revision, scope), data);
      void queryClient.invalidateQueries({ queryKey: reviewListKey(userId, revision, scope) });
      void queryClient.invalidateQueries({ queryKey: ['run-review', userId, revision, botId] });
    },
    onError: () => { void queryClient.invalidateQueries({ queryKey: reviewPolicyKey(userId, revision, scope) }); },
  });
  return <section className="chat-review-policy" aria-label={`${title}复盘策略`}>
    <div className="chat-review-policy-heading">
      <strong>{title}</strong>
      {policy.isPending && <span role="status">正在读取策略…</span>}
      {!policy.isPending && !policy.isError && policy.data && <Switch
        aria-label={`${title}自动复盘`} checkedChildren="已开启" unCheckedChildren="已关闭"
        checked={policy.data.enabled} disabled={!policy.data.can_manage || mutation.isPending}
        loading={mutation.isPending} onChange={(enabled) => { mutation.reset(); mutation.mutate(enabled); }} />}
      <Button size="small" loading={policy.isFetching} disabled={mutation.isPending} onClick={() => { mutation.reset(); void policy.refetch(); }}>刷新策略</Button>
    </div>
    {policy.isError && <p className="chat-run-error" role="alert">{reviewError(policy.error)} 无法确认策略，开关不可用。</p>}
    {mutation.isError && <p className="chat-run-error" role="alert">{reviewError(mutation.error)} 提交结果未确认时，请刷新策略；同一意图重试保留提交键。</p>}
    {!policy.isPending && !policy.isError && !policy.data && <p className="chat-run-notice">当前范围尚无可读取策略。</p>}
    {!policy.isError && policy.data && <>
      <p className="chat-run-notice" role="status">{policy.data.enabled ? '新结束的 Run 自动异步复盘；不扫描历史执行。' : '此范围自动复盘已关闭；不影响原 Run 结果。'}
        {!policy.data.can_manage && (visibility === 'conversation' ? ' 仅当前会话管理员可管理此策略。' : ' 当前无权管理此个人范围。')}</p>
      <p className="chat-run-notice">范围：<code>{policy.data.scope_key}</code> · 修订 {policy.data.revision}</p>
    </>}
    {visibility === 'personal' && <p className="chat-run-notice">仅指你在此 Bot 当前绑定下的个人范围，不能查看或修改他人的个人策略。</p>}
  </section>;
}

export function RunReviewWorkspace({ convId, bots }: { convId: string; bots: BotInConv[] }) {
  const [open, setOpen] = useState(false);
  const [selectedBot, setSelectedBot] = useState<string>();
  const botId = bots.some((bot) => bot.bot_id === selectedBot) ? selectedBot : bots[0]?.bot_id;
  return <details className="chat-run-details chat-review-workspace" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>Run 复盘策略 · 会话与我的个人范围</summary>
    {open && <>
      <p className="chat-run-notice">先评估经验价值，再生成待验证候选；最多两次模型调用。复盘失败、超预算或取消不改变原执行结果。</p>
      {bots.length === 0 ? <p className="chat-run-notice" role="status">此会话尚无 Bot 绑定，暂无可管理的复盘范围。</p> : <>
        <label htmlFor={`review-policy-bot-${convId}`}>复盘策略对应 Bot</label>
        <Select id={`review-policy-bot-${convId}`} aria-label="复盘策略对应 Bot" value={botId} onChange={setSelectedBot}
          className="chat-review-bot-select" options={bots.map((bot) => ({ value: bot.bot_id, label: bot.name }))} />
        {botId && <div key={botId} className="chat-review-policy-grid">
          <ReviewPolicyControl botId={botId} convId={convId} visibility="conversation" />
          <ReviewPolicyControl botId={botId} convId={convId} visibility="personal" />
        </div>}
      </>}
    </>}
  </details>;
}
