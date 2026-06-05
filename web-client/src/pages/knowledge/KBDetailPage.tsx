import { useState, useCallback, useEffect, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Spin, Tag, Upload, message, Modal, Input, Select, Tabs, Form, InputNumber, Switch, notification } from 'antd';
import { UploadOutlined, ReloadOutlined, SearchOutlined, ApartmentOutlined, RobotOutlined, TeamOutlined, DeleteOutlined, EditOutlined, UnorderedListOutlined } from '@ant-design/icons';
import { kbApi } from '@/services/knowledge';
import { botApi } from '@/services/bot';
import { convApi } from '@/services/conversation';
import { modelApi } from '@/services/model';
import { wsOn } from '@/services/ws';
import type { DocumentRsp, WikiPageItem, WikiIssueItem } from '@/types/model';
import { WikiMarkdown } from './WikiMarkdown';
import { WikiMaintenanceConfig } from "./WikiMaintenanceConfig";
import { modelOptionLabel } from '@/utils/provider';

const STATUS_COLOR: Record<string, string> = {
  ready: 'green', failed: 'red', pending: 'gold',
  parsing: 'blue', chunking: 'blue', embedding: 'blue',
};

const PAGE_TYPE_ORDER = ['index', 'log', 'summary', 'entity', 'concept', 'synthesis', 'comparison'];
const PAGE_TYPE_LABEL: Record<string, string> = {
  index: '索引', log: '日志', summary: '摘要', entity: '实体',
  concept: '概念', synthesis: '综述', comparison: '对比',
};
const PAGE_COLORS: Record<string, string> = {
  summary: 'blue', entity: 'green', concept: 'purple',
  synthesis: 'orange', comparison: 'cyan', index: 'default', log: 'default',
};

/* ─── Bot Bindings Modal ─── */

