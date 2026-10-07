import client, { unwrap } from './client';
import type { CreateKBReq } from '@/types/api';
import type { APIResponse, KBRsp, DocumentRsp, ChunkInfo } from '@/types/model';

export const kbApi = {
  create: (data: CreateKBReq) =>
    client.post<APIResponse<KBRsp>>('/knowledge/bases', data).then(unwrap),

  update: (id: number, data: Record<string, unknown>) =>
    client.put<APIResponse<KBRsp>>(`/knowledge/bases/${id}`, data).then(unwrap),

  delete: (id: number) =>
    client.delete<APIResponse<null>>(`/knowledge/bases/${id}`).then(unwrap),

  get: (id: number) =>
    client.get<APIResponse<KBRsp>>(`/knowledge/bases/${id}`).then(unwrap),

  list: () =>
    client.get<APIResponse<{ items: KBRsp[]; total: number }>>('/knowledge/bases')
      .then((r) => ({ list: r.data.data.items ?? [], total: r.data.data.total ?? 0 })),

  // ─── Documents ───
  uploadDocument: (kbId: number, file: File, title?: string, metadata?: string) => {
    const fd = new FormData();
    fd.append('file', file);
    if (title) fd.append('title', title);
    if (metadata) fd.append('metadata', metadata);
    return client.post<APIResponse<DocumentRsp>>(`/knowledge/bases/${kbId}/documents`, fd, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 120000,
    }).then(unwrap);
  },

  listDocuments: (kbId: number, params?: { offset?: number; limit?: number; status?: string }) =>
    client.get<APIResponse<{ items: DocumentRsp[]; total: number }>>(`/knowledge/bases/${kbId}/documents`, { params })
      .then((r) => ({ list: r.data.data.items ?? [], total: r.data.data.total ?? 0 })),

  getDocument: (docId: number) =>
    client.get<APIResponse<DocumentRsp>>(`/knowledge/documents/${docId}`).then(unwrap),

  deleteDocument: (docId: number) =>
    client.delete<APIResponse<null>>(`/knowledge/documents/${docId}`).then(unwrap),

  retryDocument: (docId: number) =>
    client.post<APIResponse<DocumentRsp>>(`/knowledge/documents/${docId}/retry`).then(unwrap),

  getDocumentContent: (docId: number) =>
    client.get<APIResponse<{ content: string; download_url: string; mime_type: string; file_size: number }>>(
      `/knowledge/documents/${docId}/content`,
    ).then(unwrap),

  listChunks: (docId: number, params?: { offset?: number; limit?: number }) =>
    client.get<APIResponse<{ chunks: ChunkInfo[]; total: number }>>(`/knowledge/documents/${docId}/chunks`, { params })
      .then((r) => ({ list: r.data.data.chunks ?? [], total: r.data.data.total ?? 0 })),

  search: (kbId: number, query: string) =>
    client.post<APIResponse<any>>(`/knowledge/bases/${kbId}/search`, { query }).then(unwrap),

  // ─── Bindings ───
  listBindings: (targetType: string, targetId: number) =>
    client.get<APIResponse<{ items: any[] }>>(`/knowledge/bases/${targetId}/bindings`, {
      params: { target_type: targetType },
    }).then((r) => r.data.data.items ?? []),

  bindToBot: (botId: string, kbId: number) =>
    client.post<APIResponse<null>>(`/bots/${botId}/knowledge`, { kb_id: kbId }).then(unwrap),

  unbindFromBot: (botId: string, kbId: number) =>
    client.delete<APIResponse<null>>(`/bots/${botId}/knowledge/${kbId}`).then(unwrap),

  listBotBindings: (botId: string) =>
    client.get<APIResponse<{ items: any[] }>>(`/bots/${botId}/knowledge`)
      .then((r) => r.data.data.items ?? []),

  bindToConv: (convId: number, kbId: number) =>
    client.post<APIResponse<null>>(`/convs/${convId}/knowledge`, { kb_id: kbId }).then(unwrap),

  unbindFromConv: (convId: number, kbId: number) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/knowledge/${kbId}`).then(unwrap),

  listConvBindings: (convId: number) =>
    client.get<APIResponse<{ items: any[] }>>(`/convs/${convId}/knowledge`)
      .then((r) => r.data.data.items ?? []),
};
