// ─── Generic API Envelope ───
export interface APIResponse<T = unknown> {
  code: number;
  message: string;
  data: T;
}

export interface PageData<T> {
  list: T[];
  total: number;
  page?: number;
  page_size?: number;
  total_pages?: number;
}

export interface CursorPageData<T> {
  list: T[];
  next_cursor?: string;
  has_more?: boolean;
  total?: number;
}

// ─── User & Auth ───
export interface UserInfo {
  id: string;
  username: string;
  phone: string;
  email: string;
  avatar: string;
  gender: number;
  bio: string;
  birthday: number;
  balance?: number;
  created_at: string;
  updated_at: string;
  settings?: Record<string, unknown>;
}

export interface TokenPair {
  access_token: string;
  refresh_token: string;
  access_expire: number;
  refresh_expire: number;
}

export interface LoginResp {
  user_id: string;
  tokens: TokenPair;
  user: UserInfo;
}

export interface SessionInfo {
  session_id: string;
  device_id: string;
  platform: string;
  ip: string;
  location: string;
  last_active_at: number;
  created_at: number;
  is_current: boolean;
}

export interface UserSettings {
  language: string;
  ai_model_id?: string | null;
  ai_model_name: string;
  notification_enabled: boolean;
  sound_enabled: boolean;
  vibration_enabled: boolean;
  theme: string;
  settings_json: string;
}

// ─── Conversation ───
export type ConvType = 'private' | 'group' | 'system';

export interface Conversation {
  id: string;
  type: ConvType;
  name: string;
  avatar: string;
  owner_id?: string;
  peer_user_id?: string;
  member_count: number;
  max_seq: number;
  last_message_id?: string;
  last_message_preview: string;
  last_read_seq: number;
  unread_count: number;
  is_muted: boolean;
  is_pinned: boolean;
  is_muted_all: boolean;
  announcement: string;
  background: string;
  created_at: number;
  updated_at: number;
}

export interface ConvMember {
  user_id?: string;
  username: string;
  avatar: string;
  role: 'MEMBER_ROLE_OWNER' | 'MEMBER_ROLE_ADMIN' | 'MEMBER_ROLE_MEMBER' | number;
  alias: string;
  joined_at: number;
  last_read_seq: number;
  is_muted: boolean;
  mute_until: number;
  member_type: 'user' | 'bot';
  bot_id?: string;
  bot_name?: string;
  bot_avatar?: string;
}

export interface ReadUser {
  user_id: string;
  read_at: number;
}

export interface ReadStatusData {
  read_count: number;
  total_count: number;
  read_users: ReadUser[];
}

export interface BotInConv {
  bot_id: string;
  name: string;
  avatar: string;
  type: string;
  response_triggers: string[];
  bot_settings: string;
  added_by: string;
  added_at: number;
}

// ─── Message ───
export type MsgType = 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9;

export interface ReplySummary {
  message_id: string;
  sender_id?: string;
  sender_type: string;
  sender_name: string;
  type: MsgType | 0;
  preview: string;
  deleted: boolean;
}

export interface TextContent {
  text: string;
  mention_user_ids: string[];
  mention_all: boolean;
}

export interface ImageContent {
  file_id: string;
  url: string;
  thumbnail_url: string;
  width: number;
  height: number;
  size: number;
  format: string;
}

export interface FileContent {
  file_id: string;
  url: string;
  name: string;
  size: number;
  ext: string;
  mime_type: string;
}

export interface AudioContent {
  file_id: string;
  url: string;
  duration: number;
  size: number;
}

export interface VideoContent {
  file_id: string;
  url: string;
  thumbnail_url: string;
  duration: number;
  width: number;
  height: number;
  size: number;
}

export interface SystemContent {
  action: string;
  detail: string;
  related_user_ids: string[];
  actor_id?: string;
  actor_type: string;
  payload: string;
}

export interface BotContent {
  bot_id: string;
  bot_name: string;
  bot_avatar: string;
  text: string;
  is_streaming: boolean;
  thinking_time_ms: number;
  raw_payload: string;
}