function BotBindButton({ kbId }: { kbId: number }) {
  const [open, setOpen] = useState(false);
  const queryClient = useQueryClient();

  const { data: bindings, isLoading: bindingsLoading } = useQuery({
    queryKey: ['kb-bindings', kbId],
    queryFn: () => kbApi.listBindings('bot', kbId),
    enabled: open,
  });

  const { data: botsData } = useQuery({
    queryKey: ['bots'],
    queryFn: () => botApi.list(),
    enabled: open,
  });

  const bots = botsData?.list ?? [];
  const [selectedBotId, setSelectedBotId] = useState<number | undefined>();

  const bindMutation = useMutation({
    mutationFn: (botId: number) => kbApi.bindToBot(botId, kbId),
    onSuccess: () => {
      message.success('绑定成功');
      queryClient.invalidateQueries({ queryKey: ['kb-bindings', kbId] });
      setSelectedBotId(undefined);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '绑定失败'),
  });

  const unbindMutation = useMutation({
    mutationFn: (botId: number) => kbApi.unbindFromBot(botId, kbId),
    onSuccess: () => {
      message.success('已解绑');
      queryClient.invalidateQueries({ queryKey: ['kb-bindings', kbId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '解绑失败'),
  });

  const boundIds = new Set((bindings ?? []).map((b: any) => b.target_id));
  const availableBots = bots.filter((b: any) => !boundIds.has(b.id));

  return (
    <>
      <Button size="small" icon={<RobotOutlined />} onClick={() => setOpen(true)}>绑定 Bot</Button>
      <Modal title="绑定 Bot" open={open} onCancel={() => setOpen(false)} footer={null} width={480}>
        {bindingsLoading ? (
          <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div>
        ) : (
          <>
            {/* Bound bots */}
            <div style={{ marginBottom: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8, color: 'var(--aim-text-secondary)' }}>已绑定的 Bot</div>
              {bindings && bindings.length > 0 ? (
                bindings.map((b: any) => {
                  const bot = bots.find((bb: any) => bb.id === b.target_id);
                  return (
                    <div key={b.target_id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid var(--aim-border)' }}>
                      <span>{bot?.name || `Bot #${b.target_id}`}</span>
                      <Button size="small" danger loading={unbindMutation.isPending} onClick={() => unbindMutation.mutate(b.target_id)}>解绑</Button>
                    </div>
                  );
                })
              ) : (
                <div style={{ color: 'var(--aim-text-tertiary)', fontSize: 13 }}>暂未绑定任何 Bot</div>
              )}
            </div>

            {/* Bind new bot */}
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <Select
                style={{ flex: 1 }}
                placeholder="选择 Bot..."
                value={selectedBotId}
                onChange={setSelectedBotId}
                options={availableBots.map((b: any) => ({ value: b.id, label: b.name }))}
                showSearch
                filterOption={(input, option) => (option?.label ?? '').toLowerCase().includes(input.toLowerCase())}
              />
              <Button type="primary" onClick={() => bindMutation.mutate(selectedBotId!)} loading={bindMutation.isPending} disabled={!selectedBotId}>
                绑定
              </Button>
            </div>
          </>
        )}
      </Modal>
    </>
  );
}

/* ─── Conv Bindings Modal ─── */

function ConvBindButton({ kbId }: { kbId: number }) {
  const [open, setOpen] = useState(false);
  const queryClient = useQueryClient();

  const { data: bindings, isLoading: bindingsLoading } = useQuery({
    queryKey: ['kb-conv-bindings', kbId],
    queryFn: () => kbApi.listBindings('conv', kbId),
    enabled: open,
  });

  const { data: convsData } = useQuery({
    queryKey: ['convs'],
    queryFn: () => convApi.list({ limit: 50 }),
    enabled: open,
  });

  const convs = convsData?.list ?? [];
  const [selectedConvId, setSelectedConvId] = useState<number | undefined>();

  const bindMutation = useMutation({
    mutationFn: (convId: number) => kbApi.bindToConv(convId, kbId),
    onSuccess: () => {
      message.success('绑定成功');
      queryClient.invalidateQueries({ queryKey: ['kb-conv-bindings', kbId] });
      setSelectedConvId(undefined);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '绑定失败'),
  });

  const unbindMutation = useMutation({
    mutationFn: (convId: number) => kbApi.unbindFromConv(convId, kbId),
    onSuccess: () => {
      message.success('已解绑');
      queryClient.invalidateQueries({ queryKey: ['kb-conv-bindings', kbId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '解绑失败'),
  });

  const boundIds = new Set((bindings ?? []).map((b: any) => b.target_id));
  const availableConvs = convs.filter((c: any) => !boundIds.has(c.id));

  return (
    <>
      <Button size="small" icon={<TeamOutlined />} onClick={() => setOpen(true)}>绑定会话</Button>
      <Modal title="绑定会话" open={open} onCancel={() => setOpen(false)} footer={null} width={480}>
        {bindingsLoading ? (
          <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div>
        ) : (
          <>
            <div style={{ marginBottom: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8, color: 'var(--aim-text-secondary)' }}>已绑定的会话</div>
              {bindings && bindings.length > 0 ? (
                bindings.map((b: any) => {
                  const conv = convs.find((cc: any) => cc.id === b.target_id);
                  return (
                    <div key={b.target_id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid var(--aim-border)' }}>
                      <span>{conv?.name || `会话 #${b.target_id}`}</span>
                      <Button size="small" danger loading={unbindMutation.isPending} onClick={() => unbindMutation.mutate(b.target_id)}>解绑</Button>
                    </div>
                  );
                })
              ) : (
                <div style={{ color: 'var(--aim-text-tertiary)', fontSize: 13 }}>暂未绑定任何会话</div>
              )}
            </div>

            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <Select
                style={{ flex: 1 }}
                placeholder="选择会话..."
                value={selectedConvId}
                onChange={setSelectedConvId}
                options={availableConvs.map((c: any) => ({ value: c.id, label: c.name || `会话 ${c.id}` }))}
                showSearch
                filterOption={(input, option) => (option?.label ?? '').toLowerCase().includes(input.toLowerCase())}
              />
              <Button type="primary" onClick={() => bindMutation.mutate(selectedConvId!)} loading={bindMutation.isPending} disabled={!selectedConvId}>
                绑定
              </Button>
            </div>
          </>
        )}
      </Modal>
    </>
  );
}

export function KBDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const { data: kb, isLoading } = useQuery({
    queryKey: ['kb', id],
    queryFn: () => kbApi.get(id as any),
    enabled: !!id,
  });

  const { data: docsData, isLoading: docsLoading } = useQuery({
    queryKey: ['kb-docs', id],
    queryFn: () => kbApi.listDocuments(id as any),
    enabled: !!id,
  });

  const docs = docsData?.list ?? [];

  const deleteKBMutation = useMutation({
    mutationFn: () => kbApi.delete(id as any),
    onSuccess: () => { message.success('知识库已删除'); navigate('/knowledge'); },
    onError: (err: any) => message.error(err?.response?.data?.message || '删除失败'),
  });

  const retryMutation = useMutation({
    mutationFn: (docId: number) => kbApi.retryDocument(docId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['kb-docs', id] }),
  });

  const uploadMutation = useMutation({
    mutationFn: (file: File) => kbApi.uploadDocument(id as any, file),
    onSuccess: () => {
      message.success('文档上传成功');
      queryClient.invalidateQueries({ queryKey: ['kb-docs', id] });
      queryClient.invalidateQueries({ queryKey: ['kb', id] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '上传失败'),
  });

  if (isLoading) {
    return <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}><Spin /></div>;
  }

  if (!kb) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', gap: 12 }}>
        <p style={{ color: 'var(--aim-text-tertiary)' }}>知识库不存在</p>
        <Button onClick={() => navigate('/knowledge')}>返回列表</Button>
      </div>
    );
  }

  const isWiki = kb.mode === 'wiki';

  if (isWiki) {
    return <WikiBrowser kbId={kb.id} kb={kb} />;
  }

  return <RagView kb={kb} docs={docs} docsLoading={docsLoading} uploadMutation={uploadMutation} deleteKBMutation={deleteKBMutation} />;
}

/* ─── RAG View ─── */

function RagView({ kb, docs, docsLoading, uploadMutation, deleteKBMutation }: any) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: modelsData } = useQuery({
    queryKey: ['models'],
    queryFn: () => modelApi.list(),
  });
  const vlmModels = (modelsData?.list ?? []).filter((m: any) => m.capability === 'vlm');

  const retryMutation = useMutation({
    mutationFn: (docId: number) => kbApi.retryDocument(docId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['kb-docs', String(kb.id)] }),
  });

  const pc = kb.pipeline_config || {};

  const [editOpen, setEditOpen] = useState(false);
  const [editForm] = Form.useForm();

  const editMutation = useMutation({
    mutationFn: (vals: Record<string, unknown>) => kbApi.update(kb.id, vals),
    onSuccess: () => {
      message.success('知识库已更新');
      queryClient.invalidateQueries({ queryKey: ['kb', String(kb.id)] });
      setEditOpen(false);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '更新失败'),
  });

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      {/* Top bar */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px 20px', borderBottom: '1px solid var(--aim-border)', flexShrink: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <Button onClick={() => navigate('/knowledge')} size="small">← 返回</Button>
          <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)' }}>
            <Tag color="green" style={{ marginRight: 4 }}>RAG</Tag>
            {kb.name}
            <span style={{ marginLeft: 12, color: 'var(--aim-text-tertiary)', fontSize: 12 }}>
              {kb.embedding_model || '默认'} · {kb.doc_count ?? 0} 文档
            </span>
          </span>
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
            最后更新: {kb.updated_at ? new Date(kb.updated_at * 1000).toLocaleString('zh-CN') : '-'}
          </span>
          <Upload
            accept=".txt,.md,.pdf,.doc,.docx,.html,.csv"
            showUploadList={false}
            customRequest={({ file }) => uploadMutation.mutate(file as File)}
          >
            <Button size="small" icon={<UploadOutlined />} loading={uploadMutation.isPending}>上传文档</Button>
          </Upload>
          <BotBindButton kbId={kb.id} />
          <ConvBindButton kbId={kb.id} />
          <Button size="small" onClick={() => {
            const pc = kb.pipeline_config || {};
            const cc = pc.chunking || {};
            const rc = pc.retrieval || {};
            const vlm = pc.parsing?.vlm || {};
            editForm.setFieldsValue({
              name: kb.name,
              description: kb.description,
              preset: pc.preset,
              engines: pc.parsing?.engines,
              vlm_model_id: vlm.model_id,
              parent_child_enabled: cc.parent_child?.enabled || false,
              chunk_size: cc.chunk_size,
              overlap: cc.overlap,
              separators: cc.separators,
              parent_size: cc.parent_child?.parent_size,
              child_size: cc.parent_child?.child_size,
              retrieval_mode: rc.mode,
              top_k: rc.top_k,
              candidate_top_k: rc.candidate_top_k,
              score_threshold: rc.score_threshold,
              dense_weight: rc.dense_weight,
              sparse_weight: rc.sparse_weight,
              rerank_enabled: rc.rerank?.enabled || false,
              rerank_model_id: rc.rerank?.model_id,
              rerank_top_n: rc.rerank?.top_n,
            });
            setEditOpen(true);
          }}>编辑</Button>
          <Button size="small" danger onClick={() => {
            Modal.confirm({
              title: '确认删除',
              content: `删除知识库「${kb.name}」及其所有内容？此操作不可恢复。`,
              onOk: () => deleteKBMutation.mutate(),
              okText: '删除', cancelText: '取消',
              okButtonProps: { danger: true },
            });
          }}>删除</Button>
        </div>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
        <div style={{ width: '100%' }}>
          {/* Description */}
          {kb.description && (
            <div style={{ marginBottom: 20, fontSize: 13, color: 'var(--aim-text-secondary)', lineHeight: 1.6 }}>
              {kb.description}
            </div>
          )}

          {/* Stats cards */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 12, marginBottom: 24 }}>
            {[
              { label: '文档数', value: kb.doc_count ?? 0, color: '#1677ff' },
              { label: '切片数', value: kb.total_chunks ?? 0, color: '#52c41a' },
              { label: '状态', value: kb.status, color: kb.status === 'active' ? '#52c41a' : '#faad14', tag: true },
              { label: '向量模型', value: kb.embedding_model || '默认', color: '#722ed1' },
              { label: '检索模式', value: pc.retrieval?.mode || 'hybrid', color: '#13c2c2' },
              { label: '预设', value: pc.preset || 'general', color: '#eb2f96' },
            ].map((stat, i) => (
              <div key={i} style={{
                background: 'var(--aim-surface)',
                border: '1px solid var(--aim-border)',
                borderRadius: 8,
                padding: '14px 16px',
                display: 'flex',
                flexDirection: 'column',
                gap: 4,
              }}>
                <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>{stat.label}</span>
                <span style={{ fontSize: 18, fontWeight: 700, color: stat.color }}>
                  {stat.tag ? <Tag color={stat.color} style={{ margin: 0, fontSize: 13, lineHeight: '20px' }}>{stat.value}</Tag> : stat.value}
                </span>
              </div>
            ))}
          </div>

          {/* Document section header */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>文档</h3>
            <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
              {docs.length} 个文件
            </span>
          </div>

          {/* Document grid */}
          {docsLoading ? (
            <div style={{ textAlign: 'center', padding: 60 }}><Spin /></div>
          ) : docs.length === 0 ? (
            <div style={{ textAlign: 'center', padding: 60, color: 'var(--aim-text-tertiary)', background: 'var(--aim-surface)', borderRadius: 8, border: '1px dashed var(--aim-border)' }}>
              暂无文档，点击右上角「上传文档」添加
            </div>
          ) : (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
              {docs.map((doc: DocumentRsp) => (
                <div key={doc.id}
                  style={{ background: 'var(--aim-surface)', border: '1px solid var(--aim-border)', borderRadius: 8, padding: 16, cursor: 'pointer', transition: 'all 0.15s' }}
                  onClick={() => navigate(`/knowledge/${kb.id}/documents/${doc.id}`)}
                  onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'var(--aim-primary)'; (e.currentTarget as HTMLDivElement).style.boxShadow = '0 2px 8px rgba(0,0,0,0.06)'; }}
                  onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'var(--aim-border)'; (e.currentTarget as HTMLDivElement).style.boxShadow = 'none'; }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
                    <span style={{ width: 36, height: 36, borderRadius: 6, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, fontWeight: 700, flexShrink: 0, background: 'rgba(22,119,255,0.06)', border: '1px solid rgba(22,119,255,0.12)', color: '#1677ff' }}>
                      {(doc.file_type || 'txt').toUpperCase().slice(0, 4)}
                    </span>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontWeight: 600, fontSize: 14, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--aim-text)' }}>
                        {doc.original_filename || doc.title || `文档 ${doc.id}`}
                      </div>
                      <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', marginTop: 2 }}>
                        {doc.file_type?.toUpperCase()} · {doc.file_size ? `${(doc.file_size / 1024).toFixed(1)} KB` : '-'}
                      </div>
                    </div>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: doc.created_at ? 6 : 0 }}>
                    <Tag color={STATUS_COLOR[doc.status] || 'default'} style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>{doc.status}</Tag>
                    <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
                      {doc.chunk_count ?? 0} 切片
                    </span>
                  </div>
                  {doc.created_at && (
                    <div style={{ fontSize: 11, color: 'var(--aim-text-tertiary)' }}>
                      {new Date(doc.created_at * 1000).toLocaleString('zh-CN')}
                    </div>
                  )}
                  {doc.status === 'failed' && (
                    <div style={{ marginTop: 8, display: 'flex', justifyContent: 'flex-end' }}>
                      <Button size="small" icon={<ReloadOutlined />} loading={retryMutation.isPending}
                        onClick={(e) => { e.stopPropagation(); retryMutation.mutate(doc.id); }}>重试</Button>
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      <Modal title="编辑知识库" open={editOpen}
        onOk={() => editForm.validateFields().then((vals) => {
          const payload: Record<string, unknown> = { name: vals.name, description: vals.description };
          const pipelineConfig: Record<string, any> = {};
          if (vals.preset) pipelineConfig.preset = vals.preset;
          pipelineConfig.parsing = {
            engines: vals.engines?.length ? vals.engines : undefined,
            vlm: vals.vlm_model_id ? {
              model_id: vals.vlm_model_id,
              enabled: true,
            } : undefined,
          };
          pipelineConfig.chunking = {
            chunk_size: vals.parent_child_enabled ? undefined : vals.chunk_size,
            overlap: vals.parent_child_enabled ? undefined : vals.overlap,
            separators: vals.separators?.length ? vals.separators : undefined,
            parent_child: {
              enabled: !!vals.parent_child_enabled,
              parent_size: vals.parent_child_enabled ? vals.parent_size : undefined,
              child_size: vals.parent_child_enabled ? vals.child_size : undefined,
            },
          };
          pipelineConfig.retrieval = {
            mode: vals.retrieval_mode,
            top_k: vals.top_k,
            candidate_top_k: vals.candidate_top_k,
            score_threshold: vals.score_threshold,
            dense_weight: vals.dense_weight,
            sparse_weight: vals.sparse_weight,
            rerank: vals.rerank_enabled ? {
              enabled: true,
              model_id: vals.rerank_model_id,
              top_n: vals.rerank_top_n,
            } : undefined,
          };
          payload.pipeline_config = pipelineConfig;
          editMutation.mutate(payload);
        })}
        onCancel={() => setEditOpen(false)} confirmLoading={editMutation.isPending}
        okText="保存" cancelText="取消" width={640}
      >
        <Form form={editForm} layout="vertical" style={{ paddingTop: 16 }}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]} style={{ marginBottom: 12 }}><Input /></Form.Item>
          <Form.Item name="description" label="描述" style={{ marginBottom: 12 }}><Input.TextArea rows={2} /></Form.Item>
          <Form.Item name="preset" label="处理预设" style={{ marginBottom: 12 }}>
            <Select allowClear placeholder="选择预设" options={[
              { value: 'general', label: '通用' },
              { value: 'tech_doc', label: '技术文档' },
              { value: 'legal', label: '法律文书' },
              { value: 'academic', label: '学术论文' },
              { value: 'customer_service', label: '客服问答' },
            ]} />
          </Form.Item>

          <div style={{ fontWeight: 600, fontSize: 14, marginBottom: 8, marginTop: 8, color: 'var(--aim-text)' }}>解析引擎</div>
          <Form.Item name="engines" label="解析引擎（按优先级）" style={{ marginBottom: 12 }}>
            <Select mode="multiple" placeholder="默认 auto（自动选择）" options={[
              { value: 'builtin', label: '内置解析器（txt/md/html）' },
              { value: 'mineru_precision', label: 'MinerU 精准解析（PDF/Office）' },
              { value: 'mineru_agent', label: 'MinerU Agent 轻量解析' },
            ]} />
          </Form.Item>
          <Form.Item name="vlm_model_id" label="VLM 图片描述模型" style={{ marginBottom: 12 }}>
            <Select allowClear placeholder="不启用" options={vlmModels.map((m: any) => ({ value: m.id, label: modelOptionLabel(m) }))} />
          </Form.Item>

          <div style={{ fontWeight: 600, fontSize: 14, marginBottom: 8, marginTop: 12, color: 'var(--aim-text)' }}>切片配置</div>
          <div style={{ display: 'flex', gap: 16, alignItems: 'center', marginBottom: 12 }}>
            <Form.Item name="parent_child_enabled" label="父子分块" valuePropName="checked" style={{ marginBottom: 0 }}>
              <Switch />
            </Form.Item>
            <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
              开启后使用大窗口父块+小窗口子块，提高检索上下文质量
            </span>
          </div>
          <Form.Item noStyle shouldUpdate={(prev, cur) => prev.parent_child_enabled !== cur.parent_child_enabled}>
            {({ getFieldValue }) =>
              getFieldValue('parent_child_enabled') ? (
                <div style={{ display: 'flex', gap: 16 }}>
                  <Form.Item name="parent_size" label="父块大小" style={{ flex: 1, marginBottom: 12 }}>
                    <InputNumber min={256} max={4096} style={{ width: '100%' }} />
                  </Form.Item>
                  <Form.Item name="child_size" label="子块大小" style={{ flex: 1, marginBottom: 12 }}>
                    <InputNumber min={64} max={1024} style={{ width: '100%' }} />
                  </Form.Item>
                </div>
              ) : (
                <div style={{ display: 'flex', gap: 16 }}>
                  <Form.Item name="chunk_size" label="切片大小" style={{ flex: 1, marginBottom: 12 }}>
                    <InputNumber min={100} max={4000} style={{ width: '100%' }} />
                  </Form.Item>
                  <Form.Item name="overlap" label="切片重叠" style={{ flex: 1, marginBottom: 12 }}>
                    <InputNumber min={0} max={500} style={{ width: '100%' }} />
                  </Form.Item>
                </div>
              )
            }
          </Form.Item>
          <Form.Item name="separators" label="分隔符（按优先级）" style={{ marginBottom: 12 }}>
            <Select mode="tags" placeholder="默认 \n\n, \n, 。" open={false} />
          </Form.Item>

          <div style={{ fontWeight: 600, fontSize: 14, marginBottom: 8, marginTop: 12, color: 'var(--aim-text)' }}>检索配置</div>
          <Form.Item name="retrieval_mode" label="检索模式" style={{ marginBottom: 12 }}>
            <Select allowClear options={[
              { value: 'hybrid', label: '混合检索 (向量+全文)' },
              { value: 'vector', label: '向量检索' },
              { value: 'fulltext', label: '全文检索' },
            ]} />
          </Form.Item>
          <div style={{ display: 'flex', gap: 16 }}>
            <Form.Item name="top_k" label="返回结果数" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={1} max={50} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="candidate_top_k" label="候选集大小" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={1} max={200} style={{ width: '100%' }} />
            </Form.Item>
          </div>
          <div style={{ display: 'flex', gap: 16 }}>
            <Form.Item name="score_threshold" label="分数阈值" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={0} max={1} step={0.05} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="dense_weight" label="向量权重" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={0} max={1} step={0.1} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="sparse_weight" label="全文权重" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={0} max={1} step={0.1} style={{ width: '100%' }} />
            </Form.Item>
          </div>
          <div style={{ display: 'flex', gap: 16, alignItems: 'center', marginBottom: 12 }}>
            <Form.Item name="rerank_enabled" label="重排序" valuePropName="checked" style={{ marginBottom: 0 }}>
              <Switch />
            </Form.Item>
          </div>
          <Form.Item noStyle shouldUpdate={(prev, cur) => prev.rerank_enabled !== cur.rerank_enabled}>
            {({ getFieldValue }) => getFieldValue('rerank_enabled') ? (
              <div style={{ display: 'flex', gap: 16 }}>
                <Form.Item name="rerank_model_id" label="Rerank 模型 ID" style={{ flex: 1, marginBottom: 12 }}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item name="rerank_top_n" label="重排数量" style={{ flex: 1, marginBottom: 12 }}>
                  <InputNumber min={1} max={50} style={{ width: '100%' }} />
                </Form.Item>
              </div>
            ) : null}
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}

/* ─── Wiki Browser ─── */

function WikiBrowser({ kbId, kb }: { kbId: number; kb: any }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [currentSlug, setCurrentSlug] = useState('index');
  const [searchQuery, setSearchQuery] = useState('');
  const [activeTab, setActiveTab] = useState('pages');
  const [batchUploading, setBatchUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // ─── Documents for wiki mode ───
  const { data: docsData, isLoading: docsLoading } = useQuery({
    queryKey: ['kb-docs', String(kbId)],
    queryFn: () => kbApi.listDocuments(kbId),
    enabled: activeTab === 'documents',
    refetchOnWindowFocus: true,
  });
  const docs = docsData?.list ?? [];

  const deleteDocMutation = useMutation({
    mutationFn: (docId: number) => kbApi.deleteDocument(docId),
    onSuccess: (_data, docId) => {
      message.success('文档已删除');
      queryClient.setQueryData(['kb-docs', String(kbId)], (old: any) => {
        if (!old?.list) return old;
        return { ...old, list: old.list.filter((d: any) => d.id !== docId) };
      });
      queryClient.invalidateQueries({ queryKey: ['kb-docs', String(kbId)] });
      queryClient.invalidateQueries({ queryKey: ['kb', String(kbId)] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '删除失败'),
  });

  const { data: pagesData, isLoading: pagesLoading, error: pagesError } = useQuery({
    queryKey: ['wiki-pages', kbId],
    queryFn: () => kbApi.wikiListPages(kbId),
    enabled: !!kbId,
  });

  const { data: page, isLoading: pageLoading, error: pageError } = useQuery({
    queryKey: ['wiki-page', kbId, currentSlug],
    queryFn: () => kbApi.wikiReadPage(kbId, currentSlug).catch((err: any) => {
      if (err?.response?.status === 404) return null;
      throw err;
    }),
    enabled: !!kbId && !!currentSlug,
  });

  // Auto-redirect to first available page if the default 'index' slug doesn't exist
  useEffect(() => {
    const list = pagesData?.list ?? [];
    if (!pageLoading && !page && currentSlug === 'index' && list.length > 0) {
      setCurrentSlug(list[0].slug);
    }
  }, [pageLoading, page, currentSlug, pagesData]);

  const deletePageMutation = useMutation({
    mutationFn: (slug: string) => kbApi.wikiDeletePage(kbId, slug),
    onSuccess: (_data, slug) => {
      message.success('页面已删除');
      queryClient.setQueryData(['wiki-pages', kbId], (old: any) => {
        if (!old?.list) return old;
        return { ...old, list: old.list.filter((p: any) => p.slug !== slug) };
      });
      queryClient.invalidateQueries({ queryKey: ['wiki-pages', kbId] });
      if (currentSlug !== 'index') setCurrentSlug('index');
    },
  });

  const uploadMutation = useMutation({
    mutationFn: (file: File) => kbApi.uploadDocument(kbId, file),
    onSuccess: () => {
      message.success('文档上传成功，处理完成后可在「原始文档」标签查看状态');
      queryClient.invalidateQueries({ queryKey: ['kb-docs', String(kbId)] });
      queryClient.invalidateQueries({ queryKey: ['kb', String(kbId)] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '上传失败'),
  });

  const refreshMutation = useMutation({
    mutationFn: () => kbApi.wikiRefresh(kbId),
    onSuccess: (data: any) => {
      message.success(`已触发 wiki 刷新，更新 ${data.pages_updated} 个页面`);
      queryClient.invalidateQueries({ queryKey: ['wiki-pages', kbId] });
    },
  });

  const maintenanceMutation = useMutation({
    mutationFn: () => kbApi.wikiRunMaintenance(kbId),
    onSuccess: () => {
      message.success('已触发维护，维护完成后将通知您');
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '维护失败'),
  });

  // Listen for wiki.maintained events from async maintenance completion
  useEffect(() => {
    const unsub = wsOn('wiki.maintained', (payload: any) => {
      if (payload.kb_id !== kbId) return;
      const kbName = payload.metadata?.kb_name || kb?.name || '';
      if (payload.level === 'error') {
        notification.error({
          message: '知识库维护失败',
          description: payload.message || `知识库「${kbName}」维护失败`,
          duration: 6,
        });
      } else {
        notification.success({
          message: '知识库维护完成',
          description: payload.message || `知识库「${kbName}」维护完成`,
          duration: 6,
        });
        queryClient.invalidateQueries({ queryKey: ['wiki-pages', kbId] });
      }
    });
    return unsub;
  }, [kbId, kb?.name, queryClient]);

  const [editOpen, setEditOpen] = useState(false);
  const [editForm] = Form.useForm();

  const { data: modelsData } = useQuery({
    queryKey: ['models'],
    queryFn: () => modelApi.list(),
  });

  const chatModelOptions = (modelsData?.list ?? [])
    .filter((m: any) => m.capability === 'chat')
    .map((m: any) => ({ value: m.id, label: modelOptionLabel(m), model_name: m.model_name }));
  const modelMap = Object.fromEntries((modelsData?.list ?? []).map((m: any) => [m.id, m.model_name]));

  const editMutation = useMutation({
    mutationFn: (vals: Record<string, any>) => {
      const payload: Record<string, any> = { name: vals.name, description: vals.description };
      const wiki: Record<string, any> = { enabled: true,
        model_id: vals.wiki_model_id || 14,
        model_name: vals.wiki_model_id ? (modelMap[vals.wiki_model_id] || '') : 'qwen-plus',
        auto_lint: vals.wiki_auto_lint ?? true,
        stale_threshold_hours: vals.wiki_stale_hours || 168,
      };
      if (vals.maintenance?.maintenance_enabled) {
        wiki.maintenance_enabled = true;
        wiki.maintenance_cron = vals.maintenance.maintenance_cron;
      }
      payload.pipeline_config = { wiki };
      return kbApi.update(kbId, payload);
    },
    onSuccess: () => {
      message.success('知识库已更新');
      queryClient.invalidateQueries({ queryKey: ['kb', String(kbId)] });
      setEditOpen(false);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '更新失败'),
  });

  const deleteKBMutation = useMutation({
    mutationFn: () => kbApi.delete(kbId),
    onSuccess: () => { message.success('知识库已删除'); navigate('/knowledge'); },
    onError: (err: any) => message.error(err?.response?.data?.message || '删除失败'),
  });

  // Listen for document processing events via WebSocket
  useEffect(() => {
    const unsubReady = wsOn('knowledge.ready', () => {
      queryClient.invalidateQueries({ queryKey: ['kb-docs', String(kbId)] });
      queryClient.invalidateQueries({ queryKey: ['wiki-pages', kbId] });
    });
    const unsubFailed = wsOn('knowledge.failed', () => {
      queryClient.invalidateQueries({ queryKey: ['kb-docs', String(kbId)] });
    });
    return () => { unsubReady(); unsubFailed(); };
  }, [kbId, queryClient]);

  const pages = pagesData?.list ?? [];

  // Group pages by type
  const grouped = PAGE_TYPE_ORDER.map(type => ({
    type,
    label: PAGE_TYPE_LABEL[type] || type,
    pages: pages.filter((p: WikiPageItem) => p.page_type === type),
  }));

  // Filter by search
  const filteredGroups = searchQuery
    ? grouped.map(g => ({
        ...g,
        pages: g.pages.filter((p: WikiPageItem) =>
          p.title.toLowerCase().includes(searchQuery.toLowerCase()) ||
          p.slug.toLowerCase().includes(searchQuery.toLowerCase()) ||
          p.summary?.toLowerCase().includes(searchQuery.toLowerCase())
        ),
      })).filter(g => g.pages.length > 0)
    : grouped.filter(g => g.pages.length > 0);

  const handleNavigate = useCallback((slug: string) => {
    setCurrentSlug(slug);
  }, []);

  const handleDelete = (slug: string, title: string) => {
    Modal.confirm({
      title: '确认删除',
      content: `删除页面「${title}」？`,
      onOk: () => deletePageMutation.mutate(slug),
      okText: '删除', cancelText: '取消',
      okButtonProps: { danger: true },
    });
  };

  const handleBatchUpload = async (files: File[]) => {
    setBatchUploading(true);
    try {
      const result = await kbApi.wikiBatchUpload(kbId, files);
      message.success(`成功上传 ${result.documents?.length ?? 0} 个文件`);
      queryClient.invalidateQueries({ queryKey: ['kb-docs', String(kbId)] });
      queryClient.invalidateQueries({ queryKey: ['wiki-pages', kbId] });
      queryClient.invalidateQueries({ queryKey: ['kb', String(kbId)] });
    } catch (err: any) {
      message.error(err?.response?.data?.message || '批量上传失败');
    } finally {
      setBatchUploading(false);
    }
  };

  const wikiModel = kb.pipeline_config?.wiki?.model_name || kb.embedding_model || '默认';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      {/* Top bar */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px 20px', borderBottom: '1px solid var(--aim-border)', flexShrink: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <Button onClick={() => navigate('/knowledge')} size="small">← 返回</Button>
          <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)' }}>
            <Tag color="blue" style={{ marginRight: 4 }}>WIKI</Tag>
            {kb.name}
            <span style={{ marginLeft: 12, color: 'var(--aim-text-tertiary)', fontSize: 12 }}>
              {wikiModel} · {kb.doc_count ?? 0} 文档
            </span>
          </span>
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
            最后更新: {kb.updated_at ? new Date(kb.updated_at * 1000).toLocaleString('zh-CN') : '-'}
          </span>
          <input
            type="file"
            multiple
            accept=".txt,.md,.pdf,.doc,.docx,.html,.csv"
            style={{ display: 'none' }}
            ref={fileInputRef}
            onChange={(e) => {
              const files = e.target.files;
              if (files && files.length > 0) {
                handleBatchUpload(Array.from(files));
              }
              e.target.value = '';
            }}
          />
          <Button
            size="small"
            icon={<UploadOutlined />}
            loading={batchUploading}
            onClick={() => fileInputRef.current?.click()}
          >
            上传文档
          </Button>
          <Button size="small" type="primary" onClick={() => navigate(`/knowledge/${kbId}/wiki/new/edit`)}>新建页面</Button>
          <Button size="small" loading={refreshMutation.isPending} onClick={() => refreshMutation.mutate()}>刷新</Button>
          <Button size="small" loading={maintenanceMutation.isPending} onClick={() => maintenanceMutation.mutate()}>维护</Button>
          <Button size="small" onClick={() => setActiveTab('issues')}><UnorderedListOutlined />待办</Button>
          <Button size="small" icon={<EditOutlined />} onClick={() => {
            const wc = kb.pipeline_config?.wiki || {};
            editForm.setFieldsValue({
              name: kb.name,
              description: kb.description,
              wiki_model_id: wc.model_id,
              wiki_auto_lint: wc.auto_lint ?? true,
              wiki_stale_hours: wc.stale_threshold_hours || 168,
              maintenance: { maintenance_enabled: wc.maintenance_enabled ?? false, maintenance_cron: wc.maintenance_cron || "" },
            });
            setEditOpen(true);
          }}>编辑</Button>
          <Button size="small" icon={<ApartmentOutlined />} onClick={() => navigate(`/knowledge/${kbId}/wiki/graph`)}>关系图</Button>
          <BotBindButton kbId={kbId} />
          <ConvBindButton kbId={kbId} />
          <Button size="small" danger onClick={() => {
            Modal.confirm({
              title: '确认删除',
              content: `删除知识库「${kb.name}」及其所有内容？此操作不可恢复。`,
              onOk: () => deleteKBMutation.mutate(),
              okText: '删除', cancelText: '取消',
              okButtonProps: { danger: true },
            });
          }}>删除</Button>
        </div>
      </div>

      {/* Tab switcher */}
      <div style={{ display: 'flex', borderBottom: '1px solid var(--aim-border)', flexShrink: 0 }}>
        <div
          onClick={() => setActiveTab('pages')}
          style={{ padding: '8px 20px', fontSize: 13, cursor: 'pointer', borderBottom: activeTab === 'pages' ? '2px solid var(--aim-primary)' : '2px solid transparent', color: activeTab === 'pages' ? 'var(--aim-primary)' : 'var(--aim-text-secondary)', fontWeight: activeTab === 'pages' ? 600 : 400 }}
        >页面</div>
        <div
          onClick={() => setActiveTab('documents')}
          style={{ padding: '8px 20px', fontSize: 13, cursor: 'pointer', borderBottom: activeTab === 'documents' ? '2px solid var(--aim-primary)' : '2px solid transparent', color: activeTab === 'documents' ? 'var(--aim-primary)' : 'var(--aim-text-secondary)', fontWeight: activeTab === 'documents' ? 600 : 400 }}
        >原始文档</div>
      </div>

      {activeTab === 'pages' ? (
        /* ===== Pages View: sidebar + content ===== */
        <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
          {/* Sidebar */}
          <div style={{ width: 260, flexShrink: 0, borderRight: '1px solid var(--aim-border)', display: 'flex', flexDirection: 'column', background: 'var(--aim-surface)' }}>
            <div style={{ padding: '10px 12px' }}>
              <Input
                size="small"
                prefix={<SearchOutlined style={{ fontSize: 13, color: 'var(--aim-text-tertiary)' }} />}
                placeholder="搜索页面..."
                value={searchQuery}
                onChange={e => setSearchQuery(e.target.value)}
                allowClear
              />
            </div>
            <div style={{ flex: 1, overflowY: 'auto', padding: '0 8px 12px' }}>
              {filteredGroups.length === 0 ? (
                <div style={{ textAlign: 'center', padding: 24, color: 'var(--aim-text-tertiary)', fontSize: 13 }}>无匹配页面</div>
              ) : (
                filteredGroups.map(group => (
                  <div key={group.type} style={{ marginBottom: 4 }}>
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '6px 8px', fontSize: 12, fontWeight: 600, color: 'var(--aim-text-secondary)' }}>
                      <span>{group.label}</span>
                      <span style={{ color: 'var(--aim-text-tertiary)', fontWeight: 400 }}>{group.pages.length}</span>
                    </div>
                    {group.pages.map((p: WikiPageItem) => (
                      <div
                        key={p.slug}
                        onClick={() => handleNavigate(p.slug)}
                        style={{
                          padding: '5px 8px 5px 20px', fontSize: 13, cursor: 'pointer', borderRadius: 4,
                          color: currentSlug === p.slug ? 'var(--aim-primary)' : 'var(--aim-text)',
                          background: currentSlug === p.slug ? 'var(--aim-primary-bg, rgba(64,150,255,0.08))' : 'transparent',
                          fontWeight: currentSlug === p.slug ? 600 : 400,
                          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', transition: 'all 0.15s',
                        }}
                        onMouseEnter={e => { if (currentSlug !== p.slug) (e.target as HTMLDivElement).style.background = 'var(--aim-hover-bg, rgba(0,0,0,0.03))'; }}
                        onMouseLeave={e => { if (currentSlug !== p.slug) (e.target as HTMLDivElement).style.background = 'transparent'; }}
                      >
                        {p.title}
                      </div>
                    ))}
                  </div>
                ))
              )}
            </div>
          </div>

          {/* Content */}
          <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
            {pageLoading ? (
              <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}><Spin /></div>
            ) : !page ? (
              <div style={{ textAlign: 'center', padding: 60, color: 'var(--aim-text-tertiary)' }}>页面不存在</div>
            ) : (
              <>
                <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 12 }}>
                  <div>
                    <h1 style={{ margin: 0, fontSize: 22, fontWeight: 600, color: 'var(--aim-text)' }}>{page.title}</h1>
                    <div style={{ marginTop: 8, display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                      <Tag color={PAGE_COLORS[page.page_type] || 'default'}>{PAGE_TYPE_LABEL[page.page_type] || page.page_type}</Tag>
                      <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
                        v{page.version} · 更新于 {page.updated_at ? new Date(page.updated_at * 1000).toLocaleString('zh-CN') : '-'}
                      </span>
                      {page.aliases?.length > 0 && page.aliases.map((a: string) => (
                        <Tag key={a} style={{ borderRadius: 4, fontSize: 11 }}>{a}</Tag>
                      ))}
                    </div>
                  </div>
                  <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
                    <Button size="small" onClick={() => navigate(`/knowledge/${kbId}/wiki/${page.slug}/edit`)}>编辑</Button>
                    <Button size="small" danger onClick={() => handleDelete(page.slug, page.title)}>删除</Button>
                  </div>
                </div>
                {page.summary && (
                  <div style={{ padding: '10px 14px', background: 'var(--aim-surface)', borderRadius: 8, border: '1px solid var(--aim-border)', marginBottom: 16, fontSize: 13, color: 'var(--aim-text-secondary)', lineHeight: 1.6 }}>
                    {page.summary}
                  </div>
                )}
                <WikiMarkdown content={page.content} pages={pages} kbId={kbId} sourceRefs={page.source_refs} onNavigate={handleNavigate} />
                {page.source_refs && page.source_refs.length > 0 && (
                  <div style={{ marginTop: 32, padding: 16, background: 'var(--aim-surface)', borderRadius: 8, border: '1px solid var(--aim-border)' }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--aim-text-secondary)', marginBottom: 10 }}>来源文档</div>
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
                {((page.out_links?.length ?? 0) > 0 || (page.in_links?.length ?? 0) > 0) && (
                  <div style={{ marginTop: 32, padding: 16, background: 'var(--aim-surface)', borderRadius: 8, border: '1px solid var(--aim-border)' }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--aim-text-secondary)', marginBottom: 10 }}>引用关系</div>
                    <div style={{ display: 'flex', gap: 24, fontSize: 13 }}>
                      {(page.out_links?.length ?? 0) > 0 && (
                        <div>
                          <span style={{ color: 'var(--aim-text-tertiary)' }}>引用:</span>
                          <div style={{ marginTop: 4, display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                            {page.out_links.map((link: string) => {
                              const found = pages.find((p: WikiPageItem) => p.slug === link);
                              return (
                                <Tag key={link} style={{ cursor: 'pointer', margin: 0 }} onClick={() => handleNavigate(link)}>
                                  {found?.title || link}
                                </Tag>
                              );
                            })}
                          </div>
                        </div>
                      )}
                      {(page.in_links?.length ?? 0) > 0 && (
                        <div>
                          <span style={{ color: 'var(--aim-text-tertiary)' }}>被引用:</span>
                          <div style={{ marginTop: 4, display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                            {page.in_links.map((link: string) => {
                              const found = pages.find((p: WikiPageItem) => p.slug === link);
                              return (
                                <Tag key={link} style={{ cursor: 'pointer', margin: 0 }} onClick={() => handleNavigate(link)}>
                                  {found?.title || link}
                                </Tag>
                              );
                            })}
                          </div>
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      ) : activeTab === 'issues' ? (
        <IssuesView kbId={kbId} />
      ) : (
        /* ===== Documents View ===== */
        <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
          {/* Header */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
            <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)' }}>
              {docs.length} 个文件
            </span>
            <Button size="small" icon={<ReloadOutlined />} onClick={() => queryClient.invalidateQueries({ queryKey: ['kb-docs', String(kbId)] })}>刷新</Button>
          </div>

          {docsLoading ? (
            <div style={{ textAlign: 'center', padding: 60 }}><Spin /></div>
          ) : docs.length === 0 ? (
            <div style={{ textAlign: 'center', padding: 60, color: 'var(--aim-text-tertiary)', background: 'var(--aim-surface)', borderRadius: 8, border: '1px dashed var(--aim-border)' }}>
              暂无原始文档
            </div>
          ) : (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
              {docs.map((doc: DocumentRsp) => (
                <div key={doc.id}
                  style={{ background: 'var(--aim-surface)', border: '1px solid var(--aim-border)', borderRadius: 8, padding: 16, cursor: 'pointer', transition: 'all 0.15s' }}
                  onClick={() => navigate(`/knowledge/${kbId}/wiki/documents/${doc.id}`)}
                  onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'var(--aim-primary)'; (e.currentTarget as HTMLDivElement).style.boxShadow = '0 2px 8px rgba(0,0,0,0.06)'; }}
                  onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'var(--aim-border)'; (e.currentTarget as HTMLDivElement).style.boxShadow = 'none'; }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
                    <span style={{ width: 36, height: 36, borderRadius: 6, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, fontWeight: 700, flexShrink: 0, background: 'rgba(22,119,255,0.06)', border: '1px solid rgba(22,119,255,0.12)', color: '#1677ff' }}>
                      {(doc.file_type || 'txt').toUpperCase().slice(0, 4)}
                    </span>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontWeight: 600, fontSize: 14, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--aim-text)' }}>
                        {doc.original_filename || doc.title || `文档 ${doc.id}`}
                      </div>
                      <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', marginTop: 2 }}>
                        {doc.file_type?.toUpperCase()} · {doc.file_size ? `${(doc.file_size / 1024).toFixed(1)} KB` : '-'} · <Tag color={doc.status === 'ready' ? 'green' : doc.status === 'failed' ? 'red' : 'blue'} style={{ fontSize: 10, lineHeight: '16px' }}>{doc.status}</Tag>
                      </div>
                    </div>
                    <Button
                      size="small"
                      danger
                      icon={<DeleteOutlined />}
                      loading={deleteDocMutation.isPending}
                      onClick={(e) => {
                        e.stopPropagation();
                        Modal.confirm({
                          title: '确认删除',
                          content: `删除文档「${doc.original_filename || doc.title || doc.id}」？不会影响已生成的 Wiki 页面。`,
                          onOk: () => deleteDocMutation.mutate(doc.id),
                          okText: '删除', cancelText: '取消',
                          okButtonProps: { danger: true },
                        });
                      }}
                    />
                  </div>
                  {doc.created_at && (
                    <div style={{ fontSize: 11, color: 'var(--aim-text-tertiary)' }}>
                      {new Date(doc.created_at * 1000).toLocaleString('zh-CN')}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
      {/* Wiki Edit Modal */}
      <Modal title="编辑 Wiki 知识库" open={editOpen}
        onOk={() => editForm.validateFields().then((vals) => editMutation.mutate(vals))}
        onCancel={() => setEditOpen(false)} confirmLoading={editMutation.isPending}
        okText="保存" cancelText="取消" width={520}
      >
        <Form form={editForm} layout="vertical" style={{ paddingTop: 16 }}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]} style={{ marginBottom: 12 }}><Input /></Form.Item>
          <Form.Item name="description" label="描述" style={{ marginBottom: 12 }}><Input.TextArea rows={2} /></Form.Item>
          <Form.Item name="wiki_model_id" label="LLM 模型" style={{ marginBottom: 12 }}>
            <Select allowClear placeholder="选择模型" options={chatModelOptions} showSearch
              filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}
            />
          </Form.Item>
          <div style={{ display: 'flex', gap: 16 }}>
            <Form.Item name="wiki_auto_lint" label="自动检查" valuePropName="checked" style={{ flex: 1, marginBottom: 12 }}>
              <Switch />
            </Form.Item>
            <Form.Item name="wiki_stale_hours" label="过期阈值(小时)" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={1} max={8760} style={{ width: '100%' }} />
            </Form.Item>
          </div>
          <Form.Item name="maintenance" label="自动维护" style={{ marginBottom: 0 }}>
            <WikiMaintenanceConfig />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}

/* ─── Issues View ─── */

const ISSUE_TYPE_LABEL: Record<string, string> = {
  low_quality: '内容过短',
  missing_ref: '引用缺失',
  stale: '内容过期',
  factual_error: '事实错误',
  merge_conflict: '合并冲突',
  outdated: '内容过时',
};

const ISSUE_LEVEL_COLOR: Record<string, string> = {
  info: 'blue',
  warning: 'orange',
  error: 'red',
};

function IssuesView({ kbId }: { kbId: number }) {
  const queryClient = useQueryClient();
  const [filterStatus, setFilterStatus] = useState('');

  const { data: issues, isLoading } = useQuery({
    queryKey: ['wiki-issues', kbId],
    queryFn: () => kbApi.wikiListIssues(kbId),
    enabled: !!kbId,
  });

  const updateMutation = useMutation({
    mutationFn: ({ issueId, status }: { issueId: number; status: string }) =>
      kbApi.wikiUpdateIssue(kbId, issueId, status),
    onSuccess: () => {
      message.success('状态已更新');
      queryClient.invalidateQueries({ queryKey: ['wiki-issues', kbId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '更新失败'),
  });

  const list = issues?.items ?? [];
  const visible = list.filter((i: WikiIssueItem) => i.status !== 'ignored');
  const filtered = filterStatus ? visible.filter((i: WikiIssueItem) => i.status === filterStatus) : visible;

  const counts = {
    all: visible.length,
    open: visible.filter((i: WikiIssueItem) => i.status === 'open').length,
    resolved: visible.filter((i: WikiIssueItem) => i.status === 'resolved').length,
  };

  const handleIgnore = (issue: WikiIssueItem) => {
    updateMutation.mutate({ issueId: issue.id, status: 'ignored' }, {
      onSuccess: () => {
        queryClient.setQueryData(['wiki-issues', kbId], (old: any) => {
          if (!old?.items) return old;
          return { ...old, items: old.items.filter((i: any) => i.id !== issue.id) };
        });
      },
    });
  };

  if (isLoading) {
    return <div style={{ display: 'flex', justifyContent: 'center', padding: 60 }}><Spin /></div>;
  }

  return (
    <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
      {/* Status filter tabs */}
      <div style={{ display: 'flex', gap: 4, marginBottom: 16, borderBottom: '1px solid var(--aim-border)', paddingBottom: 0 }}>
        {[
          { key: '', label: '全部', count: counts.all },
          { key: 'open', label: '待处理', count: counts.open },
          { key: 'resolved', label: '已解决', count: counts.resolved },
        ].map(tab => (
          <div
            key={tab.key}
            onClick={() => setFilterStatus(tab.key)}
            style={{
              padding: '6px 16px', fontSize: 13, cursor: 'pointer', borderRadius: '6px 6px 0 0',
              borderBottom: filterStatus === tab.key ? '2px solid var(--aim-primary)' : '2px solid transparent',
              color: filterStatus === tab.key ? 'var(--aim-primary)' : 'var(--aim-text-secondary)',
              fontWeight: filterStatus === tab.key ? 600 : 400,
            }}
          >
            {tab.label}
            <span style={{ marginLeft: 6, fontSize: 11, opacity: 0.7 }}>({tab.count})</span>
          </div>
        ))}
      </div>

      {filtered.length === 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', flex: 1, gap: 12, color: 'var(--aim-text-tertiary)', padding: 80 }}>
          <UnorderedListOutlined style={{ fontSize: 40, opacity: 0.3 }} />
          <span>{
            filterStatus === 'open' ? '没有待处理的问题' :
            filterStatus === 'resolved' ? '没有已解决的问题' :
            '暂无问题，知识库状态良好'
          }</span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {filtered.map((issue: WikiIssueItem) => (
            <div
              key={issue.id}
              style={{
                display: 'flex', alignItems: 'flex-start', gap: 12,
                padding: '12px 16px', background: 'var(--aim-surface)',
                border: '1px solid var(--aim-border)', borderRadius: 8,
                opacity: issue.status === 'resolved' ? 0.55 : 1,
              }}
            >
              <UnorderedListOutlined style={{ fontSize: 16, color: ISSUE_LEVEL_COLOR[issue.level] || '#faad14', flexShrink: 0, marginTop: 2 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2 }}>
                  <Tag color={issue.status === 'open' ? 'orange' : 'green'} style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
                    {issue.status === 'open' ? '待处理' : '已解决'}
                  </Tag>
                  <Tag color={ISSUE_LEVEL_COLOR[issue.level] || 'default'} style={{ margin: 0, fontSize: 11, lineHeight: '18px' }}>
                    {issue.level}
                  </Tag>
                  <span style={{ fontWeight: 500, fontSize: 14, color: 'var(--aim-text)' }}>{issue.title}</span>
                </div>
                {issue.description && (
                  <div style={{ fontSize: 13, color: 'var(--aim-text-secondary)', marginTop: 4, lineHeight: 1.5 }}>
                    {issue.description}
                  </div>
                )}
                <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', marginTop: 4 }}>
                  页面: {issue.page_slug} · 类型: {ISSUE_TYPE_LABEL[issue.issue_type] || issue.issue_type} · {new Date(issue.created_at * 1000).toLocaleString('zh-CN')}
                </div>
              </div>
              <div style={{ display: 'flex', gap: 6, flexShrink: 0, marginTop: 2 }}>
                {issue.status === 'open' ? (
                  <>
                    <Button size="small" type="primary" ghost
                      loading={updateMutation.isPending}
                      onClick={() => updateMutation.mutate({ issueId: issue.id, status: 'resolved' })}
                    >解决</Button>
                    <Button size="small"
                      loading={updateMutation.isPending}
                      onClick={() => handleIgnore(issue)}
                    >删除</Button>
                  </>
                ) : (
                  <Button size="small"
                    loading={updateMutation.isPending}
                    onClick={() => updateMutation.mutate({ issueId: issue.id, status: 'open' })}
                  >恢复</Button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

