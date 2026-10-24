import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Button, Checkbox, Input, Modal } from 'antd';
import { skillApi, type SkillVersionAction } from '@/services/skill';
import type { SkillVersion, SkillVersionPackage } from '@/types/model';
import type { SkillVersionActionReq } from '@/types/api';
import { submitSkillIntent } from '@/utils/skillSubmission';

const ACTION_LABEL = { confirm: '确认采用此版本', rollback: '回退到此版本', disable: '紧急禁用此版本' };

export function SkillVersionActions({ botId, pkg, identity, busy, onChanged, onFailure }: {
  botId: string;
  pkg: SkillVersionPackage;
  identity: string;
  busy: boolean;
  onChanged: (notice: string) => Promise<unknown>;
  onFailure: (error: unknown) => Promise<unknown>;
}) {
  const [selection, setSelection] = useState<{ action: SkillVersionAction; version: SkillVersion }>();
  const [reason, setReason] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState('');
  const mutation = useMutation({
    retry: false,
    mutationFn: ({ action, version, payload }: {
      action: SkillVersionAction; version: SkillVersion; payload: Omit<SkillVersionActionReq, 'submission_key'>;
    }) => submitSkillIntent(`${identity}:${botId}:${pkg.scope_key}:${pkg.skill_id}:${version.version_id}:${action}`, payload,
      (submission_key) => skillApi.versionAction(botId, version.version_id, action, { ...payload, submission_key })),
    onSuccess: async (_data, { action }) => {
      setSelection(undefined);
      setReason('');
      setConfirmed(false);
      await onChanged(action === 'disable'
        ? '禁用已保存：新使用及活动 Run 的后续调用被阻断，不自动替换版本。真实进程停止是否确认、外部副作用是否发生，请以 Run 的实际状态为准。'
        : action === 'rollback'
          ? '回退已保存，仅影响后续 Run；回退保护不会立即跳回已有新版。更高的新发布仍需重新核验兼容性。'
          : '版本确认已保存，仅作用于当前授权范围和后续 Run；已有 Run 不热切换版本。');
    },
    onError: async (failure: unknown) => {
      const value = failure as { response?: { status?: number; data?: { message?: string } }; message?: string };
      if (value.response?.status === 403 || value.response?.status === 409) setSelection(undefined);
      else setError(`${value.response?.data?.message || value.message || '操作结果尚未确认'}。相同意图重试保留提交键；不自动重试或更换摘要。`);
      await onFailure(failure);
    },
  });
  const newest = Math.max(0, ...pkg.versions.map((version) => version.version));
  const open = (action: SkillVersionAction, version: SkillVersion) => {
    setSelection({ action, version });
    setReason('');
    setConfirmed(false);
    setError('');
  };
  const reasonBytes = new TextEncoder().encode(reason.trim()).length;
  return <>
    <ul className="skill-version-list" aria-label="已发布版本与治理操作">
      {pkg.versions.map((version) => <li key={version.version_id}>
        <div><strong>v{version.version}</strong> · {version.status === 'disabled' ? '已禁用' : '已发布'}
          {pkg.enabled_version?.version_id === version.version_id ? ' · 当前有效' : ''}
          <div className="skill-version-secondary">{new Date(version.created_at * 1000).toLocaleString('zh-CN')} · 摘要 {version.digest}</div>
        </div>
        {pkg.can_manage && version.status === 'published' && <div className="skill-candidate-actions">
          {pkg.enabled_version?.version_id !== version.version_id && <>
            <Button disabled={busy || mutation.isPending} onClick={() => open('confirm', version)}>确认采用 v{version.version}</Button>
            {version.version < (pkg.enabled_version?.version ?? newest) &&
              <Button disabled={busy || mutation.isPending} onClick={() => open('rollback', version)}>回退到 v{version.version}</Button>}
          </>}
          <Button danger disabled={busy || mutation.isPending} onClick={() => open('disable', version)}>紧急禁用 v{version.version}</Button>
        </div>}
      </li>)}
    </ul>
    <Modal className="skill-version-modal" title={selection ? ACTION_LABEL[selection.action] : '版本治理'} open={!!selection}
      confirmLoading={mutation.isPending} maskClosable={false} cancelText="取消"
      onCancel={() => { if (!mutation.isPending) setSelection(undefined); }}
      okText={selection ? ACTION_LABEL[selection.action] : '确认'}
      okButtonProps={{ danger: selection?.action === 'disable', disabled: busy || !confirmed || reasonBytes === 0 || reasonBytes > 1024 }}
      onOk={() => {
        if (!selection || busy || !confirmed || !reasonBytes || reasonBytes > 1024 || mutation.isPending) return;
        setError('');
        mutation.mutate({ ...selection, payload: {
          digest: selection.version.digest, expected_revision: pkg.binding_revision, reason: reason.trim(),
        } });
      }}>
      {selection && <div className="skill-version-dialog">
        <p><strong>{pkg.name} · v{selection.version.version}</strong></p>
        <dl className="skill-candidate-meta"><dt>作用范围（服务器核验）</dt><dd>{pkg.scope_key}</dd>
          <dt>内容摘要</dt><dd>{selection.version.digest}</dd><dt>绑定修订</dt><dd>{pkg.binding_revision}</dd></dl>
        {selection.action === 'disable' ? <p role="alert">危险操作：禁用优先于 Run 冻结，立即阻断此版本的新使用和活动 Run 后续调用，并请求停止相关真实执行。不自动替换旧版继续执行。停止未确认仍是未知；已经发送、写入或产生的外部副作用不会因此回滚，不能宣称业务已撤销。</p>
          : selection.action === 'rollback' ? <p>仅让后续 Run 使用仍获准的旧版本；进行中、等待输入和审批后恢复的 Run 继续使用原冻结版本。回退保护保留当时发布水位，不会立即自动跳回已有新版；真正更高的新发布会重新进行兼容核验。</p>
            : <><p>请核对下列变更原因及当前权限。确认仅采用此确切摘要，不授予额外工具、材料或执行权限，也不能绕过执行前代码审查与验证。</p>
              {pkg.blocked_upgrade?.version.version_id === selection.version.version_id && <ul>{pkg.blocked_upgrade.reasons.map((item, index) => <li key={index}>{item}</li>)}</ul>}
              <p>只影响后续 Run，已有 Run 保留真实冻结版本。</p></>}
        <label className="skill-version-reason">操作原因（必填，最多 1024 UTF-8 字节）
          <Input.TextArea value={reason} disabled={mutation.isPending} rows={3} onChange={(event) => setReason(event.target.value)} />
        </label>
        {reasonBytes > 1024 && <p role="alert">原因过长，请缩短至 1024 UTF-8 字节以内。</p>}
        <Checkbox checked={confirmed} disabled={mutation.isPending} onChange={(event) => setConfirmed(event.target.checked)}>
          我已核对确切版本、摘要、范围与影响，并理解此操作不回滚外部副作用
        </Checkbox>
        {error && <p role="alert">{error}</p>}
      </div>}
    </Modal>
  </>;
}
