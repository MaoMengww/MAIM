import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Descriptions, Space, Tag } from 'antd';
import type { BotRun, PublicAction } from '@/types/model';
import { authorizationApi, type ActionDecision } from '@/services/authorization';
import { botApi } from '@/services/bot';
import { useAuthStore } from '@/stores/auth';

const statuses: Record<string, string> = { pending: '等待确认', approved: '已批准，尚未完成', denied: '已拒绝', revoked: '已撤销', expired: '已过期', executing: '执行中', succeeded: '实际成功', failed: '实际失败', unknown: '实际结局未知' };
export function RunActions({ run, onUpdated }: { run: BotRun; onUpdated: (run: BotRun) => Promise<void> }) {
 const revision = useAuthStore((s) => s.revision);
 const scope = `${revision}:${run.conv_id}:${run.id}`;
 const currentScope = useRef(scope);
 currentScope.current = scope;
 const mounted = useRef(true);
 useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
 return <>
  {run.context_notice && <Alert type="warning" showIcon message={run.context_notice} role="status" />}
  <ActionConfirmations key={scope} actions={run.actions} scopeKey={scope} onDecided={async () => {
   const updated = await botApi.getRun(run.id);
   if (mounted.current && currentScope.current === scope && useAuthStore.getState().revision === revision) await onUpdated(updated);
  }} />
 </>;
}

export function ActionConfirmations({ actions, scopeKey, onDecided }: { actions: PublicAction[]; scopeKey: string; onDecided: (action: PublicAction) => Promise<void> }) {
 const revision = useAuthStore((s) => s.revision);
 const scope = `${revision}:${scopeKey}`;
 const currentScope = useRef(scope);
 currentScope.current = scope;
 const mounted = useRef(true);
 const [busy, setBusy] = useState<string>();
 const [error, setError] = useState('');
 const keys = useRef(new Map<string, string>());
 const lock = useRef(false);
 useEffect(() => {
  mounted.current = true;
  setBusy(undefined); setError(''); keys.current.clear(); lock.current = false;
  return () => { mounted.current = false; };
 }, [scope]);
 const decide = async (action: PublicAction, op: ActionDecision) => {
  if (lock.current || currentScope.current !== scope) return;
  const signature = `${scope}:${action.id}:${op}:${action.generation}:${action.input_version}`;
  if (!keys.current.has(signature)) keys.current.set(signature, crypto.randomUUID());
  lock.current = true; setBusy(action.id); setError('');
  const valid = () => mounted.current && currentScope.current === scope && useAuthStore.getState().revision === revision;
  try {
   const updated = await authorizationApi.decideAction(action, op, keys.current.get(signature)!);
   if (!valid()) return;
   await onDecided(updated);
   if (valid()) keys.current.delete(signature);
  } catch (cause) { if (valid()) setError(cause instanceof Error ? cause.message : '行动确认失败，请查询最新状态'); }
  finally { if (valid()) { lock.current = false; setBusy(undefined); } }
 };
 return <section aria-label="具体行动确认">
  {actions.map((action) => action.server_id === 'runtime' && action.tool_name === 'search_knowledge' ? <details className="chat-run-details" key={action.id} data-action-id={action.id} data-action-status={action.status}>
   <summary>知识检索 · {statuses[action.status] || action.status}</summary>
   <p>查询：{typeof action.parameters.query === 'string' ? action.parameters.query : '当前不可披露或参数无效'}</p>
   {action.result && <details><summary>读取结果与实际来源</summary><pre>{action.result}</pre></details>}
   <p>执行代次 {action.generation} · 输入版本 {action.input_version}</p>
  </details> : <div className="chat-run-details" key={action.id} data-action-id={action.id} data-action-status={action.status}>
   <Space wrap><strong>{action.tool_name}</strong><Tag>{action.risk}</Tag><Tag>{statuses[action.status] || action.status}</Tag>{action.confirmation_shared && <Tag>已提交此具体确认</Tag>}</Space>
   <Descriptions size="small" column={1}>
    <Descriptions.Item label="目标">{action.target || '未提供目标'}</Descriptions.Item>
    <Descriptions.Item label="工具服务器">{action.server_id}</Descriptions.Item>
    <Descriptions.Item label="有效期">{new Date(action.expires_at * 1000).toLocaleString()}</Descriptions.Item>
    <Descriptions.Item label="执行范围">代次 {action.generation} · 输入版本 {action.input_version}</Descriptions.Item>
   </Descriptions>
   <details open={action.can_approve && action.status === 'pending'}><summary>具体参数（秘密已脱敏，数值原样保留）</summary><pre>{action.parameters_json}</pre></details>
   {action.result && <p>实际结果：{action.result}</p>}
   {action.error && <Alert type="error" message={action.error} />}
   {action.can_approve && action.status === 'pending' && <Space wrap>
    <Button type="primary" loading={busy === action.id} disabled={!!busy} onClick={() => void decide(action, 'approve')}>批准此具体行动</Button>
    <Button danger loading={busy === action.id} disabled={!!busy} onClick={() => void decide(action, 'deny')}>拒绝此行动</Button>
   </Space>}
   {!action.can_approve && action.status === 'pending' && <>
    <p>等待对此工具资源有权的主体批准。工作控制权和会话管理员身份不构成批准资格。</p>
    {action.can_share_confirmation && !action.confirmation_shared && <>
     <p>仅向此工具资源所有者披露本行动的目标和具体参数，不分享个人工作的来源、结果或其他行动。</p>
     <Button loading={busy === action.id} disabled={!!busy} onClick={() => void decide(action, 'share_confirmation')}>将此具体确认提交资源所有者</Button>
    </>}
   </>}
  </div>)}
  {error && <Alert type="error" message={error} showIcon />}
 </section>;
}
