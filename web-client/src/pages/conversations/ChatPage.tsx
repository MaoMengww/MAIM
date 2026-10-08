import { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { Link, useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { message, Button, Input, Tag, Select, Modal as AntModal, Image, Spin } from 'antd';
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
import { botApi } from '@/services/bot';
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
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { Message, MsgContentOneof, ConvMember, AudioContent, ImageContent, VideoContent, FileContent, KnowledgeSource, BotRun, RunSource } from '@/types/model';
import type { SendMsgContent, SearchMessagesReq } from '@/types/api';
import { sequence, knowledgeSources } from '@/utils/json';

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
        <Link to={`/knowledge/${source.kb_id}/documents/${source.doc_id}?chunk=${source.chunk_id}`} aria-label={`查看来源文档：${source.title || source.doc_id}`}>
          查看文档
        </Link>
      </div>
      {expanded && (
        <div className="chat-source-content">
          <div className="chat-source-identity">文档：{source.doc_id}<br />切片：{source.chunk_id}</div>
          {source.content}
        </div>
      )}
    </div>
  );
}

const RUN_STATUS: Record<BotRun['status'], { label: string; color: string }> = {
  accepted: { label: '已接受', color: 'default' }, running: { label: '运行中', color: 'processing' },
  succeeded: { label: '成功', color: 'success' }, failed: { label: '失败', color: 'error' },
};
const RUN_STAGE: Record<string, string> = {
  accepted: '接受请求', preparing: '准备获准上下文', model: '模型生成', tool: '工具执行',
  generated: '结果已生成', pending: '等待提交消息', committed: '消息已提交', failed: '执行失败',
};
const RUN_OUTPUT: Record<BotRun['output_status'], string> = {
  none: '尚无结果', generated: '结果已生成 · 尚未发送', pending: '结果待提交 · 尚未发送',
  committed: '最终消息已提交', failed: '输出提交失败 · 未发送',
};
const RUN_SOURCE_LABEL: Record<string, string> = { request_source: '发起请求', reference: '明确引用', history: '获准历史' };
const SOURCE_SENDER_LABEL: Record<string, string> = { user: '用户', bot: 'Bot', system: '系统' };

