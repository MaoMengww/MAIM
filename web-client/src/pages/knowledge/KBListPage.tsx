import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Modal, Form, Input, InputNumber, Select, Switch, Collapse, message, Tag, Empty } from 'antd';
import { modelApi } from '@/services/model';
import { kbApi } from '@/services/knowledge';
import { displayProvider, modelOptionLabel } from '@/utils/provider';
import { WikiIcon, FolderIcon, InfoIcon } from '@/components/common/Icons';
import { WikiMaintenanceConfig } from './WikiMaintenanceConfig';

import './KBPage.css';

const PRESET_OPTIONS = [
  { value: 'general', label: '通用' },
  { value: 'tech_doc', label: '技术文档' },
  { value: 'legal', label: '法律文书' },
  { value: 'academic', label: '学术论文' },
  { value: 'customer_service', label: '客服问答' },
];

const PRESET_VALUES: Record<string, Record<string, any>> = {
    general: {
        parsing_engines: ['builtin'],
        chunk_size: 512, overlap: 50,
        separators: ['\n\n', '\n', '。'],
        parent_child_enabled: false,
        retrieval_mode: 'hybrid', top_k: 5, candidate_top_k: 20,
        score_threshold: 0.7, rerank_enabled: false,
        dense_weight: 0.7, sparse_weight: 0.3,
    },
    tech_doc: {
        parsing_engines: ['mineru_precision', 'builtin'],
        chunk_size: 512, overlap: 50,
        separators: ['\n\n', '\n', '。'],
        parent_child_enabled: true, parent_size: 4096, child_size: 384,
        retrieval_mode: 'hybrid', top_k: 5, candidate_top_k: 30,
        score_threshold: 0.65, rerank_enabled: false,
        dense_weight: 0.8, sparse_weight: 0.2,
    },
    legal: {
        parsing_engines: ['mineru_precision', 'builtin'],
        chunk_size: 1024, overlap: 100,
        separators: ['\n\n', '\n', '。'],
        parent_child_enabled: false,
        retrieval_mode: 'hybrid', top_k: 3, candidate_top_k: 15,
        score_threshold: 0.75, rerank_enabled: false,
        dense_weight: 0.7, sparse_weight: 0.3,
    },
    academic: {
        parsing_engines: ['mineru_precision', 'builtin'],
        chunk_size: 512, overlap: 50,
        separators: ['\n\n', '\n', '。'],
        parent_child_enabled: true, parent_size: 4096, child_size: 384,
        retrieval_mode: 'hybrid', top_k: 5, candidate_top_k: 30,
        score_threshold: 0.65, rerank_enabled: false,
        dense_weight: 0.6, sparse_weight: 0.4,
    },
    customer_service: {
        parsing_engines: ['builtin'],
        chunk_size: 256, overlap: 30,
        separators: ['\n', '。'],
        parent_child_enabled: false,
        retrieval_mode: 'vector', top_k: 3, candidate_top_k: 10,
        score_threshold: 0.6, rerank_enabled: false,
        dense_weight: 0.7, sparse_weight: 0.3,
    },
};

const RETRIEVAL_MODES = [
  { value: 'hybrid', label: '混合检索 (向量+全文)' },
  { value: 'vector', label: '向量检索' },
  { value: 'fulltext', label: '全文检索' },
];

const EMBED_CHOICES = [
  { value: 'text-embedding-v4', label: 'text-embedding-v4 (千问)' },
];

const PAGE_TYPE_LABEL: Record<string, string> = {
  summary: '概览',
  entity: '实体',
  concept: '概念',
  synthesis: '综述',
  comparison: '对比',
  index: '索引',
  log: '日志',
};

const FM = { marginBottom: 12 };

function iconLabel(icon: string, text: string) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center' }}>
      <InfoIcon size={14} style={{ marginRight: 6 }} />
      {text}
    </span>
  );
}

