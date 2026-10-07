import client, { unwrap } from './client';
import type { CreateKBReq, UpdateKBReq } from '@/types/api';
import type { APIResponse, KBRsp, DocumentRsp, ChunkInfo, KnowledgeBinding, KnowledgeBoundTarget, KnowledgeRetrieveItem } from '@/types/model';

export const kbApi = {
  create: (data: CreateKBReq) =>
    client.post<APIResponse<KBRsp>>('/knowledge/bases', data).then(unwrap),

  update: (id: string, data: UpdateKBReq) =>
    client.put<APIResponse<KBRsp>>(`/knowledge/bases/${id}`, data).then(unwrap),

  delete: (id: string) =>
    client.delete<APIResponse<null>>(`/knowledge/bases/${id}`).then(unwrap),

  get: (id: string) =>
    client.get<APIResponse<KBRsp>>(`/knowledge/bases/${id}`).then(unwrap),

  list: () =>
    client.get<APIResponse<{ items: KBRsp[]; total: number }>>('/knowledge/bases')
      .then((r) => ({ list: r.data.data.items ?? [], total: r.data.data.total ?? 0 })),

  // ─── Documents ───
  uploadDocument: (kbId: string, file: File, title?: string, metadata?: string) => {
    const fd = new FormData();
    fd.append('file', file);
    if (title) fd.append('title', title);
    if (metadata) fd.append('metadata', metadata);
    return client.post<APIResponse<DocumentRsp>>(`/knowledge/bases/${kbId}/documents`, fd, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 120000,
    }).then(unwrap);
  },

  listDocuments: (kbId: string, params?: { offset?: number; limit?: number; status?: string }) =>
    client.get<APIResponse<{ items: DocumentRsp[]; total: number }>>(`/knowledge/bases/${kbId}/documents`, { params })
      .then((r) => ({ list: r.data.data.items ?? [], total: r.data.data.total ?? 0 })),

  getDocument: (docId: string) =>
    client.get<APIResponse<DocumentRsp>>(`/knowledge/documents/${docId}`).then(unwrap),

  deleteDocument: (docId: string) =>
    client.delete<APIResponse<null>>(`/knowledge/documents/${docId}`).then(unwrap),

  retryDocument: (docId: string) =>
    client.post<APIResponse<DocumentRsp>>(`/knowledge/documents/${docId}/retry`).then(unwrap),

  getDocumentContent: (docId: string) =>
    client.get<APIResponse<{ content: string; download_url: string; mime_type: string; file_size: number }>>(
      `/knowledge/documents/${docId}/content`,
    ).then(unwrap),

  listChunks: (docId: string, params?: { offset?: number; limit?: number }) =>
    client.get<APIResponse<{ chunks: ChunkInfo[]; total: number }>>(`/knowledge/documents/${docId}/chunks`, { params })
      .then((r) => ({ list: r.data.data.chunks ?? [], total: r.data.data.total ?? 0 })),

  search: (kbId: string, query: string) =>
    client.post<APIResponse<{ items: KnowledgeRetrieveItem[] }>>(`/knowledge/bases/${kbId}/search`, { query }).then(unwrap),

  // ─── Bindings ───
  listKBBindings: (kbId: string) =>
    client.get<APIResponse<{ items: KnowledgeBoundTarget[] }>>(`/knowledge/bases/${kbId}/bindings`)
      .then((r) => r.data.data.items ?? []),

  bindToBot: (botId: string, kbId: string) =>
    client.post<APIResponse<null>>(`/bots/${botId}/knowledge`, { kb_id: kbId }).then(unwrap),

  unbindFromBot: (botId: string, kbId: string) =>
    client.delete<APIResponse<null>>(`/bots/${botId}/knowledge/${kbId}`).then(unwrap),

  listBotBindings: (botId: string) =>
    client.get<APIResponse<{ items: KnowledgeBinding[] }>>(`/bots/${botId}/knowledge`)
      .then((r) => r.data.data.items ?? []),

  bindToConv: (convId: string, kbId: string) =>
    client.post<APIResponse<null>>(`/convs/${convId}/knowledge`, { kb_id: kbId }).then(unwrap),

  unbindFromConv: (convId: string, kbId: string) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/knowledge/${kbId}`).then(unwrap),

  listConvBindings: (convId: string) =>
    client.get<APIResponse<{ items: KnowledgeBinding[] }>>(`/convs/${convId}/knowledge`)
      .then((r) => r.data.data.items ?? []),
};
