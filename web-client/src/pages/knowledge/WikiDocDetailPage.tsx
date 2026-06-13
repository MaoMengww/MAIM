import { useParams, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Button, Spin, Descriptions, Tag } from 'antd';
import { ArrowLeftOutlined, FileTextOutlined } from '@ant-design/icons';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { kbApi } from '@/services/knowledge';

const STATUS_COLOR: Record<string, string> = {
  ready: 'green', failed: 'red', pending: 'gold',
  parsing: 'blue', chunking: 'blue', embedding: 'blue',
};

export function WikiDocDetailPage() {
  const { kbId, docId } = useParams<{ kbId: string; docId: string }>();
  const navigate = useNavigate();

  const { data: doc, isLoading } = useQuery({
    queryKey: ['kb-doc', docId],
    queryFn: () => kbApi.getDocument(docId as any),
    enabled: !!docId,
  });

  const { data: contentData, isLoading: contentLoading } = useQuery({
    queryKey: ['kb-doc-content', docId],
    queryFn: () => kbApi.getDocumentContent(docId as any),
    enabled: !!docId,
  });

  if (isLoading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}>
        <Spin />
      </div>
    );
  }

  if (!doc) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', gap: 12 }}>
        <p style={{ color: 'var(--aim-text-tertiary)' }}>文档不存在</p>
        <Button onClick={() => navigate(`/knowledge/${kbId}`)}>返回</Button>
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 800, margin: '0 auto', padding: 32, width: '100%', overflowY: 'auto' }}>
      <Button type="text" onClick={() => navigate(`/knowledge/${kbId}`)} style={{ marginBottom: 20, color: 'var(--aim-text-secondary)' }}>
        <ArrowLeftOutlined /> 返回知识库
      </Button>

      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
        <span style={{
          width: 44, height: 44, borderRadius: 8, display: 'inline-flex',
          alignItems: 'center', justifyContent: 'center', fontSize: 13, fontWeight: 700,
          flexShrink: 0, background: 'rgba(22,119,255,0.06)', border: '1px solid rgba(22,119,255,0.12)', color: '#1677ff',
        }}>
          {(doc.file_type || 'txt').toUpperCase().slice(0, 4)}
        </span>
        <div>
          <h1 style={{ margin: 0, fontSize: 20, fontWeight: 600, color: 'var(--aim-text)' }}>
            {doc.original_filename || doc.title || `文档 ${doc.id}`}
          </h1>
          <div style={{ marginTop: 4, fontSize: 13, color: 'var(--aim-text-tertiary)' }}>
            <Tag color="blue" style={{ marginRight: 6 }}>WIKI 源文档</Tag>
            {doc.file_size ? `${(doc.file_size / 1024).toFixed(1)} KB` : ''}
          </div>
        </div>
      </div>

      <Descriptions
        column={2}
        bordered
        size="small"
        style={{ marginBottom: 24 }}
        styles={{
          label: { color: 'var(--aim-text-secondary)', background: 'var(--aim-surface)' },
          content: { color: 'var(--aim-text)' },
        }}
      >
        <Descriptions.Item label="状态">
          <Tag color={STATUS_COLOR[doc.status] || 'default'}>{doc.status}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="文件类型">{doc.file_type?.toUpperCase() || '-'}</Descriptions.Item>
        <Descriptions.Item label="文件大小">
          {doc.file_size ? `${(doc.file_size / 1024).toFixed(1)} KB` : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="上传时间">
          {doc.created_at ? new Date(doc.created_at * 1000).toLocaleString('zh-CN') : '-'}
        </Descriptions.Item>
        {doc.error_message && (
          <Descriptions.Item label="错误信息" span={2}>
            <span style={{ color: 'var(--aim-error)' }}>{doc.error_message}</span>
          </Descriptions.Item>
        )}
      </Descriptions>

      {/* Content — always render if uploaded successfully */}
      <div style={{ marginTop: 32 }}>
        <h3 style={{ marginBottom: 16, fontSize: 16, fontWeight: 600, color: 'var(--aim-text)', display: 'flex', alignItems: 'center', gap: 8 }}>
          <FileTextOutlined /> 原文内容
        </h3>

        {contentLoading ? (
          <div style={{ textAlign: 'center', padding: 60 }}>
            <Spin />
          </div>
        ) : contentData?.content ? (
          <div style={{
            background: 'var(--aim-surface)',
            border: '1px solid var(--aim-border)',
            borderRadius: 8,
            padding: '20px 24px',
            lineHeight: 1.8,
            fontSize: 14,
            color: 'var(--aim-text)',
          }}>
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              components={{
                img: ({ src, alt }) => (
                  <img
                    src={src}
                    alt={alt ?? ''}
                    style={{ maxWidth: '100%', height: 'auto', borderRadius: 4 }}
                  />
                ),
              }}
            >
              {contentData.content}
            </ReactMarkdown>
          </div>
        ) : (
          <div style={{ textAlign: 'center', padding: 60, color: 'var(--aim-text-tertiary)' }}>
            文档内容为空
          </div>
        )}
      </div>
    </div>
  );
}
