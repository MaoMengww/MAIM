import { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { message, Button, Input, Tag, Modal as AntModal, Image, Spin } from 'antd';
import {
  MessageOutlined, DownloadOutlined,
  InfoCircleOutlined,
  SearchOutlined,
  CloseOutlined,
  PictureOutlined, PaperClipOutlined,
  AudioOutlined, PlayCircleOutlined,
  PauseCircleOutlined, MoreOutlined,
  FileOutlined, FileTextOutlined, FilePdfOutlined, FileImageOutlined, FileZipOutlined,
} from '@ant-design/icons';
import { convApi } from '@/services/conversation';
import { msgApi, normalizeRealtimeMessageContent } from '@/services/message';
import { fileApi } from '@/services/file';
import { messageSync } from '@/services/messageSync';
import { wsOn, wsSend } from '@/services/ws';
import { useWSStore } from '@/stores/ws';
import { useAuthStore } from '@/stores/auth';
import { convToolApi } from '@/services/conversation-tool';
import { SearchFilterBar } from '@/components/common/SearchFilterBar';
import { useMessages } from '@/hooks/useMessages';
import { Avatar } from '@/components/common/Avatar';
import { GroupInfoDrawer } from './GroupInfoDrawer';
import { SummaryPanel } from './components/SummaryPanel';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { MsgContentOneof, ConvMember, AudioContent, KnowledgeSource } from '@/types/model';
import type { SendMsgContent } from '@/types/api';

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
  // Bot content wrapper: { bot: { text: "..." } }
  if (content.bot?.text) return typeof content.bot.text === 'string' ? content.bot.text : JSON.stringify(content.bot.text);
  // Normalized text: { text: { text: "...", mentions: [...] } }
  if (content.text) {
    if (typeof content.text === 'string') return content.text;
    if (typeof content.text.text === 'string') return content.text.text;
    return JSON.stringify(content.text);
  }
  if (content.file_name) return `[文件] ${content.file_name}`;
  if (content.image_url) return '[图片]';
  return '';
}

const MSG_TYPE_LABELS: Record<number, string> = {
  1: '文本', 2: '图片', 3: '文件', 4: '视频',
  5: '语音', 6: '位置', 7: '系统', 8: '自定义', 9: 'Bot',
};

function convTitle(c: any): string {
  return c?.name || (c?.type === 'group' ? '群聊' : '私聊');
}

// ─── File icon helper ───
function getFileIcon(ext: string) {
  const lower = ext.toLowerCase();
  if (/^(jpg|jpeg|png|gif|webp|svg|bmp)$/.test(lower)) return <FileImageOutlined style={{ color: '#00a854', fontSize: 28 }} />;
  if (lower === 'pdf') return <FilePdfOutlined style={{ color: '#f40f02', fontSize: 28 }} />;
  if (/^(doc|docx)$/.test(lower)) return <FileTextOutlined style={{ color: '#2b579a', fontSize: 28 }} />;
  if (/^(xls|xlsx)$/.test(lower)) return <FileTextOutlined style={{ color: '#217346', fontSize: 28 }} />;
  if (/^(zip|rar|7z|tar|gz)$/.test(lower)) return <FileZipOutlined style={{ color: '#f5a623', fontSize: 28 }} />;
  if (/^(txt|md|json|xml|csv|log|yaml|yml|go|js|ts|py|java)$/.test(lower)) return <FileTextOutlined style={{ color: '#666', fontSize: 28 }} />;
  return <FileOutlined style={{ color: '#999', fontSize: 28 }} />;
}

