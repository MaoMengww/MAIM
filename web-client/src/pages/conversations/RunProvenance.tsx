import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Button, Tag } from 'antd';
import { botApi } from '@/services/bot';
import { useAuthStore } from '@/stores/auth';
import type { BotRun, RunOrigin } from '@/types/model';

export function RunOriginDetails({ origin, nameForIdentity }: { origin: RunOrigin; nameForIdentity: (id: string) => string }) {
  return <dl className="chat-run-identities" data-origin-kind={origin.kind}>
    <dt>工作发起来源</dt><dd>{origin.kind === 'rule' ? '显式自动响应规则（不是人类请求）' : 'Bot 明确委派（不是冒充原请求者）'}</dd>
    {origin.rule_id && <><dt>规则 ID</dt><dd>{origin.rule_id}</dd></>}
    {origin.rule_version !== undefined && <><dt>规则版本</dt><dd>v{origin.rule_version}</dd></>}
    {origin.authorization_user_id && <><dt>真实授权者</dt><dd title={origin.authorization_user_id}>{nameForIdentity(origin.authorization_user_id)}</dd></>}
    {origin.authorization_member_id && <><dt>授权成员生命周期</dt><dd>{origin.authorization_member_id}</dd></>}
    {origin.activity_id && <><dt>触发活动 ID</dt><dd>{origin.activity_id}</dd></>}
    {origin.delegating_bot_id && <><dt>委派 Bot</dt><dd title={origin.delegating_bot_id}>{nameForIdentity(origin.delegating_bot_id)}</dd></>}
    {origin.source_run_id && <><dt>来源 Run</dt><dd><a href={`#run-${origin.source_run_id}`}>{origin.source_run_id}</a></dd></>}
  </dl>;
}

const STOP_LABEL = { none: '未请求停止', requested: '停止请求已接受 · 等待执行器确认', confirmed: '执行器已确认停止', rejected: '执行器拒绝停止', unknown: '停止结局未知' };

function RelatedRun({ id, convId, parentId, enabled, nameForIdentity }: {
  id: string; convId: string; parentId?: string; enabled: boolean; nameForIdentity: (id: string) => string;
}) {
  const account = useAuthStore((state) => state.user?.id ?? '');
  const revision = useAuthStore((state) => state.revision);
  const query = useQuery({
    queryKey: ['delegation-run', account, revision, convId, id, parentId],
    queryFn: async () => {
      const run = await botApi.getRun(id);
      if (run.conv_id !== convId || run.visibility !== 'conversation' || parentId && run.delegation_parent_run_id !== parentId) throw new Error('关联执行不在当前可见范围');
      return run;
    },
    enabled: enabled && !!account,
    refetchInterval: enabled ? 5000 : false,
    staleTime: 0,
  });
  return <li className="chat-run-related" data-related-run-id={id}>
    <a href={`#run-${id}`}>Run {id}</a>
    {query.isPending && <p role="status">正在读取此执行的独立状态…</p>}
    {query.isError && <p role="alert" className="chat-run-error">此关联执行当前无权读取或查询失败；不能推断已停止或回滚。 <Button size="small" loading={query.isFetching} onClick={() => void query.refetch()}>重新查询</Button></p>}
    {!query.isError && query.data && <>
      <div className="chat-run-meta" data-related-stop-status={query.data.stop_status}>
        <span title={query.data.bot_id}>执行 Bot：{nameForIdentity(query.data.bot_id)}</span>
        <Tag>工作状态：{query.data.status}</Tag><Tag>{STOP_LABEL[query.data.stop_status]}</Tag>
        <span>执行器实际状态：{query.data.executor_status}</span>
        <span>结果提交：{query.data.output_status}</span>
      </div>
      <dl className="chat-run-identities"><dt>独立交互 ID</dt><dd>{query.data.interaction_id}</dd>
        <dt>实际阶段</dt><dd>{query.data.stage}</dd><dt>状态更新时间</dt><dd>{new Date(query.data.updated_at * 1000).toLocaleString('zh-CN')}</dd>
      </dl>
      {query.data.stop_reason && <p>停止原因：{query.data.stop_reason}</p>}
      {query.data.waiting_reason && <p role="status">{query.data.waiting_reason}</p>}
      {query.data.context_notice && <p role="status" className="chat-run-notice">{query.data.context_notice}</p>}
      {query.data.error && <p role="alert" className="chat-run-error">{query.data.error}</p>}
      {query.data.result_text && <details className="chat-run-result"><summary>此执行的独立结果</summary><pre>{query.data.result_text}</pre></details>}
      {query.data.result_type && query.data.result_type !== 'text' && query.data.result_content && <details className="chat-run-result"><summary>此执行的{query.data.result_type === 'file' ? '文件' : '结构化'}结果</summary><pre>{JSON.stringify(query.data.result_content, null, 2)}</pre></details>}
      <Button size="small" loading={query.isFetching} onClick={() => void query.refetch()}>查询此执行</Button>
    </>}
  </li>;
}

export function RunDelegation({ run, nameForIdentity }: { run: BotRun; nameForIdentity: (id: string) => string }) {
  const [expanded, setExpanded] = useState(false);
  const children = run.child_run_ids ?? [];
  const parent = run.delegation_parent_run_id ?? run.origin?.source_run_id;
  if (!parent && children.length === 0 && run.origin?.kind !== 'delegation') return null;
  return <details className="chat-run-details" data-delegation-run={run.id} onToggle={(event) => setExpanded(event.currentTarget.open)}>
    <summary>明确委派关系与实际结局 · {children.length} 个直接子 Run</summary>
    <p className="chat-run-notice">子 Run 有独立身份、过程和结果。停止父工作只向关联子工作请求停止，须分别查询接受、确认、拒绝或未知结局；不保证跨执行器原子停止，也不回滚已发生的副作用。</p>
    {run.delegation_depth !== undefined && <p>此 Run 委派深度：{run.delegation_depth} / 3</p>}
    <p className="chat-run-notice">整条委派链最多 8 个后代、262144 字节输入、16384 个预留输出 token；达到约束会明确拒绝继续委派，不自动扩大权限或预算。</p>
    <ul className="chat-run-steps">
      {parent && <><li>来源工作</li><RelatedRun key={parent} id={parent} convId={run.conv_id} enabled={expanded} nameForIdentity={nameForIdentity} /></>}
      {children.map((id) => <RelatedRun key={id} id={id} convId={run.conv_id} parentId={run.id} enabled={expanded} nameForIdentity={nameForIdentity} />)}
    </ul>
    {children.length === 0 && <p className="chat-run-notice">此 Run 尚无直接子工作；来源与承接继续工作是不同关系。</p>}
  </details>;
}
