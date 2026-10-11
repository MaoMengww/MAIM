import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Checkbox, Drawer, Empty, Form, Input, InputNumber, Select, Space, Tabs, Tag } from 'antd';
import { authorizationApi, type ResourceGrant, type ActionGrant } from '@/services/authorization';
import { botApi, isBotBindingActive } from '@/services/bot';
import { useAuthStore } from '@/stores/auth';
import { useWSStore } from '@/stores/ws';
import { ActionConfirmations, RunActions } from './RunActions';
import type { BotInConv, BotRun, PublicAction } from '@/types/model';
import { entityId } from '@/utils/json';
import { messageSync } from '@/services/messageSync';

const deadline = (minutes: number) => Math.floor(Date.now() / 1000) + minutes * 60;
export function AuthorizationPanel({ convId, bots, onSelect }: { convId: string; bots: BotInConv[]; onSelect: (ids: string[]) => void }) {
 const [open, setOpen] = useState(false);
 const [botId, setBotId] = useState<string>();
 const [resources, setResources] = useState<ResourceGrant[]>([]);
 const [grants, setGrants] = useState<ActionGrant[]>([]);
 const [actions, setActions] = useState<PublicAction[]>([]);
 const [personal, setPersonal] = useState<BotRun>();
 const [error, setError] = useState('');
 const [busy, setBusy] = useState(false);
 const [selected, setSelected] = useState<string[]>([]);
 const [resourceForm] = Form.useForm();
 const [actionForm] = Form.useForm();
 const [personalForm] = Form.useForm();
 const [queryForm] = Form.useForm();
 const [inputForm] = Form.useForm();
 const revision = useAuthStore((s) => s.revision);
 const reconnectVersion = useWSStore((s) => s.reconnectVersion);
 const [context, setContext] = useState({ convId, revision });
 const epoch = useRef(0);
 const privateRetry = useRef<{ signature: string; key: string }>();
 const privateControlKeys = useRef(new Map<string, string>());
 const scopeKey = `${convId}:${revision}:${botId ?? ''}`;
 const currentScope = useRef(scopeKey);
 currentScope.current = scopeKey;
 useEffect(() => {
  setContext({ convId, revision });
  epoch.current += 1; setOpen(false); setBotId(undefined); setResources([]); setGrants([]); setActions([]); setPersonal(undefined); setSelected([]); setError(''); setBusy(false);
  resourceForm.resetFields(); actionForm.resetFields(); personalForm.resetFields(); queryForm.resetFields(); inputForm.resetFields(); privateRetry.current = undefined; privateControlKeys.current.clear();
  return () => { epoch.current += 1; };
 }, [convId, revision, resourceForm, actionForm, personalForm, queryForm, inputForm]);
 useEffect(() => {
  epoch.current += 1;
  setPersonal(undefined); setActions([]); setBusy(false);
 }, [reconnectVersion]);
 useEffect(() => messageSync.subscribe((_id, _snapshotChanged, removedConversations, rebuiltConversations) => {
  if (!removedConversations.includes(convId) && !rebuiltConversations.includes(convId)) return;
  epoch.current += 1;
  setPersonal(undefined); setActions([]); setBusy(false);
 }), [convId]);
 const perform = async (work: () => Promise<void>) => {
  if (busy) return;
  const current = epoch.current;
  setBusy(true); setError('');
  try { await work(); }
  catch (cause) { if (current === epoch.current && currentScope.current === scopeKey) setError(cause instanceof Error ? cause.message : '操作失败，请查询最新授权'); }
  finally { if (current === epoch.current && currentScope.current === scopeKey) setBusy(false); }
 };
 const loadActions = async () => {
  if (currentScope.current !== scopeKey) return;
  if (!botId) throw new Error('请选择 Bot');
  const current = epoch.current;
  const automatic = await authorizationApi.listActions(botId, convId);
  if (current !== epoch.current || currentScope.current !== scopeKey) return;
  setGrants(automatic.grants); setActions(automatic.actions);
 };
 const load = async () => {
  if (currentScope.current !== scopeKey) return;
  if (!botId) throw new Error('请选择 Bot');
  const current = epoch.current;
  const [rows, automatic] = await Promise.all([
   authorizationApi.resources({ op: 'list', bot_id: botId, conv_id: convId }),
   authorizationApi.listActions(botId, convId),
  ]);
  if (current !== epoch.current || currentScope.current !== scopeKey) return;
  setResources(rows); setGrants(automatic.grants); setActions(automatic.actions);
 };
 const refreshPersonal = async (runId: string) => {
  const current = epoch.current;
  // Do not retain an old private projection when current visibility rejects it.
  setPersonal(undefined);
  const run = await botApi.getRun(entityId(runId, 'run_id'));
  if (current !== epoch.current || currentScope.current !== scopeKey) return;
  if (run.visibility !== 'personal' || run.conv_id !== convId || run.bot_id !== botId || run.personal_user_id !== useAuthStore.getState().user?.id) throw new Error('不是当前请求者的个人工作');
  setPersonal(run);
 };
 useEffect(() => {
  if (!personal || !open) return;
  const current = epoch.current;
  const id = personal.id;
  let active = true;
  let inFlight = false;
  const timer = window.setInterval(() => {
   if (inFlight) return;
   inFlight = true;
   void botApi.getRun(id).then((run) => {
    if (!active || current !== epoch.current || currentScope.current !== scopeKey) return;
    setPersonal((previous) => previous?.id === id && run.version >= previous.version ? run : previous);
   }).catch(() => {
    if (!active || current !== epoch.current || currentScope.current !== scopeKey) return;
    setPersonal((previous) => previous?.id === id ? undefined : previous);
    setError('个人工作当前不可读取，旧材料已从此页面移除。');
   }).finally(() => { inFlight = false; });
  }, 1000);
  return () => { active = false; window.clearInterval(timer); };
 }, [personal?.id, open, scopeKey]);
 const personalOwned = !!personal && personal.visibility === 'personal' && personal.personal_user_id === useAuthStore.getState().user?.id && personal.conv_id === convId && personal.bot_id === botId;
 const personalCanControl = personalOwned && personal.can_control === true && personal.interaction_status === 'active'
  && !['binding_revoked', 'bot_disabled', 'participation_revoked'].includes(personal.stop_reason)
  && bots.some((bot) => bot.bot_id === personal.bot_id && bot.binding_id === personal.binding_id && isBotBindingActive(bot));
 const personalCanInput = personalCanControl && personal.output_status === 'none' && personal.stop_status === 'none' && personal.stream_status === 'open'
  && ['accepted', 'running', 'waiting_input'].includes(personal.status) && personal.executor_capabilities.supports_input;
 const personalCanStop = personalCanControl && !['succeeded', 'failed', 'stopped'].includes(personal.status) && personal.output_status === 'none'
  && personal.stop_status === 'none' && personal.executor_capabilities.supports_stop;
 const personalCanResume = personalCanControl && (personal.status === 'waiting_input' || personal.executor_status === 'lost' && ['accepted', 'running'].includes(personal.status)) && personal.stage !== 'action_outcome_unknown' && personal.output_status === 'none'
  && personal.stop_status === 'none' && personal.executor_capabilities.supports_resume;
 const controlPersonal = async (operation: 'input' | 'stop' | 'resume', message?: string) => {
  if (currentScope.current !== scopeKey || !personal || !(operation === 'input' ? personalCanInput : operation === 'stop' ? personalCanStop : personalCanResume)) throw new Error('当前个人工作不允许此操作，请查询最新状态');
  if (operation === 'input' && !message?.trim()) throw new Error('请输入个人补充内容');
  const signature = JSON.stringify({ scopeKey, runId: personal.id, operation, message });
  if (!privateControlKeys.current.has(signature)) privateControlKeys.current.set(signature, crypto.randomUUID());
  const current = epoch.current;
  const run = await botApi.controlRun(personal.id, { operation, message, submission_key: privateControlKeys.current.get(signature)! });
  if (current !== epoch.current || currentScope.current !== scopeKey) return;
  if (run.visibility !== 'personal' || run.personal_user_id !== useAuthStore.getState().user?.id || run.conv_id !== convId || run.bot_id !== botId) throw new Error('不是当前请求者的个人工作');
  setPersonal(run); privateControlKeys.current.delete(signature);
  if (operation === 'input') inputForm.resetFields();
 };
 const expiryField = <Form.Item name="minutes" label="有效时长（分钟）" initialValue={60} rules={[{ required: true }]}><InputNumber min={1} max={10080} /></Form.Item>;
 return <>
  <Button onClick={() => setOpen(true)}>资源与行动授权 / 个人工作</Button>
  {context.convId === convId && context.revision === revision && <Drawer title="读取、分享与行动分别授权" open={open} onClose={() => { epoch.current += 1; setOpen(false); setPersonal(undefined); setBusy(false); }} width="min(640px, 100vw)">
   <Alert type="info" showIcon message="仅实际资源所有者可授予许可。会话管理权不授予私人资源读取或行动批准权。第三方自行持有的外部凭据不在 AIM 全面控制范围内。" />
   <Form layout="vertical"><Form.Item label="当前会话 Bot"><Select value={botId} placeholder="选择 Bot" options={bots.map((bot) => ({ value: bot.bot_id, label: bot.name }))} onChange={(value) => { epoch.current += 1; setBusy(false); setBotId(value); setResources([]); setGrants([]); setActions([]); setPersonal(undefined); setSelected([]); setError(''); resourceForm.resetFields(); actionForm.resetFields(); personalForm.resetFields(); queryForm.resetFields(); inputForm.resetFields(); privateRetry.current = undefined; privateControlKeys.current.clear(); onSelect([]); }} /></Form.Item></Form>
   <Button loading={busy} disabled={!botId} onClick={() => void perform(load)}>查询我的授权</Button>
   {error && <Alert type="error" message={error} showIcon />}
   <Tabs items={[
    { key: 'resources', label: '资源读取与分享', children: <>
     <Form form={resourceForm} layout="vertical" onFinish={(values) => void perform(async () => {
      if (!botId) throw new Error('请选择 Bot');
      await authorizationApi.resources({ op: 'create', bot_id: botId, conv_id: convId, resource_type: values.resource_type, resource_id: entityId(values.resource_id, 'resource_id'), scope: values.scope, can_read: !!values.can_read, can_share: !!values.can_share, expires_at: deadline(values.minutes) });
      await load();
     })}>
      <Form.Item name="resource_type" label="资源类型" initialValue="file"><Select options={[{ value: 'file', label: '文件' }, { value: 'knowledge', label: '知识库' }]} /></Form.Item>
      <Form.Item name="resource_id" label="资源 ID（由你拥有的文件或知识库）" rules={[{ required: true }]}><Input /></Form.Item>
      <Form.Item name="scope" label="使用范围" initialValue="personal"><Select options={[{ value: 'personal', label: '个人委托：仅相同请求者的个人工作' }, { value: 'conversation', label: '此会话：仅当前 Bot 绑定' }]} /></Form.Item>
      <Form.Item name="can_read" valuePropName="checked" initialValue={true}><Checkbox>允许读取</Checkbox></Form.Item>
      <Form.Item name="can_share" valuePropName="checked" initialValue={false}><Checkbox>允许进入群共享输入、记录和结果（独立于读取）</Checkbox></Form.Item>
      {expiryField}<Button htmlType="submit" type="primary" loading={busy} disabled={!botId}>授予资源许可</Button>
     </Form>
     {resources.length === 0 && <Empty description="尚无你的资源许可，请查询或授予" />}
     {resources.map((grant) => <div className="chat-run-details" key={grant.id}>
      <Space wrap><Tag>{grant.resource_type}</Tag><strong>{grant.resource_id}</strong><Tag>{grant.scope === 'personal' ? '个人' : '此会话'}</Tag><Tag>{grant.can_read ? '可读' : '不可读'}</Tag><Tag>{grant.can_share ? '可分享' : '不可分享'}</Tag></Space>
      <p>许可 ID：<code>{grant.id}</code></p>
      <p>有效期：{new Date(grant.expires_at).toLocaleString()} · {grant.revoked_at ? '已撤销' : '未撤销'}</p>
      {!grant.revoked_at && <Space wrap><Checkbox disabled={grant.scope !== 'conversation' || !grant.can_read || !grant.can_share || Date.parse(grant.expires_at) <= Date.now()} checked={selected.includes(grant.id)} onChange={(event) => {
       const next = event.target.checked ? [...selected, grant.id] : selected.filter((id) => id !== grant.id); setSelected(next); onSelect(next);
      }}>用于本次群工作</Checkbox><Button onClick={() => { personalForm.setFieldValue('grants', grant.id); if (grant.resource_type === 'file') personalForm.setFieldValue('files', grant.resource_id); }}>填入个人工作许可</Button><Button danger loading={busy} onClick={() => void perform(async () => { await authorizationApi.resources({ op: 'revoke', grant_id: grant.id }); if (currentScope.current !== scopeKey) return; const next = selected.filter((id) => id !== grant.id); setSelected(next); onSelect(next); await load(); })}>撤销读取与分享许可</Button></Space>}
     </div>)}
    </> },
    { key: 'actions', label: '受限自动行动', children: <>
     <Alert type="info" message="待确认收件箱仅展示你当前有权处理的具体行动。个人工作须由请求者明确提交此确认，不授予整份工作的来源或结果读取权。批准不代表实际完成。" />
     <Button loading={busy} disabled={!botId} onClick={() => void perform(loadActions)}>查询待确认行动</Button>
     {actions.length === 0 && <Empty description="暂无当前有权处理的具体行动" />}
     <ActionConfirmations key={scopeKey} actions={actions} scopeKey={scopeKey} onDecided={loadActions} />
     <Alert type="warning" message="仅授予下面精确参数，不是全 Run 无限许可。秘密参数不能从脱敏摘要还原；由资源所有者明确填写。" />
     <Form form={actionForm} layout="vertical" onFinish={(values) => void perform(async () => {
      if (!botId) throw new Error('请选择 Bot');
      const args: unknown = JSON.parse(values.arguments);
      if (!args || typeof args !== 'object' || Array.isArray(args)) throw new Error('参数必须为 JSON 对象');
      await authorizationApi.actions({ op: 'grant', bot_id: botId, conv_id: convId, server_id: entityId(values.server_id, 'server_id'), tool_name: values.tool_name, arguments: args, expires_at: deadline(values.minutes) });
      if (currentScope.current !== scopeKey) return;
      actionForm.setFieldValue('arguments', ''); await load();
     })}>
      <Form.Item name="server_id" label="由你管理的 MCP 服务器 ID" rules={[{ required: true }]}><Input /></Form.Item>
      <Form.Item name="tool_name" label="工具名称" rules={[{ required: true }]}><Input /></Form.Item>
      <Form.Item name="arguments" label="精确作用范围 / 参数（JSON 对象）" rules={[{ required: true }]}><Input.TextArea rows={4} /></Form.Item>
      {expiryField}<Button htmlType="submit" type="primary" loading={busy} disabled={!botId}>授予精确范围自动许可</Button>
     </Form>
     {grants.length === 0 && <Empty description="尚无你的自动行动许可" />}
     {grants.map((grant) => <div key={grant.id} className="chat-run-details"><strong>{grant.tool_name}</strong><p>服务器 {grant.server_id} · 有效期 {typeof grant.expires_at === 'number' ? new Date(grant.expires_at * 1000).toLocaleString() : new Date(grant.expires_at).toLocaleString()}</p><Tag>{grant.revoked ? '已撤销' : '已授权'}</Tag>{!grant.revoked && <Button danger loading={busy} onClick={() => void perform(async () => { await authorizationApi.actions({ op: 'revoke', grant_id: grant.id }); await load(); })}>撤销自动许可</Button>}</div>)}
    </> },
    { key: 'personal', label: '个人工作', children: <>
     <Alert type="info" message="浏览器中的个人输入和结果仅在此页内存中保存，不进入会话列表、消息历史或浏览器持久缓存。关闭面板或重连后清除旧工作投影；保留 Run ID 可重新授权查询。" />
     <Form form={personalForm} layout="vertical" onFinish={(values) => void perform(async () => {
      if (!botId) throw new Error('请选择 Bot');
      const resource_grant_ids = (values.grants || '').split(/[\s,]+/).filter(Boolean).map((id: string) => entityId(id, 'grant_id'));
      const file_ids = (values.files || '').split(/[\s,]+/).filter(Boolean).map((id: string) => entityId(id, 'file_id'));
      const signature = JSON.stringify({ botId, convId, values });
      if (privateRetry.current?.signature !== signature) privateRetry.current = { signature, key: crypto.randomUUID() };
      const current = epoch.current;
      const run = await botApi.startRun(botId, { conv_id: convId, message: values.message, visibility: 'personal', resource_grant_ids, file_ids, submission_key: privateRetry.current.key });
      if (current !== epoch.current || currentScope.current !== scopeKey) return;
      setPersonal(run); queryForm.setFieldValue('run_id', run.id); privateRetry.current = undefined;
     })}>
      <Form.Item name="message" label="个人工作内容" rules={[{ required: true }]}><Input.TextArea rows={3} /></Form.Item>
      <Form.Item name="grants" label="个人资源许可 ID（逗号分隔）"><Input /></Form.Item>
      <Form.Item name="files" label="要使用的文件 ID（逗号分隔，须对应有效许可）"><Input /></Form.Item>
      <Button htmlType="submit" type="primary" loading={busy} disabled={!botId}>发起个人工作</Button>
     </Form>
     <Form form={queryForm} layout="vertical" onFinish={(values) => void perform(() => refreshPersonal(values.run_id))}><Form.Item name="run_id" label="个人 Run ID" rules={[{ required: true }]}><Input /></Form.Item><Button htmlType="submit" loading={busy}>查询个人工作</Button></Form>
     {personalOwned && personal && <article className="chat-run-card" aria-label="个人工作结果" data-run-id={personal.id} data-run-status={personal.status} data-stop-status={personal.stop_status}>
      <strong>个人 Run：{personal.id}</strong><p>真实状态：{personal.status} · 结果状态：{personal.output_status}</p>
      {personal.waiting_reason && <p>{personal.waiting_reason}</p>}{personal.error && <Alert type="error" message={personal.error} />}
      <p>停止状态：{personal.stop_status} · 执行器状态：{personal.executor_status}。停止请求不等于实际停工，已发生的行动不会自动回滚。</p>
      {personal.executor_status === 'lost' && <Alert type="warning" message="执行器失联，旧认领已过期；安全工作须明确恢复。" />}
      {personal.stage === 'action_outcome_unknown' && <Alert type="warning" message="外部行动结果未知，等待人工核对；不会自动重做或回滚。" />}
      {personal.input_sources.length > 0 && <details className="chat-run-details"><summary>已接受输入与补充者 · {personal.input_sources.length} 条</summary>
       <ol className="chat-run-steps">{personal.input_sources.map((source) => <li key={source.version}>
        输入 v{source.version} · <span title={source.requester_id}>{source.sender_type === 'user' ? '用户' : source.sender_type === 'bot' ? 'Bot' : source.sender_type === 'rule' ? '显式规则' : '系统'} {source.requester_id || '非人类发起，无人类请求者'}</span>
        {source.content !== undefined
         ? source.content === ''
          ? <p className="chat-run-notice">此接受记录的正文已清除，不能从原消息恢复。</p>
          : <div className="chat-run-result"><pre>{source.content}</pre></div>
         : <p className="chat-run-notice">当前投影未提供已接受正文；来源消息仅用于定位。</p>}
       </li>)}</ol>
      </details>}
      <pre>{personal.result_text}</pre>{personal.result_content && <pre>{JSON.stringify(personal.result_content, null, 2)}</pre>}
      <RunActions key={`${scopeKey}:${personal.id}`} run={personal} onUpdated={async (run) => { if (currentScope.current === scopeKey && run.personal_user_id === useAuthStore.getState().user?.id && run.conv_id === convId && run.bot_id === botId) setPersonal(run); }} />
      {personalCanInput && <Form form={inputForm} layout="vertical" onFinish={(values) => void perform(() => controlPersonal('input', values.message))}>
       <Form.Item name="message" label="个人补充内容" rules={[{ required: true, whitespace: true }]}><Input.TextArea rows={3} /></Form.Item>
       <Button htmlType="submit" type="primary" loading={busy}>提交个人补充输入</Button>
      </Form>}
      <Space wrap>
       <Button loading={busy} onClick={() => void perform(() => refreshPersonal(personal.id))}>查询个人工作最新状态</Button>
       {personalCanStop && <Button danger loading={busy} onClick={() => void perform(() => controlPersonal('stop'))}>请求停止个人工作</Button>}
       {personalCanResume && <Button loading={busy} onClick={() => void perform(() => controlPersonal('resume'))}>明确恢复个人工作</Button>}
      </Space>
     </article>}
    </> },
   ]} />
  </Drawer>}
 </>;
}
