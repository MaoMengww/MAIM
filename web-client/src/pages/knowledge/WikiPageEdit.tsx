import { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Button, Spin, message, Input, Tag, Breadcrumb, Select } from 'antd';
import { kbApi } from '@/services/knowledge';

const PAGE_TYPE_OPTIONS = [
  { value: 'summary', label: '概览' },
  { value: 'entity', label: '实体' },
  { value: 'concept', label: '概念' },
  { value: 'synthesis', label: '综述' },
  { value: 'comparison', label: '对比' },
];

export function WikiPageEdit() {
  const params = useParams();
  const kbId = params.kbId;
  const slug = (params['*'] || '').replace(/\/edit$/, '');
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [summary, setSummary] = useState('');
  const [pageType, setPageType] = useState('entity');
  const [aliasesText, setAliasesText] = useState('');
  const [customSlug, setCustomSlug] = useState('');
  const [isNew, setIsNew] = useState(false);

  const { data: page, isLoading } = useQuery({
    queryKey: ['wiki-page', kbId, slug],
    queryFn: () => kbApi.wikiReadPage(Number(kbId), slug!),
    enabled: !!kbId && !!slug && slug !== 'new',
    retry: false,
  });

  useEffect(() => {
    if (page) {
      setTitle(page.title);
      setContent(page.content);
      setSummary(page.summary || '');
      setPageType(page.page_type);
      setAliasesText((page.aliases || []).join(', '));
      setIsNew(false);
    }
  }, [page]);

  useEffect(() => {
    if (slug === 'new') {
      setIsNew(true);
      setTitle('');
      setContent('');
      setSummary('');
      setPageType('entity');
      setAliasesText('');
      setCustomSlug('');
    }
  }, [slug]);

  const saveMutation = useMutation({
    mutationFn: () => {
      const aliases = aliasesText.split(',').map((s: string) => s.trim()).filter(Boolean);
      return kbApi.wikiUpdatePage(Number(kbId), effectiveSlug, {
        title, content, summary,
        aliases: aliases.length > 0 ? aliases : undefined,
      });
    },
    onSuccess: () => {
      message.success('保存成功');
      queryClient.invalidateQueries({ queryKey: ['wiki-page', kbId, isNew ? slug! : slug] });
      queryClient.invalidateQueries({ queryKey: ['wiki-pages', kbId] });
      navigate(`/knowledge/${kbId}/wiki/${isNew ? slug! : slug}`);
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '保存失败'),
  });

  const generateSlug = (title: string) => {
    if (!title) return '';
    return pageType + '/' + title.toLowerCase().replace(/\s+/g, '_').replace(/[^\w一-鿿]/g, '');
  };

  const effectiveSlug = customSlug || generateSlug(title);

  if (isLoading) {
    return <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}><Spin /></div>;
  }

  return (
    <div style={{ padding: 32, width: '100%', overflowY: 'auto' }}>
      <Breadcrumb style={{ marginBottom: 16 }} items={[
        { title: <a onClick={() => navigate('/knowledge')}>知识库</a> },
        { title: <a onClick={() => navigate(`/knowledge/${kbId}`)}>返回</a> },
        { title: isNew ? '新建页面' : `编辑: ${page?.title || ''}` },
      ]} />

      <h1 style={{ fontSize: 20, fontWeight: 600, marginBottom: 24 }}>
        {isNew ? '新建 Wiki 页面' : '编辑 Wiki 页面'}
      </h1>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div>
          <div style={{ marginBottom: 6, fontSize: 13, color: 'var(--aim-text-secondary)' }}>标题</div>
          <Input value={title} onChange={e => setTitle(e.target.value)} placeholder="页面标题" />
        </div>

        <div style={{ display: 'flex', gap: 16 }}>
          <div style={{ flex: 1 }}>
            <div style={{ marginBottom: 6, fontSize: 13, color: 'var(--aim-text-secondary)' }}>类型</div>
            <Select value={pageType} onChange={setPageType} options={PAGE_TYPE_OPTIONS} style={{ width: '100%' }} />
          </div>
          {isNew && (
            <div style={{ flex: 1 }}>
              <div style={{ marginBottom: 6, fontSize: 13, color: 'var(--aim-text-secondary)' }}>标识 (slug)</div>
              <Input value={customSlug}
                onChange={e => setCustomSlug(e.target.value)}
                placeholder={generateSlug(title) || 'entity/xxx'}
              />
              <div style={{ fontSize: 11, color: 'var(--aim-text-tertiary)', marginTop: 4 }}>
                不填则根据标题自动生成
              </div>
            </div>
          )}
        </div>

        <div>
          <div style={{ marginBottom: 6, fontSize: 13, color: 'var(--aim-text-secondary)' }}>摘要</div>
          <Input.TextArea value={summary} onChange={e => setSummary(e.target.value)} rows={2} placeholder="页面摘要（200字以内）" />
        </div>

        <div>
          <div style={{ marginBottom: 6, fontSize: 13, color: 'var(--aim-text-secondary)' }}>
            内容 <Tag style={{ marginLeft: 8, fontSize: 11 }}>支持 [[slug]] 语法创建内部链接</Tag>
          </div>
          <Input.TextArea
            value={content}
            onChange={e => setContent(e.target.value)}
            rows={16}
            placeholder="页面内容，使用 [[entity/xxx]] 引用其他页面"
            style={{ fontFamily: 'monospace', fontSize: 14 }}
          />
        </div>

        <div>
          <div style={{ marginBottom: 6, fontSize: 13, color: 'var(--aim-text-secondary)' }}>同义词/别名（逗号分隔）</div>
          <Input value={aliasesText} onChange={e => setAliasesText(e.target.value)} placeholder="别名1, 别名2" />
        </div>

        <div style={{ display: 'flex', gap: 12, marginTop: 8 }}>
          <Button type="primary" onClick={() => saveMutation.mutate()} loading={saveMutation.isPending}>
            保存
          </Button>
          <Button onClick={() => navigate(-1)}>取消</Button>
        </div>
      </div>
    </div>
  );
}
