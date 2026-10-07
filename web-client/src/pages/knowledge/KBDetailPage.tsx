import { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient, type UseMutationResult } from '@tanstack/react-query';
import { Button, Spin, Tag, Upload, message, Modal, Input, Select, Form, InputNumber, Switch } from 'antd';
import { UploadOutlined, ReloadOutlined, RobotOutlined, TeamOutlined } from '@ant-design/icons';
import { kbApi } from '@/services/knowledge';
import { botApi } from '@/services/bot';
import { convApi } from '@/services/conversation';
import { modelApi } from '@/services/model';
import { wsOn } from '@/services/ws';
import type { DocumentRsp, KBRsp, ChunkingConfig, RetrievalConfig } from '@/types/model';
import { modelOptionLabel } from '@/utils/provider';
import type { UpdateKBReq, PipelineConfig } from '@/types/api';

interface EditKBFormValues {
  name: string;
  description?: string;
  embedding_model_id?: string;
  vlm_model_id?: string;
  rerank_model_id?: string;
  engines?: string[];
  parent_child_enabled?: boolean;
  chunk_size?: number;
  overlap?: number;
  separators?: string[];
  parent_size?: number;
  child_size?: number;
  retrieval_mode?: string;
  top_k?: number;
  candidate_top_k?: number;
  score_threshold?: number;
  dense_weight?: number;
  sparse_weight?: number;
  rerank_enabled?: boolean;
  rerank_top_n?: number;
}

const STATUS_COLOR: Record<string, string> = {
  ready: 'green', failed: 'red', pending: 'gold',
  parsing: 'blue', chunking: 'blue', embedding: 'blue',
};

/* ─── Bot Bindings Modal ─── */

