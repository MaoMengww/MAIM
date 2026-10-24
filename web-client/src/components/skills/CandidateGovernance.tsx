import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Tag } from 'antd';
import { skillCandidateApi } from '@/services/skillCandidate';
import { useAuthStore } from '@/stores/auth';
import { CandidateContent } from './CandidateContent';
import { CandidateActions } from './CandidateActions';
import './CandidateGovernance.css';

const STATUS: Record<string, string> = {
  pending_validation: '待验证', evaluating: '验证中', evaluated: '验证通过 · 待批准', validation_failed: '验证未通过',
  awaiting_evidence: '待补证', approved: '已批准 · 待发布', rejected: '已拒绝', published: '已发布', expired: '已过期',
};
const EVALUATION: Record<string, string> = { passed: '通过', failed: '不通过', insufficient: '证据不足', error: '环境故障（不是技能结论）' };

export function CandidateGovernance({ botId, candidateId }: { botId: string; candidateId: string }) {
  const userId = useAuthStore((state) => state.user?.id);
  const revision = useAuthStore((state) => state.revision);
  if (!userId) return <p role="alert">请登录后核验候选及来源材料权限。</p>;
  return <CandidateQuery key={`${userId}:${revision}:${botId}:${candidateId}`} botId={botId} candidateId={candidateId} userId={userId} revision={revision} />;
}

function CandidateQuery({ botId, candidateId, userId, revision }: { botId: string; candidateId: string; userId: string; revision: number }) {
  const queryClient = useQueryClient();
  const queryKey = ['skill-candidate', userId, revision, botId, candidateId];
  const query = useQuery({
    queryKey, queryFn: ({ signal }) => skillCandidateApi.get(botId, candidateId, signal),
    retry: false, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  const [now, setNow] = useState(Date.now);
  const [refreshed, setRefreshed] = useState(false);
  useEffect(() => {
    const expires = query.data?.expires_at;
    if (!expires || expires * 1000 <= now) return;
    const timer = window.setTimeout(() => setNow(Date.now()), Math.min(Math.max(expires * 1000 - Date.now(), 0), 2147483647));
    return () => window.clearTimeout(timer);
  }, [query.data?.expires_at, now]);
  const refresh = async () => {
    setRefreshed(true);
    return query.refetch();
  };
  const changed = async () => {
    await query.refetch();
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['skill-candidates', userId, revision, botId] }),
      queryClient.invalidateQueries({ queryKey: ['bot-skills', botId] }),
      queryClient.invalidateQueries({ queryKey: ['run-reviews'] }),
    ]);
  };
  const error = query.error as { message?: string; response?: { data?: { message?: string } } } | null;
  return <article className="skill-candidate" aria-label="候选验证、批准与发布" aria-busy={query.isFetching}>
    <div className="skill-candidate-actions"><strong>候选治理</strong>
      <Button loading={query.isFetching} onClick={() => void refresh()}>刷新内容与当前权限</Button>
    </div>
    {query.isPending ? <p role="status">正在核验维护、材料查看、代码审查和发布权限…</p>
      : query.isError ? <div className="skill-candidate-error" role="alert">{error?.response?.data?.message || error?.message || '候选不可访问。'} 不能显示缓存中的来源材料。</div>
        : !query.data || !query.data.can_view_materials ? <p role="alert">缺少来源材料许可，拒绝显示候选；Bot 所有权或脱敏不构成许可。</p>
          : query.data.expires_at * 1000 <= Math.max(now, Date.now()) ? <p role="alert">材料期限已到，候选材料已隐藏，不能继续验证、批准或发布。</p>
            : <>
              {refreshed && <p role="status">已主动刷新。请重新核对当前内容摘要与证据；旧提交不会自动使用新摘要重试。</p>}
              <p><strong>{query.data.name || query.data.target_goal}</strong> <Tag>{STATUS[query.data.status] || query.data.status}</Tag></p>
              <dl className="skill-candidate-meta" aria-label="当前分别核验的权限">
                <dt>维护</dt><dd>{query.data.can_maintain ? '已授权' : '未授权'}</dd>
                <dt>材料查看</dt><dd>本次读取已授权（不代表跨范围复用许可）</dd>
                <dt>执行前代码审查</dt><dd>{query.data.can_review_code ? '已授权' : '未授权'}</dd>
                <dt>发布</dt><dd>{query.data.can_publish ? '已授权' : '未授权'}</dd>
              </dl>
              <CandidateContent candidate={query.data} />
              <section aria-label="四类验证结果">
                <p>验证结论分为：通过 / 不通过 / 证据不足 / 环境故障。</p>
                {!query.data.evaluation ? <p role="status">尚无当前内容的有效验证，不能批准发布。</p> : <>
                  <p role="status"><strong>{EVALUATION[query.data.evaluation.status]}</strong>：{query.data.evaluation.reason}</p>
                  <dl className="skill-candidate-meta">
                    <dt>验证摘要</dt><dd>{query.data.evaluation.digest}</dd>
                    <dt>任务 / 预期摘要</dt><dd>{query.data.evaluation.task_digest} / {query.data.evaluation.expected_digest}</dd>
                    <dt>产物摘要</dt><dd>{query.data.evaluation.output_digest || '未产生已核对产物'}</dd>
                    <dt>模型身份</dt><dd>{query.data.evaluation.model_version || '脚本验证不使用模型'}</dd>
                    <dt>环境身份</dt><dd>{query.data.evaluation.environment_digest || '未记录独立环境摘要'}</dd>
                    <dt>验证时间</dt><dd>{new Date(query.data.evaluation.created_at * 1000).toLocaleString('zh-CN')}</dd>
                  </dl>
                </>}
              </section>
              {query.data.published_version_id && <p role="status">已发布不可变版本：{query.data.published_version_id}。重复发布请求不会创建重复版本。</p>}
              <CandidateActions key={`${query.data.digest}:${query.data.status}:${query.data.code_review_digest}:${query.data.evaluation?.digest || ''}`}
                botId={botId} candidate={query.data} identity={userId} onChanged={changed} />
            </>}
  </article>;
}

