import { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Input, Select, Switch, Tag } from 'antd';
import { useMutation } from '@tanstack/react-query';
import { isAxiosError } from 'axios';
import { botApi, isBotBindingActive } from '@/services/bot';
import { entityId, optionalEntityId, sequence } from '@/utils/json';
import type { BotInConv } from '@/types/model';

const ACTIVITY_OPTIONS = [
  { value: 'message.created', label: '新消息' },
  { value: 'member.joined', label: '成员加入' },
  { value: 'member.left', label: '成员离开' },
  { value: 'business.activity', label: '获准业务活动' },
];
type ActivityType = 'message.created' | 'member.joined' | 'member.left' | 'business.activity';
interface ResponseRule {
  id: string; enabled: boolean; activity_type: ActivityType;
  message_contains?: string; business_type?: string;
  version?: number; authorization_user_id?: string; authorization_member_id?: string; start_seq?: number;
}
interface PolicyDraft { activity_types: ActivityType[]; response_rules: ResponseRule[] }

function readPolicy(raw: string): { settings: Record<string, unknown>; policy: PolicyDraft } {
  const settings: unknown = JSON.parse(raw || '{}');
  if (!settings || typeof settings !== 'object' || Array.isArray(settings)) throw new Error('绑定设置格式无效');
  const record = settings as Record<string, unknown>;
  const types = record.activity_types ?? [];
  const rules = record.response_rules ?? [];
  if (!Array.isArray(types) || !types.every((type) => ACTIVITY_OPTIONS.some((option) => option.value === type))
    || !Array.isArray(rules)) throw new Error('订阅或规则格式无效，请刷新当前绑定');
  const response_rules = rules.map((value): ResponseRule => {
    if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('响应规则格式无效');
    const rule = value as Record<string, unknown>;
    if (typeof rule.enabled !== 'boolean' || !ACTIVITY_OPTIONS.some((option) => option.value === rule.activity_type)
      || rule.message_contains != null && typeof rule.message_contains !== 'string'
      || rule.business_type != null && typeof rule.business_type !== 'string') throw new Error('响应规则条件无效');
    return {
      id: entityId(rule.id, 'rule.id'), enabled: rule.enabled, activity_type: rule.activity_type as ActivityType,
      message_contains: rule.message_contains as string | undefined, business_type: rule.business_type as string | undefined,
      version: rule.version == null ? undefined : sequence(rule.version, 'rule.version', true),
      authorization_user_id: optionalEntityId(rule.authorization_user_id, 'rule.authorization_user_id'),
      authorization_member_id: optionalEntityId(rule.authorization_member_id, 'rule.authorization_member_id'),
      start_seq: rule.start_seq == null ? undefined : sequence(rule.start_seq, 'rule.start_seq'),
    };
  });
  return { settings: record, policy: { activity_types: types as ActivityType[], response_rules } };
}

