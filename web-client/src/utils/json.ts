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
    if (!source || source.type !== 'rag' || typeof source.kb_name !== 'string' || typeof source.title !== 'string' || (source.content !== undefined && typeof source.content !== 'string')) {
      throw new Error('知识来源格式无效');
    }
    if (source.role !== undefined && source.role !== 'matched' && source.role !== 'supplemental') throw new Error('知识来源角色无效');
    const metadata = source.metadata_json === undefined ? undefined : JSON.parse(source.metadata_json);
    if (metadata !== undefined && (!metadata || typeof metadata !== 'object' || Array.isArray(metadata))) throw new Error('知识来源定位无效');
    const location = metadata === undefined ? undefined : {
      ...(typeof metadata.page_num === 'number' && Number.isSafeInteger(metadata.page_num) && metadata.page_num > 0 ? { page_num: metadata.page_num } : {}),
      ...(typeof metadata.section_title === 'string' && metadata.section_title ? { section_title: metadata.section_title } : {}),
      ...(typeof metadata.start_line === 'number' && Number.isSafeInteger(metadata.start_line) && metadata.start_line > 0 ? { start_line: metadata.start_line } : {}),
      ...(typeof metadata.end_line === 'number' && Number.isSafeInteger(metadata.end_line) && metadata.end_line > 0 ? { end_line: metadata.end_line } : {}),
    };
    return {
      type: 'rag',
      kb_id: entityId(source.kb_id, 'source.kb_id'),
      doc_id: entityId(source.doc_id, 'source.doc_id'),
      chunk_id: entityId(source.chunk_id, 'source.chunk_id'),
      kb_name: source.kb_name,
      title: source.title,
      ...(source.content === undefined ? {} : { content: source.content }),
      ...(source.role === undefined ? {} : { role: source.role }),
      ...(source.matched_chunk_id === undefined ? {} : { matched_chunk_id: entityId(source.matched_chunk_id, 'source.matched_chunk_id') }),
      ...(source.chunk_index === undefined ? {} : { chunk_index: quantity(source.chunk_index, 'source.chunk_index') }),
      ...(location === undefined ? {} : { location }),
    };
  });
}
