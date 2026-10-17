import { useState, useEffect } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Spin, Descriptions, Table, Tag, Modal, message } from 'antd';
import { kbApi } from '@/services/knowledge';
import { wsOn } from '@/services/ws';
import type { ChunkInfo } from '@/types/model';
import { MarkdownContent } from './MarkdownContent';
import { entityId } from '@/utils/json';

export function DocDetailPage() {
  const { kbId, docId } = useParams<{ kbId: string; docId: string }>();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const sourceChunkID = searchParams.get('chunk');
  const [contentOpen, setContentOpen] = useState(false);
  const [contentData, setContentData] = useState<{ content: string; download_url: string } | null>(null);
  const [contentLoading, setContentLoading] = useState(false);
  const [chunkOffsets, setChunkOffsets] = useState([0]);
  const chunkOffset = chunkOffsets[chunkOffsets.length - 1] ?? 0;

  useEffect(() => {
    setChunkOffsets([0]);
  }, [docId]);

  const queryClient = useQueryClient();

  useEffect(() => {
    const evts = ['knowledge.parsing', 'knowledge.chunking', 'knowledge.embedding', 'knowledge.ready', 'knowledge.failed'];
    const unsubs = evts.map((t) => wsOn(t, (payload: { kb_id: string; doc_id: string }) => {
      if (payload.kb_id !== kbId || payload.doc_id !== docId) return;
      queryClient.invalidateQueries({ queryKey: ['kb-doc', docId] });
      queryClient.invalidateQueries({ queryKey: ['kb-doc-chunks', docId] });
      queryClient.invalidateQueries({ queryKey: ['kb-source-chunk', docId] });
    }));
    return () => unsubs.forEach((fn) => fn());
  }, [kbId, docId, queryClient]);

  const { data: doc, isLoading } = useQuery({
    queryKey: ['kb-doc', docId],
    queryFn: () => kbApi.getDocument(docId!),
    enabled: !!docId,
  });

  const { data: chunksData, isLoading: chunksLoading, isError: chunksError } = useQuery({
    queryKey: ['kb-doc-chunks', docId, chunkOffset],
    queryFn: () => kbApi.listChunks(docId!, { offset: chunkOffset, limit: 20 }),
    enabled: !!docId && doc?.kb_id === kbId && doc?.status === 'ready',
  });

  const { data: sourceChunk, isLoading: sourceChunkLoading, isError: sourceChunkError } = useQuery({
    queryKey: ['kb-source-chunk', docId, sourceChunkID],
    enabled: !!sourceChunkID && !!docId && doc?.kb_id === kbId && doc?.status === 'ready',
    queryFn: async () => {
      const chunkID = entityId(sourceChunkID, 'chunk');
      let offset = 0;
      while (true) {
        const result = await kbApi.listChunks(docId!, { offset, limit: 100 });
        const chunk = result.list.find((item) => item.id === chunkID && item.doc_id === docId);
        if (chunk) return chunk;
        if (!result.has_more || result.next_offset <= offset) break;
        offset = result.next_offset;
      }
      return null;
    },
  });

  const handleDelete = () => {
    Modal.confirm({
      title: '确认删除',
      content: `确定删除文档「${doc?.original_filename || doc?.title}」吗？切片和向量数据将一并删除。`,
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: async () => {
        try {
          await kbApi.deleteDocument(docId!);
          message.success('文档已删除');
          queryClient.invalidateQueries({ queryKey: ['kb-docs'] });
          queryClient.invalidateQueries({ queryKey: ['kb', kbId] });
          navigate(`/knowledge/${kbId}`);
        } catch {
          message.error('删除失败');
        }
      },
    });
  };

  const handleViewContent = async () => {
    if (!docId) return;
    setContentLoading(true);
    try {
      const data = await kbApi.getDocumentContent(docId);
      setContentData(data);
      setContentOpen(true);
    } catch {
      Modal.error({ title: '获取失败', content: '无法读取文件内容' });
    } finally {
      setContentLoading(false);
    }
  };

  if (isLoading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}>
        <Spin />
      </div>
    );
  }

  if (!doc || doc.kb_id !== kbId) {
    return <div style={{ padding: 32, color: 'var(--aim-text-tertiary)' }}>文档不存在</div>;
  }

  const statusColor: Record<string, string> = { ready: 'green', failed: 'red', pending: 'gold', parsing: 'blue', chunking: 'blue', embedding: 'blue' };

  const chunkColumns = [
    { title: '#', dataIndex: 'chunk_index', key: 'index', width: 60 },
    { title: '内容', dataIndex: 'content', key: 'content', ellipsis: true },
    { title: 'Token', dataIndex: 'token_count', key: 'tokens', width: 80 },
  ];

  return (
    <div style={{ maxWidth: 960, margin: '0 auto', padding: 32, width: '100%', overflowY: 'auto' }}>
      <Button onClick={() => navigate(`/knowledge/${kbId}`)} style={{ marginBottom: 16 }}>← 返回</Button>

      <Descriptions
        title={doc.original_filename || doc.title}
        column={2}
        bordered
        size="small"
        style={{ marginBottom: 32 }}
        styles={{
          label: { color: 'var(--aim-text-secondary)', background: 'var(--aim-surface)' },
          content: { color: 'var(--aim-text)' },
        }}
        extra={
          <div style={{ display: 'flex', gap: 8 }}>
            <Button loading={contentLoading} onClick={handleViewContent}>查看原文</Button>
            <Button danger onClick={handleDelete}>删除文档</Button>
          </div>
        }
      >
        <Descriptions.Item label="类型">{doc.file_type}</Descriptions.Item>
        <Descriptions.Item label="大小">{doc.file_size ? `${(doc.file_size / 1024).toFixed(1)} KB` : '-'}</Descriptions.Item>
        <Descriptions.Item label="状态">
          <Tag color={statusColor[doc.status] || 'default'}>{doc.status}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="切片数">{doc.chunk_count ?? 0}</Descriptions.Item>
        {doc.error_message && (
          <Descriptions.Item label="错误信息" span={2}>
            <span style={{ color: 'var(--aim-error)' }}>{doc.error_message}</span>
          </Descriptions.Item>
        )}
      </Descriptions>

      {sourceChunkID && (
        <section aria-label="引用切片" style={{ marginBottom: 24 }}>
          <h3 style={{ color: 'var(--aim-text)', marginBottom: 8, fontSize: 16 }}>引用切片</h3>
          <div style={{ overflowWrap: 'anywhere', color: 'var(--aim-text-secondary)', marginBottom: 8 }}>{sourceChunkID}</div>
          {doc.status !== 'ready' ? (
            <Alert type="info" showIcon message="文档解析内容尚不可用，暂时无法读取引用切片。" />
          ) : sourceChunkLoading ? <Spin /> : sourceChunkError ? (
            <Alert type="error" showIcon message="无法读取引用切片" />
          ) : sourceChunk ? (
            <MarkdownContent content={sourceChunk.content} />
          ) : (
            <Alert type="info" showIcon message="引用切片已失效或删除，文档可能已重新处理。" />
          )}
        </section>
      )}

      <h3 style={{ color: 'var(--aim-text)', marginBottom: 12, fontSize: 16 }}>切片列表</h3>
      {doc.status !== 'ready' ? (
        <Alert type={doc.status === 'failed' ? 'error' : 'info'} showIcon message={doc.status === 'failed' ? '文档解析失败，暂无可读解析内容。' : '文档正在处理，解析内容就绪后会自动更新。'} />
      ) : chunksError ? (
        <Alert type="error" showIcon message="无法读取解析切片：文档可能没有可读正文，或读取服务暂不可用。" />
      ) : <>
      <Table
        dataSource={chunksData?.list ?? []}
        columns={chunkColumns}
        rowKey="id"
        loading={chunksLoading}
        pagination={false}
        size="small"
        expandable={{
          expandedRowRender: (record: ChunkInfo) => (
            <div style={{ maxHeight: 300, overflowY: 'auto', fontSize: 13, padding: '8px 0' }}>
              <MarkdownContent content={record.content} />
            </div>
          ),
          rowExpandable: () => true,
        }}
      />
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 16 }}>
        <Button disabled={chunkOffsets.length === 1 || chunksLoading} onClick={() => setChunkOffsets((offsets) => offsets.slice(0, -1))}>上一页</Button>
        <span>第 {chunkOffsets.length} 页，共 {chunksData?.total ?? 0} 个切片</span>
        <Button disabled={!chunksData?.has_more || chunksLoading} onClick={() => {
          if (chunksData && chunksData.next_offset > chunkOffset) {
            setChunkOffsets((offsets) => [...offsets, chunksData.next_offset]);
          }
        }}>下一页</Button>
      </div>
      </>}

      <Modal
        title="原文内容"
        open={contentOpen}
        onCancel={() => setContentOpen(false)}
        footer={contentData?.download_url ? (
          <Button type="primary" href={contentData.download_url} target="_blank" rel="noopener noreferrer">
            下载文件
          </Button>
        ) : null}
        width={800}
      >
        <div style={{ maxHeight: 500, overflowY: 'auto', fontSize: 13, padding: 16 }}>
          <MarkdownContent content={contentData?.content || ''} />
        </div>
      </Modal>
    </div>
  );
}
