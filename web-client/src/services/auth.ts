import client, { unwrap, refreshSession } from './client';
import { deviceCredentials } from './device';
import type { LoginReq, RegisterReq, UpdateUserSettingsReq } from '@/types/api';
import type { APIResponse, LoginResp, SessionInfo, UserInfo, UserSettings } from '@/types/model';

export const authApi = {
  login: (data: LoginReq) =>
    client.post<APIResponse<LoginResp>>('/auth/login', { ...data, ...deviceCredentials() }).then(unwrap),

  register: (data: RegisterReq) =>
    client.post<APIResponse<LoginResp>>('/auth/register', { ...data, ...deviceCredentials() }).then(unwrap),

  logout: () =>
    client.post<APIResponse<null>>('/auth/logout', {}).then(unwrap),

  refresh: refreshSession,

  getSessions: () =>
    client.get<APIResponse<{ sessions: SessionInfo[] }>>('/auth/sessions').then((r) => r.data.data.sessions),

  revokeSession: (id: string) =>
    client.delete<APIResponse<null>>(`/auth/sessions/${id}`).then(unwrap),

  getProfile: () =>
    client.get<APIResponse<UserInfo>>('/users/me').then(unwrap),

  updateProfile: (data: Record<string, unknown>) =>
    client.put<APIResponse<UserInfo>>('/users/me', data).then(unwrap),

  updatePassword: (oldPwd: string, newPwd: string) =>
    client.put<APIResponse<null>>('/users/me/password', { old_password: oldPwd, new_password: newPwd }).then(unwrap),

  bindPhone: (phone: string) =>
    client.put<APIResponse<null>>('/users/me/phone', { phone }).then(unwrap),

  bindEmail: (email: string) =>
    client.put<APIResponse<null>>('/users/me/email', { email }).then(unwrap),

  getSettings: () =>
    client.get<APIResponse<UserSettings>>('/users/me/settings').then(unwrap),

  updateSettings: (data: UpdateUserSettingsReq) =>
    client.put<APIResponse<null>>('/users/me/settings', data).then(unwrap),

  uploadAvatar: (file: File) => {
    const fd = new FormData();
    fd.append('file', file);
    return client.post<APIResponse<any>>('/users/me/avatar', fd).then(unwrap);
  },

  recharge: (amount: number) =>
    client.post<APIResponse<{ new_balance: number }>>('/users/me/recharge', { amount }).then(unwrap),
};
