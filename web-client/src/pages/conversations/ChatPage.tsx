import { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { Link, useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { message, Button, Input, InputNumber, Tag, Select, Modal as AntModal, Image, Spin } from 'antd';
import { isAxiosError } from 'axios';
import {
  MessageOutlined, DownloadOutlined,
  InfoCircleOutlined,
  SearchOutlined,
  CloseOutlined,
  RightOutlined,
  PictureOutlined, PaperClipOutlined,
  AudioOutlined, PlayCircleOutlined,
  PauseCircleOutlined, MoreOutlined,
  FileOutlined, FileTextOutlined, FilePdfOutlined, FileImageOutlined, FileZipOutlined,
} from '@ant-design/icons';
import { convApi } from '@/services/conversation';
import { msgApi, normalizeRealtimeMessageContent } from '@/services/message';
import { fileApi } from '@/services/file';
import { botApi, isBotBindingActive } from '@/services/bot';
import { messageSync } from '@/services/messageSync';
import { createPendingSend, retryPendingSend, loadPendingSends, type PendingSend } from '@/services/pendingSend';
import { createPendingRun, retryPendingRun, loadPendingRuns, type PendingRun } from '@/services/pendingRun';
import { SearchHighlight } from '@/components/common/SearchHighlight';
import { wsOn, wsSend } from '@/services/ws';
import { useAuthStore } from '@/stores/auth';
import { SearchFilterBar } from '@/components/common/SearchFilterBar';
import { useMessages } from '@/hooks/useMessages';
import { useRuns } from '@/hooks/useRuns';
import { Avatar } from '@/components/common/Avatar';
import { GroupInfoDrawer } from './GroupInfoDrawer';
import { AuthorizationPanel } from './AuthorizationPanel';
import { RunActions } from './RunActions';
import { RunDelegation, RunOriginDetails } from './RunProvenance';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { Message, MsgContentOneof, ConvMember, AudioContent, ImageContent, VideoContent, FileContent, KnowledgeSource, BotRun, RunSource, BotInConv } from '@/types/model';
import type { SendMsgContent, SearchMessagesReq, RunMaterialsReq, ControlRunReq } from '@/types/api';
import { entityId, sequence, knowledgeSources } from '@/utils/json';

import './ChatPage.css';

function formatTime(ts: number): string {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay) return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
  return `${d.getMonth() + 1}/${d.getDate()} ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
}

function extractTextPreview(content: any): string {
  if (!content) return '';
  if (typeof content === 'string') return content;
  // System messages
  if (content.system) {
    return `[系统] ${content.system.detail || content.system.action || ''}`;
  }
  // Normalized text: { text: { text: "...", mentions: [...] } }
  if (content.text) {
    if (typeof content.text === 'string') return content.text;
    if (typeof content.text.text === 'string') return content.text.text;
    return JSON.stringify(content.text);
  }
  if (content.result) return `[结果] ${content.result.type || ''} ${content.result.data || ''}`;
  if (content.file) return `[文件] ${content.file.name || ''}`;
  if (content.image) return '[图片]';
  if (content.audio) return '[语音]';
  if (content.video) return '[视频]';
  if (content.file_name) return `[文件] ${content.file_name}`;
  if (content.image_url) return '[图片]';
  return '';
}

const MSG_TYPE_LABELS: Record<number, string> = {
  1: '文本', 2: '图片', 3: '文件', 4: '视频',
  5: '语音', 6: '位置', 7: '系统', 8: '自定义', 10: '结果',
};

function convTitle(c: any): string {
  return c?.name || (c?.type === 'group' ? '群聊' : '私聊');
}

// ─── File icon helper ───
function getFileIcon(ext: string) {
  const lower = ext.replace(/^\./, '').toLowerCase();
  if (/^(jpg|jpeg|png|gif|webp|svg|bmp)$/.test(lower)) return <FileImageOutlined style={{ color: '#00a854', fontSize: 28 }} />;
  if (lower === 'pdf') return <FilePdfOutlined style={{ color: '#f40f02', fontSize: 28 }} />;
  if (/^(doc|docx)$/.test(lower)) return <FileTextOutlined style={{ color: '#2b579a', fontSize: 28 }} />;
  if (/^(xls|xlsx)$/.test(lower)) return <FileTextOutlined style={{ color: '#217346', fontSize: 28 }} />;
  if (/^(zip|rar|7z|tar|gz)$/.test(lower)) return <FileZipOutlined style={{ color: '#f5a623', fontSize: 28 }} />;
  if (/^(txt|md|json|xml|csv|log|yaml|yml|go|js|ts|py|java)$/.test(lower)) return <FileTextOutlined style={{ color: '#666', fontSize: 28 }} />;
  return <FileOutlined style={{ color: '#999', fontSize: 28 }} />;
}

// ─── Audio Player for voice messages ───
function AudioPlayer({ audio, onError }: { audio: AudioContent; onError?: () => void }) {
  const audioRef = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);

  useEffect(() => {
    const el = audioRef.current;
    if (!el) return;
    const onTimeUpdate = () => setCurrentTime(el.currentTime);
    const onEnded = () => {
      setPlaying(false);
      setCurrentTime(0);
    };
    el.addEventListener('timeupdate', onTimeUpdate);
    el.addEventListener('ended', onEnded);
    return () => {
      el.removeEventListener('timeupdate', onTimeUpdate);
      el.removeEventListener('ended', onEnded);
    };
  }, []);

  const togglePlay = () => {
    const el = audioRef.current;
    if (!el) return;
    if (playing) {
      el.pause();
      setPlaying(false);
    } else {
      void el.play().then(() => setPlaying(true)).catch(() => message.error('音频播放失败，请重试'));
    }
  };

  const handleTrackClick = (e: React.MouseEvent<HTMLDivElement>) => {
    const el = audioRef.current;
    if (!el || !el.duration) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const pct = (e.clientX - rect.left) / rect.width;
    el.currentTime = pct * el.duration;
  };

  const pct = audio.duration > 0 ? (currentTime / audio.duration) * 100 : 0;

  return (
    <div className="chat-msg-audio">
      <audio ref={audioRef} src={audio.url} preload="metadata" onError={onError} />
      <button className="chat-msg-audio-btn" onClick={togglePlay} aria-label={playing ? '暂停语音' : '播放语音'}>
        {playing ? <PauseCircleOutlined /> : <PlayCircleOutlined />}
      </button>
      <div className="chat-msg-audio-track" onClick={handleTrackClick}>
        <div className="chat-msg-audio-progress" style={{ width: `${pct}%` }} />
      </div>
      <span className="chat-msg-audio-duration">{audio.duration || 0}"</span>
    </div>
  );
}

function useFileDownload(fileId?: string) {
  const userId = useAuthStore((state) => state.user?.id);
  const revision = useAuthStore((state) => state.revision);
  return useQuery({
    queryKey: ['file', userId, revision, fileId, 'download'],
    queryFn: () => fileApi.getDownloadUrl(fileId!),
    enabled: !!userId && !!fileId,
    retry: false,
    refetchInterval: (query) => query.state.data?.expires_at
      ? Math.max(1000, query.state.data.expires_at * 1000 - Date.now() - 30000)
      : false,
  });
}

function Attachment({ kind, attachment, onPreview }: {
  kind: 'image' | 'video' | 'audio' | 'file';
  attachment: ImageContent | VideoContent | AudioContent | FileContent;
  onPreview?: (file: FileContent) => void;
}) {
  const { data, isPending, isError, refetch } = useFileDownload(attachment.file_id);
  const [mediaFailed, setMediaFailed] = useState(false);
  useEffect(() => setMediaFailed(false), [data?.download_url]);
  if (isPending) return <Spin size="small" />;
  if (isError || !data || mediaFailed) return <Button size="small" onClick={() => { setMediaFailed(false); void refetch(); }}>附件不可用，重试</Button>;
  const { file, download_url: url } = data;
  const details = <Button type="link" size="small" onClick={() => onPreview?.({ ...file, url })}>附件详情</Button>;
  if (kind === 'image') return (
    <div className="chat-msg-image">
      <Image src={url} alt={file.name || '图片'} style={{ maxWidth: 240, maxHeight: 240 }} preview={{ mask: '点击预览' }} onError={() => setMediaFailed(true)} />
      {details}
    </div>
  );
  if (kind === 'video') return <div><video src={url} controls preload="metadata" style={{ maxWidth: 320, maxHeight: 240 }} onError={() => setMediaFailed(true)} />{details}</div>;
  if (kind === 'audio') return <div><AudioPlayer audio={{ ...(attachment as AudioContent), url, size: file.size, duration: file.duration || (attachment as AudioContent).duration }} onError={() => setMediaFailed(true)} />{details}</div>;
  return (
    <div className="chat-msg-file" role="button" tabIndex={0}
      onClick={() => onPreview?.({ ...file, url })}
      onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onPreview?.({ ...file, url }); } }}>
      {getFileIcon(file.ext)}
      <div className="chat-msg-file-info">
        <div className="chat-msg-file-name">{file.name || '文件'}</div>
        <div className="chat-msg-file-size">{`${(file.size / 1024).toFixed(1)} KB`}</div>
      </div>
    </div>
  );
}

function ChatMarkdown({ children }: { children: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        a: ({ href, children }) => {
          if (href?.startsWith('http://') || href?.startsWith('https://')) {
            return <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>;
          }
          return <a href={href} target="_blank" rel="noreferrer">{children}</a>;
        },
        table: ({ children }) => (
          <div style={{ overflowX: 'auto' }}>
            <table>{children}</table>
          </div>
        ),
      }}
    >
      {children}
    </ReactMarkdown>
  );
}

function renderContent(content: MsgContentOneof, onFilePreview?: (file: FileContent) => void) {
  if ('text' in content) {
    return <ChatMarkdown>{content.text.text}</ChatMarkdown>;
  }
  if ('image' in content) return <Attachment kind="image" attachment={content.image} onPreview={onFilePreview} />;
  if ('file' in content) return <Attachment kind="file" attachment={content.file} onPreview={onFilePreview} />;
  if ('audio' in content) return <Attachment kind="audio" attachment={content.audio} onPreview={onFilePreview} />;
  if ('video' in content) return <Attachment kind="video" attachment={content.video} onPreview={onFilePreview} />;
  if ('system' in content) {
    return <span style={{ color: 'var(--aim-text-tertiary)', fontSize: 12, fontStyle: 'italic' }}>{content.system.detail || content.system.action}</span>;
  }
  if ('result' in content || 'custom' in content) {
    const result = 'result' in content ? content.result : content.custom;
    let data = result.data;
    try { data = JSON.stringify(JSON.parse(data), null, 2); } catch { /* Plain text results remain plain text. */ }
    return <div><Tag>{'result' in content ? '结果' : '自定义'}{result.type ? ` · ${result.type}` : ''}</Tag>
      <pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', margin: '8px 0 0', fontFamily: 'inherit' }}>{data}</pre></div>;
  }
  return <span>[未知消息]</span>;
}


/** Parse knowledge sources from message metadata. */
function parseSources(b: Pick<Message, 'raw_payload'>): KnowledgeSource[] {
  if (!b?.raw_payload) return [];
  try {
    const parsed = JSON.parse(b.raw_payload);
    return knowledgeSources(parsed?.kb_sources);
  } catch {
    return [];
  }
}

/** Parse tool names from message metadata. */
function parseTools(b: Pick<Message, 'raw_payload'>): string[] {
  if (!b?.raw_payload) return [];
  try {
    const parsed = JSON.parse(b.raw_payload);
    return Array.isArray(parsed?.tool_names) ? parsed.tool_names.filter((name: unknown): name is string => typeof name === 'string') : [];
  } catch {
    return [];
  }
}

function MessageMetadata({ message: msg }: { message: Pick<Message, 'raw_payload'> }) {
  const sources = parseSources(msg);
  const tools = parseTools(msg);
  return <>{tools.length > 0 && <ToolUsage tools={tools} />}{sources.length > 0 && <SourceSection sources={sources} />}</>;
}

/** Collapsible knowledge source section below bot messages */
function SourceSection({ sources }: { sources: KnowledgeSource[] }) {
  const [open, setOpen] = useState(false);
  const [expandedMap, setExpandedMap] = useState<Record<string, boolean>>({});
  if (!sources.length) return null;

  // Display names are labels only; distinct KBs retain distinct source groups.
  const groups: { kbID: string; kbName: string; type: string; sources: KnowledgeSource[] }[] = [];
  const seen = new Map<string, number>();
  for (const s of sources) {
    const key = s.kb_id;
    const idx = seen.get(key);
    if (idx !== undefined) {
      groups[idx].sources.push(s);
    } else {
      seen.set(key, groups.length);
      groups.push({ kbID: s.kb_id, kbName: s.kb_name, type: s.type, sources: [s] });
    }
  }

  return (
    <div style={{ marginTop: 8, borderTop: '1px solid var(--aim-border)', paddingTop: 6 }}>
      <Button
        type="text"
        size="small"
        className="chat-source-toggle"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        icon={<RightOutlined rotate={open ? 90 : 0} />}
      >
        消息来源 ({sources.length})
      </Button>
      {open && (
        <div style={{ marginTop: 4, fontSize: 12 }}>
          {groups.map((g) => (
            <div key={g.kbID} style={{ marginTop: 4 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
                <Link to={`/knowledge/${g.kbID}`} style={{ fontWeight: 500 }}>{g.kbName || `知识库 ${g.kbID}`}</Link>
                <span style={{ fontSize: 10, lineHeight: '16px', padding: '0 4px', borderRadius: 3, background: '#fff7e6', color: '#d48806', border: '1px solid #ffe58f' }}>
                  {g.type.toUpperCase()}
                </span>
              </div>
              {g.sources.map((s) => (
                <SourceItem key={s.chunk_id} source={s} expandedMap={expandedMap} setExpandedMap={setExpandedMap} itemKey={s.chunk_id} />
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** Tool usage badge section below bot messages */
function ToolUsage({ tools }: { tools: string[] }) {
  if (!tools?.length) return null;
  const toolLabels: Record<string, string> = {
    web_search: '网络搜索',
    search_knowledge: '知识检索',
  };
  return (
    <div style={{ marginTop: 8, borderTop: '1px solid var(--aim-border)', paddingTop: 6 }}>
      <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', display: 'flex', alignItems: 'center', gap: 4 }}>
        使用了工具：
        <span style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
          {tools.map((t, i) => (
            <span key={i} style={{
              fontSize: 11, lineHeight: '18px', padding: '0 6px', borderRadius: 3,
              background: '#e6f7ff', color: '#1890ff', border: '1px solid #91d5ff',
            }}>
              {toolLabels[t] || t}
            </span>
          ))}
        </span>
      </div>
    </div>
  );
}

function SourceItem({ source, expandedMap, setExpandedMap, itemKey }: {
  source: KnowledgeSource;
  expandedMap: Record<string, boolean>;
  setExpandedMap: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  itemKey: string;
}) {
  const expanded = !!expandedMap[itemKey];
  return (
    <div style={{ marginTop: 2 }}>
      <div className="chat-source-header">
        {source.content ? <Button
          type="text"
          size="small"
          className="chat-source-toggle chat-source-title"
          aria-expanded={expanded}
          onClick={() => setExpandedMap((prev) => ({ ...prev, [itemKey]: !prev[itemKey] }))}
          icon={<RightOutlined rotate={expanded ? 90 : 0} />}
        >
          {source.title || '未命名文档'}
        </Button> : <span className="chat-source-title">{source.title || '未命名文档'}</span>}
        {source.role && <Tag>{source.role === 'matched' ? '命中片段' : '补充上下文'}</Tag>}
        <Link to={`/knowledge/${source.kb_id}/documents/${source.doc_id}?chunk=${source.chunk_id}`} aria-label={`查看来源文档：${source.title || source.doc_id}`}>
          查看文档
        </Link>
        {source.location?.page_num !== undefined && <span>第 {source.location.page_num} 页</span>}
        {source.location?.section_title && <span>{source.location.section_title}</span>}
      </div>
      {expanded && (
        <div className="chat-source-content">
          <div className="chat-source-identity">文档：{source.doc_id}<br />切片：{source.chunk_id}</div>
          {source.chunk_index !== undefined && <div>片段序号：{source.chunk_index}</div>}
          {source.role === 'supplemental' && source.matched_chunk_id && <div>对应命中片段：{source.matched_chunk_id}</div>}
          {source.location?.start_line !== undefined && <div>行：{source.location.start_line}{source.location.end_line !== undefined ? `–${source.location.end_line}` : ''}</div>}
          {source.content}
        </div>
      )}
    </div>
  );
}

const RUN_STATUS: Record<BotRun['status'], { label: string; color: string }> = {
  accepted: { label: '已接受', color: 'default' }, running: { label: '运行中', color: 'processing' },
  succeeded: { label: '成功', color: 'success' }, failed: { label: '失败', color: 'error' },
  waiting_input: { label: '等待补充', color: 'warning' }, stopping: { label: '停止待确认', color: 'warning' },
  waiting_authorization: { label: '等待明确接手', color: 'warning' },
  stopped: { label: '已停止', color: 'default' },
};
const RUN_STAGE: Record<string, string> = {
  accepted: '接受请求', preparing: '准备获准上下文', model: '模型生成', tool: '工具执行',
  generated: '结果已生成', pending: '等待提交消息', committed: '消息已提交', failed: '执行失败',
  delivered: '工作已投递', executor_accepted: '执行器已接受', waiting_input: '等待补充输入',
  stopping: '等待执行器确认停止', stopped: '执行器确认停止', resumed: '恢复工作',
  awaiting_takeover: '负责人失去当前资格 · 暂停等待接手', takeover: '当前有权成员接手',
  control_granted: '授予执行控制权', control_revoked: '撤销执行控制权', stop_rejected: '执行器拒绝停止',
  stop_unknown: '停止结果未知', stream_closed: '会话输出流关闭',
  compacting: '压缩已完成工作段',
  executor_lost: '执行器失联 · 认领已过期',
  action_outcome_unknown: '外部行动结果未知 · 等待人工核对',
};
const RUN_OUTPUT: Record<BotRun['output_status'], string> = {
  none: '尚无结果', generated: '结果已生成 · 尚未发送', pending: '结果待提交 · 尚未发送',
  committed: '最终消息已提交', failed: '输出提交失败 · 未发送',
};
const RUN_STOP: Record<BotRun['stop_status'], { label: string; description: string; color: string }> = {
  none: { label: '未请求停止', description: '尚无停止请求，输出流关闭也不等于实际停工。', color: 'default' },
  requested: { label: '停止请求已接受 · 待确认', description: 'AIM 已接受停止请求；尚未收到执行器实际停工确认。', color: 'warning' },
  confirmed: { label: '执行器已确认停止', description: '执行器确认后续工作已停止；已发生的行动或副作用不会自动回滚。', color: 'default' },
  rejected: { label: '执行器拒绝停止 · 无法取消', description: '执行器明确表示无法取消；AIM 不能据此声称外部工作已停止。', color: 'error' },
  unknown: { label: '停止结果未知', description: '执行器失联或未及时确认，实际工作可能仍在进行。请查询状态，不将断流当作停工。', color: 'warning' },
};
const RUN_STOP_REASON: Record<string, string> = {
  user_requested: '有权成员请求停止', binding_revoked: 'Bot 已移出，旧绑定不再获准访问',
  bot_disabled: 'Bot 已停用', participation_revoked: 'Bot 当前绑定参与许可已撤销',
  controller_membership_lost: '负责人已失去当前成员资格',
  source_authorization_revoked: '工作依赖的来源授权已撤销', resource_authorization_revoked: '工作依赖的读取或分享许可已撤销',
  tool_authorization_revoked: '工作依赖的工具许可已撤销',
};
const RUN_SOURCE_LABEL: Record<string, string> = { request_source: '发起请求', reference: '明确引用', history: '获准历史', file: '文件', parent_run: '承接结果', activity: '获准活动', delegation_result: '委派返回结果' };
const SOURCE_SENDER_LABEL: Record<string, string> = { user: '用户', bot: 'Bot', system: '系统', activity: '活动主体', rule: '显式规则' };

function isTerminalRun(run: BotRun): boolean {
  return run.status === 'succeeded' || run.status === 'failed' || run.status === 'stopped';
}

function acceptsRunInput(run: BotRun): boolean {
  return run.interaction_status === 'active' && run.output_status === 'none' && run.stop_status === 'none' && run.stream_status === 'open'
    && (run.status === 'accepted' || run.status === 'running' || run.status === 'waiting_input')
    && run.executor_capabilities.supports_input;
}

function hasCurrentRunBinding(run: BotRun, bots: BotInConv[]): boolean {
  return !['binding_revoked', 'bot_disabled', 'participation_revoked'].includes(run.stop_reason)
    && bots.some((bot) => bot.bot_id === run.bot_id && bot.binding_id === run.binding_id && isBotBindingActive(bot));
}

function runOperationError(error: unknown): string {
  if (isAxiosError(error)) {
    return typeof error.response?.data?.message === 'string' ? error.response.data.message
      : '操作接受结果尚未确认，请查询状态或用原内容重试；重试保留提交键。';
  }
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') return error.message;
  return error instanceof Error ? error.message : '操作未被接受，请查询当前状态后重试';
}

type WorkRelation = { runId: string; mode: 'input' | 'continue' };
type WorkMaterials = { messageIds: string[]; fileIds: string[]; startSeq: number | null; endSeq: number | null };
function emptyWorkMaterials(): WorkMaterials {
  return { messageIds: [], fileIds: [], startSeq: null, endSeq: null };
}

function WorkSourcePicker({ materials, onChange, messages, uploadedFiles, disabled, uploading, onUpload }: {
  materials: WorkMaterials; onChange: (value: WorkMaterials) => void; messages: Message[];
  uploadedFiles: { file_id: string; name: string }[]; disabled: boolean; uploading: boolean;
  onUpload: (file: File) => Promise<void>;
}) {
  const uploadRef = useRef<HTMLInputElement>(null);
  const fileOptions = new Map<string, string>();
  for (const msg of messages) {
    if (msg.status === 2) continue;
    const content = msg.content;
    const attachment = 'file' in content ? content.file : 'image' in content ? content.image : 'audio' in content ? content.audio : 'video' in content ? content.video : undefined;
    if (attachment) fileOptions.set(attachment.file_id, 'name' in attachment ? attachment.name : `${MSG_TYPE_LABELS[msg.type]} · 消息 #${msg.seq}`);
  }
  for (const file of uploadedFiles) fileOptions.set(file.file_id, file.name);
  const count = materials.messageIds.length + materials.fileIds.length + (materials.startSeq !== null || materials.endSeq !== null ? 1 : 0);
  return <details className="chat-work-sources">
    <summary>选择工作来源{count > 0 ? ` · 已选 ${count} 项` : '（可选）'}</summary>
    <div className="chat-work-source-fields">
      <label htmlFor="chat-work-messages">引用消息</label>
      <Select id="chat-work-messages" aria-label="引用消息" mode="multiple" value={materials.messageIds}
        disabled={disabled} optionFilterProp="label" placeholder="选择当前会话中明确相关的消息" maxTagCount="responsive"
        onChange={(messageIds: string[]) => onChange({ ...materials, messageIds })}
        options={messages.filter((msg) => msg.status !== 2).map((msg) => ({ value: msg.message_id,
          label: `#${msg.seq} · ${SOURCE_SENDER_LABEL[msg.sender_type]} ${msg.sender_name || msg.sender_id || ''} · ${extractTextPreview(msg.content).slice(0, 100)}` }))} />
      <p className="chat-run-notice">这里只列出已加载消息；可加载更早消息或定位搜索结果后选择。引用是材料，不授予工具或行动权限。</p>
      <fieldset className="chat-work-history" disabled={disabled}>
        <legend>明确授权历史范围（会话消息序号，含两端）</legend>
        <label htmlFor="chat-work-history-start">起始序号</label>
        <InputNumber id="chat-work-history-start" aria-label="历史范围起始序号" min={1} precision={0} max={Number.MAX_SAFE_INTEGER} value={materials.startSeq}
          onChange={(startSeq) => onChange({ ...materials, startSeq })} />
        <label htmlFor="chat-work-history-end">结束序号</label>
        <InputNumber id="chat-work-history-end" aria-label="历史范围结束序号" min={1} precision={0} max={Number.MAX_SAFE_INTEGER} value={materials.endSeq}
          onChange={(endSeq) => onChange({ ...materials, endSeq })} />
        <Button disabled={disabled || (materials.startSeq === null && materials.endSeq === null)} onClick={() => onChange({ ...materials, startSeq: null, endSeq: null })}>移除范围</Button>
      </fieldset>
      <p className="chat-run-notice">提交时为所选 Bot 的当前绑定申请该范围的历史授权；无权授权会明确失败，不自动读取最近消息。</p>
      <label htmlFor="chat-work-files">引用文件</label>
      <Select id="chat-work-files" aria-label="引用文件" mode="multiple" value={materials.fileIds} disabled={disabled || uploading}
        optionFilterProp="label" placeholder="选择当前会话附件或上传文件" maxTagCount="responsive"
        onChange={(fileIds: string[]) => onChange({ ...materials, fileIds })}
        options={[...fileOptions].map(([value, label]) => ({ value, label }))} />
      <input type="file" ref={uploadRef} hidden aria-label="上传工作来源文件" disabled={disabled || uploading} onChange={(event) => {
        const file = event.target.files?.[0]; event.target.value = '';
        if (file) void onUpload(file);
      }} />
      <Button icon={<PaperClipOutlined />} disabled={disabled} loading={uploading} onClick={() => uploadRef.current?.click()}>上传并选择文件</Button>
      <p className="chat-run-notice">上传沿用聊天附件的公开文件权限。仅使用可分享且已确认的文件；支持的内容格式由服务端校验。</p>
    </div>
  </details>;
}