export function KBListPage() {
  const navigate = useNavigate();
  const [createOpen, setCreateOpen] = useState(false);
  const [form] = Form.useForm();
  const [creating, setCreating] = useState(false);
  const selectedMode = Form.useWatch('mode', form) || 'rag';

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['knowledge-bases'],
    queryFn: () => kbApi.list(),
  });

  const { data: modelsData } = useQuery({
    queryKey: ['models'],
    queryFn: () => modelApi.list(),
  });

  const items = data?.list ?? [];

  const embedModelOptions = (modelsData?.list ?? [])
    .filter((m: any) => m.capability === 'embedding')
    .map((m: any) => ({ value: m.id, label: modelOptionLabel(m), model_name: m.model_name }));

  const chatModelOptions = (modelsData?.list ?? [])
    .filter((m: any) => m.capability === 'chat')
    .map((m: any) => ({ value: m.id, label: modelOptionLabel(m), model_name: m.model_name }));

  const vlmModelOptions = (modelsData?.list ?? [])
    .filter((m: any) => m.capability === 'vlm')
    .map((m: any) => ({ value: m.id, label: modelOptionLabel(m), model_name: m.model_name }));

  const rerankModelOptions = (modelsData?.list ?? [])
    .filter((m: any) => m.capability === 'rerank')
    .map((m: any) => ({ value: m.id, label: modelOptionLabel(m), model_name: m.model_name }));

  const modelMap = Object.fromEntries((modelsData?.list ?? []).map((m: any) => [m.id, m.model_name]));

  const handleCreate = async () => {
    try {
      const vals = await form.validateFields();
      setCreating(true);

      const mode = vals.mode || 'rag';
      const pipelineConfig: any = {};

      if (mode === 'rag') {
        if (vals.preset) pipelineConfig.preset = vals.preset;
        if (vals.chunk_size || vals.overlap != null || vals.separators?.length || vals.parent_child_enabled) {
          pipelineConfig.chunking = {};
          if (vals.chunk_size) pipelineConfig.chunking.chunk_size = vals.chunk_size;
          if (vals.overlap != null) pipelineConfig.chunking.overlap = vals.overlap;
          if (vals.separators?.length) pipelineConfig.chunking.separators = vals.separators;
          if (vals.parent_child_enabled) {
            pipelineConfig.chunking.parent_child = { enabled: true };
            if (vals.parent_size) pipelineConfig.chunking.parent_child.parent_size = vals.parent_size;
            if (vals.child_size) pipelineConfig.chunking.parent_child.child_size = vals.child_size;
          }
        }
        if (vals.parsing_engines?.length) {
          const parsing: Record<string, any> = { engines: vals.parsing_engines };
          if (vals.mineru_api_url) {
            parsing.mineru_precision = { api_url: vals.mineru_api_url, api_token: vals.mineru_api_token || '' };
          }
          if (vals.mineru_agent_url) {
            parsing.mineru_agent = { api_url: vals.mineru_agent_url };
          }
          if (vals.vlm_model_id) {
            parsing.vlm = { model_id: vals.vlm_model_id, enabled: true };
          }
          pipelineConfig.parsing = parsing;
        }
        pipelineConfig.retrieval = {};
        if (vals.retrieval_mode) pipelineConfig.retrieval.mode = vals.retrieval_mode;
        if (vals.top_k) pipelineConfig.retrieval.top_k = vals.top_k;
        if (vals.candidate_top_k) pipelineConfig.retrieval.candidate_top_k = vals.candidate_top_k;
        if (vals.score_threshold != null) pipelineConfig.retrieval.score_threshold = vals.score_threshold;
        if (vals.dense_weight != null) pipelineConfig.retrieval.dense_weight = vals.dense_weight;
        if (vals.sparse_weight != null) pipelineConfig.retrieval.sparse_weight = vals.sparse_weight;
        if (vals.rerank_enabled) {
          pipelineConfig.retrieval.rerank = { enabled: true, model_id: vals.rerank_model || 0, top_n: vals.rerank_top_n || 20 };
        }
        if (Object.keys(pipelineConfig.retrieval).length === 0) delete pipelineConfig.retrieval;
        if (Object.keys(pipelineConfig.chunking || {}).length === 0) delete pipelineConfig.chunking;
      } else {
        const wikiConfig: Record<string, any> = {
          enabled: true,
          model_id: vals.wiki_model_id || 14,
          model_name: vals.wiki_model_id ? (modelMap[vals.wiki_model_id] || '') : 'qwen-plus',
          auto_lint: vals.wiki_auto_lint ?? true,
          stale_threshold_hours: vals.wiki_stale_hours || 168,
        };
        if (vals.maintenance?.maintenance_enabled) {
          wikiConfig.maintenance_enabled = true;
          wikiConfig.maintenance_cron = vals.maintenance.maintenance_cron;
        }
        pipelineConfig.wiki = wikiConfig;
      }

      const embedModelId = mode === 'rag' ? vals.embedding_model : undefined;
      const kb = await kbApi.create({
        name: vals.name,
        description: vals.description,
        embedding_model: embedModelId ? (modelMap[embedModelId] || '') : undefined,
        embedding_model_id: embedModelId || 0,
        pipeline_config: Object.keys(pipelineConfig).length > 0 ? pipelineConfig : undefined,
        mode,
      });
      message.success('知识库创建成功');
      setCreateOpen(false);
      form.resetFields();
      refetch();
      navigate(`/knowledge/${kb.id}`);
    } catch (err: any) {
      if (err.errorFields) return;
      message.error(err?.response?.data?.message || '创建失败');
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="kb-page">
      <div className="kb-panel">
        <div className="kb-header">
          <h2>知识库</h2>
          <button className="kb-create-btn" onClick={() => setCreateOpen(true)}>
            创建知识库
          </button>
        </div>

        {isLoading && <div className="kb-empty">加载中...</div>}

        {!isLoading && items.length === 0 && (
          <div className="kb-empty">
            <FolderIcon size={48} style={{ color: 'var(--aim-text-tertiary)', marginBottom: 16 }} />
            <div style={{ fontSize: 16, fontWeight: 600, color: 'var(--aim-text)', marginBottom: 8 }}>暂无知识库</div>
            <p style={{ fontSize: 13, color: 'var(--aim-text-tertiary)', marginBottom: 24, textAlign: 'center' }}>
              创建知识库来为 AI Bot 提供专属知识<br />支持文档上传、向量检索、Wiki 自动生成
            </p>
            <button className="kb-create-btn" onClick={() => setCreateOpen(true)}>
              创建知识库
            </button>
          </div>
        )}

        {!isLoading && items.length > 0 && (
          <div className="kb-list">
            {items.map((kb) => (
            <div
              key={kb.id}
              className="kb-card"
              onClick={() => navigate(`/knowledge/${kb.id}`)}
            >
              <div className="kb-card-top">
                <div className="kb-card-icon">
                  {kb.mode === 'wiki' ? <WikiIcon size={28} style={{ color: 'var(--aim-primary)' }} /> : <FolderIcon size={28} style={{ color: '#52c41a' }} />}
                </div>
                <div className="kb-card-info">
                  <div className="kb-card-name">
                    {kb.name}
                    <Tag color={kb.mode === 'wiki' ? 'blue' : 'green'} style={{ marginLeft: 8, fontSize: 11 }}>
                      {kb.mode === 'wiki' ? 'WIKI' : 'RAG'}
                    </Tag>
                  </div>
                  {kb.description && <div className="kb-card-desc">{kb.description}</div>}
                </div>
                <div className="kb-card-stats">
                  {kb.mode === 'rag' ? (
                    <>
                      <span>{kb.doc_count ?? 0} 文档</span>
                      <span style={{ marginLeft: 12 }}>{kb.total_chunks ?? 0} 切片</span>
                    </>
                  ) : (
                    <>
                      <span>{kb.doc_count ?? 0} 文档</span>
                    </>
                  )}
                </div>
              </div>
              <div className="kb-card-meta">
                {kb.mode === 'rag' ? (
                  <span>模型: {kb.embedding_model || '默认'}</span>
                ) : (
                  <span>Wiki 模型: {kb.pipeline_config?.wiki?.model_name || '默认'}</span>
                )}
                <span style={{ marginLeft: 16 }}>
                  状态: <Tag color={kb.status === 'active' ? 'green' : 'default'} style={{ margin: 0 }}>{kb.status}</Tag>
                </span>
              </div>
            </div>
            ))}
          </div>
        )}

      </div>

      <Modal
        title="创建知识库"
        open={createOpen}
        onOk={handleCreate}
        onCancel={() => { setCreateOpen(false); form.resetFields(); }}
        confirmLoading={creating}
        okText="创建"
        cancelText="取消"
        width={640}
        styles={{ body: { paddingTop: 16 } }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]} style={FM}>
            <Input placeholder="知识库名称" />
          </Form.Item>
          <Form.Item name="description" label="描述" style={FM}>
            <Input.TextArea rows={2} placeholder="选填" />
          </Form.Item>

          <Form.Item name="mode" label="类型" initialValue="rag" style={{ marginBottom: 16 }}>
            <Select
              options={[
                { value: 'rag', label: 'RAG 知识库 — 文档向量化 + 语义检索' },
                { value: 'wiki', label: 'Wiki 知识库 — 文档摘要 + 结构化页面' },
              ]}
            />
          </Form.Item>

          {selectedMode === 'rag' ? renderRagConfig({ form, embedModelOptions, vlmModelOptions, rerankModelOptions }) : renderWikiConfig({ chatModelOptions })}
        </Form>
      </Modal>
    </div>
  );
}

