import client, { unwrap } from './client';
import type { APIResponse } from '@/types/model';

export interface TodoItem {
  id: string;
  summary_id?: string | null;
  conv_id: string;
  content: string;
  done: boolean;
}

export interface SummaryItem {
  summary_id: string;
  summary: string;
  todos: TodoItem[];
  total_messages: number;
  created_at: number;
}

export interface SummariesResponse {
  items: SummaryItem[];
  standalone_todos: TodoItem[];
}

export interface AsyncResult {
  status: string; // "processing" | "completed"
}

export const convToolApi = {
  summarize: (convId: string, range: { last_message_count?: number; all?: boolean }) =>
    client.post<APIResponse<{ summary_id?: string; status: string }>>(`/convs/${convId}/summarize`, range).then(unwrap),

  getSummaries: (convId: string, limit?: number) =>
    client.get<APIResponse<SummariesResponse>>(`/convs/${convId}/summaries`, { params: { limit } }).then(unwrap),

  createTodo: (convId: string, data: { summary_id?: string; content: string }) =>
    client.post<APIResponse<TodoItem>>(`/convs/${convId}/todos`, data).then(unwrap),

  updateTodo: (convId: string, todoId: string, data: { content?: string; done?: boolean }) =>
    client.put<APIResponse<null>>(`/convs/${convId}/todos/${todoId}`, data).then(unwrap),

  deleteTodo: (convId: string, todoId: string) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/todos/${todoId}`).then(unwrap),

  replyCandidates: (convId: string, replyToMsgId?: string) =>
    client.post<APIResponse<{ candidates: string[]; status: string }>>(`/convs/${convId}/reply-candidates`, { reply_to_msg_id: replyToMsgId }).then(unwrap),

  translate: (msgId: string, text: string, targetLang: string) =>
    client.post<APIResponse<{ translated_text: string; detected_lang: string; status: string }>>(`/messages/${msgId}/translate`, { text, target_lang: targetLang }).then(unwrap),
};