function RunCard({ run, botName, requesterName, nameForUser, canContinue, activeWork, canManage, bindingActive, currentUserId, members, onLocate, onLocateFile, onRefresh, onCompose, onNew, onUpdated }: {
  run: BotRun; botName: string; requesterName?: string; nameForUser: (id: string) => string;
  canContinue: boolean; activeWork: boolean; canManage: boolean; bindingActive: boolean;
  currentUserId: string; members: ConvMember[];
  onLocate: (messageId: string) => Promise<void>; onLocateFile: (fileId: string) => Promise<void>;
  onRefresh: (runId: string) => Promise<void>; onCompose: (run: BotRun, mode: WorkRelation['mode']) => void;
  onNew: (run: BotRun) => void; onUpdated: (run: BotRun) => Promise<void>;
}) {
  const [refreshing, setRefreshing] = useState(false);
  const [controlling, setControlling] = useState(false);
  const [controlError, setControlError] = useState('');
  const [controlTarget, setControlTarget] = useState<string>();
  const controlKey = useRef<{ signature: string; key: string }>();
  const controlLock = useRef(false);
  const terminal = isTerminalRun(run);
  const canOperate = canManage && bindingActive && run.interaction_status === 'active';
  const latest = useRef({ canOperate, members });
  latest.current = { canOperate, members };
  useEffect(() => {
    if (!canOperate || !members.some((member) => member.user_id === controlTarget)) setControlTarget(undefined);
    if (!canOperate) controlKey.current = undefined;
  }, [canOperate, members, controlTarget]);
  useEffect(() => {
    latest.current.canOperate = canOperate;
    return () => { latest.current.canOperate = false; };
  }, [canOperate]);
  const control = async (operation: Exclude<ControlRunReq['operation'], 'input'>, targetUserId?: string) => {
    if (controlLock.current) return;
    if (!latest.current.canOperate) { setControlError('当前已无执行控制资格或绑定已失效，请查询最新状态。'); return; }
    const targetsMember = operation === 'grant_control' || operation === 'revoke_control';
    if (targetsMember && !latest.current.members.some((member) => member.user_id === targetUserId)) {
      setControlError('请选择当前会话成员；已离开的成员不能获授或撤销当前控制权。'); return;
    }
    controlLock.current = true;
    const signature = JSON.stringify({ operation, targetUserId: targetsMember ? targetUserId : undefined });
    if (controlKey.current?.signature !== signature) controlKey.current = { signature, key: crypto.randomUUID() };
    const submission_key = controlKey.current.key;
    const request: ControlRunReq = targetsMember
      ? { operation, submission_key, target_user_id: targetUserId! }
      : { operation, submission_key };
    setControlling(true);
    setControlError('');
    try {
      const updated = await botApi.controlRun(run.id, request);
      controlKey.current = undefined;
      await onUpdated(updated);
    } catch (error) { setControlError(runOperationError(error)); }
    finally { controlLock.current = false; setControlling(false); }
  };
  const closeInteraction = (operation: 'close_interaction' | 'clear_context') => AntModal.confirm({
    title: operation === 'clear_context' ? '清空此交互的工作上下文？' : '结束此交互？',
    content: '此操作关闭继续关系，不删除聊天历史、执行记录或已发生的结果。该交互中的活动工作必须先结束；之后可发起独立新请求。',
    okText: operation === 'clear_context' ? '清空上下文' : '结束交互', cancelText: '取消',
    onOk: () => control(operation),
  });
  const state = run.stage === 'action_outcome_unknown'
    ? { label: '行动结果未知', color: 'warning' }
    : run.executor_status === 'lost'
      ? { label: '执行器失联', color: 'warning' }
      : run.status === 'waiting_authorization' && run.stage === 'pending_action'
        ? { label: '等待具体行动确认', color: 'warning' }
        : run.status === 'stopping' && (run.stop_status === 'rejected' || run.stop_status === 'unknown')
          ? RUN_STOP[run.stop_status] : RUN_STATUS[run.status];
  const stop = RUN_STOP[run.stop_status];
  const targetHasGrant = !!controlTarget && run.control_user_ids.includes(controlTarget);
  const controllerPresent = members.some((member) => member.user_id === run.controller_id);
  const materialSources = run.sources.filter((source): source is Extract<RunSource, { sender_type: string }> => source.type !== 'rag' && source.type !== 'tool');
  return <article id={`run-${run.id}`} className="chat-run-card" data-run-id={run.id} data-run-version={run.version} data-run-status={run.status}
    data-stop-status={run.stop_status} data-stream-status={run.stream_status} data-origin-kind={run.origin?.kind ?? 'human'}
    data-output-status={run.output_status} data-interaction-status={run.interaction_status} aria-label={`${botName} 执行：${state.label}`}>
    <div className="chat-run-header">
      <strong>{botName} · 执行</strong>
      <Tag color={state.color}>{state.label}</Tag>
      {run.origin && <Tag>{run.origin.kind === 'rule' ? '显式规则触发' : 'Bot 明确委派'}</Tag>}
      <Tag>{run.interaction_status === 'closed' ? '交互已关闭' : '交互开放'}</Tag>
      <span className="chat-run-updated">{formatTime(run.updated_at)}</span>
      <Button size="small" loading={refreshing} onClick={async () => {
        setRefreshing(true);
        try { await onRefresh(run.id); }
        catch { message.error('执行状态查询失败'); }
        finally { setRefreshing(false); }
      }}>查询状态</Button>
    </div>
    <div className="chat-run-meta">
      {run.requester_id ? <span title={run.requester_id}>请求者：{requesterName}</span> : <span>非人类发起 · 无人类请求者</span>}
      <span title={run.controller_id}>当前负责人：{nameForUser(run.controller_id)}{!controllerPresent ? '（已非当前成员）' : ''}</span>
      <span>显式控制者：{run.control_user_ids.length > 0 ? run.control_user_ids.map((userId) => `${nameForUser(userId)}${members.some((member) => member.user_id === userId) ? '' : '（已非当前成员）'}`).join('、') : '无'}</span>
      {run.source_message_id
        ? <Button type="link" size="small" onClick={() => void onLocate(run.source_message_id!).catch(() => message.error('来源消息无法读取'))}>查看输入来源消息</Button>
        : <span>输入来源：{run.origin?.kind === 'rule' ? '获准活动与显式规则' : run.origin?.kind === 'delegation' ? '明确委派输入' : '明确操作'}</span>}
      {run.parent_run_id && <span>承接父执行：{run.parent_run_id}</span>}
    </div>
    {run.origin && <RunOriginDetails origin={run.origin} nameForIdentity={nameForUser} />}
    {run.activity && <details className="chat-run-details" data-activity-id={run.activity.id}>
      <summary>触发活动与接受输入</summary>
      <dl className="chat-run-identities">
        <dt>活动 ID</dt><dd>{run.activity.id}</dd><dt>活动类型</dt><dd>{run.activity.type}</dd>
        <dt>活动序号</dt><dd>{run.activity.seq}</dd><dt>会话 ID</dt><dd>{run.activity.conv_id}</dd>
        {run.activity.binding_id && <><dt>活动绑定 ID</dt><dd>{run.activity.binding_id}</dd></>}
        {run.activity.actor_id && <><dt>活动参与主体（不是请求者）</dt><dd>{nameForUser(run.activity.actor_id)}</dd></>}
        {run.activity.message_id && <><dt>活动消息 ID</dt><dd>{run.activity.message_id}</dd></>}
      </dl>
      {run.activity.text && <div className="chat-run-result"><pre>{run.activity.text}</pre></div>}
      {run.activity.data != null && <pre className="chat-run-activity-data">{JSON.stringify(run.activity.data, null, 2)}</pre>}
      <p className="chat-run-notice">这是平台接受的活动输入快照。原消息操作不停止已接受工作；当前访问、资源与行动许可仍分别检查。</p>
    </details>}
    <RunDelegation run={run} nameForIdentity={nameForUser} />
    {run.context_notice && <p className="chat-run-notice" role="status">{run.context_notice}</p>}
    <div className="chat-run-stage" role="status">当前阶段：{RUN_STAGE[run.stage] || run.stage} · {RUN_OUTPUT[run.output_status]}</div>
    <RunActions run={run} onUpdated={onUpdated} />
    {run.executor_status === 'lost' && <div className="chat-run-waiting" role="status">执行器认领已过期，旧执行器不能提交进展或结果。只有无未知外部行动的未结束工作可以明确恢复；失联不表示外部行动已停止。</div>}
    {run.stage === 'action_outcome_unknown' && <div className="chat-run-waiting" role="status">外部行动可能已经完成，尚无可核对的结果。请人工核对外部系统；AIM 不会重做行动，也不会自动回滚已发生的副作用。</div>}
    {run.status === 'waiting_input' && <div className="chat-run-waiting" role="status">待补充事项：{run.waiting_reason || '执行器正在等待补充信息'}</div>}
    {run.status === 'waiting_authorization' && <div className="chat-run-waiting" role="status">
      {run.actions.some((action) => action.status === 'pending')
        ? '具体行动正在等待有权主体确认；补充输入、工作控制权或一句同意不构成许可。'
        : run.waiting_reason || '工作已暂停，等待当前有权主体明确授权或接手。'}
    </div>}
    <div className="chat-run-stop" role="status" aria-live="polite">
      <div className="chat-run-meta">
        <Tag color={stop.color}>{stop.label}</Tag>
        <Tag>{run.stream_status === 'closed' ? '会话输出流已关闭' : '会话输出流开放'}</Tag>
      </div>
      <p>{stop.description}</p>
      {run.stream_status === 'closed' && run.stop_status !== 'confirmed' && <p>临时输出流已关闭，但不能据此判断执行器已停止；仍获授权的实际完成结果可提交为最终消息。</p>}
      {run.stop_reason && <p>停止原因：{RUN_STOP_REASON[run.stop_reason] || run.stop_reason}</p>}
      {run.stop_status !== 'none' && run.executor_status === 'completed' && run.status !== 'stopped' && <p>执行器实际完成；停止请求不会改写已完成的事实。</p>}
    </div>
    {run.input_sources.length > 0 && <details className="chat-run-details"><summary>已接受输入与补充者 · {run.input_sources.length} 条</summary>
      <ol className="chat-run-steps">{run.input_sources.map((source) => <li key={source.version}>
        输入 v{source.version} · <span title={source.requester_id}>{source.sender_type === 'rule' ? '显式规则' : SOURCE_SENDER_LABEL[source.sender_type]}{source.requester_id ? ` ${nameForUser(source.requester_id)}` : ' · 非人类发起，无人类请求者'}</span>
        {source.origin && <RunOriginDetails origin={source.origin} nameForIdentity={nameForUser} />}
        {source.source_message_id && <Button type="link" size="small" onClick={() => void onLocate(source.source_message_id!).catch(() => message.error('补充来源消息无法读取'))}>查看来源消息</Button>}
        {source.content !== undefined
          ? source.content === ''
            ? <p className="chat-run-notice">此接受记录的正文已清除，不能从原消息恢复。</p>
            : <div className="chat-run-result"><pre>{source.content}</pre></div>
          : <p className="chat-run-notice">当前投影未提供已接受正文；来源消息仅用于定位。</p>}
        {(source.message_ids.length > 0 || source.file_ids.length > 0 || source.history_grant_id || source.parent_run_id) && <div className="chat-run-notice">
          {source.message_ids.length > 0 && `消息引用 ${source.message_ids.length} 条 · `}
          {source.file_ids.length > 0 && `文件引用 ${source.file_ids.length} 个 · `}
          {source.history_grant_id && '明确历史范围 · '}{source.parent_run_id && `关联结果 ${source.parent_run_id}`}
        </div>}
      </li>)}</ol>
    </details>}
    {run.executor_type === 'third_party' && <>
      <div className="chat-run-meta" role="status">
        <span>第三方执行器</span>
        <span>送达：{{ pending: '等待投递', delivered: '传输已送达', acknowledged: '执行器确认收到' }[run.delivery_status]}</span>
        <span>工作：{{ pending: '执行器尚未接受', accepted: '执行器已接受', completed: '执行器已完成', stopped: '执行器已停止', stop_rejected: '执行器拒绝取消', lost: '执行器失联，认领已过期' }[run.executor_status]}</span>
      </div>
      <details className="chat-run-details"><summary>执行器实际能力</summary>
        <p>输入类型：{run.executor_capabilities.input_types.join('、')}。传输确认不表示工作完成。</p>
        <p>补充输入：{run.executor_capabilities.supports_input ? '支持' : '不支持'} · 停止：{run.executor_capabilities.supports_stop ? '支持，须等待执行器确认' : '不支持，AIM 无法强制停止外部进程'} · 恢复：{run.executor_capabilities.supports_resume ? '支持' : '不支持，不自动重新执行'}</p>
      </details>
      {!run.executor_capabilities.supports_input && <p className="chat-run-notice" role="status">此第三方执行器不支持补充输入，请发起独立新请求。</p>}
    </>}
    <div className="chat-run-controls">
      {bindingActive && acceptsRunInput(run) && <Button disabled={controlling} onClick={() => onCompose(run, 'input')}>补充输入</Button>}
      {canContinue && <Button disabled={controlling} onClick={() => onCompose(run, 'continue')}>继续此结果</Button>}
      {bindingActive && <Button disabled={controlling} onClick={() => onNew(run)}>独立新话题</Button>}
      {canOperate && ((run.child_run_ids?.length ?? 0) > 0
        || !terminal && run.output_status === 'none' && run.executor_capabilities.supports_stop && run.stop_status === 'none')
        && <Button danger loading={controlling} onClick={() => void control('stop')}>
          {(run.child_run_ids?.length ?? 0) > 0 ? terminal ? '请求关联子工作停止' : '请求父子工作停止' : '请求停止'}
        </Button>}
      {canOperate && !terminal && run.output_status === 'none' && run.executor_capabilities.supports_resume && (run.status === 'waiting_input' || run.executor_status === 'lost' && (run.status === 'accepted' || run.status === 'running')) && run.stage !== 'action_outcome_unknown' && run.stop_status === 'none'
        && <Button loading={controlling} onClick={() => void control('resume')}>请求恢复</Button>}
      {canOperate && !terminal && run.status !== 'stopping' && run.stage !== 'action_outcome_unknown'
        && (run.status === 'waiting_authorization'
          ? run.stop_status === (run.executor_type === 'third_party' ? 'confirmed' : 'none')
          : run.stop_status === 'none')
        && (run.controller_id !== currentUserId || run.status === 'waiting_authorization')
        && <Button loading={controlling} onClick={() => AntModal.confirm({
          title: '由你接手此工作？', content: '接手只变更后续负责人，不改写原请求来源、不继承他人的个人资源许可；共享工作由你明确接续。',
          okText: '由我接手', cancelText: '取消', onOk: () => control('takeover'),
        })}>由我接手</Button>}
      {canOperate && <>
        <Button disabled={activeWork || !terminal} loading={controlling} onClick={() => closeInteraction('close_interaction')}>结束交互</Button>
        <Button disabled={activeWork || !terminal} loading={controlling} onClick={() => closeInteraction('clear_context')}>清空上下文</Button>
      </>}
    </div>
    {canOperate && <details className="chat-run-details chat-run-permissions">
      <summary>管理显式控制权</summary>
      <div className="chat-run-control-members">
        <label htmlFor={`run-control-target-${run.id}`}>当前会话成员</label>
        <Select id={`run-control-target-${run.id}`} aria-label="控制权目标成员" value={controlTarget} allowClear showSearch
          disabled={controlling} optionFilterProp="label" placeholder="选择授予或撤销控制权的成员"
          onChange={(userId: string | undefined) => setControlTarget(userId)}
          options={members.map((member) => ({ value: member.user_id, label: `${member.username || member.user_id}${run.control_user_ids.includes(member.user_id) ? ' · 已获显式控制权' : ''}` }))} />
        <Button disabled={!controlTarget} loading={controlling} onClick={() => void control(targetHasGrant ? 'revoke_control' : 'grant_control', controlTarget)}>
          {targetHasGrant ? '撤销显式控制权' : '授予控制权'}
        </Button>
      </div>
      <p className="chat-run-notice">控制权只覆盖此工作，不授予会话读取或个人资源许可。撤销显式授权不改变请求者、当前负责人或当前群管理员自身的控制资格。</p>
      <p className="chat-run-notice">控制资格以当前成员身份与服务端检查为准；退出后重入不会继承原成员身份获得的控制权。</p>
    </details>}
    {!terminal && !run.executor_capabilities.supports_stop && <p className="chat-run-notice" role="status">此执行器不支持停止，AIM 无法强制停止其外部进程；{(run.child_run_ids?.length ?? 0) > 0 ? '仍可请求关联子工作停止，各自实际结局须分别查询。' : '不提供可用停止入口。'}</p>}
    {canOperate && (run.child_run_ids?.length ?? 0) > 0 && <p className="chat-run-notice">关联停止按当前权限分别请求子工作停止；父 Run 已完成时不会重开或改写其完成事实。请展开委派关系查询每个子 Run 的实际结局。</p>}
    {!bindingActive && <p className="chat-run-notice" role="status">当前 Bot 绑定已失效、停用或参与许可被撤销。只能查看执行事实，不能继续、补充、恢复或接手；重新绑定不会恢复旧交互。</p>}
    {run.interaction_status === 'active' && activeWork && <p className="chat-run-notice">清空或结束交互须先结束其中所有活动工作；聊天历史与执行事实始终保留。</p>}
    {run.interaction_status === 'active' && !canManage && <p className="chat-run-notice">你当前没有执行控制权。普通成员可按输入合同补充；停止、接手、授权与撤销仅限当前合资格请求者、负责人、显式控制者或群管理员。</p>}
    {run.interaction_status === 'closed' && <p className="chat-run-notice">继续关系已关闭。历史与结果仍可回看，新请求不会继承此交互。</p>}
    {controlError && <div className="chat-run-error" role="alert">{controlError}</div>}
    {run.error && <div className="chat-run-error" role="alert">{run.error}</div>}
    {run.result_text && <details className="chat-run-result" open={run.output_status !== 'committed'}>
      <summary>执行结果{run.output_status === 'committed' ? '（已进入消息历史）' : '（尚未发送）'}</summary>
      <div className="chat-msg-bubble"><ChatMarkdown>{run.result_text}</ChatMarkdown></div>
    </details>}
    {run.result_type && run.result_type !== 'text' && run.result_content && <details className="chat-run-result">
      <summary>{run.result_type === 'file' ? '文件结果' : '结构化结果'}{run.output_status === 'committed' ? '（已进入消息历史）' : '（尚未发送）'}</summary>
      <pre>{JSON.stringify(run.result_content, null, 2)}</pre>
    </details>}
    <SourceSection sources={run.sources.filter((source) => source.type === 'rag')} />
    <ToolUsage tools={run.sources.filter((source) => source.type === 'tool').map((source) => source.tool_name)} />
    {materialSources.length > 0 && <details className="chat-run-details">
      <summary>实际使用的材料来源</summary>
      <ul className="chat-run-steps">{materialSources.map((source, index) => <li key={index}>
        {'message_id' in source && <Button type="link" size="small" onClick={() => void onLocate(source.message_id).catch(() => message.error('引用来源消息无法读取'))}>{RUN_SOURCE_LABEL[source.type]}：查看消息</Button>}
        {'file_id' in source && <Button type="link" size="small" onClick={() => void onLocateFile(source.file_id).catch(() => message.error('引用文件无法读取'))}>文件：{source.file_id}</Button>}
        {'parent_run_id' in source && <span>{RUN_SOURCE_LABEL[source.type]}：<a href={`#run-${source.parent_run_id}`}>{source.parent_run_id}</a> · </span>}
        {'activity_id' in source && <span>活动 ID：{source.activity_id} · </span>}
        <span title={source.sender_id}>{SOURCE_SENDER_LABEL[source.sender_type]}{source.sender_id ? ` · ${nameForUser(source.sender_id)}` : ''}</span>
        {source.history_grant_id && <span title={source.history_grant_id}> · 限定历史授权</span>}
      </li>)}</ul>
    </details>}
    {run.output_status === 'committed' && run.message_id && <Button type="link" size="small" onClick={() => void onLocate(run.message_id!).catch(() => message.error('最终消息无法读取'))}>查看最终消息</Button>}
    <details className="chat-run-details">
      <summary>关键阶段与执行标识 · v{run.version}</summary>
      <ol className="chat-run-steps">{run.steps.map((step) => <li key={step.version}>{RUN_STAGE[step.stage] || step.stage} <span>v{step.version} · {formatTime(step.created_at)}</span></li>)}</ol>
      <dl className="chat-run-identities">
        <dt>Run ID</dt><dd>{run.id}</dd><dt>交互 ID</dt><dd>{run.interaction_id}</dd>
        {run.parent_run_id && <><dt>父 Run ID</dt><dd>{run.parent_run_id}</dd></>}
        <dt>交互状态</dt><dd>{run.interaction_status === 'active' ? '开放' : '已关闭'}</dd>
        <dt>输入版本</dt><dd>{run.input_version}</dd><dt>绑定 ID</dt><dd>{run.binding_id}</dd>
        <dt>认领代次</dt><dd>{run.generation}</dd>
        {run.source_message_id && <><dt>来源消息 ID</dt><dd>{run.source_message_id}</dd></>}
        {run.message_id && <><dt>最终消息 ID</dt><dd>{run.message_id}</dd></>}
      </dl>
    </details>
  </article>;
}