/* ─── RAG ─── */

function renderRagConfig({ form, embedModelOptions, vlmModelOptions, rerankModelOptions }: { form: any; embedModelOptions: any[]; vlmModelOptions: any[]; rerankModelOptions: any[] }) {
  const collapseItems = [
    {
      key: 'parsing',
      label: '解析策略',
      children: (
        <div style={{ paddingTop: 4 }}>
          <Form.Item name="parsing_engines" label="解析引擎（按优先级排序）" style={FM}>
            <Select mode="multiple" allowClear placeholder="默认 builtin"
              options={[
                { value: 'builtin', label: 'Builtin（内置，支持 TXT/MD/HTML/CSV）' },
                { value: 'mineru_precision', label: 'MinerU 精准解析（PDF/DOCX/图片）' },
                { value: 'mineru_agent', label: 'MinerU 轻量解析（PDF/DOCX/图片）' },
              ]}
            />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(prev, cur) => prev.parsing_engines !== cur.parsing_engines}>
            {({ getFieldValue }: any) => {
              const engines: string[] = getFieldValue('parsing_engines') || [];
              const hasPrecision = engines.includes('mineru_precision');
              const hasAgent = engines.includes('mineru_agent');
              return (hasPrecision || hasAgent) ? (
                <>
                  <div style={{ borderTop: '1px solid var(--aim-border)', paddingTop: 8, marginTop: 0 }}>
                    {hasPrecision && (
                      <div style={{ marginBottom: 8 }}>
                        <div style={{ fontSize: 12, color: 'var(--aim-text-secondary)', marginBottom: 8 }}>MinerU 精准解析配置</div>
                        <Form.Item name="mineru_api_url" label="API URL" style={FM} initialValue="http://localhost:30000">
                          <Input placeholder="http://localhost:30000" />
                        </Form.Item>
                        <Form.Item name="mineru_api_token" label="API Token" style={FM}>
                          <Input.Password placeholder="必填" />
                        </Form.Item>
                      </div>
                    )}
                    {hasAgent && (
                      <div style={{ marginBottom: 8 }}>
                        <div style={{ fontSize: 12, color: 'var(--aim-text-secondary)', marginBottom: 8 }}>MinerU 轻量解析配置</div>
                        <Form.Item name="mineru_agent_url" label="API URL" style={FM} initialValue="http://localhost:30000">
                          <Input placeholder="http://localhost:30000/api/v1/agent/parse/file" />
                        </Form.Item>
                      </div>
                    )}
                  </div>
                </>
              ) : null;
            }}
          </Form.Item>
          <Form.Item name="vlm_model_id" label="VLM 图片识别模型" tooltip="用于识别文档中的图片内容" style={FM}>
            <Select
              allowClear placeholder="选择 VLM 模型（选填）"
              options={vlmModelOptions}
              showSearch
              filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}
            />
          </Form.Item>
        </div>
      ),
    },
    {
      key: 'chunking',
      label: '切片配置',
      children: (
        <div style={{ paddingTop: 4 }}>
          <Form.Item name="separators" label="分隔符" style={FM}>
            <Select mode="multiple" allowClear placeholder="默认 [\n\n, \n, 。]"
              options={[
                { value: '\n\n', label: '\\n\\n（段落）' },
                { value: '\n', label: '\\n（换行）' },
                { value: '。', label: '。（句号）' },
                { value: '！', label: '！（叹号）' },
                { value: '？', label: '？（问号）' },
                { value: '；', label: '；（分号）' },
                { value: ';', label: ';（分号）' },
                { value: '，', label: '，（逗号）' },
                { value: '、', label: '、（顿号）' },
                { value: '：', label: '：（冒号）' },
                { value: '）', label: '）（括号）' },
                { value: '】', label: '】』」' },
                { value: '——', label: '——（破折号）' },
                { value: '…', label: '…（省略号）' },
                { value: '·', label: '·（间隔号）' },
                { value: ' ', label: '（空格）' },
                { value: '\t', label: '\\t（制表符）' },
                { value: '\r\n', label: '\\r\\n（Windows 换行）' },
              ]}
            />
          </Form.Item>
          <div style={{ borderTop: '1px solid var(--aim-border)', paddingTop: 8, marginTop: 0 }}>
            <Form.Item name="parent_child_enabled" label="父子分块" valuePropName="checked" style={FM}><Switch /></Form.Item>
            <Form.Item noStyle shouldUpdate={(prev, cur) => prev.parent_child_enabled !== cur.parent_child_enabled}>
              {({ getFieldValue }: any) => getFieldValue('parent_child_enabled') ? (
                <div style={{ display: 'flex', gap: 16 }}>
                  <Form.Item name="parent_size" label="父块大小" style={{ flex: 1, marginBottom: 0 }}>
                    <InputNumber min={256} max={4096} step={128} placeholder="默认 4096" style={{ width: '100%' }} />
                  </Form.Item>
                  <Form.Item name="child_size" label="子块大小" style={{ flex: 1, marginBottom: 0 }}>
                    <InputNumber min={64} max={1024} step={32} placeholder="默认 384" style={{ width: '100%' }} />
                  </Form.Item>
                </div>
              ) : (
                <div style={{ display: 'flex', gap: 16 }}>
                  <Form.Item name="chunk_size" label="切片大小" style={{ flex: 1, marginBottom: 0 }}>
                    <InputNumber min={100} max={4000} step={64} placeholder="默认 512" style={{ width: '100%' }} />
                  </Form.Item>
                  <Form.Item name="overlap" label="切片重叠" style={{ flex: 1, marginBottom: 0 }}>
                    <InputNumber min={0} max={500} step={16} placeholder="默认 50" style={{ width: '100%' }} />
                  </Form.Item>
                </div>
              )}
            </Form.Item>
          </div>
        </div>
      ),
    },
    {
      key: 'retrieval',
      label: '检索配置',
      children: (
        <div style={{ paddingTop: 4 }}>
          <Form.Item name="retrieval_mode" label="检索模式" style={FM}>
            <Select options={RETRIEVAL_MODES} placeholder="默认混合检索" allowClear />
          </Form.Item>
          <div style={{ display: 'flex', gap: 16 }}>
            <Form.Item name="top_k" label="返回结果数" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={1} max={50} placeholder="默认 5" style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="candidate_top_k" label="候选结果数" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={1} max={100} placeholder="默认 20" style={{ width: '100%' }} />
            </Form.Item>
          </div>
          <Form.Item name="score_threshold" label="分数阈值" initialValue={0.7} style={FM}>
            <InputNumber min={0} max={1} step={0.05} placeholder="默认 0.7" style={{ width: '100%' }} />
          </Form.Item>
          <div style={{ display: 'flex', gap: 16 }}>
            <Form.Item name="dense_weight" label="向量权重" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={0} max={1} step={0.1} placeholder="默认 0.7" style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="sparse_weight" label="全文权重" style={{ flex: 1, marginBottom: 12 }}>
              <InputNumber min={0} max={1} step={0.1} placeholder="默认 0.3" style={{ width: '100%' }} />
            </Form.Item>
          </div>
          <div style={{ borderTop: '1px solid var(--aim-border)', paddingTop: 8, marginTop: 0 }}>
            <Form.Item name="rerank_enabled" label="启用重排序" valuePropName="checked" style={FM}><Switch /></Form.Item>
            <Form.Item noStyle shouldUpdate={(prev, cur) => prev.rerank_enabled !== cur.rerank_enabled}>
              {({ getFieldValue }: any) => getFieldValue('rerank_enabled') ? (
                <div style={{ display: 'flex', gap: 16 }}>
                  <Form.Item name="rerank_model" label="重排序模型" style={{ flex: 1, marginBottom: 0 }}>
                    <Select placeholder="选择模型" allowClear options={rerankModelOptions} showSearch
                      filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())} />
                  </Form.Item>
                  <Form.Item name="rerank_top_n" label="Top N" style={{ width: 120, marginBottom: 0 }}>
                    <InputNumber min={1} max={100} placeholder="20" style={{ width: '100%' }} />
                  </Form.Item>
                </div>
              ) : null}
            </Form.Item>
          </div>
        </div>
      ),
    },
  ];

  return (
    <>
      <Form.Item name="embedding_model" label="向量模型" style={FM}>
        <Select
          placeholder="选择向量模型"
          allowClear
          options={embedModelOptions.length > 0 ? embedModelOptions : EMBED_CHOICES}
          showSearch
          filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}
        />
      </Form.Item>
      <Form.Item name="preset" label="处理预设" style={FM}>
        <Select
          placeholder="选择预设（默认通用）"
          allowClear options={PRESET_OPTIONS}
          onChange={(value) => {
            if (value && PRESET_VALUES[value]) {
              form.setFieldsValue(PRESET_VALUES[value]);
            }
          }}
        />
      </Form.Item>

      <Collapse ghost expandIconPosition="end" size="small" items={collapseItems} />
    </>
  );
}

