import client, { unwrap } from './client';
import type { CreateKBReq } from '@/types/api';
import type { APIResponse, KBRsp, DocumentRsp, ChunkInfo, WikiPageRsp, WikiPageItem, WikiSearchItem, WikiSourceDocRsp, WikiIssueItem, WikiGraphData } from '@/types/model';

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

  bindToBot: (botId: number, kbId: number) =>
    client.post<APIResponse<null>>(`/bots/${botId}/knowledge`, { kb_id: kbId }).then(unwrap),

  unbindFromBot: (botId: number, kbId: number) =>
    client.delete<APIResponse<null>>(`/bots/${botId}/knowledge/${kbId}`).then(unwrap),

  listBotBindings: (botId: number) =>
    client.get<APIResponse<{ items: any[] }>>(`/bots/${botId}/knowledge`)
      .then((r) => r.data.data.items ?? []),

  bindToConv: (convId: number, kbId: number) =>
    client.post<APIResponse<null>>(`/convs/${convId}/knowledge`, { kb_id: kbId }).then(unwrap),

  unbindFromConv: (convId: number, kbId: number) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/knowledge/${kbId}`).then(unwrap),

  listConvBindings: (convId: number) =>
    client.get<APIResponse<{ items: any[] }>>(`/convs/${convId}/knowledge`)
      .then((r) => r.data.data.items ?? []),

  // ─── Wiki ───
  wikiReadPage: (kbId: number | string, slug: string) =>
    client.get<APIResponse<WikiPageRsp>>(`/knowledge/bases/${kbId}/wiki/pages/${encodeURIComponent(slug)}`).then(unwrap),

  wikiListPages: (kbId: number | string, params?: { page_type?: string; limit?: number; offset?: number }) =>
    client.get<APIResponse<{ items: WikiPageItem[]; total: number }>>(`/knowledge/bases/${kbId}/wiki/pages`, { params })
      .then((r) => ({ list: (r.data as any)?.data?.items ?? [], total: (r.data as any)?.data?.total ?? 0 })),

  wikiSearch: (kbId: number, query: string, limit?: number) =>
    client.get<APIResponse<WikiSearchItem[]>>(`/knowledge/bases/${kbId}/wiki/search`, { params: { query, limit } })
      .then(unwrap),

  wikiUpdatePage: (kbId: number, slug: string, data: { title?: string; content?: string; summary?: string; aliases?: string[] }) =>
    client.put<APIResponse<WikiPageRsp>>(`/knowledge/bases/${kbId}/wiki/pages/${encodeURIComponent(slug)}`, data).then(unwrap),

  wikiDeletePage: (kbId: number | string, slug: string) =>
    client.delete<APIResponse<null>>(`/knowledge/bases/${kbId}/wiki/pages/${encodeURIComponent(slug)}`).then(unwrap),

  wikiListIssues: (kbId: number, status?: string) =>
    client.get<APIResponse<{ items: WikiIssueItem[] }>>(`/knowledge/bases/${kbId}/wiki/issues`, { params: { status } }).then(unwrap),

  wikiRefresh: (kbId: number) =>
    client.post<APIResponse<{ pages_updated: number }>>(`/knowledge/bases/${kbId}/wiki/refresh`).then(unwrap),

  wikiRunMaintenance: (kbId: number) =>
    client.post<APIResponse<{ pages_created: number; issues_found: number; duration_ms: number }>>(
      `/knowledge/bases/${kbId}/wiki/maintenance`,
    ).then(unwrap),

  wikiGraph: (kbId: string) =>
    client.get<APIResponse<WikiGraphData>>(`/knowledge/bases/${kbId}/wiki/graph`).then(unwrap),

  // ─── Wiki — New ───

  wikiBatchUpload: (kbId: number, files: File[]) => {
    const fd = new FormData();
    files.forEach(f => fd.append('files', f));
    return client.post<APIResponse<{ documents: DocumentRsp[] }>>(
      `/knowledge/bases/${kbId}/wiki/batch-upload`, fd,
      { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 300000 },
    ).then(unwrap);
  },

  wikiReadSourceDoc: (kbId: number, docId: number, query?: string) =>
    client.get<APIResponse<WikiSourceDocRsp>>(
      `/knowledge/bases/${kbId}/wiki/source/${docId}`, { params: { query } }
    ).then(unwrap),

  wikiReplaceText: (kbId: number, slug: string, oldText: string, newText: string) =>
    client.put<APIResponse<WikiPageRsp>>(
      `/knowledge/bases/${kbId}/wiki/replace-text/${encodeURIComponent(slug)}`,
      { old_text: oldText, new_text: newText }
    ).then(unwrap),

  wikiRenamePage: (kbId: number, slug: string, newSlug: string) =>
    client.put<APIResponse<WikiPageRsp>>(
      `/knowledge/bases/${kbId}/wiki/rename-page/${encodeURIComponent(slug)}`,
      { new_slug: newSlug }
    ).then(unwrap),

  wikiFlagIssue: (kbId: number, slug: string, issueType: string, description: string) =>
    client.post<APIResponse<{ issue_id: number }>>(
      `/knowledge/bases/${kbId}/wiki/issues`, { slug, issue_type: issueType, description }
    ).then(unwrap),

  wikiReadIssue: (kbId: number, params?: { slug?: string; issue_id?: number; status?: string }) =>
    client.get<APIResponse<WikiIssueItem[]>>(
      `/knowledge/bases/${kbId}/wiki/issues`, { params }
    ).then(unwrap),

  wikiUpdateIssue: (kbId: number, issueId: number, status: string) =>
    client.put<APIResponse<null>>(
      `/knowledge/bases/${kbId}/wiki/issues/${issueId}`, { status }
    ).then(unwrap),
};