// ─── File Preview Modal ───
function FilePreviewModal({ file, onClose }: { file: FileContent; onClose: () => void }) {
  const download = useFileDownload(file.file_id);
  const queryClient = useQueryClient();
  const userId = useAuthStore((state) => state.user?.id);
  const revision = useAuthStore((state) => state.revision);
  const [textContent, setTextContent] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [fetchError, setFetchError] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [deleted, setDeleted] = useState(false);
  const info = download.data?.file;
  const url = !download.isError && !deleted ? download.data?.download_url : undefined;
  const ext = info?.ext.replace(/^\./, '') || '';
  const isImage = info?.mime_type.startsWith('image/');
  const isVideo = info?.mime_type.startsWith('video/');
  const isAudio = info?.mime_type.startsWith('audio/');
  const isPdf = info?.mime_type === 'application/pdf';
  const isRenderableText = /^(md|txt|json|xml|csv|yaml|yml|log|sh|js|ts|tsx|jsx|py|go|java|rb|rs|css|scss|less|html)$/i.test(ext);
  const deletion = useMutation({
    mutationFn: () => fileApi.deleteFile(file.file_id),
    onSuccess: () => {
      setDeleted(true);
      void queryClient.invalidateQueries({ queryKey: ['file', userId, revision, file.file_id] });
      message.success('文件已删除');
    },
    onError: () => message.error('文件删除失败'),
  });

  useEffect(() => {
    setTextContent(null);
    setFetchError(false);
    if (!url || !isRenderableText) { setLoading(false); return; }
    const controller = new AbortController();
    setLoading(true);
    void fetch(url, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error('文件预览失败');
        return response.text();
      })
      .then((text) => { if (!controller.signal.aborted) setTextContent(text); })
      .catch(() => { if (!controller.signal.aborted) setFetchError(true); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [url, isRenderableText]);

  const handleDownload = async () => {
    setDownloading(true);
    try {
      const fresh = await fileApi.getDownloadUrl(file.file_id);
      if (useAuthStore.getState().user?.id !== userId || useAuthStore.getState().revision !== revision) return;
      const response = await fetch(fresh.download_url);
      if (!response.ok) throw new Error('文件下载失败');
      const blob = await response.blob();
      if (useAuthStore.getState().user?.id !== userId || useAuthStore.getState().revision !== revision) return;
      const objectUrl = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = objectUrl;
      anchor.download = fresh.file.name;
      anchor.click();
      setTimeout(() => URL.revokeObjectURL(objectUrl), 0);
    } catch { message.error('文件下载失败'); }
    finally { setDownloading(false); }
  };

  return (
    <AntModal title={info?.name || file.name || '文件预览'} open onCancel={onClose} width={800}
      styles={{ body: { maxHeight: '80vh', overflow: 'auto' } }}
      footer={deleted ? null : <>
        {info?.uploader_id === userId && <Button danger disabled={!url || downloading} loading={deletion.isPending} onClick={() => {
          AntModal.confirm({ title: '删除文件？', content: '删除后，消息中的附件将无法访问。', okText: '删除', okButtonProps: { danger: true }, onOk: () => deletion.mutateAsync() });
        }}>删除文件</Button>}
        <Button type="primary" icon={<DownloadOutlined />} disabled={!url || deletion.isPending} loading={downloading} onClick={() => void handleDownload()}>下载文件</Button>
      </>}>
      {deleted ? <p role="status">文件已删除</p> : download.isError ? <div role="alert"><p>文件不可用或无权访问</p><Button onClick={() => void download.refetch()}>重试</Button></div> : download.isPending || loading ? <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div> : url && <>
        {isImage && <Image src={url} alt={info?.name || '图片'} style={{ maxWidth: '100%' }} onError={() => message.error('图片预览失败')} />}
        {isVideo && <video src={url} controls style={{ maxWidth: '100%' }} onError={() => message.error('视频预览失败')} />}
        {isAudio && <audio src={url} controls onError={() => message.error('音频预览失败')} />}
        {isPdf && <iframe src={url} style={{ width: '100%', height: '70vh', border: 'none', borderRadius: 8 }} title="PDF 预览" />}
        {isRenderableText && (fetchError ? <p role="alert">无法加载文件内容</p> : ext.toLowerCase() === 'md'
          ? <div className="chat-msg-bubble" style={{ background: 'var(--aim-surface)', border: '1px solid var(--aim-border)' }}><ChatMarkdown>{textContent || ''}</ChatMarkdown></div>
          : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontSize: 13, lineHeight: 1.6, margin: 0 }}>{textContent}</pre>)}
        {!isImage && !isVideo && !isAudio && !isPdf && !isRenderableText && <p>此文件类型暂不支持预览</p>}
      </>}
    </AntModal>
  );
}

