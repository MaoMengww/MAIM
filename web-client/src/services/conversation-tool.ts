import client, { unwrap } from './client';
import type { APIResponse } from '@/types/model';

export interface TodoItem {
  id: number;
  summary_id: number;
  content: string;
  done: boolean;
}

export interface SummaryItem {
  summary_id: number;
  summary: string;
  todos: TodoItem[];
  total_messages: number;
  created_at: number;
}

export interface SummariesResponse {
  items: SummaryItem[];
}

export interface AsyncResult {
  status: string; // "processing" | "completed"
}

export const convToolApi = {
  summarize: (convId: number | string, range: { last_message_count?: number; all?: boolean }) =>
    client.post<APIResponse<{ summary_id: number; status: string }>>(`/convs/${convId}/summarize`, range).then(unwrap),

  getSummaries: (convId: number | string, limit?: number) =>
    client.get<APIResponse<SummariesResponse>>(`/convs/${convId}/summaries`, { params: { limit } }).then(unwrap),

  createTodo: (convId: number | string, data: { summary_id: number; content: string }) =>
    client.post<APIResponse<TodoItem>>(`/convs/${convId}/todos`, data).then(unwrap),

  updateTodo: (convId: number | string, todoId: number, data: { content?: string; done?: boolean }) =>
    client.put<APIResponse<TodoItem>>(`/convs/${convId}/todos/${todoId}`, data).then(unwrap),

  deleteTodo: (convId: number | string, todoId: number) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/todos/${todoId}`).then(unwrap),

  replyCandidates: (convId: number | string, replyToMsgId?: number | string) =>
    client.post<APIResponse<{ candidates: string[]; status: string }>>(`/convs/${convId}/reply-candidates`, { reply_to_msg_id: replyToMsgId }).then(unwrap),

  translate: (msgId: number | string, text: string, targetLang: string) =>
    client.post<APIResponse<{ translated_text: string; detected_lang: string; status: string }>>(`/messages/${msgId}/translate`, { text, target_lang: targetLang }).then(unwrap),
};
