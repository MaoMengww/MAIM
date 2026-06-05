import client, { unwrap } from './client';
import type { LoginReq, RegisterReq } from '@/types/api';
import type { APIResponse, LoginResp, SessionInfo, UserInfo } from '@/types/model';

export const authApi = {
  login: (data: LoginReq) =>
    client.post<APIResponse<LoginResp>>('/auth/login', data).then(unwrap),

  register: (data: RegisterReq) =>
    client.post<APIResponse<LoginResp>>('/auth/register', data).then(unwrap),

  logout: () =>
    client.post<APIResponse<null>>('/auth/logout', {}).then(unwrap),

  refresh: (refreshToken: string) =>
    client.post<APIResponse<LoginResp>>('/auth/refresh', { refresh_token: refreshToken }).then(unwrap),

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
    client.get<APIResponse<any>>('/users/me/settings').then(unwrap),

  updateSettings: (data: Record<string, unknown>) =>
    client.put<APIResponse<null>>('/users/me/settings', data).then(unwrap),

  uploadAvatar: (file: File) => {
    const fd = new FormData();
    fd.append('file', file);
    return client.post<APIResponse<any>>('/users/me/avatar', fd).then(unwrap);
  },

  recharge: (amount: number) =>
    client.post<APIResponse<{ new_balance: number }>>('/users/me/recharge', { amount }).then(unwrap),
};