/** 获取消息发送者的显示名和头像 */
function getMsgUserInfo(msg: Partial<Message>, userMap: Map<string, { username: string; avatar: string }>): { username: string; avatar: string } {
  const info = msg.sender_type === 'bot' || !msg.sender_id ? undefined : userMap.get(msg.sender_id);
  return { username: msg.sender_name || info?.username || `${msg.sender_type === 'bot' ? 'Bot' : '用户'} ${msg.sender_id || ''}`,
    avatar: msg.sender_avatar || info?.avatar || '' };
}

function renderReadStatus(conv: any, msg: any, isSelf: boolean, readStatuses: Record<string, { read: boolean; readCount?: number; totalCount?: number }>): any {
  if (!isSelf || msg.status === 2) return null;
  const rs = readStatuses[msg.message_id];
  if (conv?.type === 'private' && !rs) return null;
  if (conv?.type === 'group' && rs?.readCount !== undefined && rs?.totalCount !== undefined) {
    return <span className="chat-msg-read chat-msg-read-done">{rs.readCount}/{rs.totalCount} 已读</span>;
  }
  if (rs?.read) return <span className="chat-msg-read chat-msg-read-done">已读</span>;
  // Show "未读" for own messages until confirmed read
  return <span className="chat-msg-read">未读</span>;
}


