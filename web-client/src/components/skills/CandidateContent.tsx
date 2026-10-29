import type { GovernedCandidate } from '@/services/skillCandidate';

function CandidateFiles({ title, files, baseline, beforeLabel = '基线（原）', afterLabel = '候选（新）' }: {
  title: string;
  files: Record<string, string>;
  baseline?: Record<string, string>;
  beforeLabel?: string;
  afterLabel?: string;
}) {
  const names = [...new Set([...Object.keys(baseline || {}), ...Object.keys(files)])].sort();
  return <details className="skill-candidate-details">
    <summary>{title} · {beforeLabel}与{afterLabel}（{names.length}）</summary>
    {baseline == null && <p>未提供可核对的基线材料；不能推断原版没有此文件。</p>}
    {names.map((name) => <details key={name} className="skill-candidate-details">
      <summary>{name} · {baseline == null ? '基线未提供' : baseline[name] === files[name] ? '未变更' : !(name in files) ? '删除' : !(name in baseline) ? '新增' : '修改'}</summary>
      <div className="skill-candidate-diff">
        <section><strong>{beforeLabel}</strong><pre tabIndex={0} aria-label={`${beforeLabel}${title}：${name}`}>{baseline == null ? '（基线未提供）' : baseline[name] ?? '（无此文件）'}</pre></section>
        <section><strong>{afterLabel}</strong><pre tabIndex={0} aria-label={`${afterLabel}${title}：${name}`}>{files[name] ?? '（已删除）'}</pre></section>
      </div>
    </details>)}
    {names.length === 0 && <p>无{title}。</p>}
  </details>;
}