export function SourceCandidateList({ botId }: { botId: string }) {
  const userId = useAuthStore((state) => state.user?.id);
  const revision = useAuthStore((state) => state.revision);
  const [selected, setSelected] = useState('');
  const query = useQuery({
    queryKey: ['skill-candidates', userId, revision, botId],
    queryFn: ({ signal }) => skillCandidateApi.list(botId, signal), enabled: !!userId,
    retry: false, gcTime: 0, refetchOnWindowFocus: false,
  });
  return <section className="skill-candidate" aria-label="来源 Run 经验候选">
    <div className="skill-candidate-actions"><strong>来源 Run 经验候选</strong>
      <Button loading={query.isFetching} onClick={() => void query.refetch()}>刷新来源候选列表</Button>
    </div>
    <p>仅列出当前拥有维护和来源材料许可的候选；可在这里或来源会话使用同一治理接口。</p>
    {query.isPending ? <p role="status">正在读取授权候选…</p>
      : query.isError ? <p role="alert">来源候选列表读取失败或权限已变化；不展示缓存材料，请刷新重试。</p>
        : <>
          {query.data?.filter((candidate) => candidate.can_view_materials && candidate.expires_at * 1000 > Date.now()).map((candidate) => <div key={candidate.id} className="skill-candidate-list-row">
            <Button aria-expanded={selected === candidate.id} onClick={() => setSelected(selected === candidate.id ? '' : candidate.id)}>
              {selected === candidate.id ? '收起' : '审阅'}：{candidate.name || candidate.target_goal} · {STATUS[candidate.status] || candidate.status}
            </Button>
            {selected === candidate.id && <CandidateGovernance botId={botId} candidateId={candidate.id} />}
          </div>)}
          {query.data?.length === 0 && <p role="status">当前没有获准维护和查看的来源候选。</p>}
        </>}
  </section>;
}