/* ─── Wiki ─── */

function renderWikiConfig({ chatModelOptions }: { chatModelOptions: any[] }) {
  return (
    <>
      <Form.Item name="wiki_model_id" label="LLM 模型" tooltip="用于 wiki 页面生成的模型" style={FM}>
        <Select
          placeholder="gpt-4o"
          allowClear
          showSearch
          options={chatModelOptions}
          filterOption={(input, option) => (option?.model_name ?? '').toLowerCase().includes(input.toLowerCase())}
        />
      </Form.Item>
      <div style={{ display: 'flex', gap: 16 }}>
        <Form.Item name="wiki_auto_lint" label="自动检查" valuePropName="checked" initialValue={true} style={{ flex: 1, marginBottom: 12 }}>
          <Switch />
        </Form.Item>
        <Form.Item name="wiki_stale_hours" label="过期阈值(小时)" initialValue={168} style={{ flex: 1, marginBottom: 12 }}>
          <InputNumber min={1} max={8760} placeholder="默认 168 (7天)" style={{ width: '100%' }} />
        </Form.Item>
      </div>
      <Form.Item name="maintenance" label="自动维护" style={{ marginBottom: 16 }}>
        <WikiMaintenanceConfig />
      </Form.Item>
      <div style={{ padding: '6px 10px', background: 'var(--aim-surface)', borderRadius: 6, fontSize: 12, color: 'var(--aim-text-secondary)' }}>
        <InfoIcon size={12} style={{ marginRight: 4 }} />
        Wiki 知识库创建后自动对上传的文档做摘要、提取实体/概念，生成结构化 wiki 页面。创建后类型不可更改。
      </div>
    </>
  );
}

export { PAGE_TYPE_LABEL };