export interface KnowledgeSource {
  type: 'rag';
  kb_name: string;
  kb_id: string;
  title: string;
  content: string;
}

export interface CustomContent {
  type: string;
  data: string;
}

export type MsgContentOneof =
  | { text: TextContent }
  | { image: ImageContent }
  | { file: FileContent }
  | { video: VideoContent }
  | { audio: AudioContent }
  | { system: SystemContent }
  | { bot: BotContent }
  | { custom: CustomContent };

export interface Message {
  message_id: string;
  conversation_id: string;
  seq: number;
  from_user_id?: string;
  type: MsgType;
  status: number; // 1=normal, 2=recalled, 3=edited, 4=streaming
  content: MsgContentOneof;
  reply_to_id?: string;
  reply_to?: ReplySummary;
  edited_at: number;
  edit_count: number;
  created_at: number;
  updated_at: number;
  client_msg_id?: string;
}

// ─── Friend ───
export interface FriendInfo {
  user_id: string;
  username: string;
  avatar: string;
  remark: string;
  group_id?: string | null;
  group_name: string;
  status: string;
  created_at: number;
}

export interface FriendRequest {
  request_id: string;
  from_user_id: string;
  to_user_id: string;
  message: string;
  status: string;
  created_at: number;
  updated_at: number;
  from_username?: string;
  from_avatar?: string;
}

export interface FriendGroup {
  id: string;
  name: string;
  sort_order: number;
  friend_count: number;
  created_at: number;
}

// ─── Bot ───
export interface Bot {
  id: string;
  owner_type: 'user' | 'platform';
  owner_id?: string | null;
  name: string;
  avatar: string;
  type: string;
  status: string;
  template_id?: string;
  sub_type?: string;
  use_platform_model: boolean;
  model_name: string;
  model_id?: string | null;
  base_url: string;
  system_prompt: string;
  persona: string;
  enable_knowledge: boolean;
  temperature: number;
  max_context_messages: number;
  max_context_tokens: number;
  streaming_enabled: boolean;
  memory_model_name: string;
  memory_model_id?: string | null;
  memory_use_platform_model: boolean;
  memory_limit: number;
  memory_embedding_model_name: string;
  memory_embedding_model_id?: string | null;
  conn_mode: string;
  callback_url: string;
  has_webhook_secret: boolean;
  has_app_secret: boolean;
  bot_tags: string[];
  response_triggers: string[];
  capabilities: string;
  settings: string;
  created_at: number;
  updated_at: number;
}

// ─── MCP Tool Info ───
export interface McpToolInfo {
  id: string;
  mcp_server_id: string;
  name: string;
  description: string;
  input_schema: string;
  updated_at: number;
}

// ─── MCP Server ───
export interface McpServerInfo {
  id: string;
  owner_type: 'user' | 'platform';
  owner_id?: string | null;
  name: string;
  description: string;
  transport: string;
  url: string;
  command: string;
  args: string[];
  env: string;
  timeout_ms: number;
  status: string;
  created_by?: string | null;
  created_at: number;
  updated_at: number;
  auth_config: string;
  advanced_config: string;
  enabled: boolean;
}

export interface BotMcpServerInfo {
  id: string;
  mcp_server_id: string;
  name: string;
  description: string;
  transport: string;
  url: string;
  discovery: string;
  tools: string[];
  timeout_ms: number;
  status: string;
  enabled: boolean;
}

// ─── Knowledge Base ───
export interface KBRsp {
  id: string;
  owner_type: 'user' | 'platform';
  owner_id?: string | null;
  name: string;
  description: string;
  embedding_model: string;
  embedding_model_id?: string | null;
  pipeline_config: PipelineConfig;
  doc_count: number;
  total_chunks: number;
  status: string;
  created_at: number;
  updated_at: number;
  mode: string;        // "rag"
}

export interface PipelineConfig {
  parsing: ParsingConfig;
  chunking: ChunkingConfig;
  retrieval: RetrievalConfig;
}

export interface ParsingConfig {
  engines: string[];
  mineru_precision?: MinerUConfig;
  mineru_agent?: MinerUConfig;
  vlm?: VLMConfig;
}

