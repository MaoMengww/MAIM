import client, { unwrap } from './client';
import type { CreateMcpServerReq, UpdateMcpServerReq } from '@/types/api';
import type { APIResponse, McpServerInfo, McpToolInfo, BotMcpServerInfo } from '@/types/model';

export const mcpApi = {
  list: (params?: { status?: string }) =>
    client.get<APIResponse<{ servers: McpServerInfo[]; pagination?: any }>>('/mcp-servers', { params })
      .then((r) => ({ list: r.data.data.servers ?? [], total: r.data.data.pagination?.total ?? 0 })),

  get: (id: string) =>
    client.get<APIResponse<McpServerInfo>>(`/mcp-servers/${id}`).then(unwrap),

  create: (data: CreateMcpServerReq) =>
    client.post<APIResponse<McpServerInfo>>('/mcp-servers', data).then(unwrap),

  update: (id: string, data: UpdateMcpServerReq) =>
    client.put<APIResponse<McpServerInfo>>(`/mcp-servers/${id}`, data).then(unwrap),

  delete: (id: string) =>
    client.delete<APIResponse<null>>(`/mcp-servers/${id}`).then(unwrap),

  // Tool discovery
  discoverTools: (id: string) =>
    client.post<APIResponse<{ tools: McpToolInfo[] }>>(`/mcp-servers/${id}/discover`).then(unwrap),

  listTools: (id: string) =>
    client.get<APIResponse<{ tools: McpToolInfo[] }>>(`/mcp-servers/${id}/tools`)
      .then((r) => r.data.data.tools ?? []),

  // Bot-MCP assignment
  assignToBot: (botId: string, mcpServerId: string) =>
    client.post<APIResponse<null>>(`/bots/${botId}/mcp-servers`, { mcp_server_id: mcpServerId }).then(unwrap),

  unassignFromBot: (botId: string, mcpServerId: string) =>
    client.delete<APIResponse<null>>(`/bots/${botId}/mcp-servers/${mcpServerId}`).then(unwrap),

  updateBotMcpServer: (botId: string, mcpServerId: string, enabled: boolean) =>
    client.put<APIResponse<null>>(`/bots/${botId}/mcp-servers/${mcpServerId}`, { enabled }).then(unwrap),

  listBotMcpServers: (botId: string) =>
    client.get<APIResponse<{ servers: BotMcpServerInfo[] }>>(`/bots/${botId}/mcp-servers`)
      .then((r) => r.data.data.servers ?? []),
};
