import client, { unwrap } from './client';
import type { APIResponse, PublicAction } from '@/types/model';
import { normalizeAction } from './bot';

export interface ResourceGrant {
 id: string;
 bot_id: string;
 conv_id: string;
 resource_type: 'file' | 'knowledge';
 resource_id: string;
 scope: 'conversation' | 'personal';
 can_read: boolean;
 can_share: boolean;
 expires_at: string;
 revoked_at?: string;
}
export interface ActionGrant {
 id: string;
 bot_id: string;
 conv_id: string;
 server_id: string;
 tool_name: string;
 expires_at: number | string;
 revoked: boolean;
}
export type ActionDecision = 'approve' | 'deny' | 'share_confirmation';
export const authorizationApi = {
 resources: <T = ResourceGrant[]>(body: Record<string, unknown>) => client.post<APIResponse<T>>('/bot-authorizations/resources', body).then(unwrap),
 actions: <T = unknown>(body: Record<string, unknown>) => client.post<APIResponse<T>>('/bot-authorizations/actions', body).then(unwrap),
 listActions: async (botId: string, convId: string): Promise<{ grants: ActionGrant[]; actions: PublicAction[] }> => {
  const value = await authorizationApi.actions<{ grants: ActionGrant[]; actions?: unknown[] }>({ op: 'list', bot_id: botId, conv_id: convId });
  return { grants: value.grants, actions: (value.actions ?? []).map(normalizeAction) };
 },
 decideAction: async (action: PublicAction, op: ActionDecision, submissionKey: string): Promise<PublicAction> => normalizeAction(await authorizationApi.actions({
  op, run_id: action.run_id, action_id: action.id, generation: action.generation, input_version: action.input_version,
  submission_key: submissionKey, expires_at: action.expires_at,
 })),
};