export function CandidateContent({ candidate }: { candidate: GovernedCandidate }) {
  const before = candidate.baseline_instructions ? candidate.baseline_instructions.split('\n') : [];
  const after = candidate.instructions.split('\n');
  let commonStart = 0;
  while (commonStart < before.length && commonStart < after.length && before[commonStart] === after[commonStart]) commonStart++;
  let commonEnd = 0;
  while (commonEnd < before.length - commonStart && commonEnd < after.length - commonStart
    && before[before.length - commonEnd - 1] === after[after.length - commonEnd - 1]) commonEnd++;
  const baseline = candidate.baseline_content;
  const emptyBaseline = candidate.baseline_version_id ? undefined : {};
  return <>
    <dl className="skill-candidate-meta">
      <dt>目标 / 适用条件</dt><dd>{candidate.target_goal} / {candidate.applicability}</dd>
      <dt>变更理由</dt><dd>{candidate.rationale}</dd>
      <dt>来源</dt><dd>{candidate.origin === 'archive' ? '管理者归档导入 · 无来源 Run · 未审脚本不执行' : candidate.source_run_id}</dd>
      <dt>发布者 / 范围</dt><dd>{candidate.publisher_id} / {candidate.scope_key}</dd>
      <dt>候选 / 技能</dt><dd>{candidate.id} / {candidate.skill_id}</dd>
      <dt>基线版本</dt><dd>{candidate.baseline_version_id || '无（新建候选）'}</dd>
      <dt>当前版本</dt><dd>{candidate.current_version_id || '无当前版本'}</dd>
      <dt>内容摘要</dt><dd><code>{candidate.digest}</code></dd>
      <dt>证据摘要</dt><dd><code>{candidate.evidence_digest || '未提供'}</code></dd>
      <dt>证据身份 / 修订</dt><dd>{candidate.evidence_id || '未记录'} / {candidate.evidence_revision}</dd>
      <dt>来源许可截止</dt><dd>{candidate.source_license_expires_at ? new Date(candidate.source_license_expires_at * 1000).toLocaleString('zh-CN') : '按当前已确认来源许可限制'}</dd>
      <dt>材料期限</dt><dd>{new Date(candidate.expires_at * 1000).toLocaleString('zh-CN')}</dd>
    </dl>
    {candidate.baseline_stale && <section aria-label="过时基线与当前版本对照">
      <p role="alert">候选基线已过时：原基线 {candidate.baseline_version_id || '无'}，当前版本 {candidate.current_version_id || '无'}。不会自动合并或覆盖当前版本；请对照后人工提交完整重新整合包。</p>
      <p>旧验证结论与发布批准不能用于重新整合后的内容。脚本、镜像或入口发生变化时，必须重新进行可信人工代码审查，再重新验证和批准。</p>
      {!candidate.current_version_id ? <p role="note">当前没有可绑定的已发布版本，不能提交重新整合；请刷新并核实当前版本状态。</p>
        : !candidate.current_content ? <p role="note">当前版本材料未提供或未获准查看，不能推断为空包，也不能重新整合。请重新核验材料权限并刷新。</p>
          : <details className="skill-candidate-details" open>
            <summary>原基线与当前已发布版本 · 完整包对照</summary>
            <div className="skill-candidate-diff">
              <section><strong>原基线指令</strong><pre tabIndex={0} aria-label="重新整合原基线指令">{baseline?.instructions ?? (candidate.baseline_version_id ? '（基线材料未提供）' : '（无原基线）')}</pre></section>
              <section><strong>当前版本指令</strong><pre tabIndex={0} aria-label="重新整合当前版本指令">{candidate.current_content.instructions || '（空指令）'}</pre></section>
            </div>
            <CandidateFiles title="资源材料" baseline={baseline?.resources ?? emptyBaseline} files={candidate.current_content.resources} beforeLabel="原基线" afterLabel="当前版本" />
            <CandidateFiles title="完整脚本" baseline={baseline?.scripts ?? emptyBaseline} files={candidate.current_content.scripts} beforeLabel="原基线" afterLabel="当前版本" />
            <div className="skill-candidate-diff">
              <section><strong>原基线执行环境</strong><dl className="skill-candidate-meta">
                <dt>固定镜像摘要</dt><dd><code>{baseline?.image_digest ?? (candidate.baseline_version_id ? '（基线材料未提供）' : '（无原基线）')}</code></dd>
                <dt>固定入口</dt><dd><code>{baseline?.entry_point ?? (candidate.baseline_version_id ? '（基线材料未提供）' : '（无原基线）')}</code></dd>
              </dl></section>
              <section><strong>当前版本执行环境</strong><dl className="skill-candidate-meta">
                <dt>固定镜像摘要</dt><dd><code>{candidate.current_content.image_digest || '（无脚本镜像）'}</code></dd>
                <dt>固定入口</dt><dd><code>{candidate.current_content.entry_point || '（无脚本入口）'}</code></dd>
              </dl></section>
            </div>
          </details>}
    </section>}
    <details className="skill-candidate-details" open>
      <summary>指令差异 · 基线与候选对照</summary>
      {candidate.instructions === candidate.baseline_instructions && <p>指令内容与基线一致。</p>}
      <div className="skill-candidate-diff">
        <section aria-label="基线内容"><strong>基线内容（原）</strong>
          <pre tabIndex={0} aria-label="基线指令，减号为被替换内容">{before.length === 0 ? '（无基线内容）' : before.map((line, index) =>
            <span key={index} className={index >= commonStart && index < before.length - commonEnd ? 'skill-candidate-removed' : undefined}>
              {index >= commonStart && index < before.length - commonEnd ? '− ' : '  '}{line}{'\n'}
            </span>)}</pre>
        </section>
        <section aria-label="候选新内容"><strong>候选内容（新）</strong>
          <pre tabIndex={0} aria-label="候选指令，加号为新增内容">{after.map((line, index) =>
            <span key={index} className={index >= commonStart && index < after.length - commonEnd ? 'skill-candidate-added' : undefined}>
              {index >= commonStart && index < after.length - commonEnd ? '+ ' : '  '}{line}{'\n'}
            </span>)}</pre>
        </section>
      </div>
    </details>
    <CandidateFiles title="资源材料" files={candidate.resources} baseline={baseline?.resources ?? emptyBaseline} />
    <CandidateFiles title="完整脚本（执行前须人工审查）" files={candidate.scripts} baseline={baseline?.scripts ?? emptyBaseline} />
    <div className="skill-candidate-diff">
      <section><strong>基线执行环境（原）</strong><dl className="skill-candidate-meta">
        <dt>固定镜像摘要</dt><dd><code>{baseline?.image_digest || (candidate.baseline_version_id ? '未提供基线镜像' : '无基线镜像')}</code></dd>
        <dt>固定入口</dt><dd><code>{baseline?.entry_point || (candidate.baseline_version_id ? '未提供基线入口' : '无基线入口')}</code></dd>
      </dl></section>
      <section><strong>候选执行环境（新）</strong><dl className="skill-candidate-meta">
        <dt>固定镜像摘要</dt><dd><code>{candidate.image_digest || '无脚本镜像'}</code></dd>
        <dt>固定入口</dt><dd><code>{candidate.entry_point || '无脚本入口'}</code></dd>
      </dl></section>
    </div>
    <dl className="skill-candidate-meta">
      <dt>代码审查身份</dt><dd><code>{candidate.code_review_digest || '尚未可信准入 / 未完成执行前审查'}</code></dd>
      <dt>代表任务</dt><dd>{candidate.representative_task || '未提供'}</dd>
      <dt>具体预期结果</dt><dd>{candidate.expected_output || '未提供'}</dd>
    </dl>
    <details className="skill-candidate-details">
      <summary>当前获准证据与精确 CSV 样例</summary>
      <pre tabIndex={0} aria-label="当前获准来源证据">{candidate.evidence || '未提供额外证据；不能以界面可见或单次反馈代替验证。'}</pre>
      <p>CSV 样例按原始字节核对，以下 base64 不丢失换行或编码信息。</p>
      <pre tabIndex={0} aria-label="输入 CSV 原始字节 base64">{candidate.input_b64 || '未提供输入样例'}</pre>
      <pre tabIndex={0} aria-label="预期 CSV 产物原始字节 base64">{candidate.expected_output_b64 || '未提供预期产物样例'}</pre>
    </details>
    <details className="skill-candidate-details">
      <summary>新获准基线回归样例（{candidate.baseline_samples.length}）</summary>
      <p>以下样例随当前证据身份、来源许可和材料期限授权，仅用于绑定版本的回归验证，不修改已发布基线包或结论；原基线授权失效时仍不可验证。</p>
      {candidate.baseline_samples.map((sample, index) => <details key={`${sample.version_id}-${index}`} className="skill-candidate-details">
        <summary>样例 {index + 1} · 绑定基线版本 {sample.version_id}</summary>
        <dl className="skill-candidate-meta">
          <dt>绑定版本</dt><dd><code>{sample.version_id}</code></dd>
          <dt>代表任务</dt><dd><pre tabIndex={0} aria-label={`基线样例 ${index + 1} 代表任务`}>{sample.representative_task || '未提供'}</pre></dd>
          <dt>具体预期结果</dt><dd><pre tabIndex={0} aria-label={`基线样例 ${index + 1} 具体预期结果`}>{sample.expected_output || '未提供'}</pre></dd>
        </dl>
        <p>CSV 样例按原始字节核对；以下 base64 保留换行与编码信息。</p>
        <pre tabIndex={0} aria-label={`基线样例 ${index + 1} 输入 CSV 原始字节 base64`}>{sample.input_b64 || '未提供输入样例'}</pre>
        <pre tabIndex={0} aria-label={`基线样例 ${index + 1} 预期 CSV 产物原始字节 base64`}>{sample.expected_output_b64 || '未提供预期产物样例'}</pre>
      </details>)}
      {candidate.baseline_samples.length === 0 && <p>未提交新获准基线回归样例；验证仍须使用当前获准且未过期的原基线样例。</p>}
    </details>
  </>;
}
