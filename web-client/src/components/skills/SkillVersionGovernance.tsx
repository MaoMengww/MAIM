import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Empty, Skeleton, Tag } from 'antd';
import { skillApi } from '@/services/skill';
import type { SkillVersionPackage } from '@/types/model';
import { useAuthStore } from '@/stores/auth';
import { SkillVersionActions } from './SkillVersionActions';
import './CandidateGovernance.css';

const ACTIVITY_LABEL: Record<string, string> = {
  confirm: '确认采用', rollback: '显式回退', disable: '紧急禁用',
  auto_upgrade: '兼容自动升级', publish: '发布',
};

export function SkillVersionGovernance({ botId }: { botId: string }) {
  const userId = useAuthStore((state) => state.user?.id);
  const revision = useAuthStore((state) => state.revision);
  if (!userId) return null;
  return <VersionQuery key={`${userId}:${revision}:${botId}`} botId={botId} identity={userId} revision={revision} />;
}

function VersionQuery({ botId, identity, revision }: { botId: string; identity: string; revision: number }) {
  const queryClient = useQueryClient();
  const queryKey = ['skill-versions', botId, identity, revision];
  const [notice, setNotice] = useState('');
  const [revoked, setRevoked] = useState(false);
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      try { return await skillApi.versions(botId, signal); }
      catch (error) {
        // Failed authority checks must never leave previous scope material cached.
        if (!signal.aborted) queryClient.setQueryData<SkillVersionPackage[]>(queryKey, []);
        throw error;
      }
    },
    retry: false, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  const refresh = async () => {
    const result = await query.refetch();
    if (!result.isError) setRevoked(false);
    return result;
  };
  const changed = async (value: string) => {
    setNotice(value);
    await query.refetch();
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['bot-skills', botId] }),
      queryClient.invalidateQueries({ queryKey: ['skill-catalog'] }),
      queryClient.invalidateQueries({ queryKey: ['run-skills'] }),
    ]);
  };
  const failed = async (error: unknown) => {
    const value = error as { response?: { status?: number; data?: { message?: string } }; message?: string };
    if (value.response?.status === 403) {
      setRevoked(true);
      setNotice('当前维护或来源材料权限已失效，已清除版本列表缓存。请刷新重新核验，不使用旧授权继续操作。');
      await queryClient.cancelQueries({ queryKey, exact: true });
      queryClient.setQueryData<SkillVersionPackage[]>(queryKey, []);
      queryClient.removeQueries({ queryKey: ['skill-catalog'] });
    } else if (value.response?.status === 409) {
      setNotice('版本绑定已变化（409）。已刷新当前版本与修订；旧操作没有自动改用新摘要，请重新审阅并明确提交。');
      await query.refetch();
    }
  };
  const error = query.error as { response?: { status?: number; data?: { message?: string } }; message?: string } | null;
  return <section className="skill-candidate skill-version-governance" aria-label="技能版本治理" aria-busy={query.isFetching}>
    <div className="skill-candidate-actions"><h3>技能版本治理</h3>
      <Button loading={query.isFetching} onClick={() => void refresh()}>刷新版本与当前权限</Button>
    </div>
    <p>发布不等于生效。兼容且已获批准的新版本自动用于后续 Run；进行中、等待输入和审批后恢复的 Run 保留各自真实冻结版本。这里只显示本次核验获准的 Bot 与来源范围。</p>
    {notice && <Alert type={revoked ? 'warning' : 'info'} showIcon message={notice} />}
    {query.isPending ? <div role="status" aria-label="正在核验版本与范围权限"><Skeleton active paragraph={{ rows: 3 }} /></div>
      : query.isError || revoked ? <div role="alert" className="skill-candidate-error">
        {error?.response?.status === 403 || revoked ? '无权读取当前版本或来源范围；缓存材料已清除。'
          : error?.response?.data?.message || error?.message || '版本治理列表读取失败。'} 请使用刷新按钮重新核验。
      </div>
        : !query.data?.length ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有获准查看的技能版本范围" />
          : query.data.map((pkg) => <article key={`${pkg.skill_id}:${pkg.scope_key}`} className="skill-version-package" aria-label={`${pkg.name} 版本范围`}>
            <div className="skill-candidate-actions"><strong>{pkg.name}</strong>
              {pkg.enabled_version ? <Tag color="green">当前有效 v{pkg.enabled_version.version}</Tag> : <Tag color="warning">无有效版本 · 不提供此能力</Tag>}
              {pkg.rollback_hold && <Tag color="blue">回退保护中</Tag>}
              {!pkg.can_manage && <Tag>只读 · 无治理操作权限</Tag>}
            </div>
            <dl className="skill-candidate-meta"><dt>授权范围</dt><dd>{pkg.scope_key === 'bot' ? 'Bot 范围' : `来源范围：${pkg.scope_key}`}</dd>
              <dt>绑定修订</dt><dd>{pkg.binding_revision}</dd>
              <dt>已发布版本</dt><dd>{pkg.versions.length ? pkg.versions.map((version) => `v${version.version}${version.status === 'disabled' ? '（已禁用）' : ''}`).join('、') : '无'}</dd>
              <dt>当前有效版本</dt><dd>{pkg.enabled_version ? `v${pkg.enabled_version.version} · ${pkg.enabled_version.digest}` : '无；拒绝使用，不静默切换版本、供应商或环境'}</dd>
            </dl>
            {pkg.blocked_upgrade && <div className="skill-version-blocked" role="status">
              <strong>升级受阻：v{pkg.blocked_upgrade.version.version}</strong>
              <ul>{pkg.blocked_upgrade.reasons.map((reason, index) => <li key={index}>{reason}</li>)}</ul>
              <p>{pkg.enabled_version ? `原获准且未禁用的 v${pkg.enabled_version.version} 仍有效。` : '当前无有效版本，能力不可用。'} 确认不自动扩大权限。</p>
            </div>}
            {pkg.rollback_hold && <p>回退保护：不会立即重新采用回退时已有的较新发布；真正更高的新发布仍按兼容性和当前授权处理。</p>}
            <SkillVersionActions key={`${pkg.binding_revision}:${pkg.versions.map((version) => `${version.version_id}:${version.status}`).join(',')}`}
              botId={botId} pkg={pkg} identity={identity} busy={query.isFetching}
              onChanged={changed} onFailure={failed} />
            <details className="skill-candidate-details"><summary>版本活动审计（{pkg.activities?.length ?? 0}）</summary>
              {!pkg.activities?.length ? <p>暂无可展示的版本治理活动。</p> : <ol className="skill-version-list">
                {pkg.activities.map((activity, index) => <li key={`${activity.created_at}:${activity.binding_revision}:${index}`}>
                  <strong>{ACTIVITY_LABEL[activity.action] || activity.action} · v{activity.version}</strong>
                  <p>{activity.reason || '未记录原因'}</p>
                  <div className="skill-version-secondary">操作者 {activity.actor_id} · {new Date(activity.created_at * 1000).toLocaleString('zh-CN')} · 修订 {activity.binding_revision}</div>
                  <div className="skill-version-secondary">版本 {activity.version_id} · 摘要 {activity.digest}</div>
                </li>)}
              </ol>}
            </details>
          </article>)}
  </section>;
}