function BotBindButton({ kbId }: { kbId: string }) {
  const [open, setOpen] = useState(false);
  const queryClient = useQueryClient();

  const { data: bindings, isLoading: bindingsLoading } = useQuery({
    queryKey: ['kb-bindings', kbId],
    queryFn: () => kbApi.listKBBindings(kbId).then((items) => items.filter((item) => item.target_type === 'bot')),
    enabled: open,
  });

  const { data: botsData } = useQuery({
    queryKey: ['bots'],
    queryFn: () => botApi.list(),
    enabled: open,
  });

  const bots = botsData?.list ?? [];
  const [selectedBotId, setSelectedBotId] = useState<string | undefined>();

  const bindMutation = useMutation({
    mutationFn: (botId: string) => kbApi.bindToBot(botId, kbId),
    onSuccess: () => {
      message.success('绑定成功');
      queryClient.invalidateQueries({ queryKey: ['kb-bindings', kbId] });
      setSelectedBotId(undefined);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '绑定失败'),
  });

  const unbindMutation = useMutation({
    mutationFn: (botId: string) => kbApi.unbindFromBot(botId, kbId),
    onSuccess: () => {
      message.success('已解绑');
      queryClient.invalidateQueries({ queryKey: ['kb-bindings', kbId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '解绑失败'),
  });

  const boundIds = new Set((bindings ?? []).map((b) => b.target_id));
  const availableBots = bots.filter((b) => !boundIds.has(b.id));

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
                bindings.map((b) => {
                  const bot = bots.find((bb) => bb.id === b.target_id);
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
                options={availableBots.map((b) => ({ value: b.id, label: b.name }))}
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

function ConvBindButton({ kbId }: { kbId: string }) {
  const [open, setOpen] = useState(false);
  const queryClient = useQueryClient();

  const { data: bindings, isLoading: bindingsLoading } = useQuery({
    queryKey: ['kb-conv-bindings', kbId],
    queryFn: () => kbApi.listKBBindings(kbId).then((items) => items.filter((item) => item.target_type === 'conv')),
    enabled: open,
  });

  const { data: convsData } = useQuery({
    queryKey: ['convs'],
    queryFn: () => convApi.list({ limit: 50 }),
    enabled: open,
  });

  const convs = convsData?.list ?? [];
  const [selectedConvId, setSelectedConvId] = useState<string | undefined>();

  const bindMutation = useMutation({
    mutationFn: (convId: string) => kbApi.bindToConv(convId, kbId),
    onSuccess: () => {
      message.success('绑定成功');
      queryClient.invalidateQueries({ queryKey: ['kb-conv-bindings', kbId] });
      setSelectedConvId(undefined);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '绑定失败'),
  });

  const unbindMutation = useMutation({
    mutationFn: (convId: string) => kbApi.unbindFromConv(convId, kbId),
    onSuccess: () => {
      message.success('已解绑');
      queryClient.invalidateQueries({ queryKey: ['kb-conv-bindings', kbId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '解绑失败'),
  });

  const boundIds = new Set((bindings ?? []).map((b) => b.target_id));
  const availableConvs = convs.filter((c) => !boundIds.has(c.id));

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
                bindings.map((b) => {
                  const conv = convs.find((cc) => cc.id === b.target_id);
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
                options={availableConvs.map((c) => ({ value: c.id, label: c.name || `会话 ${c.id}` }))}
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

  useEffect(() => {
    const events = ['knowledge.parsing', 'knowledge.chunking', 'knowledge.embedding', 'knowledge.ready', 'knowledge.failed'];
    const unsubscribe = events.map((event) => wsOn(event, (payload: { kb_id: string; doc_id: string }) => {
      if (payload.kb_id !== id) return;
      queryClient.invalidateQueries({ queryKey: ['kb-docs', id] });
      queryClient.invalidateQueries({ queryKey: ['kb', id] });
    }));
    return () => unsubscribe.forEach((off) => off());
  }, [id, queryClient]);

  const { data: kb, isLoading } = useQuery({
    queryKey: ['kb', id],
    queryFn: () => kbApi.get(id!),
    enabled: !!id,
  });

  const { data: docsData, isLoading: docsLoading } = useQuery({
    queryKey: ['kb-docs', id],
    queryFn: () => kbApi.listDocuments(id!),
    enabled: !!id,
  });

  const docs = docsData?.list ?? [];

  const deleteKBMutation = useMutation({
    mutationFn: () => kbApi.delete(id!),
    onSuccess: () => { message.success('知识库已删除'); navigate('/knowledge'); },
    onError: (err: any) => message.error(err?.response?.data?.message || '删除失败'),
  });


  const uploadMutation = useMutation({
    mutationFn: (file: File) => kbApi.uploadDocument(id!, file),
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

  return <RagView kb={kb} docs={docs} docsLoading={docsLoading} uploadMutation={uploadMutation} deleteKBMutation={deleteKBMutation} />;
}

/* ─── RAG View ─── */

function RagView({ kb, docs, docsLoading, uploadMutation, deleteKBMutation }: {
  kb: KBRsp;
  docs: DocumentRsp[];
  docsLoading: boolean;
  uploadMutation: UseMutationResult<DocumentRsp, Error, File>;
  deleteKBMutation: UseMutationResult<null, Error, void>;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: modelsData } = useQuery({
    queryKey: ['models'],
    queryFn: () => modelApi.list(),
  });
  const vlmModels = (modelsData?.list ?? []).filter((m) => m.capability === 'vlm');

  const retryMutation = useMutation({
    mutationFn: (docId: string) => kbApi.retryDocument(docId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['kb-docs', kb.id] }),
  });

  const pc: Partial<KBRsp['pipeline_config']> = kb.pipeline_config || {};

  const [editOpen, setEditOpen] = useState(false);
  const [editForm] = Form.useForm<EditKBFormValues>();
  const [changedModelFields, setChangedModelFields] = useState<Set<string>>(new Set());

  const editMutation = useMutation({
    mutationFn: (vals: UpdateKBReq) => kbApi.update(kb.id, vals),
    onSuccess: () => {
      message.success('知识库已更新');
      queryClient.invalidateQueries({ queryKey: ['kb', kb.id] });
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
            accept=".txt,.md,.html,.json,.xml,.csv,.yaml,.yml,.pdf,.doc,.docx,.ppt,.pptx,.xls,.xlsx,.png,.jpg,.jpeg,.bmp,.tiff"
            showUploadList={false}
            customRequest={({ file }) => uploadMutation.mutate(file as File)}
          >
            <Button size="small" icon={<UploadOutlined />} loading={uploadMutation.isPending}>上传文档</Button>
          </Upload>
          <BotBindButton kbId={kb.id} />
          <ConvBindButton kbId={kb.id} />
          <Button size="small" onClick={() => {
            const pc = kb.pipeline_config;
            const cc: Partial<ChunkingConfig> = pc?.chunking || {};
            const rc: Partial<RetrievalConfig> = pc?.retrieval || {};
            const vlm = pc?.parsing?.vlm;
            editForm.resetFields();
            setChangedModelFields(new Set());
            editForm.setFieldsValue({
              name: kb.name,
              description: kb.description,
              embedding_model_id: kb.embedding_model_id ?? undefined,
              engines: pc?.parsing?.engines,
              vlm_model_id: vlm?.model_id ?? undefined,
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
              rerank_model_id: rc.rerank?.model_id ?? undefined,
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
          const payload: UpdateKBReq = { name: vals.name, description: vals.description };
          const pipelineConfig: PipelineConfig = {};
          pipelineConfig.parsing = {
            engines: vals.engines?.length ? vals.engines : undefined,
            vlm: {
              enabled: changedModelFields.has('vlm_model_id') ? vals.vlm_model_id != null : pc.parsing?.vlm?.enabled,
              ...(changedModelFields.has('vlm_model_id')
                ? vals.vlm_model_id != null ? { model_id: vals.vlm_model_id } : { clear_model_id: true }
                : {}),
            },
          };
          pipelineConfig.chunking = {
            chunk_size: vals.parent_child_enabled ? undefined : (vals.chunk_size != null ? Number(vals.chunk_size) : undefined),
            overlap: vals.parent_child_enabled ? undefined : (vals.overlap != null ? Number(vals.overlap) : undefined),
            separators: vals.separators?.length ? vals.separators : undefined,
            parent_child: {
              enabled: !!vals.parent_child_enabled,
              parent_size: vals.parent_child_enabled ? (vals.parent_size != null ? Number(vals.parent_size) : undefined) : undefined,
              child_size: vals.parent_child_enabled ? (vals.child_size != null ? Number(vals.child_size) : undefined) : undefined,
            },
          };
          pipelineConfig.retrieval = {
            mode: vals.retrieval_mode,
            top_k: vals.top_k != null ? Number(vals.top_k) : undefined,
            candidate_top_k: vals.candidate_top_k != null ? Number(vals.candidate_top_k) : undefined,
            score_threshold: vals.score_threshold != null ? Number(vals.score_threshold) : undefined,
            dense_weight: vals.dense_weight != null ? Number(vals.dense_weight) : undefined,
            sparse_weight: vals.sparse_weight != null ? Number(vals.sparse_weight) : undefined,
            rerank: {
              enabled: !!vals.rerank_enabled,
              ...(changedModelFields.has('rerank_model_id')
                ? vals.rerank_model_id != null ? { model_id: vals.rerank_model_id } : { clear_model_id: true }
                : {}),
              top_n: vals.rerank_top_n != null ? Number(vals.rerank_top_n) : undefined,
            },
          };
          payload.pipeline_config = pipelineConfig;
          if (changedModelFields.has('embedding_model_id')) {
            if (vals.embedding_model_id == null) {
              payload.clear_embedding_model_id = true;
            } else {
              payload.embedding_model_id = vals.embedding_model_id;
              payload.embedding_model = modelsData?.list.find((model) => model.id === vals.embedding_model_id)?.model_name;
            }
          }
          editMutation.mutate(payload);
        })}
        onCancel={() => setEditOpen(false)} confirmLoading={editMutation.isPending}
        okText="保存" cancelText="取消" width={640}
      >
        <Form form={editForm} layout="vertical" style={{ paddingTop: 16 }} onValuesChange={(changed: Record<string, unknown>) => {
          setChangedModelFields((previous) => new Set([...previous, ...Object.keys(changed)]));
        }}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]} style={{ marginBottom: 12 }}><Input /></Form.Item>
          <Form.Item name="description" label="描述" style={{ marginBottom: 12 }}><Input.TextArea rows={2} /></Form.Item>
          <Form.Item name="embedding_model_id" label="Embedding 模型" style={{ marginBottom: 12 }}>
            <Select allowClear placeholder="未配置" options={(modelsData?.list ?? []).filter((model) => model.capability === 'embed').map((model) => ({ value: model.id, label: modelOptionLabel(model) }))} />
          </Form.Item>
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
            <Select allowClear placeholder="不启用" options={vlmModels.map((m) => ({ value: m.id, label: modelOptionLabel(m) }))} />
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
                <Form.Item name="rerank_model_id" label="Rerank 模型" style={{ flex: 1, marginBottom: 12 }}>
                  <Select allowClear placeholder="未配置" options={(modelsData?.list ?? []).filter((model) => model.capability === 'rerank').map((model) => ({ value: model.id, label: modelOptionLabel(model) }))} />
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
