import { Tag } from 'antd';
import { CrownOutlined } from '@ant-design/icons';

const providerNameMap: Record<string, string> = {
  qwen: '千问',
  deepseek: 'DeepSeek',
  openai: 'OpenAI',
  zhipu: '智谱',
  baidu: '百度',
  moonshot: '月之暗面',
  minimax: 'MiniMax',
  spark: '讯飞星火',
  hunyuan: '腾讯混元',
  doubao: '豆包',
};

export function displayProvider(key: string): string {
  return providerNameMap[key.toLowerCase()] || key;
}

export const providerOptions = Object.entries(providerNameMap).map(([value, label]) => ({
  value,
  label: `${label} (${value})`,
}));

// For model selectors, wrap the label with an "官方" tag for owner_id === 0
export function modelOptionLabel(m: { model_name: string; provider: string; owner_id?: number | string }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      {m.model_name}
      {Number(m.owner_id) === 0 && (
        <Tag icon={<CrownOutlined />} color="gold" style={{ margin: 0, fontSize: 10, lineHeight: '16px' }}>官方</Tag>
      )}
    </span>
  );
}
