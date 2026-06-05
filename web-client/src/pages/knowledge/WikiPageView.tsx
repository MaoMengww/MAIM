import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Spin, Tag, message, Modal, Breadcrumb } from 'antd';
import { kbApi } from '@/services/knowledge';
import { PAGE_TYPE_LABEL } from './KBListPage';
import { WikiMarkdown } from './WikiMarkdown';
import { WikiPageEdit } from './WikiPageEdit';
import { EditIcon } from '@/components/common/Icons';

const PAGE_COLORS: Record<string, string> = {
  summary: 'blue', entity: 'green', concept: 'purple',
  synthesis: 'orange', comparison: 'cyan',
  index: 'default', log: 'default',
};

export function WikiPageView() {
  const params = useParams();
  const kbId = params.kbId;
  const rawSlug = params['*'] || '';
  const isEdit = rawSlug.endsWith('/edit');
  const slug = isEdit ? rawSlug.slice(0, -5) : rawSlug;
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  // If URL ends with /edit, render the edit page component
  if (isEdit) {
    return <WikiPageEdit />;
  }

  const { data: page, isLoading } = useQuery({
    queryKey: ['wiki-page', kbId, slug],
    queryFn: () => kbApi.wikiReadPage(kbId!, slug!).catch((err: any) => {
      if (err?.response?.status === 404) return null;
      throw err;
    }),
    enabled: !!kbId && !!slug,
  });

  const { data: pageList } = useQuery({
    queryKey: ['wiki-pages', kbId],
    queryFn: () => kbApi.wikiListPages(kbId!),
    enabled: !!kbId,
  });

  const deleteMutation = useMutation({
    mutationFn: () => kbApi.wikiDeletePage(kbId!, slug!),
    onSuccess: () => {
      message.success('页面已删除');
      queryClient.setQueryData(['wiki-pages', kbId], (old: any) => {
        if (!old?.list) return old;
        return { ...old, list: old.list.filter((p: any) => p.slug !== slug) };
      });
      queryClient.invalidateQueries({ queryKey: ['wiki-pages', kbId] });
      navigate(`/knowledge/${kbId}`);
    },
  });

  const handleDelete = () => {
    Modal.confirm({
      title: '确认删除',
      content: `删除页面「${page?.title}」？`,
      onOk: () => deleteMutation.mutate(),
      okText: '删除', cancelText: '取消',
      okButtonProps: { danger: true },
    });
  };

  const pages = pageList?.list ?? [];
  const currentIndex = pages.findIndex((p: any) => p.slug === slug);
  const prevPage = currentIndex > 0 ? pages[currentIndex - 1] : null;
  const nextPage = currentIndex < pages.length - 1 ? pages[currentIndex + 1] : null;

  if (isLoading) {
    return <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}><Spin /></div>;
  }
  if (!page) {
    return <div style={{ textAlign: 'center', padding: 60, color: 'var(--aim-text-tertiary)' }}>页面不存在</div>;
  }

  // Log page: render as timeline
  if (page.page_type === 'log') {
    return <LogViewer page={page} kbId={kbId!} navigate={navigate} />;
  }

  return (
    <div style={{ padding: 32, width: '100%', overflowY: 'auto' }}>
      <Breadcrumb style={{ marginBottom: 16 }} items={[
        { title: <a onClick={() => navigate('/knowledge')}>知识库</a> },
        { title: <a onClick={() => navigate(`/knowledge/${kbId}`)}>返回</a> },
        { title: page.slug },
      ]} />

      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 8 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: 24, fontWeight: 600 }}>{page.title}</h1>
          <div style={{ marginTop: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Tag color={PAGE_COLORS[page.page_type] || 'default'}>
              {PAGE_TYPE_LABEL[page.page_type] || page.page_type}
            </Tag>
            <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
              v{page.version} | 更新于 {new Date(page.updated_at * 1000).toLocaleString('zh-CN')}
            </span>
          </div>
        </div>
        <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
          <Button onClick={() => navigate(`/knowledge/${kbId}/wiki/${slug}/edit`)}>
            <EditIcon size={14} style={{ marginRight: 4 }} />
            编辑
          </Button>
          <Button danger onClick={handleDelete}>删除</Button>
        </div>
      </div>

      <div style={{ marginTop: 8, display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        {page.aliases?.length > 0 && page.aliases.map((a: string) => (
          <Tag key={a} style={{ borderRadius: 4 }}>{a}</Tag>
        ))}
      </div>

      {page.summary && (
        <div style={{ marginTop: 16, padding: 12, background: 'var(--aim-surface)', borderRadius: 8, color: 'var(--aim-text-secondary)', fontSize: 14 }}>
          {page.summary}
        </div>
      )}

      <div style={{ marginTop: 24 }}>
        <WikiMarkdown content={page.content} pages={pages} kbId={kbId!} sourceRefs={page.source_refs} onNavigate={(slug) => navigate(`/knowledge/${kbId}/wiki/${slug}`)} />
      </div>

      {/* Source documents */}
      {page.source_refs && page.source_refs.length > 0 && (
        <div style={{ marginTop: 32, padding: 16, background: 'var(--aim-surface)', borderRadius: 8 }}>
          <div style={{ fontSize: 13, color: 'var(--aim-text-tertiary)', marginBottom: 8 }}>来源文档</div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {page.source_refs.map((ref: any, idx: number) => (
              <Tag key={idx} color="blue" style={{ cursor: 'pointer', margin: 0 }}
                onClick={() => navigate(`/knowledge/${kbId}/wiki/documents/${ref.doc_id}`)}>
                {ref.title || `文档 ${ref.doc_id}`}
              </Tag>
            ))}
          </div>
        </div>
      )}

      {/* Cross references */}
      <div style={{ marginTop: 32, padding: 16, background: 'var(--aim-surface)', borderRadius: 8 }}>
        <div style={{ fontSize: 13, color: 'var(--aim-text-tertiary)', marginBottom: 8 }}>引用关系</div>
        <div style={{ display: 'flex', gap: 16, fontSize: 13 }}>
          <div>
            <span style={{ color: 'var(--aim-text-secondary)' }}>引用:</span>
            <div style={{ marginTop: 4 }}>
              {page.out_links?.length > 0 ? page.out_links.map((link: string) => {
                const found = pages.find((p: any) => p.slug === link);
                return <Tag key={link} style={{ cursor: 'pointer', marginBottom: 4 }} onClick={() => navigate(`/knowledge/${kbId}/wiki/${link}`)}>{found?.title || link}</Tag>;
              }) : <span style={{ color: 'var(--aim-text-tertiary)' }}>无</span>}
            </div>
          </div>
          <div>
            <span style={{ color: 'var(--aim-text-secondary)' }}>被引用:</span>
            <div style={{ marginTop: 4 }}>
              {page.in_links?.length > 0 ? page.in_links.map((link: string) => {
                const found = pages.find((p: any) => p.slug === link);
                return <Tag key={link} style={{ cursor: 'pointer', marginBottom: 4 }} onClick={() => navigate(`/knowledge/${kbId}/wiki/${link}`)}>{found?.title || link}</Tag>;
              }) : <span style={{ color: 'var(--aim-text-tertiary)' }}>无</span>}
            </div>
          </div>
        </div>
      </div>

      {/* Prev/Next navigation */}
      <div style={{ marginTop: 24, display: 'flex', justifyContent: 'space-between' }}>
        {prevPage ? (
          <Button type="link" onClick={() => navigate(`/knowledge/${kbId}/wiki/${prevPage.slug}`)}>
            ← {prevPage.title}
          </Button>
        ) : <div />}
        {nextPage ? (
          <Button type="link" onClick={() => navigate(`/knowledge/${kbId}/wiki/${nextPage.slug}`)}>
            {nextPage.title} →
          </Button>
        ) : <div />}
      </div>
    </div>
  );
}

/* ─── Log Timeline ─── */

interface LogEntry {
  time: string;
  action: string;
  detail: string;
}

function parseLogContent(content: string): LogEntry[] {
  const entries: LogEntry[] = [];
  const regex = /### (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\n\n\*\*([^*]+)\*\*: ([^\n]+)/g;
  let match;
  while ((match = regex.exec(content)) !== null) {
    entries.push({ time: match[1], action: match[2], detail: match[3] });
  }
  return entries;
}

function LogViewer({ page, kbId, navigate }: { page: any; kbId: number | string; navigate: any }) {
  const entries = parseLogContent(page.content || '');

  const actionColors: Record<string, string> = {
    '文档导入': 'blue',
    '页面编辑': 'orange',
    '页面删除': 'red',
    '自动维护': 'purple',
    'Agent 采纳': 'cyan',
  };

  return (
    <div style={{ padding: 32, width: '100%', overflowY: 'auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
        <Button onClick={() => navigate(`/knowledge/${kbId}`)} size="small">← 返回</Button>
        <h1 style={{ margin: 0, fontSize: 24, fontWeight: 600 }}>{page.title}</h1>
        <Tag color="default" style={{ marginLeft: 8 }}>v{page.version}</Tag>
      </div>

      {entries.length === 0 ? (
        <div style={{ textAlign: 'center', padding: 60, color: 'var(--aim-text-tertiary)' }}>
          暂无变更日志
        </div>
      ) : (
        <div style={{ position: 'relative', paddingLeft: 32 }}>
          {/* Timeline line */}
          <div style={{ position: 'absolute', left: 14, top: 8, bottom: 8, width: 2, background: 'var(--aim-border)' }} />

          {entries.map((entry, i) => (
            <div key={i} style={{ position: 'relative', marginBottom: 20, paddingLeft: 20 }}>
              {/* Timeline dot */}
              <div style={{
                position: 'absolute', left: -26, top: 4, width: 10, height: 10, borderRadius: '50%',
                background: actionColors[entry.action] || '#1677ff', border: '2px solid var(--aim-surface)',
              }} />

              {/* Time */}
              <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', marginBottom: 4 }}>
                {entry.time}
              </div>

              {/* Card */}
              <div style={{
                background: 'var(--aim-surface)', border: '1px solid var(--aim-border)',
                borderRadius: 8, padding: '12px 16px',
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                  <Tag color={actionColors[entry.action] || 'default'} style={{ margin: 0 }}>
                    {entry.action}
                  </Tag>
                </div>
                <div style={{ fontSize: 13, color: 'var(--aim-text)', lineHeight: 1.6 }}>
                  {entry.detail}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
