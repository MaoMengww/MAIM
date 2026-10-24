import type { GovernedCandidate } from '@/services/skillCandidate';

function CandidateFiles({ title, files, baseline }: { title: string; files: Record<string, string>; baseline?: Record<string, string> }) {
  const names = [...new Set([...Object.keys(baseline || {}), ...Object.keys(files)])].sort();
  return <details className="skill-candidate-details">
    <summary>{title} · 基线与候选（{names.length}）</summary>
    {baseline == null && <p>未提供可核对的基线材料；不能推断原版没有此文件。</p>}
    {names.map((name) => <details key={name} className="skill-candidate-details">
      <summary>{name} · {baseline == null ? '基线未提供' : baseline[name] === files[name] ? '未变更' : !(name in files) ? '删除' : !(name in baseline) ? '新增' : '修改'}</summary>
      <div className="skill-candidate-diff">
        <section><strong>基线（原）</strong><pre tabIndex={0} aria-label={`基线${title}：${name}`}>{baseline == null ? '（基线未提供）' : baseline[name] ?? '（无此文件）'}</pre></section>
        <section><strong>候选（新）</strong><pre tabIndex={0} aria-label={`候选${title}：${name}`}>{files[name] ?? '（已删除）'}</pre></section>
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
      <dt>来源 Run</dt><dd>{candidate.source_run_id}</dd>
      <dt>发布者 / 范围</dt><dd>{candidate.publisher_id} / {candidate.scope_key}</dd>
      <dt>候选 / 技能</dt><dd>{candidate.id} / {candidate.skill_id}</dd>
      <dt>基线版本</dt><dd>{candidate.baseline_version_id || '无（新建候选）'}</dd>
      <dt>内容摘要</dt><dd><code>{candidate.digest}</code></dd>
      <dt>证据摘要</dt><dd><code>{candidate.evidence_digest || '未提供'}</code></dd>
      <dt>材料期限</dt><dd>{new Date(candidate.expires_at * 1000).toLocaleString('zh-CN')}</dd>
    </dl>
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
  </>;
}