export interface MinerUConfig {
  api_url: string;
  api_token: string;
  api_key: string;
}

export interface VLMConfig {
  enabled: boolean;
  model_id?: string | null;
  provider: string;
  model: string;
  api_key: string;
  base_url: string;
}

export interface ChunkingConfig {
  chunk_size: number;
  overlap: number;
  separators: string[];
  parent_child: ParentChildConfig;
}

export interface ParentChildConfig {
  enabled: boolean;
  parent_size: number;
  child_size: number;
  child_overlap: number;
}

export interface RetrievalConfig {
  mode: string;
  top_k: number;
  candidate_top_k: number;
  score_threshold: number;
  dense_weight: number;
  sparse_weight: number;
  rerank: RerankConfig;
}

export interface RerankConfig {
  enabled: boolean;
  model_id?: string | null;
  top_n: number;
}

export interface DocumentRsp {
  id: string;
  kb_id: string;
  title: string;
  file_type: string;
  file_size: number;
  original_filename: string;
  status: string;
  chunk_count: number;
  error_message: string;
  metadata: string;
  stages: StageInfo[];
  created_at: number;
  updated_at: number;
}

export interface StageInfo {
  name: string;
  status: string;
  retries: number;
  error: string;
  started_at: number;
  ended_at: number;
}

export interface ChunkInfo {
  id: string;
  doc_id: string;
  chunk_index: number;
  content: string;
  token_count: number;
  metadata: string;
  created_at: number;
}

export interface KnowledgeBinding {
  id: string;
  kb_id: string;
  kb_name: string;
  target_type: 'bot' | 'conv';
  target_id: string;
  created_at: number;
  mode: string;
}

export interface KnowledgeBoundTarget {
  target_type: 'bot' | 'conv';
  target_id: string;
  created_at: number;
}

export interface KnowledgeRetrieveItem {
  chunk_id: string;
  doc_id: string;
  kb_id: string;
  content: string;
  matched_content: string;
  score: number;
  doc_title: string;
  kb_name: string;
  metadata?: Record<string, unknown>;
}

// ─── Model (LLM) ───
export interface ModelResp {
  id: string;
  model_name: string;
  provider: string;
  capability: string;
  base_url: string;
  owner_type: 'user' | 'platform';
  owner_id?: string | null;
  status: string;
  api_key: string;
  input_price_per_mtok?: number;
  output_price_per_mtok?: number;
}

export interface BillingStatsResp {
  total_input_tokens: number;
  total_output_tokens: number;
  total_cost: number;
  by_model: BillingModelStat[];
}

export interface BillingModelStat {
  model_name: string;
  capability: string;
  input_tokens: number;
  output_tokens: number;
  total_cost: number;
}

export interface BillingRecordItem {
  id: string;
  owner_type: 'user' | 'platform';
  owner_id?: string | null;
  bot_id?: string | null;
  user_id?: string | null;
  model_id: string;
  model_name: string;
  capability: string;
  input_tokens: number;
  output_tokens: number;
  input_cost: number;
  output_cost: number;
  total_cost: number;
  provider: string;
  created_at: string;
}

// ─── Notification ───
export type NotificationReferenceType =
  | 'user' | 'friend_request' | 'conversation' | 'message'
  | 'bot' | 'knowledge_base' | 'document' | 'model';

export interface Notification {
  id: string;
  user_id: string;
  type: number;
  title: string;
  content: string;
  is_read: boolean;
  reference_id?: string | null;
  reference_type?: NotificationReferenceType | null;
  created_at: number;
}

// ─── File ───
export interface FileInfo {
  file_id: string;
  name: string;
  key: string;
  size: number;
  mime_type: string;
  ext: string;
  width: number;
  height: number;
  duration: number;
  md5: string;
  purpose: number;
  access: number;
  uploader_id: string;
  bucket: string;
  created_at: number;
}

export interface UploadURLData {
  file_id: string;
  upload_url: string;
  key: string;
  expires_at: number;
}

export interface FileDownloadData {
  download_url: string;
  expires_at: number;
  file: FileInfo;
}