export function ChatPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [input, setInput] = useState('');
  const listRef = useRef<HTMLDivElement>(null);
  const chatInputRef = useRef<HTMLTextAreaElement>(null);
  const currentUserId = useAuthStore((s) => s.user?.id ?? '');
  const currentUserName = useAuthStore((s) => s.user?.username ?? '');
  const authRevision = useAuthStore((state) => state.revision);
  const pageConversation = useRef(id);
  const [pendingRuns, setPendingRuns] = useState<PendingRun[]>([]);
  const [requestingKey, setRequestingKey] = useState<string | null>(null);
  const [creatingRun, setCreatingRun] = useState(false);
  const [runTarget, setRunTarget] = useState<string>();
  const [workRelation, setWorkRelation] = useState<WorkRelation>();
  const [workMaterials, setWorkMaterials] = useState<WorkMaterials>(emptyWorkMaterials);
  const [resourceGrantIDs, setResourceGrantIDs] = useState<string[]>([]);
  const [uploadedWorkFiles, setUploadedWorkFiles] = useState<{ file_id: string; name: string }[]>([]);
  const [workError, setWorkError] = useState('');
  const inputRetry = useRef<{ signature: string; runId: string; request: ControlRunReq }>();
  const historyGrant = useRef<{ signature: string; id: string }>();
  const selectedBinding = useRef<{ botId: string; bindingId: string }>();
  const runRetryLock = useRef(false);
  pageConversation.current = id;
  const [pendingSends, setPendingSends] = useState<PendingSend[]>([]);
  const [retryingKey, setRetryingKey] = useState<string | null>(null);
  const newSendLock = useRef(false);
  const pageAccount = useRef(currentUserId);
  pageAccount.current = currentUserId;
  const reloadPending = useCallback(async () => {
    const account = currentUserId;
    if (!account) return;
    const pending = await loadPendingSends(account);
    if (pageAccount.current === account && authRevision === useAuthStore.getState().revision) setPendingSends(pending);
  }, [currentUserId, authRevision]);
  const reloadRunSubmissions = useCallback(async () => {
    if (!currentUserId) return;
    const pending = await loadPendingRuns(currentUserId);
    if (pageAccount.current === currentUserId && authRevision === useAuthStore.getState().revision) setPendingRuns(pending);
  }, [currentUserId, authRevision]);
  useEffect(() => {
    setPendingRuns([]);
    setRequestingKey(null);
    void reloadRunSubmissions().catch(() => message.error('待确认执行请求读取失败'));
  }, [reloadRunSubmissions]);
  useEffect(() => {
    setRunTarget(undefined);
    setCreatingRun(false);
    setWorkRelation(undefined);
    setWorkMaterials(emptyWorkMaterials());
    setUploadedWorkFiles([]);
    setWorkError('');
    setResourceGrantIDs([]);
    setReplyTo(null);
    inputRetry.current = undefined;
    historyGrant.current = undefined;
    selectedBinding.current = undefined;
  }, [id, authRevision]);
  useEffect(() => {
    setPendingSends([]);
    setRetryingKey(null);
    void reloadPending().catch(() => message.error('待发送消息读取失败'));
  }, [reloadPending]);
  const retrySend = async (pending: PendingSend) => {
    if (retryingKey) return;
    const account = currentUserId;
    if (account !== useAuthStore.getState().user?.id || authRevision !== useAuthStore.getState().revision) return;
    setRetryingKey(pending.request.client_msg_id);
    try {
      await retryPendingSend(account, pending.request.client_msg_id);
      if (pageAccount.current === account && authRevision === useAuthStore.getState().revision) void messageSync.reSync();
    } catch { if (pageAccount.current === account && authRevision === useAuthStore.getState().revision) message.error('发送失败，可再次重试'); }
    finally { if (pageAccount.current === account && authRevision === useAuthStore.getState().revision) { setRetryingKey(null); void reloadPending(); } }
  };
  const [typingUsers, setTypingUsers] = useState<Record<string, { name: string; timestamp: number }>>({});
  const [isAtBottom, setIsAtBottom] = useState(true);
  const lastTypingSentRef = useRef(0);
  const typingStopTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastMarkedSeqRef = useRef(0);
  const [mentionOpen, setMentionOpen] = useState(false);
  const [mentionQuery, setMentionQuery] = useState('');
  const [mentionStartPos, setMentionStartPos] = useState(0);
  const [mentionSelected, setMentionSelected] = useState(0);

  // ─── Media upload & recording state ───
  const [uploading, setUploading] = useState(false);
  const [recording, setRecording] = useState(false);
  const [recordingDuration, setRecordingDuration] = useState(0);
  const [dragOver, setDragOver] = useState(false);

  const imageInputRef = useRef<HTMLInputElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);
  const recordingTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const isRecordingRef = useRef(false);
  const recordingDurationRef = useRef(0);

  // Group info drawer state
  const [groupInfoOpen, setGroupInfoOpen] = useState(false);


  // ─── In-conversation search ───
  const [convSearchOpen, setConvSearchOpen] = useState(false);
  const [convSearchQuery, setConvSearchQuery] = useState('');
  const [convSearchResults, setConvSearchResults] = useState<any[]>([]);
  const [convSearchTotal, setConvSearchTotal] = useState(0);
  const [convSearching, setConvSearching] = useState(false);
  const [convSearchHighlights, setConvSearchHighlights] = useState<Record<string, string>>({});
  const [convTypeCounts, setConvTypeCounts] = useState<{ msg_type: number; count: number }[]>([]);
  const [activeConvTypeFilters, setActiveConvTypeFilters] = useState<number[]>([]);
  const [convSearchIndex, setConvSearchIndex] = useState(0);
  const [convSenderId, setConvSenderId] = useState<string | undefined>();
  const [convSenderType, setConvSenderType] = useState<'' | 'user' | 'bot'>('');
  const [convStartTime, setConvStartTime] = useState<number | undefined>();
  const [convEndTime, setConvEndTime] = useState<number | undefined>();
  const [convSearchPage, setConvSearchPage] = useState(1);
  const convSearchDebounceRef = useRef<ReturnType<typeof setTimeout>>();


  // Message action menu state
  const [msgActions, setMsgActions] = useState<{ msg: any; x: number; y: number } | null>(null);
  const [replyTo, setReplyTo] = useState<{ msg_id: string; preview: string } | null>(null);
  const [editingMsgId, setEditingMsgId] = useState<string | null>(null);
  const [editText, setEditText] = useState('');

  // File preview for in-page file viewing
  const [filePreview, setFilePreview] = useState<FileContent | null>(null);
  useEffect(() => setFilePreview(null), [id, currentUserId, authRevision]);

  // Reset page when filter conditions change
  useEffect(() => {
    setConvSearchPage(1);
  }, [convSearchQuery, activeConvTypeFilters, convSenderId, convSenderType, convStartTime, convEndTime]);

  useEffect(() => {
    if (convSearchDebounceRef.current) {
      clearTimeout(convSearchDebounceRef.current);
    }
    if (!id) {
      setConvSearchResults([]);
      setConvSearchTotal(0);
      setConvSearchHighlights({});
      setConvTypeCounts([]);
      setConvSearchIndex(0);
      setConvSearching(false);
      return;
    }
    const q = convSearchQuery.trim();
    const hasAnyCondition = q !== '' ||
      convSenderId !== undefined ||
      convSenderType !== '' ||
      convStartTime !== undefined ||
      convEndTime !== undefined ||
      activeConvTypeFilters.length > 0;
    if (!hasAnyCondition) {
      setConvSearchResults([]);
      setConvSearchTotal(0);
      setConvSearchHighlights({});
      setConvTypeCounts([]);
      setConvSearchIndex(0);
      setConvSearching(false);
      return;
    }
    setConvSearching(true);
    convSearchDebounceRef.current = setTimeout(async () => {
      try {
        const params: SearchMessagesReq = { conversation_id: id, page: convSearchPage, page_size: 20 };
        if (q) params.keyword = q;
        if (activeConvTypeFilters.length > 0) {
          params.message_types = activeConvTypeFilters;
        }
        if (convSenderId) params.sender_id = convSenderId;
        if (convSenderType) params.sender_type = convSenderType;
        if (convStartTime) params.start_time = convStartTime;
        if (convEndTime) params.end_time = convEndTime;
        const res = await msgApi.search(params);
        setConvSearchResults(res.list);
        setConvSearchTotal(res.total);
        setConvSearchHighlights(res.highlights ?? {});
        setConvTypeCounts(res.type_counts ?? []);
        setConvSearchIndex(0);
      } catch {
        setConvSearchResults([]);
        setConvSearchTotal(0);
        setConvSearchHighlights({});
        setConvTypeCounts([]);
        setConvSearchIndex(0);
      } finally {
        setConvSearching(false);
      }
    }, 300);
    return () => {
      if (convSearchDebounceRef.current) clearTimeout(convSearchDebounceRef.current);
    };
  }, [convSearchQuery, id, activeConvTypeFilters, convSenderId, convSenderType, convStartTime, convEndTime, convSearchPage]);

  const scrollToMessage = async (msgId: string, closeSearch = true) => {
    const account = currentUserId;
    const revision = useAuthStore.getState().revision;
    const hit = convSearchResults.find((msg) => msg.message_id === msgId);
    if (id && hit) {
      try {
        const around = await msgApi.getAroundSeq(id, sequence(hit.seq, 'search.seq', true));
        for (const msg of around) {
          if (pageConversation.current !== id || account !== useAuthStore.getState().user?.id || revision !== useAuthStore.getState().revision) return;
          await messageSync.addMessage(id, msg);
        }
      } catch { message.error('定位消息失败'); return; }
    }
    if (closeSearch) { setConvSearchOpen(false); setConvSearchQuery(''); }
    requestAnimationFrame(() => {
      const el = document.querySelector(`[data-msg-id="${msgId}"]`);
      if (!el) return;
      el.scrollIntoView({ behavior: 'smooth', block: 'center' });
      el.classList.add('chat-msg-highlight');
      setTimeout(() => el.classList.remove('chat-msg-highlight'), 2000);
    });
  };

  const locateRunMessage = async (messageId: string) => {
    if (!id) return;
    const revision = authRevision;
    const source = await msgApi.getById(messageId);
    if (source.conversation_id !== id) throw new Error('消息不属于当前会话');
    const around = await msgApi.getAroundSeq(id, source.seq);
    for (const msg of around) {
      if (pageConversation.current !== id || revision !== useAuthStore.getState().revision) return;
      await messageSync.addMessage(id, msg);
    }
    if (pageConversation.current === id && revision === useAuthStore.getState().revision) await scrollToMessage(messageId);
  };
  const navigateSearch = (dir: 'prev' | 'next') => {
    const total = convSearchResults.length;
    if (total === 0) return;

    setConvSearchIndex((prev) => {
      const next = dir === 'next' ? (prev + 1) % total : (prev - 1 + total) % total;
      const msgId = convSearchResults[next]?.message_id;
      if (msgId) void scrollToMessage(msgId, false);
      return next;
    });
  };


  const { data: conv } = useQuery({
    queryKey: ['conversation', id],
    queryFn: () => convApi.get(id!),
    enabled: !!id,
  });

  const { messages, loading: msgsLoading, error: msgsError, reSync, loadOlder, hasOlder, loadingOlder } = useMessages(id);
  const runQuery = useRuns(id ?? '');
  const relatedRun = workRelation ? runQuery.runs.find((run) => run.id === workRelation.runId) : undefined;
  const handleUpdatedRun = async (run: BotRun) => {
    if (pageConversation.current !== id || pageAccount.current !== currentUserId || authRevision !== useAuthStore.getState().revision) return;
    runQuery.applyRun(run);
    if (run.interaction_status === 'closed' && relatedRun?.interaction_id === run.interaction_id) {
      setWorkRelation(undefined);
      setWorkMaterials(emptyWorkMaterials());
      setReplyTo(null);
      setInput('');
      inputRetry.current = undefined;
      historyGrant.current = undefined;
    }
    await runQuery.refetch();
  };
  const beginWork = (run: BotRun, mode: WorkRelation['mode']) => {
    if (creatingRun || requestingKey !== null) return;
    if (!hasCurrentRunBinding(run, runnableBots)) { setWorkError('原执行绑定已失效，不恢复旧交互。请为当前 Bot 明确发起独立新请求。'); return; }
    setRunTarget(run.bot_id);
    setWorkRelation({ runId: run.id, mode });
    setWorkMaterials(emptyWorkMaterials());
    setReplyTo(null);
    setWorkError('');
    inputRetry.current = undefined;
    historyGrant.current = undefined;
    requestAnimationFrame(() => chatInputRef.current?.focus());
  };
  const beginNewTopic = (botId?: string) => {
    if (creatingRun || requestingKey !== null) return;
    setRunTarget(botId);
    setWorkRelation(undefined);
    setWorkMaterials(emptyWorkMaterials());
    setReplyTo(null);
    setInput('');
    setWorkError('');
    inputRetry.current = undefined;
    historyGrant.current = undefined;
    requestAnimationFrame(() => chatInputRef.current?.focus());
  };
  const replyToMessage = async (msg: Message) => {
    if (creatingRun || requestingKey !== null) return;
    const conversation = id;
    const revision = authRevision;
    setMsgActions(null);
    let run = runQuery.runs.find((candidate) => candidate.message_id === msg.message_id);
    let publishedRunId: string | undefined;
    if (!run && msg.sender_type === 'bot' && msg.raw_payload) {
      try { publishedRunId = JSON.parse(msg.raw_payload).run_id; } catch { /* Ordinary Bot messages need not be execution cards. */ }
    }
    if (publishedRunId) {
      try {
        run = await botApi.getRun(entityId(publishedRunId, 'message.run_id'));
        if (pageConversation.current !== conversation || revision !== useAuthStore.getState().revision) return;
        if (run.conv_id !== conversation || run.message_id !== msg.message_id) throw new Error('执行卡片与当前会话消息不匹配');
        runQuery.applyRun(run);
      } catch (error) {
        if (pageConversation.current === conversation && revision === useAuthStore.getState().revision) setWorkError(runOperationError(error));
        return;
      }
    }
    if (run) {
      beginWork(run, 'input');
      if (!acceptsRunInput(run)) setWorkError(isTerminalRun(run)
        ? '这条回复明确指向已结束的 Run，不能继续推进原执行；请选择“继续此结果”创建关联新 Run，或“独立新话题”。'
        : '此执行当前不接受补充输入；请查询状态或发起独立新请求。');
    } else {
      setReplyTo({ msg_id: msg.message_id, preview: extractTextPreview(msg.content) });
      requestAnimationFrame(() => chatInputRef.current?.focus());
    }
  };
  const retryRunSubmission = async (pending: PendingRun) => {
    if (runRetryLock.current) return;
    const account = currentUserId;
    if (account !== useAuthStore.getState().user?.id || authRevision !== useAuthStore.getState().revision) return;
    runRetryLock.current = true;
    setRequestingKey(pending.request.submission_key);
    try {
      const run = await retryPendingRun(account, pending.request.submission_key);
      runQuery.applyRun(run);
    } catch (error) {
      if (pageAccount.current === account && authRevision === useAuthStore.getState().revision) {
        message.error(isAxiosError(error) && typeof error.response?.data?.message === 'string' ? error.response.data.message : '请求接受结果尚未确认，可用原请求重试');
      }
    } finally {
      runRetryLock.current = false;
      if (pageAccount.current === account && authRevision === useAuthStore.getState().revision) {
        setRequestingKey(null);
        void reloadRunSubmissions();
      }
    }
  };
  const handleStartRun = async () => {
    if (!id || !runTarget || !input.trim() || newSendLock.current || runRetryLock.current || uploading) return;
    const account = currentUserId;
    if (account !== useAuthStore.getState().user?.id || authRevision !== useAuthStore.getState().revision) return;
    const text = input;
    const signature = JSON.stringify({ botId: runTarget, relation: workRelation, text, materials: workMaterials, replyTo });
    const retry = inputRetry.current?.signature === signature ? inputRetry.current : undefined;
    newSendLock.current = true;
    setCreatingRun(true);
    setWorkError('');
    try {
      const bot = runnableBots.find((candidate) => candidate.bot_id === runTarget);
      if (!bot) throw new Error('Bot 当前不可参与此会话，请查询当前绑定或选择其他 Bot');
      if (workRelation && (!relatedRun || !hasCurrentRunBinding(relatedRun, runnableBots) || relatedRun.binding_id !== bot.binding_id)) throw new Error('关联执行已不可见或不属于当前获准绑定，请发起独立新话题');
      if (workRelation?.mode === 'continue' && (!relatedRun || !isTerminalRun(relatedRun) || relatedRun.interaction_status !== 'active')) {
        throw new Error('仅可继续当前绑定中交互开放的已结束执行，请发起独立新话题');
      }
      if (workRelation?.mode === 'input' && !retry && (!relatedRun || !acceptsRunInput(relatedRun))) {
        throw new Error(relatedRun?.executor_type === 'third_party' && !relatedRun.executor_capabilities.supports_input
          ? '此第三方执行器不支持补充输入，请发起独立新请求'
          : '此执行已结束或不再接受补充；请明确继续结果创建关联新 Run，或发起独立新话题');
      }
      const materials: RunMaterialsReq = {
        ...(workMaterials.messageIds.length > 0 ? { message_ids: [...workMaterials.messageIds] } : {}),
        ...(workMaterials.fileIds.length > 0 ? { file_ids: [...workMaterials.fileIds] } : {}),
      };
      if (!retry && (workMaterials.startSeq !== null || workMaterials.endSeq !== null)) {
        const start = workMaterials.startSeq;
        const end = workMaterials.endSeq;
        if (start === null || end === null || !Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 1 || end < start) {
          throw new Error('请填写有效的历史范围：起始与结束均为正整数，结束不小于起始');
        }
        const grantSignature = JSON.stringify({ conversation: id, bot: runTarget, binding: bot.binding_id, start, end });
        if (historyGrant.current?.signature !== grantSignature) {
          const grant = await botApi.grantHistory(id, runTarget, bot.binding_id, start, end);
          if (pageAccount.current !== account || pageConversation.current !== id || authRevision !== useAuthStore.getState().revision) return;
          historyGrant.current = { signature: grantSignature, id: grant.grant_id };
        }
        materials.history_grant_id = historyGrant.current.id;
      }
      if (workRelation?.mode === 'input') {
        const request = retry?.request ?? { ...materials, operation: 'input' as const, message: text, submission_key: crypto.randomUUID() };
        inputRetry.current = { signature, runId: workRelation.runId, request };
        const updated = await botApi.controlRun(workRelation.runId, request);
        if (pageAccount.current !== account || pageConversation.current !== id || authRevision !== useAuthStore.getState().revision) return;
        inputRetry.current = undefined;
        setInput('');
        setWorkMaterials(emptyWorkMaterials());
        setReplyTo(null);
        historyGrant.current = undefined;
        await handleUpdatedRun(updated);
        message.success(`补充已接受 · 输入版本 v${updated.input_version}`);
        return;
      }
      const pending = await createPendingRun(account, runTarget, {
        ...materials, conv_id: id, message: text, visibility: 'conversation', resource_grant_ids: resourceGrantIDs,
        ...(replyTo ? { reply_to_msg_id: replyTo.msg_id } : {}),
        ...(workRelation?.mode === 'continue' ? { parent_run_id: workRelation.runId } : {}),
      });
      if (pageAccount.current !== account || authRevision !== useAuthStore.getState().revision) return;
      await reloadRunSubmissions();
      if (pageConversation.current === id) {
        setInput((current) => current === text ? '' : current);
        setReplyTo(null);
        setWorkRelation(undefined);
        setWorkMaterials(emptyWorkMaterials());
        historyGrant.current = undefined;
      }
      await retryRunSubmission(pending);
    } catch (error) {
      if (pageAccount.current === account && pageConversation.current === id && authRevision === useAuthStore.getState().revision) setWorkError(runOperationError(error));
    } finally {
      newSendLock.current = false;
      if (pageAccount.current === account && pageConversation.current === id && authRevision === useAuthStore.getState().revision) setCreatingRun(false);
    }
  };

  // Message synchronization and new-message delivery are owned by the account lifecycle.
  useEffect(() => {
    if (!id) return;



    // Real-time read status: when someone reads messages in this conversation,
    // refetch members + conv to get updated last_read_seq
    const unsubRead = wsOn('unread_count', (payload: any) => {
      if (payload.conv_id == null || payload.conv_id !== id) return;
      queryClient.invalidateQueries({ queryKey: ['conv-members', id] });
      queryClient.invalidateQueries({ queryKey: ['conversation', id] });
    });

    const unsubReadReceipt = wsOn('read_receipt', (payload: any) => {
      if (payload.conv_id == null || payload.conv_id !== id) return;
      queryClient.setQueryData(['conv-members', id], (old: ConvMember[]) => {
        if (!old) return old;
        return old.map((m) =>
          m.user_id === payload.user_id
            ? { ...m, last_read_seq: Math.max(m.last_read_seq, sequence(payload.last_read_seq, 'read.last_read_seq')) }
            : m,
        );
      });
    });

    return () => { unsubRead(); unsubReadReceipt(); };
  }, [id, queryClient]);

  // Reset scroll state when conversation changes
  useEffect(() => {
    setIsAtBottom(true);
  }, [id]);


  useEffect(() => {
    if (listRef.current && (messages.length > 0 || runQuery.runs.length > 0) && isAtBottom) {
      requestAnimationFrame(() => {
        if (listRef.current) {
          listRef.current.scrollTop = listRef.current.scrollHeight;
        }
      });
    }
  }, [messages, runQuery.runs]);


  const handleScroll = () => {
    if (!listRef.current) return;
    const el = listRef.current;
    const threshold = 60;
    setIsAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < threshold);
  };

  const scrollToBottom = () => {
    if (listRef.current) {
      listRef.current.scrollTo({ top: listRef.current.scrollHeight, behavior: 'smooth' });
      setIsAtBottom(true);
    }
  };

  // Listen for typing indicators from WebSocket
  useEffect(() => {
    if (!id) return;

    const unsubTyping = wsOn('typing', (payload: any) => {
      const userId = payload.user_id;
      if (payload.conv_id == null || payload.conv_id !== id || userId === currentUserId) return;
      // 群聊不展示 typing 指示器
      if (conv?.type === 'group') return;

      setTypingUsers((prev) => ({
        ...prev,
        [userId]: {
          name: payload.username || payload.user_name || `用户${userId}`,
          timestamp: Date.now(),
        },
      }));
    });

    const unsubStop = wsOn('typing.stop', (payload: any) => {
      const userId = payload.user_id;
      if (payload.conv_id == null || payload.conv_id !== id || userId === currentUserId) return;
      // 群聊不展示 typing 指示器
      if (conv?.type === 'group') return;

      setTypingUsers((prev) => {
        if (!(userId in prev)) return prev;
        const next = { ...prev };
        delete next[userId];
        return next;
      });
    });

    return () => {
      unsubTyping();
      unsubStop();
      // 群聊离开时不发送 stop（从未发送过 typing）
      if (conv?.type !== 'group' && lastTypingSentRef.current > 0) {
        wsSend({ type: 'typing.stop', conv_id: id, user_id: currentUserId });
        lastTypingSentRef.current = 0;
      }
    };
  }, [id, currentUserId, conv?.type]);

  // Periodically clean up stale typing indicators (5s timeout)
  useEffect(() => {
    const interval = setInterval(() => {
      const now = Date.now();
      setTypingUsers((prev) => {
        let changed = false;
        for (const uid of Object.keys(prev)) {
          if (now - prev[uid].timestamp > 5000) {
            changed = true;
            break;
          }
        }
        if (!changed) return prev;
        const next: Record<string, { name: string; timestamp: number }> = {};
        for (const [uid, data] of Object.entries(prev)) {
          if (now - data.timestamp <= 5000) {
            next[uid] = data;
          }
        }
        return next;
      });
    }, 1000);

    return () => clearInterval(interval);
  }, []);

  // Auto mark as read when messages are loaded or conversation changes
  useEffect(() => {
    if (!id || !messages.length) return;
    const maxSeq = Math.max(...messages.map((m: any) => m.seq || 0));
    if (maxSeq > 0 && maxSeq > lastMarkedSeqRef.current) {
      lastMarkedSeqRef.current = maxSeq;
      convApi.markRead(id!, maxSeq)
        .catch(() => {
          queryClient.invalidateQueries({ queryKey: ['conversations'] });
        });
    }
  }, [id, messages, queryClient]);

  const { data: members = [], refetch: refetchMembers, isError: membersError } = useQuery({
    queryKey: ['conv-members', id],
    queryFn: () => convApi.getMembers(id!),
    enabled: !!id,
    refetchInterval: 5000,
  });

  const { data: convBots = [], isError: bindingsError, isPending: bindingsPending } = useQuery({
    queryKey: ['conv-bots', id], queryFn: () => botApi.listConvBots(id!), enabled: !!id, refetchInterval: 5000,
  });
  const privateBot = conv?.type === 'private' ? convBots[0] : undefined;
  const runnableBots = bindingsError ? [] : convBots.filter(isBotBindingActive);
  const currentBinding = runnableBots.find((bot) => bot.bot_id === runTarget);
  const inputBlocked = workRelation?.mode === 'input' && (!relatedRun || !hasCurrentRunBinding(relatedRun, runnableBots) || !acceptsRunInput(relatedRun));
  const continuationAllowed = !!relatedRun && isTerminalRun(relatedRun) && relatedRun.interaction_status === 'active'
    && hasCurrentRunBinding(relatedRun, runnableBots) && currentBinding?.binding_id === relatedRun.binding_id;
  const retryInputReady = !!inputRetry.current && inputRetry.current.signature === JSON.stringify({
    botId: runTarget, relation: workRelation, text: input, materials: workMaterials, replyTo,
  });
  useEffect(() => {
    if (!runTarget) { selectedBinding.current = undefined; return; }
    if (bindingsPending || bindingsError) return;
    const previous = selectedBinding.current;
    if (!currentBinding || (previous?.botId === runTarget && previous.bindingId !== currentBinding.binding_id)
      || (relatedRun && (!hasCurrentRunBinding(relatedRun, runnableBots) || relatedRun.interaction_status === 'closed'))) {
      selectedBinding.current = undefined;
      setRunTarget(undefined);
      setWorkRelation(undefined);
      setWorkMaterials(emptyWorkMaterials());
      setUploadedWorkFiles([]);
      setReplyTo(null);
      setInput('');
      inputRetry.current = undefined;
      historyGrant.current = undefined;
      setWorkError('Bot 绑定或原交互已失效，已清除旧工作选择；重新绑定后请明确发起新请求。');
      return;
    }
    selectedBinding.current = { botId: runTarget, bindingId: currentBinding.binding_id };
  }, [runTarget, currentBinding, relatedRun, runnableBots, bindingsPending, bindingsError]);

  const mentionTargets = useMemo(() => [
    ...members.map((member) => ({ id: member.user_id, name: member.username, avatar: member.avatar, isBot: false })),
    ...convBots.map((bot) => ({ id: bot.bot_id, name: bot.name, avatar: bot.avatar, isBot: true })),
  ], [members, convBots]);

  // Compute read status for self-sent messages from member last_read_seq
  const readStatuses = useMemo(() => {
    const result: Record<string, { read: boolean; readCount?: number; totalCount?: number }> = {};
    if (!id || !messages.length || !conv || !members.length) return result;

    const selfMsgs = messages.filter((m) => m.sender_type === 'user' && m.sender_id === currentUserId);
    for (const msg of selfMsgs) {
      if (conv.type === 'private') {
        const peer = members.find((m: ConvMember) => m.user_id !== currentUserId);
        if (!peer) continue;
        result[msg.message_id] = {
          read: !!peer && (peer.last_read_seq || 0) >= (msg.seq || 0),
        };
      } else {
        const readableMembers = members;
        const totalCount = readableMembers.length;
        const readCount = readableMembers.filter((m: ConvMember) => (m.last_read_seq || 0) >= (msg.seq || 0)).length;
        result[msg.message_id] = { read: readCount > 0, readCount, totalCount };
      }
    }
    return result;
  }, [id, messages, conv, currentUserId, members]);

  const currentMember = members.find((m) => m.user_id === currentUserId);
  const isOwner = !membersError && !!currentMember && (currentMember.role === 'MEMBER_ROLE_OWNER' || currentMember.role === 1);
  const isAdmin = !membersError && !!currentMember && (isOwner || currentMember.role === 'MEMBER_ROLE_ADMIN' || currentMember.role === 2);

  const userMap = useMemo(() => {
    const map = new Map<string, { username: string; avatar: string }>();
    if (members?.length) {
      members.forEach((m) => map.set(m.user_id, { username: m.username, avatar: m.avatar }));
    }
    const currentUser = useAuthStore.getState().user;
    if (currentUser) {
      map.set(currentUser.id, {
        username: currentUser.username,
        avatar: currentUser.avatar,
      });
    }
    return map;
  }, [members, conv]);

  const memberRoleMap = useMemo(() => {
    const map = new Map<string, ConvMember['role']>();
    if (members?.length) {
      members.forEach((m: ConvMember) => {
        if (m.user_id) map.set(m.user_id, m.role);
      });
    }
    return map;
  }, [members]);


  const convSenderOptions = useMemo(() => {
    const senders = new Map<string, { id: string; name: string; isBot: boolean }>();
    members.forEach((member) => senders.set(member.user_id, { id: member.user_id, name: member.username, isBot: false }));
    convBots.forEach((bot) => senders.set(bot.bot_id, { id: bot.bot_id, name: bot.name, isBot: true }));
    messages.forEach((msg) => {
      if (msg.sender_id && !senders.has(msg.sender_id) && msg.sender_type !== 'system') {
        senders.set(msg.sender_id, { id: msg.sender_id, name: msg.sender_name || msg.sender_id, isBot: msg.sender_type === 'bot' });
      }
    });
    return [...senders.values()];
  }, [members, convBots, messages]);

  const roleLabels: Record<string, string> = { MEMBER_ROLE_OWNER: '群主', MEMBER_ROLE_ADMIN: '管理员', MEMBER_ROLE_MEMBER: '成员' };
  const roleColors: Record<string, string> = { MEMBER_ROLE_OWNER: 'gold', MEMBER_ROLE_ADMIN: 'blue', MEMBER_ROLE_MEMBER: 'default' };
  const { data: convListData } = useQuery({
    queryKey: ['conversations'],
    queryFn: () => convApi.list(),

  });

  // ─── Upload helper ───
  async function uploadFile(file: File) {
    setUploading(true);
    try {
      const uploadData = await fileApi.getUploadUrl({
        name: file.name,
        mime_type: file.type || 'application/octet-stream',
        size: file.size,
        purpose: 1,
        access: 3,
      });
      const uploaded = await fetch(uploadData.upload_url, {
        method: 'PUT',
        body: file,
        headers: { 'Content-Type': file.type || 'application/octet-stream' },
      });
      if (!uploaded.ok) throw new Error('文件上传失败');
      const fileInfo = await fileApi.confirmUpload(uploadData.file_id);
      return fileInfo;
    } finally {
      setUploading(false);
    }
  }

  const uploadWorkFile = async (file: File) => {
    const account = currentUserId;
    const conversation = id;
    setWorkError('');
    try {
      const uploaded = await uploadFile(file);
      if (pageAccount.current !== account || pageConversation.current !== conversation || authRevision !== useAuthStore.getState().revision) return;
      setUploadedWorkFiles((files) => [...files, { file_id: uploaded.file_id, name: uploaded.name }]);
      setWorkMaterials((materials) => ({ ...materials, fileIds: [...materials.fileIds, uploaded.file_id] }));
    } catch (error) {
      if (pageAccount.current === account && pageConversation.current === conversation && authRevision === useAuthStore.getState().revision) setWorkError(runOperationError(error));
    }
  };

  // ─── Image selection ───
  function handleImagePick() {
    imageInputRef.current?.click();
  }

  async function handleImageChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = '';
    if (runTarget) { await uploadWorkFile(file); return; }
    try {
      const fileInfo = await uploadFile(file);
      sendMutation.mutate({
        type: 2,
        content: { files: [{ file_id: fileInfo.file_id, file_name: fileInfo.name, mime_type: fileInfo.mime_type, size: fileInfo.size }] },
      });
    } catch {
      message.error('图片发送失败');
    }
  }

  // ─── File selection ───
  function handleFilePick() {
    fileInputRef.current?.click();
  }

  async function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = '';
    if (runTarget) { await uploadWorkFile(file); return; }
    try {
      const fileInfo = await uploadFile(file);
      sendMutation.mutate({
        type: fileInfo.mime_type.startsWith('image/') ? 2 : fileInfo.mime_type.startsWith('video/') ? 4 : fileInfo.mime_type.startsWith('audio/') ? 5 : 3,
        content: { files: [{ file_id: fileInfo.file_id, file_name: fileInfo.name, mime_type: fileInfo.mime_type, size: fileInfo.size, duration: fileInfo.duration }] },
      });
    } catch {
      message.error('文件发送失败');
    }
  }

  // ─── Voice recording ───
  async function startRecording() {
    try {
      isRecordingRef.current = true;
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      streamRef.current = stream;
      const recorder = new MediaRecorder(stream);
      mediaRecorderRef.current = recorder;
      audioChunksRef.current = [];

      recorder.ondataavailable = (e) => {
        if (e.data.size > 0) audioChunksRef.current.push(e.data);
      };

      recorder.onstop = async () => {
        stream.getTracks().forEach((t) => t.stop());
        streamRef.current = null;

        const mimeType = recorder.mimeType || 'audio/webm';
        const blob = new Blob(audioChunksRef.current, { type: mimeType });
        const file = new File([blob], `voice_${Date.now()}.webm`, { type: mimeType });
        const duration = recordingDurationRef.current;

        try {
          const fileInfo = await uploadFile(file);
          sendMutation.mutate({
            type: 5,
            content: { files: [{ file_id: fileInfo.file_id, file_name: fileInfo.name, mime_type: fileInfo.mime_type, size: fileInfo.size, duration }] },
          });
        } catch {
          message.error('语音发送失败');
        }
      };

      recorder.start();
      setRecording(true);
      recordingDurationRef.current = 0;
      setRecordingDuration(0);

      const startTime = Date.now();
      recordingTimerRef.current = setInterval(() => {
        recordingDurationRef.current = Math.floor((Date.now() - startTime) / 1000);
        setRecordingDuration(recordingDurationRef.current);
      }, 1000);
    } catch {
      isRecordingRef.current = false;
      message.error('无法访问麦克风');
    }
  }

  function stopRecording() {
    const recorder = mediaRecorderRef.current;
    if (recorder && recorder.state !== 'inactive') {
      recorder.stop();
    }
    isRecordingRef.current = false;
    setRecording(false);
    if (recordingTimerRef.current) {
      clearInterval(recordingTimerRef.current);
      recordingTimerRef.current = null;
    }
  }

  const handleVoiceStart = useCallback((e: React.MouseEvent | React.TouchEvent) => {
    e.preventDefault();
    startRecording();
  }, []);

  const handleVoiceEnd = useCallback(() => {
    if (isRecordingRef.current && recordingDurationRef.current < 1) {
      // Click (too short): cancel the recording without uploading
      const recorder = mediaRecorderRef.current;
      if (recorder && recorder.state !== 'inactive') {
        recorder.ondataavailable = null;
        recorder.stop();
      }
      isRecordingRef.current = false;
      setRecording(false);
      if (recordingTimerRef.current) {
        clearInterval(recordingTimerRef.current);
        recordingTimerRef.current = null;
      }
      if (streamRef.current) {
        streamRef.current.getTracks().forEach((t) => t.stop());
        streamRef.current = null;
      }
      return;
    }
    if (isRecordingRef.current) stopRecording();
  }, []);

  // ─── Drag & drop ───
  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(true);
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);
  }, []);

  const handleDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);

    const file = e.dataTransfer.files?.[0];
    if (!file) return;
    if (creatingRun || uploading) return;
    if (runTarget) { await uploadWorkFile(file); return; }

    if (!file.type.startsWith('image/')) {
      message.info('仅支持拖拽图片');
      return;
    }

    try {
      const fileInfo = await uploadFile(file);
      sendMutation.mutate({
        type: 2,
        content: { files: [{ file_id: fileInfo.file_id, file_name: fileInfo.name, mime_type: fileInfo.mime_type, size: fileInfo.size }] },
      });
    } catch {
      message.error('图片发送失败');
    }
  }, [runTarget, creatingRun, uploading, uploadWorkFile]);

  const sendMutation = useMutation({
    mutationFn: async (data: { type: number; content: SendMsgContent }) => {
      if (newSendLock.current) return;
      newSendLock.current = true;
      const account = currentUserId;
      try {
        const pending = await createPendingSend(account, { conversation_id: id!, ...data, reply_to_msg_id: replyTo?.msg_id });
        if (pageAccount.current !== account || authRevision !== useAuthStore.getState().revision) return;
        setInput('');
        setReplyTo(null);
        await reloadPending();
        await retrySend(pending);
      } finally { newSendLock.current = false; }
    },
    onError: () => message.error('无法保存发送状态，消息未发送'),
  });

  const handleInputChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const value = e.target.value;
    const pos = e.target.selectionStart ?? 0;
    setInput(value);

    // Detect @ for mention autocomplete
    const beforeCursor = value.slice(0, pos);
    const atIndex = beforeCursor.lastIndexOf('@');
    if (atIndex !== -1) {
      const afterAt = beforeCursor.slice(atIndex + 1);
      const isAtBoundary = atIndex === 0 || /[\s\n]/.test(value[atIndex - 1]);
      if (isAtBoundary && !afterAt.includes(' ') && !afterAt.includes('\n')) {
        setMentionOpen(true);
        setMentionQuery(afterAt);
        setMentionStartPos(atIndex);
        setMentionSelected(0);
      } else {
        setMentionOpen(false);
      }
    } else {
      setMentionOpen(false);
    }

    if (!id) return;
    // 群聊不发送 typing 事件
    if (conv?.type === 'group') return;

    const now = Date.now();
    if (now - lastTypingSentRef.current > 3000) {
      wsSend({ type: 'typing', conv_id: id, user_id: currentUserId, username: currentUserName });
      lastTypingSentRef.current = now;
    }

    if (typingStopTimerRef.current) {
      clearTimeout(typingStopTimerRef.current);
    }
    typingStopTimerRef.current = setTimeout(() => {
      wsSend({ type: 'typing.stop', conv_id: id, user_id: currentUserId });
      lastTypingSentRef.current = 0;
    }, 2000);
  };

  const handleSend = useCallback(() => {
    const text = input.trim();
    if (!text || sendMutation.isPending || newSendLock.current) return;

    // 发送消息时立即停止 typing 指示器
    if (typingStopTimerRef.current) {
      clearTimeout(typingStopTimerRef.current);
      typingStopTimerRef.current = null;
    }
    if (lastTypingSentRef.current > 0) {
      wsSend({ type: 'typing.stop', conv_id: id, user_id: currentUserId });
      lastTypingSentRef.current = 0;
    }

    // Parse @username mentions from text
    const mentionSet = new Set<string>();
    const mentionRegex = /@(\S+)/g;
    let match;
    while ((match = mentionRegex.exec(text)) !== null) {
      const username = match[1];
      const sender = mentionTargets.find((candidate) => candidate.name === username);
      if (sender) mentionSet.add(sender.id);
    }

    // Private chat: auto-mention peer
    if (conv?.type === 'private') {
      (members as ConvMember[]).forEach((m) => {
        if (m.user_id && m.user_id !== currentUserId) {
          mentionSet.add(m.user_id);
        }
      });
    }

    sendMutation.mutate({
      type: 1,
      content: { text, mentions: [...mentionSet], mention_all: false },
    });
  }, [input, sendMutation, members, mentionTargets, conv, currentUserId]);

  const insertMention = useCallback((target: { name: string }) => {
    const mentionLen = mentionQuery.length;
    const before = input.slice(0, mentionStartPos);
    const after = input.slice(mentionStartPos + 1 + mentionLen);
    const newText = `${before}@${target.name} ${after}`;
    setInput(newText);
    setMentionOpen(false);
    setTimeout(() => {
      chatInputRef.current?.focus();
    }, 0);
  }, [input, mentionStartPos, mentionQuery]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (mentionOpen) {
      const filtered = mentionTargets.filter((target) => target.id !== currentUserId && target.name.toLowerCase().includes(mentionQuery.toLowerCase()));
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setMentionSelected((prev) => Math.min(prev + 1, filtered.length - 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setMentionSelected((prev) => Math.max(prev - 1, 0));
      } else if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        if (filtered.length > 0) {
          insertMention(filtered[mentionSelected]);
        }
      } else if (e.key === 'Escape') {
        e.preventDefault();
        setMentionOpen(false);
      }
      return;
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (runTarget) void handleStartRun();
      else handleSend();
    }
  };

  // ─── Cleanup recording on unmount ───
  useEffect(() => {
    return () => {
      if (recordingTimerRef.current) {
        clearInterval(recordingTimerRef.current);
      }
      if (streamRef.current) {
        streamRef.current.getTracks().forEach((t) => t.stop());
      }
      if (mediaRecorderRef.current && mediaRecorderRef.current.state !== 'inactive') {
        mediaRecorderRef.current.stop();
      }
    };
  }, []);

  function showMsgActions(e: React.MouseEvent, msg: any) {
    e.stopPropagation();
    e.preventDefault();
    if (e.type === 'contextmenu') {
      setMsgActions({ msg, x: e.clientX, y: e.clientY });
    } else {
      const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
      setMsgActions({ msg, x: rect.right + 4, y: rect.top });
    }
  }

  // Build typing indicator text
  const typingText = (() => {
    const entries = Object.entries(typingUsers);
    if (entries.length === 0) return null;
    if (conv?.type === 'group') {
      return entries.map(([, data]) => data.name).join('、') + ' 正在输入...';
    }
    return '对方正在输入...';
  })();

  if (!id) {
    return (
      <div className="chat-empty-state">
        <MessageOutlined style={{ fontSize: 48, opacity: 0.5, color: 'var(--aim-primary)' }} />
        <p>选择一个会话开始聊天</p>
      </div>
    );
  }

  return (
    <>
    <div className="chat-page">
      <div className="chat-header">
        <div className="chat-header-info">
          <Button className="chat-mobile-back" size="small" onClick={() => navigate('/conversations')}>返回会话</Button>
          <Avatar name={privateBot?.name || conv?.name || (conv?.type === 'group' ? '群聊' : '私聊')} src={privateBot?.avatar || conv?.avatar} size={36} />
          <div>
            <div className="chat-header-name">{privateBot?.name || conv?.name || (conv?.type === 'group' ? '群聊' : '私聊')}</div>
            {conv && <div className="chat-header-meta">{conv.type === 'group' ? `${conv.member_count} 人` : privateBot ? 'Bot · 独立会话绑定' : ''}</div>}
          </div>
        </div>
        <div className="chat-header-actions">
          <button className="chat-header-info-btn" onClick={() => setConvSearchOpen(!convSearchOpen)} title="搜索消息">
            <SearchOutlined />
          </button>
          {(conv?.type === 'group' || conv?.type === 'private' && (convBots.length > 0 || members.length === 1)) && (
            <button className="chat-header-info-btn" onClick={() => setGroupInfoOpen(true)} title={conv?.type === 'group' ? '群聊信息' : 'Bot 绑定信息'}>
              <InfoCircleOutlined />
            </button>
          )}
        </div>
      </div>

        <div className="chat-main">

      {convSearchOpen && (
        <div className="chat-search-overlay">
          <div className="chat-search-bar">
            <SearchOutlined className="chat-search-bar-icon" />
            <input
              className="chat-search-bar-input"
              placeholder="搜索当前会话消息（可按发送者、类型、时间等组合筛选）..."
              value={convSearchQuery}
              onChange={(e) => setConvSearchQuery(e.target.value)}
              autoFocus
            />
            <button className="chat-search-bar-close" onClick={() => { setConvSearchOpen(false); setConvSearchQuery(''); }}>
              <CloseOutlined />
            </button>
          </div>
          <div className="chat-search-results">
            <SearchFilterBar
              senderOptions={convSenderOptions}
              senderId={convSenderId}
              onSenderIdChange={setConvSenderId}
              senderType={convSenderType}
              onSenderTypeChange={setConvSenderType}
              startTime={convStartTime}
              endTime={convEndTime}
              onTimeRangeChange={(s, e) => { setConvStartTime(s); setConvEndTime(e); }}
              messageTypes={activeConvTypeFilters}
              onMessageTypesChange={setActiveConvTypeFilters}
              typeCounts={convTypeCounts}
              typeLabels={MSG_TYPE_LABELS}
              total={convSearchTotal}
              page={convSearchPage}
              pageSize={20}
              onPageChange={setConvSearchPage}
              navIndex={convSearchIndex}
              navTotal={convSearchResults.length}
              onNavPrev={() => navigateSearch('prev')}
              onNavNext={() => navigateSearch('next')}
            />
            {convSearching && <div className="chat-search-status">搜索中...</div>}
            {!convSearching && !convSearchQuery.trim() && !convSenderId && !convSenderType && !convStartTime && !convEndTime && activeConvTypeFilters.length === 0 && (
              <div className="chat-search-status">输入关键词或设置过滤条件开始搜索</div>
            )}
            {!convSearching && (convSearchQuery.trim() || convSenderId || convSenderType || convStartTime || convEndTime || activeConvTypeFilters.length > 0) && convSearchResults.length === 0 && (
              <div className="chat-search-status">未找到相关消息</div>
            )}
            {convSearchResults.map((msg: any) => {
              const highlight = convSearchHighlights[msg.message_id];
              const senderInfo = getMsgUserInfo(msg, userMap);
              return (
                <div
                  key={msg.message_id}
                  className="chat-search-result-item"
                  onClick={() => scrollToMessage(msg.message_id)}
                >
                  <Avatar
                    src={senderInfo?.avatar}
                    name={senderInfo?.username || `用户${msg.sender_id}`}
                    size={28}
                  />
                  <div className="chat-search-result-body">
                    <div className="chat-search-result-sender">
                      {senderInfo.username}{msg.sender_type === 'bot' ? ' [Bot]' : ''}
                    </div>
                    <div className="chat-search-result-preview">
                      {highlight ? (
                        <SearchHighlight text={highlight} />
                      ) : extractTextPreview(msg.content)}
                    </div>
                  </div>
                  <div className="chat-search-result-time">
                    {formatTime(msg.created_at)}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      <div className="chat-messages-wrapper">
        <div className="chat-messages" ref={listRef} onScroll={handleScroll}>
          {msgsError && <div className="chat-loading" role="alert">同步失败：{msgsError} <button onClick={reSync}>重试同步</button></div>}
          {msgsLoading && messages.length === 0 && <div className="chat-loading">加载消息中...</div>}
          {hasOlder && <Button size="small" loading={loadingOlder} onClick={loadOlder}>加载更早消息</Button>}
          {!msgsLoading && !msgsError && messages.length === 0 && runQuery.runs.length === 0 && <div className="chat-loading">暂无消息，发送第一条消息吧</div>}

          {messages.map((msg: any) => {
          const isSelf = msg.sender_type === 'user' && msg.sender_id === currentUserId;
          if (editingMsgId === msg.message_id) {
            const editUserInfo = getMsgUserInfo(msg, userMap);
            return (
              <div key={msg.message_id} className="chat-msg chat-msg-self" data-msg-id={msg.message_id} onContextMenu={(e) => showMsgActions(e, msg)}>
                <div className="chat-msg-avatar-col">
                  <Avatar
                    src={editUserInfo?.avatar}
                    name={editUserInfo?.username || `用户${msg.sender_id}`}
                    size={32}
                  />
                </div>
                <div className="chat-msg-content">
                  <Input.TextArea
                    value={editText}
                    onChange={(e) => setEditText(e.target.value)}
                    autoSize={{ minRows: 2, maxRows: 6 }}
                    style={{ marginBottom: 4 }}
                  />
                  <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                    <Button size="small" onClick={() => setEditingMsgId(null)}>取消</Button>
                    <Button size="small" type="primary" onClick={() => {
                      msgApi.edit(msg.message_id, editText).then(() => {
                        setEditingMsgId(null);
                        void messageSync.reSync();
                      }).catch(() => message.error('编辑失败'));
                    }}>保存</Button>
                  </div>
                </div>
              </div>
            );
          }
          if (msg.type === 7) {
            const sysContent = msg.content?.system || {};
            const action = sysContent.action || '';

            // 公告变更 — 使用卡片式展示
            if (action === 'announcement.updated' || action === 'announcement.deleted') {
              const isDeleted = action === 'announcement.deleted';
              let payload: { content?: string; old_content?: string } = {};
              try { payload = JSON.parse(sysContent.payload || '{}'); } catch { /* ignore */ }
              const announcementText = payload.content || '';
              const operatorInfo = getMsgUserInfo({ sender_id: sysContent.actor_id, sender_type: sysContent.actor_type }, userMap);
              const operatorName = operatorInfo?.username || `用户${sysContent.actor_id}`;
              const operatorRole = sysContent.actor_id ? memberRoleMap.get(sysContent.actor_id) : undefined;
              const roleLabel = roleLabels[operatorRole || ''] || '成员';
              const roleColor = roleColors[operatorRole || ''] || 'default';

              return (
                <div key={msg.message_id || msg.seq} className="chat-announcement-card">
                  <div className="chat-announcement-card-inner">
                    {/* Header */}
                    <div className="chat-announcement-card-header">
                      <div className="chat-announcement-card-title">
                        <div className="chat-announcement-card-icon">📢</div>
                        <span className="chat-announcement-card-title-text">
                          {isDeleted ? '群公告已删除' : '群公告'}
                        </span>
                      </div>
                      <span className="chat-announcement-card-time">{formatTime(msg.created_at)}</span>
                    </div>
                    <hr className="chat-announcement-card-divider" />
                    {/* Body */}
                    <div className="chat-announcement-card-body">
                      {isDeleted ? (
                        <div className="chat-announcement-card-empty">
                          <span className="chat-announcement-card-empty-icon">📭</span>
                          <span className="chat-announcement-card-empty-text">该群暂无公告</span>
                        </div>
                      ) : (
                        <div className="chat-announcement-card-content">{announcementText}</div>
                      )}
                    </div>
                    <hr className="chat-announcement-card-divider" />
                    {/* Footer */}
                    <div className="chat-announcement-card-footer">
                      <Tag color={roleColor} style={{ fontSize: 11, lineHeight: '18px', padding: '0 4px', margin: 0 }}>
                        {roleLabel}
                      </Tag>
                      <span>{operatorName}</span>
                      <span>{isDeleted ? '删除了群公告' : '更新了群公告'}</span>
                    </div>
                  </div>
                </div>
              );
            }

            // 解析操作者和被操作者信息
            const actorInfo = getMsgUserInfo({ sender_id: sysContent.actor_id, sender_type: sysContent.actor_type }, userMap);
            const actorName = actorInfo?.username || `用户${sysContent.actor_id}`;
            const relatedIDs: string[] = Array.isArray(sysContent.related_user_ids) ? sysContent.related_user_ids : [];
            const relatedNames = relatedIDs.map((uid) => {
              const info = getMsgUserInfo({ sender_id: uid, sender_type: 'user' }, userMap);
              return info?.username || `用户${uid}`;
            });

            // 转让群主 — 突出展示
            if (action === 'conversation.owner.transferred') {
              const targetName = relatedNames[0] || '新成员';
              return (
                <div key={msg.message_id || msg.seq} className="chat-system-msg">
                  <div className="chat-system-msg-time">{formatTime(msg.created_at)}</div>
                  <div className="chat-system-msg-body">
                    <span className="chat-system-msg-actor">{actorName}</span>
                    <span> 将群主转让给 </span>
                    <span className="chat-system-msg-target">{targetName}</span>
                  </div>
                </div>
              );
            }

            // 禁言/取消禁言 — 展示操作双方
            if (action === 'member.muted' || action === 'member.unmuted') {
              const targetName = relatedNames[0] || '成员';
              const actText = action === 'member.muted' ? '禁言' : '取消禁言';
              return (
                <div key={msg.message_id || msg.seq} className="chat-system-msg">
                  <div className="chat-system-msg-time">{formatTime(msg.created_at)}</div>
                  <div className="chat-system-msg-body">
                    <span className="chat-system-msg-actor">{actorName}</span>
                    <span> {actText}了 </span>
                    <span className="chat-system-msg-target">{targetName}</span>
                    {sysContent.detail && !sysContent.detail.startsWith('被') && (
                      <span>（{sysContent.detail}）</span>
                    )}
                  </div>
                </div>
              );
            }

            // 全员禁言/取消 — 展示操作者
            if (action === 'conversation.muted_all' || action === 'conversation.unmuted_all') {
              return (
                <div key={msg.message_id || msg.seq} className="chat-system-msg">
                  <div className="chat-system-msg-time">{formatTime(msg.created_at)}</div>
                  <div className="chat-system-msg-body">
                    <span className="chat-system-msg-actor">{actorName}</span>
                    <span> {sysContent.detail || action}</span>
                  </div>
                </div>
              );
            }

            // 成员加入/退出、机器人加入/退出 — 带名字展示
            if (action === 'member.joined' || action === 'member.left' ||
                action === 'bot.joined' || action === 'bot.removed') {
              const displayNames = relatedNames.length > 0
                ? relatedNames.join('、')
                : '';
              if (action === 'member.left' && relatedNames.length === 1 && sysContent.actor_id === relatedIDs[0]) {
                // 自己退出
                return (
                  <div key={msg.message_id || msg.seq} className="chat-system-msg">
                    <div className="chat-system-msg-time">{formatTime(msg.created_at)}</div>
                    <div className="chat-system-msg-body">
                      <span className="chat-system-msg-actor">{displayNames}</span>
                      <span> 退出了群聊</span>
                    </div>
                  </div>
                );
              }
              return (
                <div key={msg.message_id || msg.seq} className="chat-system-msg">
                  <div className="chat-system-msg-time">{formatTime(msg.created_at)}</div>
                  <div className="chat-system-msg-body">
                    {displayNames && (
                      <span className="chat-system-msg-target">{displayNames} </span>
                    )}
                    <span>{sysContent.detail || action}</span>
                  </div>
                </div>
              );
            }

            // 其他系统消息 — 兜底展示
            return (
              <div key={msg.message_id || msg.seq} className="chat-system-msg">
                <div className="chat-system-msg-time">
                  {formatTime(msg.created_at)}
                </div>
                <div className="chat-system-msg-body">
                  {sysContent.actor_id ? (
                    <>
                      <span className="chat-system-msg-actor">{actorName}</span>
                      <span> {sysContent.detail || sysContent.action || ''}</span>
                    </>
                  ) : (
                    sysContent.detail || sysContent.action || ''
                  )}
                </div>
              </div>
            );
          }
          const msgUserInfo = getMsgUserInfo(msg, userMap);
          const isMediaMsg = msg.type === 2 || msg.type === 3; // image or file — no bubble background
          const identityTitle = msg.sender_type === 'bot' ? `Bot ID: ${msg.sender_id}${msg.binding_id ? ` · 绑定 ID: ${msg.binding_id}` : ''}` : undefined;
          return (
            <div key={msg.message_id || msg.seq} className={`chat-msg ${isSelf ? 'chat-msg-self' : 'chat-msg-other'}`} data-msg-id={msg.message_id}
              data-sender-id={msg.sender_id} data-sender-type={msg.sender_type} data-binding-id={msg.binding_id}
              onContextMenu={(e) => showMsgActions(e, msg)}>
              <div className="chat-msg-avatar-col">
                <Avatar
                  src={msgUserInfo?.avatar}
                  name={msgUserInfo?.username || `用户${msg.sender_id}`}
                  size={32}
                />
              </div>
              <div className="chat-msg-content">
                {!isSelf && msgUserInfo?.username && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 2 }}>
                    <span className="chat-msg-nickname" title={identityTitle}>{msgUserInfo.username}</span>
                    {(() => {
                      const role = msg.sender_type === 'user' ? memberRoleMap.get(msg.sender_id) : undefined;
                      const isBot = msg.sender_type === 'bot';
                      return (
                        <>
                          {role && role !== 'MEMBER_ROLE_MEMBER' ? (
                            <Tag color={roleColors[role]} style={{ fontSize: 10, lineHeight: '16px', padding: '0 4px', margin: 0 }}>
                              {roleLabels[role]}
                            </Tag>
                          ) : null}
                          {isBot ? (
                            <Tag color="purple" style={{ fontSize: 10, lineHeight: '16px', padding: '0 4px', margin: 0 }}>
                              BOT
                            </Tag>
                          ) : null}
                        </>
                      );
                    })()}
                  </div>
                )}
                {msg.reply_to && (
                  <div className="chat-msg-reply">回复: {msg.reply_to.sender_type === 'bot' ? '[Bot] ' : ''}{msg.reply_to.sender_name ? `${msg.reply_to.sender_name}: ` : ''}{msg.reply_to.preview || '消息不可用'}</div>
                )}
                <div className={`chat-msg-bubble-wrapper${isMediaMsg ? ' chat-msg-bubble-wrapper-media' : ''}`}>
                  <div className={`chat-msg-bubble${isMediaMsg ? ' chat-msg-bubble-media' : ''} ${msg.status === 2 ? 'recalled' : ''}`}>
                    {msg.status === 2 ? <span style={{ fontStyle: 'italic', opacity: 0.6 }}>消息已撤回</span> : <>
                      {renderContent(msg.content || {}, setFilePreview)}
                      <MessageMetadata message={msg} />
                    </>}
                  </div>
                  <button className="chat-msg-actions-btn" onClick={(e) => showMsgActions(e, msg)}>
                    <MoreOutlined />
                  </button>
                </div>
                <div className="chat-msg-footer">
                    <span className="chat-msg-time">{formatTime(msg.created_at)}</span>
                    {msg.sender_type === 'bot' && msg.requester_id && <span title={`请求者 ID: ${msg.requester_id}`}>请求者: {userMap.get(msg.requester_id)?.username || msg.requester_id}</span>}
                    {runQuery.runs.some((run) => run.message_id === msg.message_id) && <Button type="link" size="small" onClick={() => void replyToMessage(msg)}>回复此执行</Button>}
                    {renderReadStatus(conv, msg, isSelf, readStatuses)}
                  </div>
              </div>
            </div>
          );
        })}

          <section className="chat-run-list" aria-label="Bot 执行记录">
            {(runnableBots.length > 0 || runQuery.runs.length > 0 || runQuery.isError) && <div className="chat-run-list-header">
              <strong>Bot 执行记录</strong>
              <Button size="small" loading={runQuery.isFetching} onClick={() => { void runQuery.refetch(); void refetchMembers(); void queryClient.invalidateQueries({ queryKey: ['conv-bots', id] }); }}>刷新执行</Button>
            </div>}
            {runQuery.isPending && runnableBots.length > 0 && <div role="status">正在读取执行记录…</div>}
            {runQuery.connection !== 'connected' && runQuery.runs.length > 0 && <div className="chat-run-notice" role="status">实时连接已断开；执行不因此结束，查询将恢复最新状态。</div>}
            {runQuery.isError && <div className="chat-run-error" role="alert">执行记录读取失败，请刷新重试。</div>}
            {membersError && <div className="chat-run-error" role="alert">当前成员资格读取失败，控制入口暂不可用；请刷新执行。</div>}
            {bindingsError && <div className="chat-run-error" role="alert">当前 Bot 绑定读取失败，补充、继续及控制入口暂不可用；请刷新执行。</div>}
            {!runQuery.isError && !runQuery.isPending && runnableBots.length > 0 && runQuery.runs.length === 0 && <div className="chat-run-notice">尚无执行。选择 Bot 并明确发起工作，或在消息中提及它。</div>}
            {!runQuery.isError && runQuery.runs.map((run) => <RunCard key={run.id} run={run}
              botName={convBots.find((bot) => bot.bot_id === run.bot_id)?.name || `Bot ${run.bot_id}`}
              requesterName={run.requester_id ? userMap.get(run.requester_id)?.username || run.requester_id : undefined}
              nameForUser={(userId) => userMap.get(userId)?.username || convBots.find((bot) => bot.bot_id === userId)?.name || userId}
              canContinue={!membersError && !!currentMember && isTerminalRun(run) && run.interaction_status === 'active' && hasCurrentRunBinding(run, runnableBots)}
              activeWork={runQuery.runs.some((candidate) => candidate.interaction_id === run.interaction_id && !isTerminalRun(candidate))}
              canManage={!membersError && !!currentMember && run.can_control === true}
              bindingActive={!membersError && !!currentMember && hasCurrentRunBinding(run, runnableBots)} currentUserId={currentUserId} members={members}
              onLocate={locateRunMessage} onLocateFile={async (fileId) => {
                const file = await fileApi.getDownloadUrl(fileId);
                if (pageConversation.current === id && authRevision === useAuthStore.getState().revision) setFilePreview({ ...file.file, url: file.download_url });
              }}
              onRefresh={async (runId) => {
                try { await runQuery.refreshRun(runId); }
                finally { await runQuery.refetch(); await refetchMembers(); void queryClient.invalidateQueries({ queryKey: ['conv-bots', id] }); }
              }}
              onCompose={beginWork} onNew={(run) => beginNewTopic(run.bot_id)} onUpdated={handleUpdatedRun} />)}
          </section>
        </div>
        {!isAtBottom && (
          <button className="chat-scroll-bottom" onClick={scrollToBottom}>
            <span className="chat-scroll-bottom__icon">↓</span>
          </button>
        )}
      </div>

      {typingText && (
        <div className="chat-typing">
          <span className="chat-typing-text">{typingText}</span>
          <span className="chat-typing-dots">
            <span className="chat-typing-dot" />
            <span className="chat-typing-dot" />
            <span className="chat-typing-dot" />
          </span>
        </div>
      )}


      <div
        className={`chat-input-area${dragOver ? ' drag-over' : ''}`}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        {pendingRuns.filter((pending) => pending.request.conv_id === id).map((pending) => (
          <div key={pending.request.submission_key} className="chat-reply-indicator" role="status">
            <span className="chat-reply-text" title={pending.request.message}>
              {requestingKey === pending.request.submission_key ? '正在确认执行请求' : '执行请求尚未确认'} · {pending.request.message}
              {pending.request.parent_run_id ? ` · 明确承接 ${pending.request.parent_run_id}` : ' · 独立新请求'}
              {(pending.request.message_ids?.length || pending.request.file_ids?.length || pending.request.history_grant_id) ? ' · 保留原来源选择' : ''}
              {' · 重试保留原内容与提交键，不创建重复执行'}
            </span>
            <Button size="small" loading={requestingKey === pending.request.submission_key} disabled={requestingKey !== null} onClick={() => void retryRunSubmission(pending)}>重试原请求</Button>
          </div>
        ))}
        {pendingSends.filter((pending) => pending.request.conversation_id === id).map((pending) => (
          <div key={pending.request.client_msg_id} className="chat-reply-indicator" role="status">
            <span className="chat-reply-text" title={pending.request.content.text || '附件发送'}>
              {retryingKey === pending.request.client_msg_id ? '正在确认发送' : '发送结果尚未确认'}
              {pending.request.content.text ? ` · 原发送：${pending.request.content.text}` : ' · 原发送：附件'}
              {' · 重试将确认原发送，不会新建消息'}
            </span>
            <Button size="small" disabled={retryingKey !== null} onClick={() => void retrySend(pending)}>重试发送</Button>
          </div>
        ))}
        <input type="file" ref={imageInputRef} accept="image/*" style={{ display: 'none' }} onChange={handleImageChange} />
        <input type="file" ref={fileInputRef} style={{ display: 'none' }} onChange={handleFileChange} />

        {runnableBots.length > 0 && <div className="chat-run-composer">
          <label htmlFor="chat-run-target">发送方式</label>
          <Select id="chat-run-target" aria-label="工作执行 Bot" value={runTarget} allowClear placeholder="普通消息"
            disabled={creatingRun || requestingKey !== null} onChange={(value: string | undefined) => {
              setRunTarget(value); setWorkRelation(undefined); setWorkError(''); setReplyTo(null);
              historyGrant.current = undefined; inputRetry.current = undefined;
            }}
            options={runnableBots.map((bot) => ({ value: bot.bot_id, label: `交给 ${bot.name} 开展工作` }))} />
          {id && <AuthorizationPanel convId={id} bots={runnableBots} onSelect={setResourceGrantIDs} />}
          {resourceGrantIDs.length > 0 && <Tag>本次资源许可：{resourceGrantIDs.length} 项</Tag>}
          <span>{workRelation?.mode === 'input' ? '向明确 Run 追加输入；参与者身份与接受顺序保留，补充不等于授权'
            : workRelation?.mode === 'continue' ? '明确承接父 Run 的获准材料与结果，创建关联新 Run，不改写旧执行'
            : runTarget ? '独立新请求：只提交本次输入与明确来源，不继承其他交互或群最近消息' : '选择 Bot 可发起独立执行'}</span>
        </div>}
        {workRelation && <div className="chat-work-relation" role="status">
          <strong>{workRelation.mode === 'input' ? '补充到 Run' : '继续父 Run'}：{workRelation.runId}</strong>
          <span>交互：{relatedRun?.interaction_id || '正在查询'} · {relatedRun?.interaction_status === 'closed' ? '已关闭' : relatedRun ? '开放' : '当前不可见'}</span>
          <div className="chat-run-controls">
            {inputBlocked && continuationAllowed && <Button disabled={creatingRun} onClick={() => beginWork(relatedRun!, 'continue')}>继续此结果</Button>}
            <Button disabled={creatingRun} onClick={() => beginNewTopic(runTarget)}>独立新话题</Button>
          </div>
          {inputBlocked && <p className="chat-run-notice">原 Run 当前不接受补充，不会暗中创建新执行。已结束的开放交互可明确继续，其余情况请发起独立新话题。</p>}
        </div>}
        {runTarget && <WorkSourcePicker materials={workMaterials} onChange={setWorkMaterials} messages={messages}
          uploadedFiles={uploadedWorkFiles} disabled={creatingRun || requestingKey !== null} uploading={uploading} onUpload={uploadWorkFile} />}
        {workError && <div className="chat-run-error" role="alert">{workError}</div>}
        <div className="chat-input-wrapper">
          <div className="chat-input-toolbar">
            <button className="chat-input-toolbar-btn" onClick={handleImagePick} disabled={uploading || creatingRun} aria-label={runTarget ? '选择图片作为工作来源' : '发送图片'} title={runTarget ? '选择工作图片' : '发送图片'}>
              <PictureOutlined />
            </button>
            <button className="chat-input-toolbar-btn" onClick={handleFilePick} disabled={uploading || creatingRun} aria-label={runTarget ? '上传文件作为工作来源' : '发送文件'} title={runTarget ? '选择工作文件' : '发送文件'}>
              <PaperClipOutlined />
            </button>
            <button
              className={`chat-input-toolbar-btn${recording ? ' recording' : ''}`}
              onMouseDown={handleVoiceStart}
              onMouseUp={handleVoiceEnd}
              onMouseLeave={handleVoiceEnd}
              onTouchStart={handleVoiceStart}
              onTouchEnd={handleVoiceEnd}
              disabled={uploading || creatingRun || !!runTarget}
              title={recording ? `${recordingDuration}s · 松开停止` : '按住录音'}
              aria-label={runTarget ? '工作输入请使用文字或明确文件来源' : recording ? '松开停止录音' : '按住录音'}
            >
              {recording ? <span style={{ fontSize: 11, fontWeight: 600 }}>{recordingDuration}s</span> : <AudioOutlined />}
            </button>
          </div>

          {replyTo && (
            <div className="chat-reply-indicator">
              <span className="chat-reply-text">回复: {replyTo.preview}</span>
              <button className="chat-reply-close" aria-label="取消消息引用" onClick={() => setReplyTo(null)}>
                <CloseOutlined />
              </button>
            </div>
          )}
          {mentionOpen && (
            <div className="chat-mention-dropdown">
              {mentionTargets
                .filter((target) => target.id !== currentUserId && target.name.toLowerCase().includes(mentionQuery.toLowerCase()))
                .map((target, i) => (
                  <div key={target.id}
                    className={`chat-mention-item${i === mentionSelected ? ' active' : ''}`}
                    onMouseDown={(e) => { e.preventDefault(); insertMention(target); }}
                    onMouseEnter={() => setMentionSelected(i)}>
                    <Avatar src={target.avatar} name={target.name} size={24} />
                    <span className="chat-mention-name">{target.name}</span>
                    {target.isBot && <span className="chat-mention-badge">BOT</span>}
                  </div>
                ))}
              {mentionTargets.filter((target) => target.id !== currentUserId && target.name.toLowerCase().includes(mentionQuery.toLowerCase())).length === 0 && (
                <div className="chat-mention-empty">无匹配成员</div>
              )}
            </div>
          )}
          <textarea
            ref={chatInputRef}
            className="chat-input"
            aria-label={workRelation?.mode === 'input' ? '补充工作输入' : workRelation?.mode === 'continue' ? '关联后续工作内容' : runTarget ? '工作请求内容' : '消息内容'}
            placeholder={uploading ? '上传中...' : creatingRun ? '提交工作输入中...' : sendMutation.isPending ? '发送中...' : workRelation?.mode === 'input' ? '补充到指定 Run；Enter 提交，Shift+Enter 换行' : workRelation?.mode === 'continue' ? '描述承接结果的后续工作；Enter 创建关联 Run' : runTarget ? '描述独立新工作；Enter 发起，Shift+Enter 换行' : '输入消息...'}
            value={input}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            rows={1}
            disabled={creatingRun || requestingKey !== null}
          />
          <button className="chat-send-btn" onClick={runTarget ? () => void handleStartRun() : handleSend} disabled={!input.trim() || sendMutation.isPending || creatingRun || requestingKey !== null || uploading || (inputBlocked && !retryInputReady)}>
            {creatingRun || requestingKey !== null ? '提交中…' : workRelation?.mode === 'input' ? retryInputReady ? '重试原补充' : '提交补充' : workRelation?.mode === 'continue' ? '创建关联执行' : runTarget ? '发起工作' : sendMutation.isPending ? '...' : '发送'}
          </button>
        </div>
      </div>
        </div>
    </div>

      {/* Message action menu */}
      {msgActions && (
        <>
          <div className="chat-msg-actions-overlay" onClick={() => setMsgActions(null)} />
          <div
            className="chat-msg-actions-menu"
            style={{ position: 'fixed', left: msgActions.x, top: msgActions.y, zIndex: 1000 }}
          >
            <button className="chat-msg-action-item" onClick={() => void replyToMessage(msgActions.msg)}>回复</button>
            {runnableBots.length > 0 && msgActions.msg.status !== 2 && <button className="chat-msg-action-item" onClick={() => {
              const messageId = msgActions.msg.message_id;
              setWorkMaterials((materials) => ({ ...materials, messageIds: [...new Set([...materials.messageIds, messageId])] }));
              setMsgActions(null);
              if (!runTarget) message.info('消息已选为工作来源，请选择要开展工作的 Bot');
            }}>选为工作来源</button>}
            <div className="chat-msg-action-item" onClick={() => {
              const name = getMsgUserInfo(msgActions.msg, userMap).username;
              if (name) {
                setInput((prev) => prev ? `${prev} @${name} ` : `@${name} `);
                chatInputRef.current?.focus();
              }
              setMsgActions(null);
            }}>
              @提及
            </div>
            {msgActions.msg.sender_type === 'user' && msgActions.msg.sender_id === currentUserId && msgActions.msg.type === 1 && (
              <div className="chat-msg-action-item" onClick={() => { setEditingMsgId(msgActions.msg.message_id); setEditText(extractTextPreview(msgActions.msg.content)); setMsgActions(null); }}>
                编辑
              </div>
            )}
            {msgActions.msg.sender_type === 'user' && msgActions.msg.sender_id === currentUserId && msgActions.msg.status === 1 && (
              <div className="chat-msg-action-item" onClick={() => {
                const msgId = msgActions.msg.message_id;
                setMsgActions(null);
                msgApi.recall(msgId)
                  .then(() => { void messageSync.reSync(); })
                  .catch(() => message.error('撤回失败'));
              }}>
                撤回
              </div>
            )}
            <div className="chat-msg-action-item chat-msg-action-danger" onClick={() => {
              const msgId = msgActions.msg.message_id;
              setMsgActions(null);
              msgApi.delete(msgId).then(() => {
                messageSync.removeMessage(id!, msgId);
              }).catch(() => message.error('删除失败'));
            }}>
              删除
            </div>
            {(msgActions.msg.sender_type === 'user' && msgActions.msg.sender_id === currentUserId || isAdmin) && (
              <div className="chat-msg-action-item chat-msg-action-danger" onClick={() => {
                const msgId = msgActions.msg.message_id;
                setMsgActions(null);
                AntModal.confirm({ title: '为所有成员删除消息？', content: '此消息将从所有成员的历史中移除。',
                  okText: '删除', cancelText: '取消', okButtonProps: { danger: true },
                  onOk: async () => { await msgApi.delete(msgId, true); void messageSync.reSync(); } });
              }}>为所有成员删除</div>
            )}
          </div>
        </>
      )}

      {/* Group Info Drawer */}
      {(conv?.type === 'group' || conv?.type === 'private' && (convBots.length > 0 || members.length === 1)) && (
        <GroupInfoDrawer
          conv={conv}
          members={members}
          isOwner={isOwner}
          isAdmin={isAdmin}
          currentUserId={currentUserId}
          open={groupInfoOpen}
          onClose={() => setGroupInfoOpen(false)}
          onMembersChange={() => { refetchMembers(); queryClient.invalidateQueries({ queryKey: ['conversation', id] }); queryClient.invalidateQueries({ queryKey: ['conversations'] }); queryClient.invalidateQueries({ queryKey: ['bot-runs', currentUserId, authRevision, id] }); }}
        />
      )}



      {/* File Preview Modal */}
      {filePreview && <FilePreviewModal key={filePreview.file_id} file={filePreview} onClose={() => setFilePreview(null)} />}
    </>
    );
  }