function RunCard({ run, botName, requesterName, onLocate, onRefresh }: {
  run: BotRun; botName: string; requesterName: string;
  onLocate: (messageId: string) => Promise<void>; onRefresh: (runId: string) => Promise<void>;
}) {
  const [refreshing, setRefreshing] = useState(false);
  const state = RUN_STATUS[run.status];
  return <article className="chat-run-card" data-run-id={run.id} data-run-version={run.version} data-run-status={run.status} data-output-status={run.output_status} aria-label={`${botName} 执行：${state.label}`}>
    <div className="chat-run-header">
      <strong>{botName} · 执行</strong>
      <Tag color={state.color}>{state.label}</Tag>
      <span className="chat-run-updated">{formatTime(run.updated_at)}</span>
      <Button size="small" loading={refreshing} onClick={async () => {
        setRefreshing(true);
        try { await onRefresh(run.id); }
        catch { message.error('执行状态查询失败'); }
        finally { setRefreshing(false); }
      }}>查询状态</Button>
    </div>
    <div className="chat-run-meta">
      <span title={run.requester_id}>请求者：{requesterName}</span>
      {run.source_message_id
        ? <Button type="link" size="small" onClick={() => void onLocate(run.source_message_id!).catch(() => message.error('来源消息无法读取'))}>查看输入来源消息</Button>
        : <span>输入来源：明确操作</span>}
    </div>
    <div className="chat-run-stage" role="status">当前阶段：{RUN_STAGE[run.stage] || run.stage} · {RUN_OUTPUT[run.output_status]}</div>
    {run.error && <div className="chat-run-error" role="alert">{run.error}</div>}
    {run.result_text && <details className="chat-run-result" open={run.output_status !== 'committed'}>
      <summary>执行结果{run.output_status === 'committed' ? '（已进入消息历史）' : '（尚未发送）'}</summary>
      <div className="chat-msg-bubble"><ChatMarkdown>{run.result_text}</ChatMarkdown></div>
    </details>}
    <SourceSection sources={run.sources.filter((source) => source.type === 'rag')} />
    <ToolUsage tools={run.sources.filter((source) => source.type === 'tool').map((source) => source.tool_name)} />
    {run.sources.some((source) => source.type === 'request_source' || source.type === 'reference' || source.type === 'history') && <details className="chat-run-details">
      <summary>请求与引用来源</summary>
      <ul className="chat-run-steps">{run.sources.filter((source): source is Extract<RunSource, { type: 'request_source' | 'reference' | 'history' }> => source.type === 'request_source' || source.type === 'reference' || source.type === 'history').map((source) => <li key={`${source.type}:${source.message_id}`}>
        <Button type="link" size="small" onClick={() => void onLocate(source.message_id).catch(() => message.error('引用来源消息无法读取'))}>{RUN_SOURCE_LABEL[source.type]}：查看消息</Button>
        <span title={source.sender_id}>{SOURCE_SENDER_LABEL[source.sender_type]}{source.sender_id ? ` · ${source.sender_id}` : ''}</span>
      </li>)}</ul>
    </details>}
    {run.output_status === 'committed' && run.message_id && <Button type="link" size="small" onClick={() => void onLocate(run.message_id!).catch(() => message.error('最终消息无法读取'))}>查看最终消息</Button>}
    <details className="chat-run-details">
      <summary>关键阶段与执行标识 · v{run.version}</summary>
      <ol className="chat-run-steps">{run.steps.map((step) => <li key={step.version}>{RUN_STAGE[step.stage] || step.stage} <span>v{step.version} · {formatTime(step.created_at)}</span></li>)}</ol>
      <dl className="chat-run-identities">
        <dt>Run ID</dt><dd>{run.id}</dd><dt>交互 ID</dt><dd>{run.interaction_id}</dd>
        <dt>输入版本</dt><dd>{run.input_version}</dd><dt>绑定 ID</dt><dd>{run.binding_id}</dd>
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
  useEffect(() => { setRunTarget(undefined); }, [id, authRevision]);
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
    if (!id || !runTarget || !input.trim() || newSendLock.current || runRetryLock.current) return;
    const account = currentUserId;
    const text = input;
    newSendLock.current = true;
    setCreatingRun(true);
    try {
      const pending = await createPendingRun(account, runTarget, { conv_id: id, message: text, ...(replyTo ? { reply_to_msg_id: replyTo.msg_id } : {}) });
      if (pageAccount.current !== account || authRevision !== useAuthStore.getState().revision) return;
      await reloadRunSubmissions();
      if (pageConversation.current === id) {
        setInput((current) => current === text ? '' : current);
        setReplyTo(null);
      }
      await retryRunSubmission(pending);
    } catch { if (pageAccount.current === account && authRevision === useAuthStore.getState().revision) message.error('无法保存请求状态，执行未提交'); }
    finally { newSendLock.current = false; setCreatingRun(false); }
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

  const { data: members = [], refetch: refetchMembers } = useQuery({
    queryKey: ['conv-members', id],
    queryFn: () => convApi.getMembers(id!),
    enabled: !!id,
  });

  const { data: convBots = [] } = useQuery({
    queryKey: ['conv-bots', id], queryFn: () => botApi.listConvBots(id!), enabled: !!id,
  });
  const privateBot = conv?.type === 'private' ? convBots[0] : undefined;
  const builtinBots = convBots.filter((bot) => bot.type !== 'third_party');

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
  const isOwner = conv?.owner_id === currentUserId;
  const isAdmin = isOwner || currentMember?.role === 'MEMBER_ROLE_ADMIN' || currentMember?.role === 2;

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

  // ─── Image selection ───
  function handleImagePick() {
    imageInputRef.current?.click();
  }

  async function handleImageChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = '';
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
  }, []);

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
                    {renderReadStatus(conv, msg, isSelf, readStatuses)}
                  </div>
              </div>
            </div>
          );
        })}

          <section className="chat-run-list" aria-label="Bot 执行记录">
            {(builtinBots.length > 0 || runQuery.runs.length > 0 || runQuery.isError) && <div className="chat-run-list-header">
              <strong>Bot 执行记录</strong>
              <Button size="small" loading={runQuery.isFetching} onClick={() => void runQuery.refetch()}>刷新执行</Button>
            </div>}
            {runQuery.isPending && builtinBots.length > 0 && <div role="status">正在读取执行记录…</div>}
            {runQuery.connection !== 'connected' && runQuery.runs.length > 0 && <div className="chat-run-notice" role="status">实时连接已断开；执行不因此结束，查询将恢复最新状态。</div>}
            {runQuery.isError && <div className="chat-run-error" role="alert">执行记录读取失败，请刷新重试。</div>}
            {!runQuery.isError && !runQuery.isPending && builtinBots.length > 0 && runQuery.runs.length === 0 && <div className="chat-run-notice">尚无执行。选择 Bot 并明确发起工作，或在消息中提及它。</div>}
            {!runQuery.isError && runQuery.runs.map((run) => <RunCard key={run.id} run={run}
              botName={convBots.find((bot) => bot.bot_id === run.bot_id)?.name || `Bot ${run.bot_id}`}
              requesterName={userMap.get(run.requester_id)?.username || run.requester_id}
              onLocate={locateRunMessage} onRefresh={runQuery.refreshRun} />)}
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

        {builtinBots.length > 0 && <div className="chat-run-composer">
          <label htmlFor="chat-run-target">发送方式</label>
          <Select id="chat-run-target" aria-label="工作执行 Bot" value={runTarget} allowClear placeholder="普通消息"
            disabled={creatingRun || requestingKey !== null} onChange={(value) => setRunTarget(value)}
            options={builtinBots.map((bot) => ({ value: bot.bot_id, label: `交给 ${bot.name} 开展工作` }))} />
          <span>{runTarget ? '仅提交本次输入与明确引用，不自动读取群最近消息' : '选择 Bot 可发起独立执行'}</span>
        </div>}
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
            aria-label={runTarget ? '工作请求内容' : '消息内容'}
            placeholder={uploading ? '上传中...' : creatingRun ? '提交工作请求中...' : sendMutation.isPending ? '发送中...' : runTarget ? '描述本次工作；Enter 发起，Shift+Enter 换行' : '输入消息...'}
            value={input}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            rows={1}
          />
          <button className="chat-send-btn" onClick={runTarget ? () => void handleStartRun() : handleSend} disabled={!input.trim() || sendMutation.isPending || creatingRun || requestingKey !== null}>
            {creatingRun || requestingKey !== null ? '提交中…' : runTarget ? '发起工作' : sendMutation.isPending ? '...' : '发送'}
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
            <div className="chat-msg-action-item" onClick={() => { setReplyTo({ msg_id: msgActions.msg.message_id, preview: extractTextPreview(msgActions.msg.content) }); setMsgActions(null); }}>
              回复
            </div>
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
          onMembersChange={() => { refetchMembers(); queryClient.invalidateQueries({ queryKey: ['conversation', id] }); queryClient.invalidateQueries({ queryKey: ['conversations'] }); }}
        />
      )}



      {/* File Preview Modal */}
      {filePreview && <FilePreviewModal key={filePreview.file_id} file={filePreview} onClose={() => setFilePreview(null)} />}
    </>
    );
  }
