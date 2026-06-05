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
  id: number;
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
  user_id: number;
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
  ai_model_id: number;
  ai_model_name: string;
  notification_enabled: boolean;
  sound_enabled: boolean;
  vibration_enabled: boolean;
  theme: string;
  settings_json: string;
}

// ─── Conversation ───
export type ConvType = 'private' | 'group';

export interface Conversation {
  id: number;
  type: ConvType;
  name: string;
  avatar: string;
  owner_id: number;
  member_count: number;
  max_seq: number;
  last_message_id: number;
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
  user_id: number;
  username: string;
  avatar: string;
  role: 'owner' | 'admin' | 'member';
  alias: string;
  joined_at: number;
  last_read_seq: number;
  is_muted: boolean;
  mute_until: number;
  member_type: 'user' | 'bot';
  bot_id?: number;
  bot_name?: string;
  bot_avatar?: string;
}

export interface ReadUser {
  user_id: number;
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
  added_by: number;
  added_at: number;
}

// ─── Message ───
export type MsgType = 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9;

export interface ReplySummary {
  message_id: number;
  sender_id: number;
  sender_type: string;
  sender_name: string;
  type: MsgType;
  preview: string;
  deleted: boolean;
}

export interface TextContent {
  text: string;
  mention_user_ids: number[];
  mention_all: boolean;
}

export interface ImageContent {
  file_id: number;
  url: string;
  thumbnail_url: string;
  width: number;
  height: number;
  size: number;
  format: string;
}

export interface FileContent {
  file_id: number;
  url: string;
  name: string;
  size: number;
  ext: string;
  mime_type: string;
}

export interface AudioContent {
  file_id: number;
  url: string;
  duration: number;
  size: number;
}

export interface VideoContent {
  file_id: number;
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
  related_user_ids: number[];
  actor_id: number;
  actor_type: string;
  payload: string;
}

export interface BotContent {
  bot_id: number;
  bot_name: string;
  bot_avatar: string;
  text: string;
  is_streaming: boolean;
  thinking_time_ms: number;
  raw_payload: string;
}

export interface KnowledgeSource {
  type: 'rag' | 'wiki';
  kb_name: string;
  kb_id: number;
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
  message_id: number;
  conversation_id: number;
  seq: number;
  from_user_id: number;
  type: MsgType;
  status: number; // 1=normal, 2=recalled, 3=edited, 4=streaming
  content: MsgContentOneof;
  reply_to_id?: number;
  reply_to?: ReplySummary;
  edited_at: number;
  edit_count: number;
  created_at: number;
  updated_at: number;
  client_msg_id?: string;
}

// ─── Friend ───
export interface FriendInfo {
  user_id: number;
  username: string;
  avatar: string;
  remark: string;
  group_id: number;
  group_name: string;
  status: string;
  created_at: number;
}

export interface FriendRequest {
  request_id: number;
  from_user_id: number;
  to_user_id: number;
  message: string;
  status: string;
  created_at: number;
  updated_at: number;
  from_username?: string;
  from_avatar?: string;
}

export interface FriendGroup {
  id: number;
  name: string;
  sort_order: number;
  friend_count: number;
  created_at: number;
}

// ─── Bot ───
export interface Bot {
  id: string;
  owner_id: string;
  name: string;
  avatar: string;
  type: string;
  status: string;
  template_id?: string;
  sub_type?: string;
  use_platform_model: boolean;
  model_name: string;
  model_id: number;
  base_url: string;
  system_prompt: string;
  persona: string;
  enable_knowledge: boolean;
  temperature: number;
  max_context_messages: number;
  streaming_enabled: boolean;
  memory_model_name: string;
  memory_model_id: number;
  memory_use_platform_model: boolean;
  memory_limit: number;
  conn_mode: string;
  callback_url: string;
  has_webhook_secret: boolean;
  has_app_secret: boolean;
  bot_tags: string[];
  capabilities: string;
  settings: string;
  created_at: number;
  updated_at: number;
}

export interface BotMemory {
  id: number;
  content: string;
  memory_type: string;   // "fact" | "episode"
  category: string;
  importance: number;
  created_at: string;
  final_score: number;
}

// ─── MCP Tool Info ───
export interface McpToolInfo {
  id: number;
  mcp_server_id: number;
  name: string;
  description: string;
  input_schema: string;
  updated_at: number;
}

// ─── MCP Server ───
export interface McpServerInfo {
  id: number;
  name: string;
  description: string;
  transport: string;
  url: string;
  command: string;
  args: string[];
  env: string;
  timeout_ms: number;
  status: string;
  created_by: number;
  created_at: number;
  updated_at: number;
  auth_config: string;
  advanced_config: string;
  enabled: boolean;
}

// ─── Knowledge Base ───
export interface KBRsp {
  id: number;
  owner_id: number;
  name: string;
  description: string;
  embedding_model: string;
  embedding_model_id: number;
  pipeline_config: PipelineConfig;
  doc_count: number;
  total_chunks: number;
  status: string;
  created_at: number;
  updated_at: number;
  mode: string;        // "rag" | "wiki"
}

export interface PipelineConfig {
  preset: string;
  parsing: ParsingConfig;
  chunking: ChunkingConfig;
  retrieval: RetrievalConfig;
  wiki?: WikiConfig;
}

export interface WikiConfig {
  enabled: boolean;
  model_id: number;
  model_name?: string;
  auto_lint: boolean;
  stale_threshold_hours: number;
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
  model_id: number;
  top_n: number;
}

export interface DocumentRsp {
  id: number;
  kb_id: number;
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
  id: number;
  doc_id: number;
  chunk_index: number;
  content: string;
  token_count: number;
  metadata: string;
  created_at: number;
}

// ─── Wiki ───
export interface WikiSourceRef {
  doc_id: number;
  title: string;
}

export interface WikiPageRsp {
  id: number;
  slug: string;
  title: string;
  page_type: string;
  content: string;
  summary: string;
  source_refs?: WikiSourceRef[];
  aliases: string[];
  out_links: string[];
  in_links: string[];
  version: number;
  created_at: number;
  updated_at: number;
}

export interface WikiPageItem {
  id: number;
  slug: string;
  title: string;
  page_type: string;
  summary: string;
  version: number;
  updated_at: number;
}

export interface WikiSearchItem {
  slug: string;
  title: string;
  page_type: string;
  snippet: string;
}

export interface WikiGraphNode {
  id: string;
  title: string;
  page_type: string;
  group: string;
  summary: string;
  citation_count: number;
}

export interface WikiGraphEdge {
  source: string;
  target: string;
  weight: number;
}

export interface WikiGraphData {
  nodes: WikiGraphNode[];
  edges: WikiGraphEdge[];
}

export interface WikiIssueItem {
  id: number;
  page_slug: string;
  issue_type: string;
  level: string;
  title: string;
  description: string;
  status: string;
  created_at: number;
}

export interface WikiSourceDocRsp {
  doc_id: number;
  title: string;
  content: string;
}

// ─── Model (LLM) ───
export interface ModelResp {
  id: number;
  model_name: string;
  provider: string;
  capability: string;
  base_url: string;
  owner_id: number;
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
  id: number;
  bot_id: number;
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
export interface Notification {
  id: number;
  user_id: number;
  type: string;
  title: string;
  content: Record<string, unknown>;
  is_read: boolean;
  reference_id: string;
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
  uploader_id: number;
  bucket: string;
  created_at: number;
}

export interface UploadURLData {
  file_id: string;
  upload_url: string;
  key: string;
  expires_at: number;
}
