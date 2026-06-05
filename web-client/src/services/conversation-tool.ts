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

export const convToolApi = {
  summarize: (convId: number | string, range: { last_message_count?: number; all?: boolean }) =>
    client.post<APIResponse<{ summary_id: number }>>(`/convs/${convId}/summarize`, range).then(unwrap),

  getSummaries: (convId: number | string, limit?: number) =>
    client.get<APIResponse<SummariesResponse>>(`/convs/${convId}/summaries`, { params: { limit } }).then(unwrap),

  createTodo: (convId: number | string, data: { summary_id: number; content: string }) =>
    client.post<APIResponse<TodoItem>>(`/convs/${convId}/todos`, data).then(unwrap),

  updateTodo: (convId: number | string, todoId: number, data: { content?: string; done?: boolean }) =>
    client.put<APIResponse<TodoItem>>(`/convs/${convId}/todos/${todoId}`, data).then(unwrap),

  deleteTodo: (convId: number | string, todoId: number) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/todos/${todoId}`).then(unwrap),

  replyCandidates: (convId: number | string, replyToMsgId?: number) =>
    client.post<APIResponse<{ candidates: string[] }>>(`/convs/${convId}/reply-candidates`, { reply_to_msg_id: replyToMsgId }).then(unwrap),

  translate: (msgId: number, text: string, targetLang: string) =>
    client.post<APIResponse<{ translated_text: string }>>(`/messages/${msgId}/translate`, { text, target_lang: targetLang }).then(unwrap),
};
