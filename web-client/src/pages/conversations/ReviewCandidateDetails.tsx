import type { ReviewCandidate } from '@/services/runReview';

function Files({ title, files }: { title: string; files: Record<string, string> }) {
  return <details className="chat-run-details">
    <summary>{title}（{Object.keys(files).length}）</summary>
    {Object.entries(files).map(([name, content]) => <details key={name} className="chat-run-details">
      <summary>{name}</summary><pre className="chat-review-code" tabIndex={0} aria-label={`${title}：${name}`}>{content}</pre>
    </details>)}
    {Object.keys(files).length === 0 && <p className="chat-run-notice">无{title}。</p>}
  </details>;
}

export function ReviewCandidateDetails({ candidate }: { candidate: ReviewCandidate }) {
  const hasScripts = Object.keys(candidate.scripts).length > 0;
  const before = candidate.baseline_instructions ? candidate.baseline_instructions.split('\n') : [];
  const after = candidate.instructions ? candidate.instructions.split('\n') : [];
  let commonStart = 0;
  while (commonStart < before.length && commonStart < after.length && before[commonStart] === after[commonStart]) commonStart++;
  let commonEnd = 0;
  while (commonEnd < before.length - commonStart && commonEnd < after.length - commonStart
    && before[before.length - commonEnd - 1] === after[after.length - commonEnd - 1]) commonEnd++;
  return <section className="chat-review-candidate" aria-label="复盘候选差异">
    <p><strong>{candidate.intent === 'modify' ? '修改候选' : '新建候选'} · 待验证</strong></p>
    <p className="chat-run-notice">仅供审阅，不会启用、发布或自动执行。模型生成内容不构成验证或可信代码证明。</p>
    <dl className="chat-review-meta">
      <dt>目标</dt><dd>{candidate.target_goal}</dd>
      <dt>适用条件</dt><dd>{candidate.applicability}</dd>
      <dt>变更理由</dt><dd>{candidate.rationale}</dd>
      <dt>来源 Run</dt><dd><a href={`#run-${candidate.source_run_id}`}>{candidate.source_run_id}</a></dd>
      <dt>发布者</dt><dd>{candidate.publisher_id}</dd>
      <dt>范围</dt><dd>{candidate.scope_key}</dd>
      <dt>候选 / 技能</dt><dd>{candidate.id} / {candidate.skill_id}</dd>
      <dt>基线版本</dt><dd>{candidate.baseline_version_id || '无（新建候选）'}</dd>
      <dt>内容摘要</dt><dd><code>{candidate.digest}</code></dd>
      <dt>证据摘要</dt><dd><code>{candidate.evidence_digest || '未提供'}</code></dd>
      <dt>候选期限</dt><dd>{new Date(candidate.expires_at * 1000).toLocaleString('zh-CN')}</dd>
    </dl>
    <details className="chat-run-details" open>
      <summary>指令差异 · 基线与候选对照</summary>
      {candidate.instructions === candidate.baseline_instructions && <p className="chat-run-notice">指令内容与基线一致。</p>}
      <div className="chat-review-diff">
        <section aria-label="基线内容"><strong>基线内容（原）</strong>
          <pre className="chat-review-code" tabIndex={0} aria-label="基线指令，减号为被替换内容">{before.length === 0 ? '（无基线内容）' : before.map((line, index) =>
            <span key={index} className={index >= commonStart && index < before.length - commonEnd ? 'chat-review-removed' : undefined}>
              {index >= commonStart && index < before.length - commonEnd ? '− ' : '  '}{line}{'\n'}
            </span>)}</pre>
        </section>
        <section aria-label="候选新内容"><strong>候选内容（新）</strong>
          <pre className="chat-review-code" tabIndex={0} aria-label="候选指令，加号为新增内容">{after.map((line, index) =>
            <span key={index} className={index >= commonStart && index < after.length - commonEnd ? 'chat-review-added' : undefined}>
              {index >= commonStart && index < after.length - commonEnd ? '+ ' : '  '}{line}{'\n'}
            </span>)}</pre>
        </section>
      </div>
    </details>
    <Files title="资源材料" files={candidate.resources} />
    <div className="chat-run-notice" role="note">
      {hasScripts || candidate.code_review_required
        ? '包含脚本或需代码审查：必须先经可信人工审查并冻结脚本、镜像和入口身份。此处只展示待审材料，不提供执行。'
        : '当前候选无脚本；仍须完成后续验证才能启用。'}
    </div>
    <Files title="脚本待人工审查" files={candidate.scripts} />
    {(hasScripts || candidate.code_review_required) && <dl className="chat-review-meta">
      <dt>镜像摘要</dt><dd><code>{candidate.image_digest || '未指定'}</code></dd>
      <dt>入口</dt><dd><code>{candidate.entry_point || '未指定'}</code></dd>
    </dl>}
  </section>;
}
