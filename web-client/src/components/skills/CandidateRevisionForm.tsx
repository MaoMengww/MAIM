import { useState } from 'react';
import { Button, Form, Input, InputNumber } from 'antd';
import type { CandidateRevision, GovernedCandidate } from '@/services/skillCandidate';
import { skillFileBase64 } from '@/utils/skillSubmission';

function parseFiles(value: string): Record<string, string> {
  const parsed: unknown = JSON.parse(value);
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)
    || Object.values(parsed).some((content) => typeof content !== 'string')) throw new Error('必须是文件路径到完整文本的 JSON 对象');
  return parsed as Record<string, string>;
}

interface RevisionValues extends Omit<CandidateRevision, 'resources' | 'scripts'> {
  resources: string;
  scripts: string;
}

export function CandidateRevisionForm({ candidate, pending, onSubmit }: {
  candidate: GovernedCandidate;
  pending: boolean;
  onSubmit: (revision: CandidateRevision) => void;
}) {
  const [form] = Form.useForm<RevisionValues>();
  const [fileError, setFileError] = useState('');
  const [reading, setReading] = useState(false);
  const jsonRule = { validator: async (_: unknown, value: string) => { parseFiles(value); } };
  const initialValues: RevisionValues = {
    instructions: candidate.instructions, resources: JSON.stringify(candidate.resources, null, 2), scripts: JSON.stringify(candidate.scripts, null, 2),
    image_digest: candidate.image_digest, entry_point: candidate.entry_point,
    representative_task: candidate.representative_task, expected_output: candidate.expected_output,
    input_b64: candidate.input_b64, expected_output_b64: candidate.expected_output_b64, expires_at: candidate.expires_at,
  };
  const loadSample = async (field: 'input_b64' | 'expected_output_b64', file?: File) => {
    if (!file) return;
    setReading(true);
    setFileError('');
    try { form.setFieldValue(field, await skillFileBase64(file)); }
    catch (error) { setFileError(error instanceof Error ? error.message : '样例读取失败'); }
    finally { setReading(false); }
  };
  return <details className="skill-candidate-details">
    <summary>修订内容 / 补充代表任务和获准 CSV 样例</summary>
    <p>保存将使旧验证与发布批准失效；修改脚本、镜像或入口还须重新执行前代码审查。已发布或已拒绝候选不可修订。</p>
    <p>修改已有 skill 时，服务端保留并验证原代表场景，不能通过换样例绕过基线回归。</p>
    <Form form={form} layout="vertical" initialValues={initialValues} disabled={pending || reading}
      onValuesChange={() => setFileError('')}
      onFinish={(values) => onSubmit({ ...values, resources: parseFiles(values.resources), scripts: parseFiles(values.scripts) })}>
      <Form.Item name="instructions" label="完整候选指令" rules={[{ required: true, message: '请填写完整候选指令' }]}>
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
      <Form.Item name="representative_task" label="代表任务（纯指令验证 / 回归场景）">
        <Input.TextArea rows={3} maxLength={16384} />
      </Form.Item>
      <Form.Item name="expected_output" label="具体预期结果（纯指令 TrimSpace 后精确匹配，非包含匹配）">
        <Input.TextArea rows={3} maxLength={2048} />
      </Form.Item>
      <label className="skill-candidate-file-label">获准输入 CSV 文件（最多 64 KiB）
        <input type="file" accept=".csv,text/csv" disabled={pending || reading}
          onChange={(event) => void loadSample('input_b64', event.target.files?.[0])} />
      </label>
      <Form.Item name="input_b64" label="输入样例原始字节（base64，可粘贴或由文件读取）"><Input.TextArea rows={2} /></Form.Item>
      <label className="skill-candidate-file-label">具体预期 CSV 产物（最多 64 KiB，不以退出码代替）
        <input type="file" accept=".csv,text/csv" disabled={pending || reading}
          onChange={(event) => void loadSample('expected_output_b64', event.target.files?.[0])} />
      </label>
      <Form.Item name="expected_output_b64" label="预期产物原始字节（base64）"><Input.TextArea rows={2} /></Form.Item>
      <Form.Item name="expires_at" label="样例 / 材料到期时间（Unix 秒，不得超过来源许可期限）"
        rules={[{ required: true, message: '填写有限材料期限' }]}>
        <InputNumber min={1} precision={0} />
      </Form.Item>
      {fileError && <p role="alert">{fileError}</p>}
      <Button htmlType="submit" loading={pending || reading} disabled={!!fileError}>保存修订并使旧验证失效</Button>
    </Form>
  </details>;
}