// ─── Audio Player for voice messages ───
function AudioPlayer({ audio }: { audio: AudioContent }) {
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
      el.play().catch(() => {});
      setPlaying(true);
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
      <audio ref={audioRef} src={audio.url} preload="metadata" />
      <button className="chat-msg-audio-btn" onClick={togglePlay}>
        {playing ? <PauseCircleOutlined /> : <PlayCircleOutlined />}
      </button>
      <div className="chat-msg-audio-track" onClick={handleTrackClick}>
        <div className="chat-msg-audio-progress" style={{ width: `${pct}%` }} />
      </div>
      <span className="chat-msg-audio-duration">{audio.duration || 0}"</span>
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

function renderContent(content: MsgContentOneof, onFilePreview?: (f: any) => void) {
  if ('text' in content) {
    return <ChatMarkdown>{content.text.text}</ChatMarkdown>;
  }
  if ('image' in content) {
    const img = content.image;
    return (
      <div className="chat-msg-image">
        <Image
          src={img.url}
          alt="图片"
          style={{ maxWidth: 240, maxHeight: 240 }}
          preview={{ mask: '点击预览' }}
          fallback="data:image/svg+xml;base64,PHN2ZyB3aWR0aD0iMjQwIiBoZWlnaHQ9IjI0MCIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIj48cmVjdCB3aWR0aD0iMTAwJSIgaGVpZ2h0PSIxMDAlIiBmaWxsPSIjZjBmMGYwIi8+PHRleHQgeD0iNTAlIiB5PSI1MCUiIGRvbWluYW50LWJhc2VsaW5lPSJtaWRkbGUiIHRleHQtYW5jaG9yPSJtaWRkbGUiIGZpbGw9IiNjMGMwYzAiIGZvbnQtc2l6ZT0iMTQiPuWbvueJueWKqOWPkeWksei0pTwvdGV4dD48L3N2Zz4="
        />
      </div>
    );
  }
  if ('file' in content) {
    const f = content.file;
    const ext = f.ext || (f.name?.includes('.') ? f.name.split('.').pop() : '') || '';
    return (
      <div className="chat-msg-file" onClick={() => onFilePreview?.(f)}>
        {getFileIcon(ext)}
        <div className="chat-msg-file-info">
          <div className="chat-msg-file-name">{f.name || '文件'}</div>
          <div className="chat-msg-file-size">{f.size ? `${(f.size / 1024).toFixed(1)} KB` : ''}</div>
        </div>
      </div>
    );
  }
  if ('audio' in content) {
    return <AudioPlayer audio={content.audio} />;
  }
  if ('video' in content) {
    return <span>🎬 [视频消息]</span>;
  }
  if ('system' in content) {
    return <span style={{ color: 'var(--aim-text-tertiary)', fontSize: 12, fontStyle: 'italic' }}>{content.system.detail || content.system.action}</span>;
  }
  if ('bot' in content) {
    const b = content.bot;
    const sources = parseSources(b);
    const tools = parseTools(b);
    return (
      <div>
        {b.bot_name && <div style={{ fontWeight: 500, fontSize: 12, marginBottom: 4, color: 'var(--aim-primary)' }}>{b.bot_name}</div>}
        <ChatMarkdown>{b.text}</ChatMarkdown>
        {tools.length > 0 && <ToolUsage tools={tools} />}
        {sources.length > 0 && <SourceSection sources={sources} />}
      </div>
    );
  }
  // Fallback: try to detect audio from raw fields (e.g. sync API flattens oneof)
  const raw = content as any;
  if ((raw.url || raw.file_url) && raw.duration !== undefined) {
    return <AudioPlayer audio={{ url: raw.url || raw.file_url || '', duration: raw.duration || 0, file_id: raw.file_id, size: raw.size || 0 }} />;
  }
  return <span>[未知消息]</span>;
}

/** Parse KnowledgeSource[] from BotContent.raw_payload */
function parseSources(b: any): KnowledgeSource[] {
  if (!b?.raw_payload) return [];
  try {
    const parsed = JSON.parse(b.raw_payload);
    return Array.isArray(parsed?.kb_sources) ? parsed.kb_sources : [];
  } catch {
    return [];
  }
}

/** Parse tool names from BotContent.raw_payload */
function parseTools(b: any): string[] {
  if (!b?.raw_payload) return [];
  try {
    const parsed = JSON.parse(b.raw_payload);
    return Array.isArray(parsed?.tool_names) ? parsed.tool_names : [];
  } catch {
    return [];
  }
}

/** Collapsible knowledge source section below bot messages */
function SourceSection({ sources }: { sources: KnowledgeSource[] }) {
  const [open, setOpen] = useState(false);
  const [expandedMap, setExpandedMap] = useState<Record<string, boolean>>({});
  if (!sources.length) return null;

  // Group by kb_name, preserve original order
  const groups: { kbName: string; type: string; sources: KnowledgeSource[] }[] = [];
  const seen = new Map<string, number>();
  for (const s of sources) {
    const key = s.kb_name || s.type;
    const idx = seen.get(key);
    if (idx !== undefined) {
      groups[idx].sources.push(s);
    } else {
      seen.set(key, groups.length);
      groups.push({ kbName: s.kb_name, type: s.type, sources: [s] });
    }
  }

  return (
    <div style={{ marginTop: 8, borderTop: '1px solid var(--aim-border)', paddingTop: 6 }}>
      <div
        onClick={() => setOpen(!open)}
        style={{ cursor: 'pointer', fontSize: 12, color: 'var(--aim-text-tertiary)', userSelect: 'none', display: 'flex', alignItems: 'center', gap: 4 }}
      >
        <span style={{ transform: `rotate(${open ? 90 : 0}deg)`, transition: 'transform 0.15s', fontSize: 10 }}>▶</span>
        消息来源<span style={{ marginLeft: 4, opacity: 0.6 }}>({sources.length})</span>
      </div>
      {open && (
        <div style={{ marginTop: 4, fontSize: 12 }}>
          {groups.map((g, gi) => (
            <div key={gi} style={{ marginTop: 4 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
                <span style={{ fontWeight: 500, color: 'var(--aim-text-secondary)' }}>{g.kbName || '知识库'}</span>
                <span style={{ fontSize: 10, lineHeight: '16px', padding: '0 4px', borderRadius: 3, background: '#fff7e6', color: '#d48806', border: '1px solid #ffe58f' }}>
                  {g.type.toUpperCase()}
                </span>
              </div>
              {g.sources.map((s, i) => (
                <SourceItem key={`${gi}-${i}`} source={s} expandedMap={expandedMap} setExpandedMap={setExpandedMap} itemKey={`${gi}-${i}`} />
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
      <div
        onClick={() => setExpandedMap((prev: Record<string, boolean>) => ({ ...prev, [itemKey]: !prev[itemKey] }))}
        style={{ cursor: 'pointer', padding: '2px 6px', borderRadius: 4, background: 'var(--aim-bg-tertiary)', display: 'flex', alignItems: 'center', gap: 4, userSelect: 'none' }}
      >
        <span style={{ transform: `rotate(${expanded ? 90 : 0}deg)`, transition: 'transform 0.15s', fontSize: 10 }}>▶</span>
        <span style={{ color: 'var(--aim-text-secondary)' }}>{source.title || '未命名'}</span>
      </div>
      {expanded && source.content && (
        <div style={{ padding: '4px 6px 4px 20px', color: 'var(--aim-text-tertiary)', lineHeight: 1.5, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
          {source.content.length > 200 ? source.content.slice(0, 200) + '...' : source.content}
        </div>
      )}
    </div>
  );
}

/** Protobuf enum serializes member_type as number (1=user, 2=bot), but TS type says string */
function isBotMember(m: ConvMember): boolean {
  const mt = (m as any).member_type;
  return mt === 2 || mt === 'bot';
}

// ─── File Preview Modal ───
function FilePreviewModal({ file, onClose }: { file: any; onClose: () => void }) {
  const [textContent, setTextContent] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [fetchError, setFetchError] = useState(false);

  const ext = (file?.name?.includes('.') ? file.name.split('.').pop() : '') || '';
  const isImage = file?.mime_type?.startsWith('image/') || /^(jpg|jpeg|png|gif|webp|svg|bmp)$/i.test(ext);
  const isPdf = file?.mime_type === 'application/pdf' || ext.toLowerCase() === 'pdf';
  const isRenderableText = /^(md|txt|json|xml|csv|yaml|yml|log|sh|js|ts|tsx|jsx|py|go|java|rb|rs|css|scss|less|html)$/i.test(ext);

  useEffect(() => {
    if (!file || !isRenderableText) return;
    setLoading(true);
    setFetchError(false);
    fetch(file.url)
      .then((r) => {
        if (!r.ok) throw new Error('fetch failed');
        return r.text();
      })
      .then(setTextContent)
      .catch(() => { setFetchError(true); setTextContent(''); })
      .finally(() => setLoading(false));
  }, [file]);

  if (!file) return null;

  const title = (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      {getFileIcon(ext)}
      <span style={{ fontSize: 14 }}>{file.name || '文件预览'}</span>
    </div>
  );

  return (
    <AntModal
      title={title}
      open={!!file}
      onCancel={onClose}
      footer={null}
      width={800}
      styles={{ body: { maxHeight: '80vh', overflow: 'auto' } }}
    >
      {loading && <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div>}
      {!loading && isImage && (
        <div style={{ textAlign: 'center' }}>
          <Image src={file.url} style={{ maxWidth: '100%' }} />
        </div>
      )}
      {!loading && isPdf && (
        <iframe src={file.url} style={{ width: '100%', height: '70vh', border: 'none', borderRadius: 8 }} title="PDF 预览" />
      )}
      {!loading && isRenderableText && fetchError && (
        <div style={{ textAlign: 'center', padding: 40, color: 'var(--aim-text-tertiary)' }}>
          <p>无法加载文件内容</p>
          <Button type="primary" href={file.url} target="_blank" icon={<DownloadOutlined />}>下载文件</Button>
        </div>
      )}
      {!loading && isRenderableText && !fetchError && (
        ext.toLowerCase() === 'md'
          ? <div className="chat-msg-bubble" style={{ background: 'var(--aim-surface)', border: '1px solid var(--aim-border)' }}><ChatMarkdown>{textContent || ''}</ChatMarkdown></div>
          : <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontSize: 13, lineHeight: 1.6, margin: 0 }}>{textContent}</pre>
      )}
      {!loading && !isImage && !isPdf && !isRenderableText && (
        <div style={{ textAlign: 'center', padding: 40 }}>
          <FileOutlined style={{ fontSize: 48, color: 'var(--aim-text-tertiary)' }} />
          <p style={{ marginTop: 12, color: 'var(--aim-text-secondary)' }}>此文件类型暂不支持预览</p>
          <Button type="primary" href={file.url} target="_blank" icon={<DownloadOutlined />}>下载文件</Button>
        </div>
      )}
    </AntModal>
  );
}

/** 获取消息发送者的显示名和头像 */
function getMsgUserInfo(msg: any, userMap: Map<string, { username: string; avatar: string }>): { username: string; avatar: string } | undefined {
  // Bot 消息的 from_user_id 曾经是伪用户 ID，迁移后改为 bot_id。
  // 旧消息仍可能带有旧伪用户 ID，兜底用 content.bot.bot_id 查询。
  const uid = String(msg.from_user_id);
  const info = userMap.get(uid);
  if (info) return info;
  if (msg.type === 9 && msg.content?.bot?.bot_id != null) {
    return userMap.get(String(msg.content.bot.bot_id));
  }
  return undefined;
}

function renderReadStatus(conv: any, msg: any, isSelf: boolean, readStatuses: Record<string, { read: boolean; readCount?: number; totalCount?: number }>): any {
  if (!isSelf || msg.status === 2) return null;
  const rs = readStatuses[String(msg.message_id)];
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
  const currentUserId = String(useAuthStore((s) => s.user?.id ?? ''));
  const currentUserName = useAuthStore((s) => s.user?.username ?? '');
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

  // ─── Summary panel ───
  const [summaryOpen, setSummaryOpen] = useState(false);

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
  const [convSenderId, setConvSenderId] = useState<number | undefined>();
  const [convSenderType, setConvSenderType] = useState<'' | 'user' | 'bot'>('');
  const [convStartTime, setConvStartTime] = useState<number | undefined>();
  const [convEndTime, setConvEndTime] = useState<number | undefined>();
  const [convSearchPage, setConvSearchPage] = useState(1);
  const convSearchDebounceRef = useRef<ReturnType<typeof setTimeout>>();

  // Streaming bot messages (key: `${convId}:${botId}`, value: accumulated text + reply info + sources)
  const [streamingMap, setStreamingMap] = useState<Record<string, { text: string; replyToMsgId?: string; createdAt: number; sources?: KnowledgeSource[]; tools?: string[] }>>({});
  const streamingRef = useRef(streamingMap);
  streamingRef.current = streamingMap;

  // Message action menu state
  const [msgActions, setMsgActions] = useState<{ msg: any; x: number; y: number } | null>(null);
  const [replyTo, setReplyTo] = useState<{ msg_id: string; preview: string } | null>(null);
  const [editingMsgId, setEditingMsgId] = useState<string | null>(null);
  const [editText, setEditText] = useState('');
  const [forwardMsgId, setForwardMsgId] = useState<string | null>(null);
  // File preview for in-page file viewing
  const [filePreview, setFilePreview] = useState<any>(null);
  const [replyCandidates, setReplyCandidates] = useState<string[]>([]);
  const [translateMap, setTranslateMap] = useState<Record<string, string>>({});
  // Clear streaming state when conversation changes
  useEffect(() => {
    return () => {
      setStreamingMap({});
    };
  }, [id]);

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
        const params: any = { conversation_id: id as any, page: convSearchPage, page_size: 20 };
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

  const scrollToMessage = (msgId: number) => {
    setConvSearchOpen(false);
    setConvSearchQuery('');
    setTimeout(() => {
      const el = document.querySelector(`[data-msg-id="${msgId}"]`);
      if (el) {
        el.scrollIntoView({ behavior: 'smooth', block: 'center' });
        el.classList.add('chat-msg-highlight');
        setTimeout(() => el.classList.remove('chat-msg-highlight'), 2000);
      }
    }, 100);
  };

  const navigateSearch = (dir: 'prev' | 'next') => {
    const total = convSearchResults.length;
    if (total === 0) return;
    setConvSearchIndex((prev) => {
      const next = dir === 'next' ? (prev + 1) % total : (prev - 1 + total) % total;
      const msgId = convSearchResults[next]?.message_id;
      if (msgId) {
        setTimeout(() => {
          const el = document.querySelector(`[data-msg-id="${msgId}"]`);
          if (el) {
            el.scrollIntoView({ behavior: 'smooth', block: 'center' });
            el.classList.add('chat-msg-highlight');
            setTimeout(() => el.classList.remove('chat-msg-highlight'), 2000);
          }
        }, 100);
      }
      return next;
    });
  };

  const numericUserId = Number(currentUserId);
  const strUserId = String(currentUserId);

  const { data: conv } = useQuery({
    queryKey: ['conversation', id],
    queryFn: () => convApi.get(id as any),
    enabled: !!id,
  });

  const { messages, loading: msgsLoading, reSync } = useMessages(id);

  // WS is the real-time source of truth; HTTP sync is only for initial load + reconnect
  useEffect(() => {
    if (!id) return;

    const unsubNew = wsOn('message.new', (payload: any) => {
      // 外层 payload.conv_id 是由后端 strconv.FormatInt 生成的 string，精度无损
      if (!payload.message || String(payload.conv_id) !== id) return;
      messageSync.onWsMessage(id!, payload.message);
    });

    const unsubRecalled = wsOn('message.recalled', (payload: any) => {
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      const msgId = Number(payload.message_id);
      if (msgId) messageSync.updateMessage(id!, msgId, (m: any) => ({ ...m, status: 2 }));
    });

    const unsubEdited = wsOn('message.edited', (payload: any) => {
      if (!payload.message) return;
      const edited = normalizeRealtimeMessageContent(payload.message);
      const cid = (edited as any).conv_id ?? edited.conversation_id;
      if (cid == null || Number(cid) !== Number(id)) return;
      const msgId = edited?.message_id;
      if (msgId) messageSync.updateMessage(id!, msgId, () => edited);
    });

    // Real-time read status: when someone reads messages in this conversation,
    // refetch members + conv to get updated last_read_seq
    const unsubRead = wsOn('unread_count', (payload: any) => {
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      queryClient.invalidateQueries({ queryKey: ['conv-members', id] });
      queryClient.invalidateQueries({ queryKey: ['conversation', id] });
    });

    const unsubReadReceipt = wsOn('read_receipt', (payload: any) => {
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      queryClient.setQueryData(['conv-members', id], (old: ConvMember[]) => {
        if (!old) return old;
        return old.map((m) =>
          String(m.user_id) === String(payload.user_id)
            ? { ...m, last_read_seq: payload.last_read_seq }
            : m,
        );
      });
    });

    // Bot streaming: accumulate chunks into a temporary message
    const unsubStreamChunk = wsOn('bot.streaming.chunk', (payload: any) => {
      // Number 比较避免 int64 Snowflake ID 精度丢失
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      const key = `${id}:${payload.bot_id}`;
      setStreamingMap((prev) => {
        const existing = prev[key];
        const replyToMsgId = existing?.replyToMsgId || String(payload.reply_to_msg_id || '');
        return {
          ...prev,
          [key]: {
            text: (existing?.text || '') + (payload.content || ''),
            createdAt: existing?.createdAt ?? Math.floor(Date.now() / 1000),
            ...(replyToMsgId ? { replyToMsgId } : {}),
          },
        };
      });
    });

    // Bot streaming tool_call/tool_result — could show inline indicators
    const unsubStreamTool = wsOn('bot.streaming.tool_call', (payload: any) => {
      // tool calls are silently consumed for now
    });

    // Bot streaming sources: store knowledge source metadata for the streaming message
    const unsubStreamSources = wsOn('bot.streaming.sources', (payload: any) => {
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      const key = `${id}:${payload.bot_id}`;
      let sources: KnowledgeSource[] = [];
      try {
        sources = JSON.parse(payload.content || '[]');
      } catch { /* ignore parse errors */ }
      if (sources.length === 0) return;
      setStreamingMap((prev) => {
        const existing = prev[key];
        // Create entry if streaming hasn't started yet (sources may arrive before first chunk)
        if (!existing) {
          return { ...prev, [key]: { text: '', sources, createdAt: Math.floor(Date.now() / 1000) } };
        }
        return { ...prev, [key]: { ...existing, sources } };
      });
    });

    // Bot streaming tool_used: record which tools were used
    const unsubStreamToolUsed = wsOn('bot.streaming.tool_used', (payload: any) => {
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      const key = `${id}:${payload.bot_id}`;
      const tools = payload.content ? payload.content.split(',').filter(Boolean) : [];
      if (!tools.length) return;
      setStreamingMap((prev) => {
        const existing = prev[key];
        if (!existing) {
          return { ...prev, [key]: { text: '', tools, createdAt: Math.floor(Date.now() / 1000) } };
        }
        return { ...prev, [key]: { ...existing, tools } };
      });
    });

    // Bot streaming done: transition streaming message into the real message cache
    const unsubStreamDone = wsOn('bot.streaming.done', (payload: any) => {
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      const key = `${id}:${payload.bot_id}`;
      // Read accumulated text from ref to avoid React batching race
      const entry = streamingRef.current[key];
      if (!payload.message_id || !entry?.text) {
        setStreamingMap((prev) => {
          const next = { ...prev };
          delete next[key];
          return next;
        });
        return;
      }

      const now = Math.floor(Date.now() / 1000);
      let replyToObj: { msg_id: string; preview: string } | undefined;
      if (entry.replyToMsgId) {
        const syncState = messageSync.getState(id!);
        const replyMsg = syncState?.messages?.find((m: any) => String(m.message_id) === entry.replyToMsgId);
        replyToObj = {
          msg_id: entry.replyToMsgId,
          preview: replyMsg ? extractTextPreview(replyMsg.content) : '[消息]',
        };
      }

      const rawPayload = entry.sources?.length || entry.tools?.length
        ? JSON.stringify({ kb_sources: entry.sources || [], tool_names: entry.tools || [] })
        : '';

      const newMsg = normalizeRealtimeMessageContent({
        message_id: Number(payload.message_id),
        conv_id: id,
        from_user_id: String(payload.bot_id),
        type: 9,
        content: { bot: { bot_id: payload.bot_id, text: entry.text, raw_payload: rawPayload } },
        status: 1,
        created_at: now,
        reply_to: replyToObj,
      });

      if (newMsg?.message_id) {
        messageSync.addMessage(id!, newMsg);
      }

      setStreamingMap((prev) => {
        const next = { ...prev };
        delete next[key];
        return next;
      });
    });

    // Async reply candidates result
    const unsubReplyCandidatesDone = wsOn('conv.reply_candidates.done', (payload: any) => {
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      setReplyCandidates(payload.candidates || []);
    });
    const unsubReplyCandidatesFailed = wsOn('conv.reply_candidates.failed', (payload: any) => {
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id)) return;
      message.error(payload.error || '生成回复建议失败');
    });

    // Async translate result (payload includes msg_id from original request)
    const unsubTranslateDone = wsOn('conv.translate.done', (payload: any) => {
      if (payload.msg_id) {
        setTranslateMap((prev: Record<string, string>) => ({ ...prev, [String(payload.msg_id)]: payload.translated_text }));
      }
    });
    const unsubTranslateFailed = wsOn('conv.translate.failed', (payload: any) => {
      if (payload.msg_id) {
        setTranslateMap((prev: Record<string, string>) => ({ ...prev, [String(payload.msg_id)]: '' }));
      }
      message.error(payload.error || '翻译失败');
    });

    return () => { unsubNew(); unsubRecalled(); unsubEdited(); unsubRead(); unsubReadReceipt(); unsubStreamChunk(); unsubStreamTool(); unsubStreamSources(); unsubStreamDone(); unsubReplyCandidatesDone(); unsubReplyCandidatesFailed(); unsubTranslateDone(); unsubTranslateFailed(); };
  }, [id, queryClient]);

  // Reset scroll state when conversation changes
  useEffect(() => {
    setIsAtBottom(true);
  }, [id]);

  // Re-sync on WS reconnect to catch up missed messages
  const wsReconnectVersion = useWSStore((s) => s.reconnectVersion);
  useEffect(() => {
    if (!id || wsReconnectVersion === 0) return;
    messageSync.reSync(id);
  }, [id, wsReconnectVersion]);

  useEffect(() => {
    if (listRef.current && messages.length > 0 && isAtBottom) {
      requestAnimationFrame(() => {
        if (listRef.current) {
          listRef.current.scrollTop = listRef.current.scrollHeight;
        }
      });
    }
  }, [messages]);

  // Auto-scroll on streaming content update
  useEffect(() => {
    if (listRef.current && Object.keys(streamingMap).length > 0 && isAtBottom) {
      requestAnimationFrame(() => {
        if (listRef.current) {
          listRef.current.scrollTop = listRef.current.scrollHeight;
        }
      });
    }
  }, [streamingMap]);

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
      const userId = String(payload.user_id);
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id) || userId === currentUserId) return;
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
      const userId = String(payload.user_id);
      if (payload.conv_id == null || Number(payload.conv_id) !== Number(id) || userId === currentUserId) return;
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
      queryClient.setQueryData(['conversations'], (old: any) => {
        if (!old?.list) return old;
        return {
          ...old,
          list: old.list.map((c: any) =>
            String(c.id) === id ? { ...c, unread_count: 0 } : c,
          ),
        };
      });
      convApi.markRead(id as any, maxSeq)
        .catch(() => {
          queryClient.invalidateQueries({ queryKey: ['conversations'] });
        });
    }
  }, [id, messages, queryClient]);

  const { data: members = [], refetch: refetchMembers } = useQuery({
    queryKey: ['conv-members', id],
    queryFn: () => convApi.getMembers(id as any),
    enabled: !!id,
  });

  // Compute read status for self-sent messages from member last_read_seq
  const readStatuses = useMemo(() => {
    const result: Record<string, { read: boolean; readCount?: number; totalCount?: number }> = {};
    if (!id || !messages.length || !conv || !members.length) return result;

    const selfMsgs = messages.filter((m: any) => String(m.from_user_id) === currentUserId);
    for (const msg of selfMsgs) {
      if (conv.type === 'private') {
        const peer = members.find((m: ConvMember) => String(m.user_id) !== currentUserId);
        result[String(msg.message_id)] = {
          read: !!peer && (peer.last_read_seq || 0) >= (msg.seq || 0),
        };
      } else {
        const readableMembers = members.filter((m: ConvMember) => !isBotMember(m));
        const totalCount = readableMembers.length;
        const readCount = readableMembers.filter((m: ConvMember) => (m.last_read_seq || 0) >= (msg.seq || 0)).length;
        result[String(msg.message_id)] = { read: readCount > 0, readCount, totalCount };
      }
    }
    return result;
  }, [id, messages, conv, currentUserId, members]);

  const currentMember = members.find((m) => String(m.user_id) === strUserId);
  const isOwner = String(conv?.owner_id) === strUserId;
  const isAdmin = isOwner || currentMember?.role === 'MEMBER_ROLE_ADMIN';

  const userMap = useMemo(() => {
    const map = new Map<string, { username: string; avatar: string }>();
    if (members?.length) {
      members.forEach((m: ConvMember) => {
        // Protobuf enum: 1=user, 2=bot (numbers, not strings)
        const isBot = isBotMember(m);
        const name = isBot ? (m.bot_name || m.username) : m.username;
        const avatar = isBot ? (m.bot_avatar || m.avatar) : m.avatar;
        map.set(String(m.user_id), { username: name, avatar });
        // Bot messages use bot_id (primary key) as sender_id,
        // not pseudo_user_id, so add both keys for lookup.
        if (m.bot_id) {
          map.set(String(m.bot_id), { username: name, avatar });
        }
      });
    }
    const currentUser = useAuthStore.getState().user;
    if (currentUser) {
      map.set(String(currentUser.id), {
        username: currentUser.username,
        avatar: currentUser.avatar,
      });
    }
    return map;
  }, [members, conv]);

  const memberRoleMap = useMemo(() => {
    const map = new Map<string, string>();
    if (members?.length) {
      members.forEach((m: ConvMember) => {
        map.set(String(m.user_id), m.role);
        if (m.bot_id) {
          map.set(String(m.bot_id), m.role);
        }
      });
    }
    return map;
  }, [members]);

  const botIdSet = useMemo(() => {
    const set = new Set<string>();
    if (members?.length) {
      members.forEach((m: ConvMember) => {
        if (isBotMember(m)) {
          set.add(String(m.user_id));
          if (m.bot_id) {
            set.add(String(m.bot_id));
          }
        }
      });
    }
    return set;
  }, [members]);

  const convSenderOptions = useMemo(() => {
    if (!members?.length) return [];
    return members.map((m: ConvMember) => ({
      id: m.user_id,
      name: isBotMember(m) ? (m.bot_name || m.username) : m.username,
      isBot: isBotMember(m),
    }));
  }, [members]);

  const roleLabels: Record<string, string> = { MEMBER_ROLE_OWNER: '群主', MEMBER_ROLE_ADMIN: '管理员', MEMBER_ROLE_MEMBER: '成员' };
  const roleColors: Record<string, string> = { MEMBER_ROLE_OWNER: 'gold', MEMBER_ROLE_ADMIN: 'blue', MEMBER_ROLE_MEMBER: 'default' };
  const { data: convListData } = useQuery({
    queryKey: ['conversations'],
    queryFn: () => convApi.list(),
    enabled: !!forwardMsgId,
  });

  // ─── Upload helper ───
  async function uploadFile(file: File) {
    setUploading(true);
    try {
      const uploadData = await fileApi.getUploadUrl({
        name: file.name,
        mime_type: file.type || 'application/octet-stream',
        size: file.size,
      });
      await fetch(uploadData.upload_url, {
        method: 'PUT',
        body: file,
        headers: { 'Content-Type': file.type || 'application/octet-stream' },
      });
      const fileInfo = await fileApi.confirmUpload(uploadData.file_id);
      const downloadData = await fileApi.getDownloadUrl(uploadData.file_id);
      const fileId = fileInfo.file_id; // string, precision preserved by safeJsonParse
      return { fileId, downloadUrl: downloadData.download_url, fileInfo };
    } finally {
      setUploading(false);
    }
  }

  // ─── Image selection ───
  function handleImagePick() {
    imageInputRef.current?.click();
  }

  async function handleImageChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = '';
    try {
      const { fileId, downloadUrl } = await uploadFile(file);
      sendMutation.mutate({
        type: 2,
        content: { file_id: fileId, image_url: downloadUrl, image_thumb: downloadUrl } as any,
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
    try {
      const { fileId, downloadUrl } = await uploadFile(file);
      sendMutation.mutate({
        type: 3,
        content: {
          file_id: fileId,
          file_url: downloadUrl,
          file_name: file.name,
          file_size: file.size,
          file_mime: file.type,
        } as any,
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
          const { fileId, downloadUrl } = await uploadFile(file);
          sendMutation.mutate({
            type: 5,
            content: { file_id: fileId, file_url: downloadUrl, duration } as any,
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

    if (!file.type.startsWith('image/')) {
      message.info('仅支持拖拽图片');
      return;
    }

    try {
      const { fileId, downloadUrl } = await uploadFile(file);
      sendMutation.mutate({
        type: 2,
        content: { file_id: fileId, image_url: downloadUrl, image_thumb: downloadUrl } as any,
      });
    } catch {
      message.error('图片发送失败');
    }
  }, []);

  const sendMutation = useMutation({
    mutationFn: (data: { type: number; content: SendMsgContent }) =>
      msgApi.send({
        conversation_id: id as any,
        ...data,
        reply_to_msg_id: replyTo?.msg_id as any,
      }),
    onSuccess: (result: any, variables) => {
      setInput('');
      setReplyTo(null);
      // Optimistically add the sent message to cache for immediate display
      const now = Math.floor(Date.now() / 1000);
      const newMsg = normalizeRealtimeMessageContent({
        message_id: result.message_id,
        conv_id: id,
        from_user_id: currentUserId,
        type: variables.type,
        content: variables.content,
        seq: result.seq,
        status: 1,
        created_at: result.created_at ?? now,
        reply_to: replyTo ? { msg_id: replyTo.msg_id, preview: replyTo.preview } : undefined,
      });
      if (newMsg?.message_id && id) {
        messageSync.addMessage(id, newMsg);
      }
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
    onError: () => message.error('发送失败'),
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
    if (!text) return;

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
      const member = (members as ConvMember[]).find((m) => m.username === username);
      if (member) {
        mentionSet.add(String(member.user_id));
      }
    }

    // Private chat: auto-mention peer
    if (conv?.type === 'private') {
      (members as ConvMember[]).forEach((m) => {
        if (String(m.user_id) !== currentUserId) {
          mentionSet.add(String(m.user_id));
        }
      });
    }

    sendMutation.mutate({
      type: 1,
      content: { text, mentions: [...mentionSet], mention_all: false },
    });
  }, [input, sendMutation, members, conv, currentUserId]);

  const insertMention = useCallback((member: ConvMember) => {
    const mentionLen = mentionQuery.length;
    const before = input.slice(0, mentionStartPos);
    const after = input.slice(mentionStartPos + 1 + mentionLen);
    const newText = `${before}@${member.username} ${after}`;
    setInput(newText);
    setMentionOpen(false);
    setTimeout(() => {
      chatInputRef.current?.focus();
    }, 0);
  }, [input, mentionStartPos, mentionQuery]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (mentionOpen) {
      const filtered = (members as ConvMember[]).filter(
        (m) => String(m.user_id) !== currentUserId && m.username.toLowerCase().includes(mentionQuery.toLowerCase())
      );
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
      handleSend();
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
          <Avatar name={conv?.name || (conv?.type === 'group' ? '群聊' : '私聊')} src={conv?.avatar} size={36} />
          <div>
            <div className="chat-header-name">{conv?.name || (conv?.type === 'group' ? '群聊' : '私聊')}</div>
            {conv && <div className="chat-header-meta">{conv.type === 'group' ? `${conv.member_count} 人` : ''}</div>}
          </div>
        </div>
        <div className="chat-header-actions">
          <button className="chat-header-info-btn" onClick={() => setConvSearchOpen(!convSearchOpen)} title="搜索消息">
            <SearchOutlined />
          </button>
          {conv?.type === 'group' && (
            <button className="chat-header-info-btn" onClick={() => setGroupInfoOpen(true)} title="群聊信息">
              <InfoCircleOutlined />
            </button>
          )}
          <button className="chat-header-info-btn" onClick={() => setSummaryOpen(!summaryOpen)} title="会话工具">
            <FileTextOutlined />
          </button>
        </div>
      </div>

      <div className="chat-body">
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
              const highlight = convSearchHighlights[String(msg.message_id)];
              const senderInfo = getMsgUserInfo(msg, userMap);
              return (
                <div
                  key={msg.message_id}
                  className="chat-search-result-item"
                  onClick={() => scrollToMessage(msg.message_id)}
                >
                  <Avatar
                    src={senderInfo?.avatar}
                    name={senderInfo?.username || `用户${msg.from_user_id}`}
                    size={28}
                  />
                  <div className="chat-search-result-body">
                    <div className="chat-search-result-sender">
                      {senderInfo?.username || `用户${msg.from_user_id}`}
                    </div>
                    <div className="chat-search-result-preview">
                      {highlight ? (
                        <span dangerouslySetInnerHTML={{ __html: highlight }} />
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
          {msgsLoading && messages.length === 0 && <div className="chat-loading">加载消息中...</div>}
          {!msgsLoading && messages.length === 0 && <div className="chat-loading">暂无消息，发送第一条消息吧</div>}

          {messages.map((msg: any) => {
          const isSelf = String(msg.from_user_id) === currentUserId;
          if (editingMsgId === String(msg.message_id)) {
            const editUserInfo = getMsgUserInfo(msg, userMap);
            return (
              <div key={msg.message_id} className="chat-msg chat-msg-self" data-msg-id={msg.message_id} onContextMenu={(e) => showMsgActions(e, msg)}>
                <div className="chat-msg-avatar-col">
                  <Avatar
                    src={editUserInfo?.avatar}
                    name={editUserInfo?.username || `用户${msg.from_user_id}`}
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
              const operatorInfo = getMsgUserInfo({ from_user_id: sysContent.actor_id }, userMap);
              const operatorName = operatorInfo?.username || `用户${sysContent.actor_id}`;
              const operatorRole = memberRoleMap.get(String(sysContent.actor_id));
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
            const actorInfo = getMsgUserInfo({ from_user_id: sysContent.actor_id }, userMap);
            const actorName = actorInfo?.username || `用户${sysContent.actor_id}`;
            const relatedIDs: number[] = Array.isArray(sysContent.related_user_ids) ? sysContent.related_user_ids : [];
            const relatedNames = relatedIDs.map((uid: number) => {
              const info = getMsgUserInfo({ from_user_id: uid }, userMap);
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
              if (action === 'member.left' && relatedNames.length === 1 && String(sysContent.actor_id) === String(relatedIDs[0])) {
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
          return (
            <div key={msg.message_id || msg.seq} className={`chat-msg ${isSelf ? 'chat-msg-self' : 'chat-msg-other'}`} data-msg-id={msg.message_id} onContextMenu={(e) => showMsgActions(e, msg)}>
              <div className="chat-msg-avatar-col">
                <Avatar
                  src={msgUserInfo?.avatar}
                  name={msgUserInfo?.username || `用户${msg.from_user_id}`}
                  size={32}
                />
              </div>
              <div className="chat-msg-content">
                {!isSelf && msgUserInfo?.username && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 2 }}>
                    <span className="chat-msg-nickname">{msgUserInfo.username}</span>
                    {(() => {
                      const role = memberRoleMap.get(String(msg.from_user_id));
                      const isBot = botIdSet.has(String(msg.from_user_id));
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
                  <div className="chat-msg-reply">回复: {msg.reply_to.preview}</div>
                )}
                <div className={`chat-msg-bubble-wrapper${isMediaMsg ? ' chat-msg-bubble-wrapper-media' : ''}`}>
                  <div className={`chat-msg-bubble${isMediaMsg ? ' chat-msg-bubble-media' : ''} ${msg.status === 2 ? 'recalled' : ''}`}>
                    {msg.status === 2 ? <span style={{ fontStyle: 'italic', opacity: 0.6 }}>消息已撤回</span> : renderContent(msg.content || {}, setFilePreview)}
                  {translateMap[String(msg.message_id)] && (
                    <div
                      className="chat-msg-translate"
                      onClick={() => setTranslateMap((prev) => {
                        const next = { ...prev };
                        delete next[String(msg.message_id)];
                        return next;
                      })}
                    >
                      🌐 {translateMap[String(msg.message_id)]}
                      <span className="chat-msg-translate-hide">收起</span>
                    </div>
                  )}
                  </div>
                  <button className="chat-msg-actions-btn" onClick={(e) => showMsgActions(e, msg)}>
                    <MoreOutlined />
                  </button>
                </div>
                <div className="chat-msg-footer">
                    <span className="chat-msg-time">{formatTime(msg.created_at)}</span>
                    {renderReadStatus(conv, msg, isSelf, readStatuses)}
                  </div>
              </div>
            </div>
          );
        })}

        {/* Streaming bot messages */}
        {Object.entries(streamingMap)
          .filter(([key]) => key.startsWith(`${id}:`))
          .map(([key, entry]) => {
            const botId = key.split(':')[1];
            const userInfo = getMsgUserInfo({ from_user_id: botId, content: { bot: { bot_id: botId, text: entry.text } }, type: 9 }, userMap);
            return (
              <div key={key} className="chat-msg chat-msg-other">
                <div className="chat-msg-avatar-col">
                  <Avatar
                    src={userInfo?.avatar}
                    name={userInfo?.username || `Bot${botId}`}
                    size={32}
                  />
                </div>
                <div className="chat-msg-content">
                  {userInfo?.username && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 2 }}>
                      <span className="chat-msg-nickname">{userInfo.username}</span>
                      {(() => {
                        const role = memberRoleMap.get(botId);
                        const isBot = botIdSet.has(botId);
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
                  {entry.replyToMsgId && (() => {
                    const replyMsg = messages.find((m: any) => String(m.message_id) === entry.replyToMsgId);
                    const replyPreview = replyMsg ? extractTextPreview(replyMsg.content) : `消息 ${entry.replyToMsgId}`;
                    return <div className="chat-msg-reply">回复: {replyPreview}</div>;
                  })()}
                  <div className="chat-msg-bubble-wrapper">
                    <div className="chat-msg-bubble">
                      <ChatMarkdown>{entry.text}</ChatMarkdown>
                      {entry.tools && entry.tools.length > 0 && <ToolUsage tools={entry.tools} />}
                      {entry.sources && entry.sources.length > 0 ? (
                        <SourceSection sources={entry.sources} />
                      ) : (
                        <span className="chat-msg-streaming-cursor">▍</span>
                      )}
                    </div>
                  </div>
                  <div className="chat-msg-footer">
                    <span className="chat-msg-time">{formatTime(entry.createdAt)}</span>
                  </div>
                </div>
              </div>
            );
          })}
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

      {replyCandidates.length > 0 && (
        <div className="chat-reply-candidates">
          <span className="chat-reply-candidates-label">💡 回复建议</span>
          <div className="chat-reply-candidates-list">
            {replyCandidates.map((text, i) => (
              <button
                key={i}
                className="chat-reply-candidate-btn"
                onClick={async () => {
                  setReplyCandidates([]);
                  sendMutation.mutate({
                    type: 1,
                    content: { text, mentions: [] as string[], mention_all: false },
                  } as any);
                }}
              >
                {text}
              </button>
            ))}
          </div>
        </div>
      )}

      <div
        className={`chat-input-area${dragOver ? ' drag-over' : ''}`}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        <input type="file" ref={imageInputRef} accept="image/*" style={{ display: 'none' }} onChange={handleImageChange} />
        <input type="file" ref={fileInputRef} style={{ display: 'none' }} onChange={handleFileChange} />

        <div className="chat-input-wrapper">
          <div className="chat-input-toolbar">
            <button className="chat-input-toolbar-btn" onClick={handleImagePick} disabled={uploading} title="发送图片">
              <PictureOutlined />
            </button>
            <button className="chat-input-toolbar-btn" onClick={handleFilePick} disabled={uploading} title="发送文件">
              <PaperClipOutlined />
            </button>
            <button
              className={`chat-input-toolbar-btn${recording ? ' recording' : ''}`}
              onMouseDown={handleVoiceStart}
              onMouseUp={handleVoiceEnd}
              onMouseLeave={handleVoiceEnd}
              onTouchStart={handleVoiceStart}
              onTouchEnd={handleVoiceEnd}
              disabled={uploading}
              title={recording ? `${recordingDuration}s · 松开停止` : '按住录音'}
            >
              {recording ? <span style={{ fontSize: 11, fontWeight: 600 }}>{recordingDuration}s</span> : <AudioOutlined />}
            </button>
          </div>

          {replyTo && (
            <div className="chat-reply-indicator">
              <span className="chat-reply-text">回复: {replyTo.preview}</span>
              <button className="chat-reply-close" onClick={() => setReplyTo(null)}>
                <CloseOutlined />
              </button>
            </div>
          )}
          {mentionOpen && (
            <div className="chat-mention-dropdown">
              {(members as ConvMember[])
                .filter((m) => String(m.user_id) !== currentUserId && m.username.toLowerCase().includes(mentionQuery.toLowerCase()))
                .map((m, i) => (
                  <div key={m.user_id}
                    className={`chat-mention-item${i === mentionSelected ? ' active' : ''}`}
                    onMouseDown={(e) => { e.preventDefault(); insertMention(m); }}
                    onMouseEnter={() => setMentionSelected(i)}>
                    <Avatar src={m.avatar} name={m.username} size={24} />
                    <span className="chat-mention-name">{m.username}</span>
                    {isBotMember(m) && <span className="chat-mention-badge">BOT</span>}
                  </div>
                ))}
              {(members as ConvMember[]).filter(
                (m) => String(m.user_id) !== currentUserId && m.username.toLowerCase().includes(mentionQuery.toLowerCase())
              ).length === 0 && (
                <div className="chat-mention-empty">无匹配成员</div>
              )}
            </div>
          )}
          <textarea
            ref={chatInputRef}
            className="chat-input"
            placeholder={uploading ? '上传中...' : sendMutation.isPending ? '发送中...' : '输入消息...'}
            value={input}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            rows={1}
          />
          <button className="chat-send-btn" onClick={handleSend} disabled={!input.trim() || sendMutation.isPending}>
            {sendMutation.isPending ? '...' : '发送'}
          </button>
        </div>
      </div>
        </div>
        <SummaryPanel convId={id!} open={summaryOpen} onClose={() => setSummaryOpen(false)} />
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
            <div className="chat-msg-action-item" onClick={() => { setReplyTo({ msg_id: msgActions.msg.message_id, preview: extractTextPreview(msgActions.msg.content) }); setMsgActions(null); }}>
              回复
            </div>
            <div className="chat-msg-action-item" onClick={() => {
              const name = userMap.get(String(msgActions.msg.from_user_id))?.username;
              if (name) {
                setInput((prev) => prev ? `${prev} @${name} ` : `@${name} `);
                chatInputRef.current?.focus();
              }
              setMsgActions(null);
            }}>
              @提及
            </div>
            <div className="chat-msg-action-item" onClick={() => { setForwardMsgId(msgActions.msg.message_id); setMsgActions(null); }}>
              转发
            </div>
            {(msgActions.msg.type === 1 || msgActions.msg.type === 9) && (
              <>
                <div className="chat-msg-action-item" onClick={async () => {
                  const msgId = msgActions.msg.message_id;
                  const convId = id;
                  setMsgActions(null);
                  try {
                    const resp: any = await convToolApi.replyCandidates(convId as any, msgId);
                    if (resp?.status === 'processing') {
                      message.info('正在生成回复建议...');
                      // Result will arrive via WS conv.reply_candidates.done
                    } else {
                      setReplyCandidates(resp?.candidates || []);
                    }
                  } catch {
                    message.error('生成回复失败');
                  }
                }}>
                  💬 生成回复
                </div>
                <div className="chat-msg-action-item" onClick={async () => {
                  const text = extractTextPreview(msgActions.msg.content);
                  const msgId = msgActions.msg.message_id;
                  setMsgActions(null);
                  if (!text) { message.info('无法翻译此消息'); return; }
                  try {
                    const userLang = (useAuthStore.getState().user as any)?.settings?.language || 'zh-CN';
                    const resp: any = await convToolApi.translate(msgId, text, userLang);
                    if (resp?.status === 'processing') {
                      message.info('正在翻译...');
                      // Result will arrive via WS conv.translate.done
                    } else {
                      setTranslateMap((prev: Record<string, string>) => ({ ...prev, [msgId]: resp.translated_text }));
                    }
                  } catch {
                    message.error('翻译失败');
                  }
                }}>
                  🌐 翻译
                </div>
              </>
            )}
            {String(msgActions.msg.from_user_id) === currentUserId && msgActions.msg.type === 1 && (
              <div className="chat-msg-action-item" onClick={() => { setEditingMsgId(msgActions.msg.message_id); setEditText(extractTextPreview(msgActions.msg.content)); setMsgActions(null); }}>
                编辑
              </div>
            )}
            {String(msgActions.msg.from_user_id) === currentUserId && msgActions.msg.status === 1 && (
              <div className="chat-msg-action-item" onClick={() => {
                const msgId = msgActions.msg.message_id;
                setMsgActions(null);
                msgApi.recall(msgId)
                  .then(() => {})
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
          </div>
        </>
      )}

      {/* Group Info Drawer */}
      {conv?.type === 'group' && (
        <GroupInfoDrawer
          conv={conv}
          members={members}
          isOwner={isOwner}
          isAdmin={isAdmin}
          currentUserId={strUserId}
          numericUserId={numericUserId}
          open={groupInfoOpen}
          onClose={() => setGroupInfoOpen(false)}
          onMembersChange={() => { refetchMembers(); queryClient.invalidateQueries({ queryKey: ['conversation', id] }); queryClient.invalidateQueries({ queryKey: ['conversations'] }); }}
        />
      )}

      {/* Forward Message Modal */}
      {forwardMsgId && (
        <AntModal
          title="转发消息"
          open={!!forwardMsgId}
          onCancel={() => setForwardMsgId(null)}
          footer={null}
        >
          <Input.Search
            placeholder="搜索会话..."
            onChange={(e) => {
              // filter handled by existing conv list data
            }}
          />
          <div style={{ marginTop: 8, maxHeight: 300, overflow: 'auto' }}>
            {(convListData?.list || []).map((conv: any) => (
              <div
                key={conv.id}
                className="chat-forward-conv-item"
                onClick={() => {
                  msgApi.forward([forwardMsgId] as any, conv.id as any)
                    .then(() => { message.success('已转发'); setForwardMsgId(null); })
                    .catch(() => message.error('转发失败'));
                }}
              >
                <Avatar name={convTitle(conv)} size={28} />
                <span className="chat-forward-conv-name">{convTitle(conv)}</span>
              </div>
            ))}
          </div>
        </AntModal>
      )}

      {/* File Preview Modal */}
      <FilePreviewModal file={filePreview} onClose={() => setFilePreview(null)} />
    </>
    );
  }
