import ReactMarkdown from 'react-markdown';
import rehypeRaw from 'rehype-raw';
import remarkGfm from 'remark-gfm';
import type { WikiPageItem, WikiSourceRef } from '@/types/model';

interface WikiMarkdownProps {
  content: string;
  pages: WikiPageItem[];
  kbId?: number | string;
  sourceRefs?: WikiSourceRef[];
  onNavigate: (slug: string) => void;
}

export function WikiMarkdown({ content, pages, onNavigate, kbId, sourceRefs }: WikiMarkdownProps) {
  // Build doc_id → title lookup
  const docMap = new Map((sourceRefs ?? []).map(ref => [ref.doc_id, ref.title]));

  // Pre-process [doc:数字] → markdown link with document title
  let processed = (content ?? '').replace(/\[doc:(\d+)\]/g, (_m: string, docId: string) => {
    const id = parseInt(docId, 10);
    const title = docMap.get(id) || `文档 ${id}`;
    const url = kbId ? `/knowledge/${kbId}/wiki/documents/${id}` : '#';
    return `[${title}](${url})`;
  });

  // Pre-process [[slug]]: resolved → markdown link with real URL, unresolved → gray text
  processed = processed.replace(/\[\[([^\[\]]+)\]\]/g, (_m: string, slug: string) => {
    const found = pages.find(p => p.slug === slug);
    if (found) {
      const url = kbId ? `/knowledge/${kbId}/wiki/${slug}` : '#';
      return `[${found.title}](${url})`;
    }
    return `<span style="color:var(--aim-text-tertiary)">[[${slug}]]</span>`;
  });

  return (
    <div className="wiki-content">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeRaw]}
        components={{
          a: ({ href, children }) => {
            // Internal wiki link — render as span with onClick, matching footer pattern
            if (href?.startsWith('/knowledge/')) {
              const slug = href.split('/wiki/').pop() || '';
              return (
                <span
                  onClick={() => onNavigate(slug)}
                  role="link"
                  tabIndex={0}
                  onKeyDown={(e) => { if (e.key === 'Enter') onNavigate(slug); }}
                  style={{
                    color: '#e8b830',
                    textDecoration: 'underline',
                    textUnderlineOffset: '2px',
                    cursor: 'pointer',
                  }}
                >
                  {children}
                </span>
              );
            }
            // External link (target=_blank kept for actual external URLs only)
            if (href?.startsWith('http://') || href?.startsWith('https://')) {
              return (
                <a href={href} target="_blank" rel="noopener noreferrer">
                  {children}
                </a>
              );
            }
            // Other links (relative, anchor, mailto, etc.) — same window
            return <a href={href}>{children}</a>;
          },
          img: ({ src, alt }) => (
            <img
              src={src}
              alt={alt ?? ''}
              style={{ maxWidth: '100%', height: 'auto', borderRadius: 4 }}
            />
          ),
          table: ({ children }) => (
            <div style={{ overflowX: 'auto' }}>
              <table>{children}</table>
            </div>
          ),
          figure: ({ children }) => (
            <figure>{children}</figure>
          ),
          figcaption: ({ children }) => (
            <figcaption>{children}</figcaption>
          ),
        }}
      >
        {processed}
      </ReactMarkdown>
    </div>
  );
}
