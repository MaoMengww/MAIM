// ─── Auth ───
export interface LoginReq {
  account: string;
  password: string;
  device_id?: string;
  platform?: string;
}

export interface RegisterReq {
  username: string;
  password: string;
  phone?: string;
  email?: string;
  device_id?: string;
}

// ─── Conversation ───
export interface CreateConvReq {
  type: 'single' | 'group';
  peer_user_id?: string;
  member_ids?: string[];
  group_name?: string;
}

export interface UpdateConvReq {
  name?: string;
  avatar?: string;
  background?: string;
}

// ─── Message ───
export interface SendMessageReq {
  conversation_id: string;
  type: number;
  content: SendMsgContent;
  reply_to_msg_id?: string;
  client_msg_id: string;
}

export interface SendMsgContent {
  text?: string;
  mentions?: string[];
  mention_all?: boolean;
  files?: Array<{
    file_id: string;
    file_name?: string;
    size?: number;
    mime_type?: string;
    duration?: number;
  }>;
  file_id?: string;
  file_name?: string;
  file_size?: number;
  file_mime?: string;
  file_url?: string;
  duration?: number;
  image_url?: string;
  image_thumb?: string;
  image_width?: number;
  image_height?: number;
}

export interface SyncMessagesReq {
  position?: number;
  limit?: number;
}

export interface SearchMessagesReq {
  keyword?: string;
  conversation_id?: string;
  sender_id?: string;
  sender_type?: string;
  message_types?: number[];
  start_time?: number;
  end_time?: number;
  page?: number;
  page_size?: number;
}

// ─── Friend ───
export interface SendFriendRequestReq {
  to_user_id: string;
  message?: string;
  from_user_id?: string;
}

export interface SetRemarkReq {
  remark: string;
  friend_id?: string;
}

export interface SetGroupReq {
  group_id?: string;
  clear_group_id?: boolean;
  friend_id?: string;
}

export interface CreateFriendGroupReq {
  name: string;
}

export interface UpdateUserSettingsReq {
  language?: string;
  ai_model_id?: string;
  clear_ai_model_id?: boolean;
  ai_model_name?: string;
  notification_enabled?: boolean;
  sound_enabled?: boolean;
  vibration_enabled?: boolean;
  theme?: string;
  settings_json?: string;
}

// ─── Bot ───
export interface CreateBotReq {
  name: string;
  type: string;
  owner_type: 'user' | 'platform';
  owner_id?: string;
  avatar?: string;
  template_id?: string;    // "qa" | "knowledge"
  sub_type?: string;       // "webhook" | "ws"
  model_name?: string;
  base_url?: string;
  api_key?: string;
  system_prompt?: string;
  persona?: string;
  enable_knowledge?: boolean;
  temperature?: number;
  max_context_messages?: number;
  max_context_tokens?: number;
  streaming_enabled?: boolean;
  memory_model_name?: string;
  memory_model_id?: string;
  memory_embedding_model_name?: string;
  memory_embedding_model_id?: string;
  conn_mode?: string;
  callback_url?: string;
  use_platform_model?: boolean;
  memory_use_platform_model?: boolean;
  memory_limit?: number;
  response_triggers?: string[];
  bot_tags?: string[];
  capabilities?: string;
  settings?: string;
  model_id?: string;
}

export interface UpdateBotReq {
  bot_id?: string;
  name?: string;
  avatar?: string;
  status?: string;
  template_id?: string;
  sub_type?: string;
  model_name?: string;
  base_url?: string;
  api_key?: string;
  system_prompt?: string;
  persona?: string;
  enable_knowledge?: boolean;
  temperature?: number;
  max_context_messages?: number;
  max_context_tokens?: number;
  streaming_enabled?: boolean;
  memory_model_name?: string;
  memory_model_id?: string;
  clear_memory_model_id?: boolean;
  memory_embedding_model_name?: string;
  memory_embedding_model_id?: string;
  clear_memory_embedding_model_id?: boolean;
  conn_mode?: string;
  callback_url?: string;
  bot_tags?: string[];
  use_platform_model?: boolean;
  memory_use_platform_model?: boolean;
  memory_limit?: number;
  response_triggers?: string[];
  capabilities?: string;
  settings?: string;
  model_id?: string;
  clear_model_id?: boolean;
}

// ─── MCP ───
export interface CreateMcpServerReq {
  name: string;
  owner_type: 'user' | 'platform';
  owner_id?: string;
  description?: string;
  transport?: string;
  url?: string;
  command?: string;
  args?: string[];
  env?: string;
  timeout_ms?: number;
  auth_config?: string;
  advanced_config?: string;
  enabled?: boolean;
}

export interface UpdateMcpServerReq {
  name?: string;
  description?: string;
  transport?: string;
  url?: string;
  command?: string;
  args?: string[];
  env?: string;
  timeout_ms?: number;
  status?: string;
  auth_config?: string;
  advanced_config?: string;
  enabled?: boolean;
}

// ─── Knowledge Base ───
export interface CreateKBReq {
  name: string;
  description?: string;
  embedding_model?: string;
  embedding_model_id?: string;
  pipeline_config?: PipelineConfig;
  mode?: string;           // "rag"
}

export interface UpdateKBReq {
  name?: string;
  description?: string;
  embedding_model?: string;
  embedding_model_id?: string;
  clear_embedding_model_id?: boolean;
  pipeline_config?: PipelineConfig;
}

export interface PipelineConfig {
  parsing?: ParsingConfigReq;
  chunking?: ChunkingConfigReq;
  retrieval?: RetrievalConfigReq;
}

export interface ParsingConfigReq {
  engines?: string[];
  mineru_precision?: MinerUConfigReq;
  mineru_agent?: MinerUConfigReq;
  vlm?: VLMConfigReq;
}

export interface MinerUConfigReq {
  api_url?: string;
  api_token?: string;
  api_key?: string;
}

export interface VLMConfigReq {
  enabled?: boolean;
  model_id?: string;
  clear_model_id?: boolean;
  provider?: string;
  model?: string;
  api_key?: string;
  base_url?: string;
}

export interface ChunkingConfigReq {
  chunk_size?: number;
  overlap?: number;
  separators?: string[];
  parent_child?: ParentChildConfigReq;
}

export interface ParentChildConfigReq {
  enabled?: boolean;
  parent_size?: number;
  child_size?: number;
  child_overlap?: number;
}

export interface RetrievalConfigReq {
  mode?: string;
  top_k?: number;
  candidate_top_k?: number;
  score_threshold?: number;
  dense_weight?: number;
  sparse_weight?: number;
  rerank?: RerankConfigReq;
}

export interface RerankConfigReq {
  enabled?: boolean;
  model_id?: string;
  clear_model_id?: boolean;
  top_n?: number;
}

// ─── File ───
export interface GetUploadURLReq {
  name: string;
  mime_type: string;
  size: number;
  purpose?: number;
  access?: number;
  expires_in?: number;
}

// ─── Model ───
export interface CreateModelReq {
  model_name: string;
  provider: string;
  capability: string;
  base_url: string;
  api_key?: string;
  owner_type: 'user' | 'platform';
  owner_id?: string;
}
