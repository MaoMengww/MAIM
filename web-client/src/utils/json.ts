import type { KnowledgeSource } from '../types/model';

export function entityId(value: unknown, field: string): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value)) {
    throw new Error(`${field} 必须是标准 UUID`);
  }
  return value;
}

export function optionalEntityId(value: unknown, field: string): string | undefined {
  return value === undefined || value === null ? undefined : entityId(value, field);
}

export function sequence(value: unknown, field: string, positive = false): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < (positive ? 1 : 0)) {
    throw new Error(`${field} 必须是安全整数 number`);
  }
  return value;
}

// Protobuf timestamps and attachment sizes retain their own int64 text contract.
// This is never used for identity, seq, read or inbox position.
export function quantity(value: unknown, field: string): number {
  if (value === undefined) return 0;
  if (typeof value === 'string' && /^\d+$/.test(value)) value = Number(value);
  return sequence(value, field);
}

export function knowledgeSources(value: unknown): KnowledgeSource[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error('知识来源必须是数组');
  return value.map((source) => {
    if (!source || source.type !== 'rag' || typeof source.kb_name !== 'string' || typeof source.title !== 'string' || typeof source.content !== 'string') {
      throw new Error('知识来源格式无效');
    }
    return {
      type: 'rag',
      kb_id: entityId(source.kb_id, 'source.kb_id'),
      doc_id: entityId(source.doc_id, 'source.doc_id'),
      chunk_id: entityId(source.chunk_id, 'source.chunk_id'),
      kb_name: source.kb_name,
      title: source.title,
      content: source.content,
    };
  });
}
