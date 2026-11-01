import type { ModelCapability } from '@/types/model';

export const capabilityOptions: { value: ModelCapability; label: string }[] = [
  { value: 'chat', label: 'Chat' },
  { value: 'embed', label: 'Embedding' },
  { value: 'rerank', label: 'Rerank' },
  { value: 'judge', label: '结构化判断 (judge)' },
  { value: 'vlm', label: 'VLM' },
  { value: 'tts', label: 'TTS' },
];

export function displayCapability(capability: string): string {
  return capabilityOptions.find((option) => option.value === capability)?.label ?? capability;
}
