import { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Spin, Descriptions, Table, Tag, Modal, message } from 'antd';
import { kbApi } from '@/services/knowledge';
import { wsOn } from '@/services/ws';
import type { ChunkInfo } from '@/types/model';
import { WikiMarkdown } from './WikiMarkdown';

export function DocDetailPage() {
  const { kbId, docId } = useParams<{ kbId: string; docId: string }>();
  const navigate = useNavigate();
  const [contentOpen, setContentOpen] = useState(false);
  const [contentData, setContentData] = useState<{ content: string; download_url: string } | null>(null);
  const [contentLoading, setContentLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);

  const queryClient = useQueryClient();

  useEffect(() => {
    const evts = ['knowledge.parsing', 'knowledge.chunking', 'knowledge.embedding', 'knowledge.ready', 'knowledge.failed'];
    const unsubs = evts.map((t) => wsOn(t, (payload: any) => {
      if (payload.kb_id && Number(payload.kb_id) !== Number(kbId)) return;
      if (payload.doc_id && Number(payload.doc_id) !== Number(docId)) return;
      queryClient.invalidateQueries({ queryKey: ['kb-doc', docId] });
    }));
    return () => unsubs.forEach((fn) => fn());
  }, [kbId, docId, queryClient]);

  const { data: doc, isLoading } = useQuery({
    queryKey: ['kb-doc', docId],
    queryFn: () => kbApi.getDocument(docId as any),
    enabled: !!docId,
  });

  const { data: chunksData, isLoading: chunksLoading } = useQuery({
    queryKey: ['kb-doc-chunks', docId, page, pageSize],
    queryFn: () => kbApi.listChunks(docId as any, { offset: (page - 1) * pageSize, limit: pageSize }),
    enabled: !!docId,
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
          await kbApi.deleteDocument(docId as any);
          message.success('文档已删除');
          queryClient.invalidateQueries({ queryKey: ['kb-docs'] });
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
      const data = await kbApi.getDocumentContent(docId as any);
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

  if (!doc) {
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

      <h3 style={{ color: 'var(--aim-text)', marginBottom: 12, fontSize: 16 }}>切片列表</h3>
      <Table
        dataSource={chunksData?.list ?? []}
        columns={chunkColumns}
        rowKey="id"
        loading={chunksLoading}
        pagination={{ current: page, pageSize, total: chunksData?.total ?? 0, showSizeChanger: false, onChange: (p, ps) => { setPage(p); setPageSize(ps); } }}
        size="small"
        expandable={{
          expandedRowRender: (record: ChunkInfo) => (
            <div style={{ maxHeight: 300, overflowY: 'auto', fontSize: 13, padding: '8px 0' }}>
              <WikiMarkdown content={record.content} pages={[]} kbId={kbId ? Number(kbId) : undefined} onNavigate={() => {}} />
            </div>
          ),
          rowExpandable: () => true,
        }}
      />

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
          <WikiMarkdown content={contentData?.content || ''} pages={[]} kbId={kbId ? Number(kbId) : undefined} onNavigate={() => {}} />
        </div>
      </Modal>
    </div>
  );
}
