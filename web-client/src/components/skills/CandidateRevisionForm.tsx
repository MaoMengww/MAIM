import { useEffect, useRef, useState } from 'react';
import { Button, Checkbox, Form, Input, InputNumber } from 'antd';
import { candidateEvidenceExpired, type BaselineSample, type CandidateRevision, type GovernedCandidate } from '@/services/skillCandidate';
import { skillFileBase64 } from '@/utils/skillSubmission';

function parseFiles(value: string): Record<string, string> {
  const parsed: unknown = JSON.parse(value);
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)
    || Object.values(parsed).some((content) => typeof content !== 'string')) throw new Error('必须是文件路径到完整文本的 JSON 对象');
  return parsed as Record<string, string>;
}

function parseBaselineSamples(value?: string): BaselineSample[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(value?.trim() || '[]');
  } catch {
    throw new Error('基线回归样例必须是有效的 JSON 数组');
  }
  if (!Array.isArray(parsed) || parsed.length > 7) throw new Error('基线回归样例必须是最多 7 项的 JSON 数组');
  return parsed.map((item: unknown) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)
      || !('version_id' in item) || typeof item.version_id !== 'string'
      || !('representative_task' in item) || typeof item.representative_task !== 'string'
      || !('expected_output' in item) || typeof item.expected_output !== 'string'
      || !('input_b64' in item) || typeof item.input_b64 !== 'string'
      || !('expected_output_b64' in item) || typeof item.expected_output_b64 !== 'string') {
      throw new Error('每项须包含 version_id、representative_task、expected_output、input_b64、expected_output_b64 五个字符串字段');
    }
    return {
      version_id: item.version_id, representative_task: item.representative_task, expected_output: item.expected_output,
      input_b64: item.input_b64, expected_output_b64: item.expected_output_b64,
    };
  });
}

interface RevisionValues extends Omit<CandidateRevision, 'resources' | 'scripts' | 'baseline_samples'> {
  resources: string;
  scripts: string;
  baseline_samples: string;
}