export function BindingRules({ bot, convId, isAdmin, unavailable, onSaved }: {
  bot: BotInConv; convId: string; isAdmin: boolean; unavailable: boolean; onSaved: () => Promise<unknown>;
}) {
  const loaded = useMemo(() => {
    try { return { value: readPolicy(bot.bot_settings) }; }
    catch (error) { return { error: error instanceof Error ? error.message : '绑定策略读取失败' }; }
  }, [bot.bot_settings]);
  const [draft, setDraft] = useState<PolicyDraft>();
  const [error, setError] = useState('');
  const draftBase = useRef('');
  useEffect(() => {
    if (!draft && loaded.value) draftBase.current = JSON.stringify(loaded.value.policy);
  }, [draft, loaded]);
  const allowed = isAdmin && !unavailable && isBotBindingActive(bot) && !!loaded.value;
  const latest = useRef({ allowed, bindingId: bot.binding_id });
  latest.current = { allowed, bindingId: bot.binding_id };
  useEffect(() => { if (!allowed) setDraft(undefined); }, [allowed]);
  useEffect(() => {
    latest.current.allowed = allowed;
    return () => { latest.current.allowed = false; };
  }, [allowed]);
  const policy = draft ?? loaded.value?.policy;
  const save = useMutation({
    mutationFn: async () => {
      if (!latest.current.allowed || !draft || !loaded.value) throw new Error('当前无权管理规则或绑定已失效，请刷新');
      if (draft.response_rules.some((rule) => rule.enabled && !draft.activity_types.includes(rule.activity_type))) throw new Error('启用规则前，请先订阅对应活动类型');
      const current = (await botApi.listConvBots(convId)).find((candidate) => candidate.binding_id === bot.binding_id);
      if (!latest.current.allowed || latest.current.bindingId !== bot.binding_id || !isBotBindingActive(current)) throw new Error('当前绑定已失效，请刷新');
      const fresh = readPolicy(current.bot_settings);
      if (JSON.stringify(fresh.policy) !== draftBase.current) throw new Error('规则已被更新，请取消编辑并刷新后重试');
      const response_rules = draft.response_rules.map((rule) => ({
        id: rule.id, enabled: rule.enabled, activity_type: rule.activity_type,
        ...(rule.activity_type === 'message.created' && rule.message_contains ? { message_contains: rule.message_contains } : {}),
        ...(rule.activity_type === 'business.activity' && rule.business_type ? { business_type: rule.business_type } : {}),
      }));
      await botApi.setParticipation(convId, bot.bot_id, JSON.stringify({ ...fresh.settings, activity_types: draft.activity_types, response_rules }));
    },
    onSuccess: async () => { await onSaved(); setDraft(undefined); setError(''); },
    onError: (failure: unknown) => setError(isAxiosError(failure) && typeof failure.response?.data?.message === 'string'
      ? failure.response.data.message : failure instanceof Error ? failure.message : '规则保存失败，请刷新后重试'),
  });
  const updateRule = (id: string, patch: Partial<ResponseRule>) => {
    if (!allowed || save.isPending || !policy) return;
    setDraft({ ...policy, response_rules: policy.response_rules.map((rule) => rule.id === id ? { ...rule, ...patch } : rule) });
    setError('');
  };
  return <details className="group-info-binding-rules" data-binding-rules={bot.binding_id}>
    <summary>输入订阅与自动响应{policy ? ` · ${policy.response_rules.filter((rule) => rule.enabled).length} 条已启用` : ''}</summary>
    <p className="chat-run-notice">输入订阅只允许接收当前获准活动，不授予行动权限或历史读取权。默认仅精确提及、明确操作或明确委派启动工作；普通 Bot 发言不触发自动协作。</p>
    {loaded.error && <p role="alert" className="chat-run-error">{loaded.error}</p>}
    {!isAdmin && <p role="status" className="chat-run-notice">仅当前会话管理员可修改订阅与显式响应规则。</p>}
    {isAdmin && !allowed && <p role="status" className="chat-run-notice">绑定停用、参与许可撤销或绑定读取失败时不能保存规则；请先恢复有效参与资格。</p>}
    {policy && <div className="group-info-rule-fields" aria-busy={save.isPending}>
      <label htmlFor={`binding-subscriptions-${bot.binding_id}`}>输入活动订阅</label>
      <Select id={`binding-subscriptions-${bot.binding_id}`} aria-label="输入活动订阅" mode="multiple" maxTagCount="responsive"
        disabled={!allowed || save.isPending} value={policy.activity_types} options={ACTIVITY_OPTIONS} placeholder="默认不订阅活动"
        onChange={(activity_types: ActivityType[]) => { setDraft({ ...policy, activity_types }); setError(''); }} />
      <strong>显式自动响应规则</strong>
      {policy.response_rules.length === 0 && <p className="chat-run-notice">尚无自动响应规则。添加后仍默认关闭，须主动启用并保存。</p>}
      {policy.response_rules.map((rule) => <fieldset key={rule.id} className="group-info-response-rule" disabled={!allowed || save.isPending} data-rule-id={rule.id}>
        <legend>规则 {rule.id}</legend>
        <div className="group-info-binding-permission">
          <label htmlFor={`rule-enabled-${rule.id}`}>显式启用自动响应</label>
          <Switch id={`rule-enabled-${rule.id}`} aria-label="显式启用自动响应" checked={rule.enabled} disabled={!allowed || save.isPending}
            onChange={(enabled) => updateRule(rule.id, { enabled })} />
        </div>
        <label htmlFor={`rule-activity-${rule.id}`}>触发活动类型</label>
        <Select id={`rule-activity-${rule.id}`} aria-label="触发活动类型" value={rule.activity_type} options={ACTIVITY_OPTIONS} disabled={!allowed || save.isPending}
          onChange={(activity_type: ActivityType) => updateRule(rule.id, { activity_type, message_contains: undefined, business_type: undefined })} />
        {rule.activity_type === 'message.created' && <>
          <label htmlFor={`rule-message-${rule.id}`}>消息包含（可选，留空匹配所有获准用户新消息）</label>
          <Input id={`rule-message-${rule.id}`} value={rule.message_contains ?? ''} disabled={!allowed || save.isPending}
            onChange={(event) => updateRule(rule.id, { message_contains: event.target.value })} />
        </>}
        {rule.activity_type === 'business.activity' && <>
          <label htmlFor={`rule-business-${rule.id}`}>业务活动类型（可选，留空匹配所有获准业务活动）</label>
          <Input id={`rule-business-${rule.id}`} value={rule.business_type ?? ''} disabled={!allowed || save.isPending}
            onChange={(event) => updateRule(rule.id, { business_type: event.target.value })} />
        </>}
        {rule.enabled && !policy.activity_types.includes(rule.activity_type) && <p className="chat-run-error" role="alert">此规则缺少对应输入订阅，不能保存。</p>}
        <div className="group-info-binding-details">
          <Tag>{rule.version ? `已保存 v${rule.version}` : '未保存'}</Tag>
          {rule.authorization_user_id && <div>授权管理员：{rule.authorization_user_id}</div>}
          {rule.authorization_member_id && <div>授权成员生命周期：{rule.authorization_member_id}</div>}
          {rule.start_seq !== undefined && <div>后续活动起点：#{rule.start_seq}</div>}
        </div>
        {isAdmin && <Button danger size="small" disabled={!allowed || save.isPending} onClick={() => {
          setDraft({ ...policy, response_rules: policy.response_rules.filter((candidate) => candidate.id !== rule.id) }); setError('');
        }}>移除规则</Button>}
      </fieldset>)}
      {isAdmin && <Button disabled={!allowed || save.isPending} onClick={() => {
        setDraft({ ...policy, response_rules: [...policy.response_rules, { id: crypto.randomUUID(), enabled: false, activity_type: 'message.created' }] }); setError('');
      }}>添加响应规则</Button>}
      <p className="chat-run-notice">保存只影响后续触发，不重跑历史活动或改写已接受工作。规则版本和真实授权来源由服务端记录；撤权仍限制当前访问。</p>
      {isAdmin && draft && <div className="group-info-edit-actions">
        <Button type="primary" loading={save.isPending} disabled={!allowed || policy.response_rules.some((rule) => rule.enabled && !policy.activity_types.includes(rule.activity_type))}
          onClick={() => { setError(''); save.mutate(); }}>保存订阅与规则</Button>
        <Button disabled={save.isPending} onClick={() => { setDraft(undefined); setError(''); }}>取消编辑</Button>
      </div>}
      {error && <div role="alert" className="chat-run-error">{error}</div>}
    </div>}
  </details>;
}
