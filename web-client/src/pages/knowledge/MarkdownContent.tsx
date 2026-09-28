import ReactMarkdown from 'react-markdown';
import rehypeRaw from 'rehype-raw';
import remarkGfm from 'remark-gfm';

interface MarkdownContentProps {
  content: string;
}

export function MarkdownContent({ content }: MarkdownContentProps) {
  return (
    <div className="md-content">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeRaw]}
        components={{
          a: ({ href, children }) => {
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
        {content}
      </ReactMarkdown>
    </div>
  );
}