export function CandidateRevisionForm({ candidate, pending, reintegrating = false, onSubmit }: {
  candidate: GovernedCandidate;
  pending: boolean;
  reintegrating?: boolean;
  onSubmit: (revision: CandidateRevision) => void;
}) {
  const [form] = Form.useForm<RevisionValues>();
  const [fileError, setFileError] = useState('');
  const [reading, setReading] = useState(false);
  const formActive = useRef(true);
  const expired = candidateEvidenceExpired(candidate);
  const jsonRule = { validator: async (_: unknown, value: string) => { parseFiles(value); } };
  const requiredText = { required: true, whitespace: true, message: '请明确填写新的获准内容' };
  const initialValues: Partial<RevisionValues> = {
    name: expired ? '' : candidate.name, description: expired ? '' : candidate.description,
    rationale: expired ? '' : candidate.rationale, target_goal: expired ? '' : candidate.target_goal,
    applicability: expired ? '' : candidate.applicability,
    instructions: expired ? '' : candidate.instructions,
    resources: JSON.stringify(expired ? {} : candidate.resources, null, 2), scripts: JSON.stringify(expired ? {} : candidate.scripts, null, 2),
    image_digest: expired ? '' : candidate.image_digest, entry_point: expired ? '' : candidate.entry_point,
    representative_task: '', expected_output: '', input_b64: '', expected_output_b64: '', evidence_authorized: false,
    baseline_samples: '[]',
  };
  const csvRule = { validator: async (_: unknown, value: string) => {
    if ((candidate.code_review_required || Object.keys(parseFiles(form.getFieldValue('scripts'))).length > 0) && !value?.trim()) {
      throw new Error('脚本补证必须提交新的获准输入 CSV 及具体预期 CSV 产物');
    }
  } };
  const loadSample = async (field: 'input_b64' | 'expected_output_b64', file?: File) => {
    if (!file) return;
    setReading(true);
    setFileError('');
    try {
      const value = await skillFileBase64(file);
      if (formActive.current) form.setFieldValue(field, value);
    } catch (error) {
      if (formActive.current) setFileError(error instanceof Error ? error.message : '样例读取失败');
    } finally {
      if (formActive.current) setReading(false);
    }
  };
  useEffect(() => {
    formActive.current = true;
    return () => { formActive.current = false; };
  }, []);
  return <details className="skill-candidate-details">
    <summary>{reintegrating ? '人工重新整合到当前版本 · 提交完整候选包' : expired ? '补充新证据并重建完整候选' : '修订内容 / 提交新的获准证据'}</summary>
    <p>旧材料已到期时不会回填原文。请重新提交完整候选包、新获准代表任务和具体预期；仅修改期限不能恢复资格。</p>
    <p>每次补证生成新的证据身份，清除旧验证、代码审查与发布批准；保存后须重新审查、验证和批准。原 Run workspace 不会延期，新验证使用新环境。</p>
    <p>材料期限取当前留存政策与来源许可的较短者；默认政策为 30 天，查看和重试不会延长。修改已有 skill 仍须验证当前获准的基线回归场景。</p>
    {reintegrating && <>
      <p>本次仅以已观察到的当前版本 <code>{candidate.current_version_id}</code> 为新基线。下方保留候选内容供人工编辑，不会自动拼接、采用或合并当前版本。</p>
      <p>请逐项核对上方原基线与当前完整包，明确填写最终指令、资源、脚本、镜像摘要和入口；空对象表示不保留该类文件。若当前版本再次变化，必须刷新、重新对照后提交，不能自动覆盖。</p>
      <p>提交后旧验证和发布批准均不复用；脚本内容或执行环境变更必须重新可信人工审查，并重新验证及批准。</p>
    </>}
    <Form form={form} layout="vertical" initialValues={initialValues} disabled={pending || reading}
      onValuesChange={() => setFileError('')}
      onFinish={(values) => {
        const revision: CandidateRevision = {
          name: values.name.trim(), description: values.description.trim(), rationale: values.rationale.trim(),
          target_goal: values.target_goal?.trim() || '', applicability: values.applicability?.trim() || '',
          instructions: values.instructions, resources: parseFiles(values.resources), scripts: parseFiles(values.scripts),
          image_digest: values.image_digest?.trim() || '', entry_point: values.entry_point?.trim() || '',
          representative_task: values.representative_task.trim(), expected_output: values.expected_output.trim(),
          input_b64: values.input_b64?.trim() || '', expected_output_b64: values.expected_output_b64?.trim() || '',
          baseline_samples: parseBaselineSamples(values.baseline_samples),
          evidence_authorized: values.evidence_authorized === true,
          source_license_expires_at: Number(values.source_license_expires_at),
          ...(values.expires_at == null ? {} : { expires_at: Number(values.expires_at) }),
        };
        onSubmit(revision);
      }}>
      <Form.Item name="name" label="完整候选名称" rules={[requiredText]}><Input maxLength={128} /></Form.Item>
      <Form.Item name="description" label="完整候选描述" rules={[requiredText]}><Input.TextArea rows={2} maxLength={1024} /></Form.Item>
      <Form.Item name="rationale" label="新的变更依据（不得粘贴失效材料）" rules={[requiredText]}><Input.TextArea rows={2} /></Form.Item>
      <Form.Item name="target_goal" label="目标"><Input.TextArea rows={2} /></Form.Item>
      <Form.Item name="applicability" label="适用边界"><Input.TextArea rows={2} /></Form.Item>
      <Form.Item name="instructions" label="完整候选指令" rules={[requiredText]}>
        <Input.TextArea rows={6} maxLength={32768} />
      </Form.Item>
      <Form.Item name="resources" label="完整资源文件 JSON（路径 → 文本）" rules={[jsonRule]}>
        <Input.TextArea rows={4} />
      </Form.Item>
      <Form.Item name="scripts" label="完整脚本 JSON（路径 → 全文；无脚本填 {}）" rules={[jsonRule]}>
        <Input.TextArea rows={6} />
      </Form.Item>
      <Form.Item name="image_digest" label="依赖镜像固定摘要"><Input /></Form.Item>
      <Form.Item name="entry_point" label="固定入口"><Input /></Form.Item>
      <Form.Item name="representative_task" label="新的获准代表任务（纯指令验证 / 回归场景）" rules={[requiredText]}>
        <Input.TextArea rows={3} maxLength={16384} />
      </Form.Item>
      <Form.Item name="expected_output" label="新的具体预期结果（TrimSpace 后精确匹配，非包含匹配）" rules={[requiredText]}>
        <Input.TextArea rows={3} maxLength={2048} />
      </Form.Item>
      <label className="skill-candidate-file-label">获准输入 CSV 文件（最多 64 KiB）
        <input type="file" accept=".csv,text/csv" disabled={pending || reading}
          onChange={(event) => void loadSample('input_b64', event.target.files?.[0])} />
      </label>
      <Form.Item name="input_b64" label="新输入样例原始字节（base64，可粘贴或由文件读取）" dependencies={['scripts']} rules={[csvRule]}><Input.TextArea rows={2} /></Form.Item>
      <label className="skill-candidate-file-label">具体预期 CSV 产物（最多 64 KiB，不以退出码代替）
        <input type="file" accept=".csv,text/csv" disabled={pending || reading}
          onChange={(event) => void loadSample('expected_output_b64', event.target.files?.[0])} />
      </label>
      <Form.Item name="expected_output_b64" label="新预期产物原始字节（base64）" dependencies={['scripts']} rules={[csvRule]}><Input.TextArea rows={2} /></Form.Item>
      <Form.Item name="baseline_samples" label="新获准基线回归样例 JSON 数组（可选，最多 7 项）"
        extra="每项填写 version_id、representative_task、expected_output、input_b64、expected_output_b64 五个字符串字段；不适用字段填空字符串，无新样例填 []。version_id 须精确绑定祖先基线版本。"
        rules={[{ validator: async (_: unknown, value?: string) => { parseBaselineSamples(value); } }]}>
        <Input.TextArea rows={6} />
      </Form.Item>
      <p>基线回归新样例不会回填旧材料，随本次新证据身份、来源许可和材料期限授权；只替代已过期的基线样例，不修改已发布包或结论。原基线授权失效时仍不可验证。</p>
      <Form.Item name="source_license_expires_at" label="新材料来源许可截止（Unix 秒，必填）"
        rules={[{ required: true, type: 'number', message: '填写新材料来源许可截止时间' }, { validator: async (_: unknown, value: number) => {
          if (!Number.isSafeInteger(value) || value * 1000 <= Date.now()) throw new Error('来源许可截止必须为未来的 Unix 整数秒');
        } }]}>
        <InputNumber min={1} precision={0} />
      </Form.Item>
      <Form.Item name="expires_at" label="申请更短的材料期限（Unix 秒，可选；留空按政策与许可取最小值）"
        dependencies={['source_license_expires_at']} rules={[{ validator: async (_: unknown, value?: number) => {
          if (value != null && (!Number.isSafeInteger(value) || value * 1000 <= Date.now() || value > form.getFieldValue('source_license_expires_at'))) {
            throw new Error('申请期限须在未来且不得超过新材料来源许可；服务端还会核验当前政策上限');
          }
        } }]}><InputNumber min={1} precision={0} /></Form.Item>
      <Form.Item name="evidence_authorized" valuePropName="checked" rules={[{ validator: async (_: unknown, value: boolean) => {
        if (value !== true) throw new Error('请确认新材料已获准在当前范围留存与验证');
      } }]}>
        <Checkbox>我确认以上材料（含新获准基线回归样例）是新提交且明确获准在当前范围留存、验证的材料，许可覆盖所填期限；脱敏和 Bot 所有权不是许可。</Checkbox>
      </Form.Item>
      {fileError && <p role="alert">{fileError}</p>}
      <Button htmlType="submit" loading={pending || reading} disabled={!!fileError}>{reintegrating ? '提交完整重新整合包并撤销旧验证 / 批准' : '保存新证据并撤销旧验证 / 批准'}</Button>
    </Form>
  </details>;
}
