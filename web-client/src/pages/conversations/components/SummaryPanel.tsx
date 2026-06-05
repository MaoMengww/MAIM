import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Select, Input, message, Spin, Empty, Checkbox, Popconfirm } from 'antd';
import { PlusOutlined, DeleteOutlined, EditOutlined, CheckOutlined, CloseOutlined } from '@ant-design/icons';
import { convToolApi } from '@/services/conversation-tool';
import type { TodoItem, SummaryItem, SummariesResponse } from '@/services/conversation-tool';

interface SummaryPanelProps {
  convId: string;
  open: boolean;
  onClose: () => void;
}

export function SummaryPanel({ convId, open }: SummaryPanelProps) {
  const queryClient = useQueryClient();
  const [summarizing, setSummarizing] = useState(false);
  const [range, setRange] = useState<number>(100);
  const [expandedIds, setExpandedIds] = useState<Set<number>>(new Set());
  const [addingTodo, setAddingTodo] = useState<Record<number, boolean>>({});
  const [newTodoText, setNewTodoText] = useState<Record<number, string>>({});
  const [editingTodo, setEditingTodo] = useState<{ id: number; summaryId: number; content: string } | null>(null);
  const [editText, setEditText] = useState('');

  const { data: summaries, isLoading } = useQuery<SummariesResponse>({
    queryKey: ['conv-summaries', convId],
    queryFn: () => convToolApi.getSummaries(convId, 20),
    enabled: open,
  });

  const handleSummarize = async () => {
    setSummarizing(true);
    try {
      const payload = range === 0 ? { all: true } : { last_message_count: range };
      await convToolApi.summarize(convId, payload);
      message.success('总结完成');
      queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
    } catch {
      message.error('总结失败');
    } finally {
      setSummarizing(false);
    }
  };

  const toggleExpand = (id: number) => {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleTodo = async (todo: TodoItem) => {
    try {
      await convToolApi.updateTodo(convId, todo.id, { done: !todo.done });
      queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
    } catch {
      message.error('更新失败');
    }
  };

  const handleDeleteTodo = async (id: number) => {
    try {
      await convToolApi.deleteTodo(convId, id);
      queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
    } catch {
      message.error('删除失败');
    }
  };

  const handleAddTodo = async (summaryId: number) => {
    const text = (newTodoText[summaryId] || '').trim();
    if (!text) return;
    try {
      await convToolApi.createTodo(convId, { summary_id: summaryId, content: text });
      setNewTodoText((prev) => ({ ...prev, [summaryId]: '' }));
      setAddingTodo((prev) => ({ ...prev, [summaryId]: false }));
      queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
    } catch {
      message.error('添加失败');
    }
  };

  const handleEditTodo = async (todo: TodoItem) => {
    if (!editText.trim()) return;
    try {
      await convToolApi.updateTodo(convId, todo.id, { content: editText.trim() });
      setEditingTodo(null);
      queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
    } catch {
      message.error('编辑失败');
    }
  };

  const items = summaries?.items || [];
  const allExpanded = items.length > 0 && items.every((s) => expandedIds.has(s.summary_id));

  if (!open) return null;

  return (
    <div className="summary-panel">
      <div className="summary-panel-header">
        <span>📋 会话工具</span>
      </div>

      <div className="summary-panel-action">
        <Button
          type="primary"
          size="small"
          loading={summarizing}
          onClick={handleSummarize}
          style={{ flexShrink: 0 }}
        >
          ⚡ 开始总结
        </Button>
        <Select
          size="small"
          value={range}
          onChange={setRange}
          style={{ width: 120 }}
          options={[
            { value: 50, label: '最近 50 条' },
            { value: 100, label: '最近 100 条' },
            { value: 200, label: '最近 200 条' },
            { value: 0, label: '全部消息' },
          ]}
        />
      </div>

      <div className="summary-panel-list">
        {isLoading && <div style={{ textAlign: 'center', padding: 24 }}><Spin size="small" /></div>}

        {!isLoading && items.length === 0 && (
          <Empty description="暂无总结" style={{ padding: 24 }} />
        )}

        {items.map((s) => {
          const isExpanded = expandedIds.has(s.summary_id);
          const summaryDate = s.created_at
            ? new Date(s.created_at * 1000).toLocaleDateString('zh-CN')
            : '';
          return (
            <div key={s.summary_id} className="summary-panel-item">
              <div
                className="summary-panel-item-header"
                onClick={() => toggleExpand(s.summary_id)}
              >
                <span className="summary-panel-item-date">
                  {summaryDate && `📅 ${summaryDate}`} · {s.total_messages} 条消息
                </span>
                <span className="summary-panel-item-toggle">
                  {isExpanded ? '收起' : '展开'}
                </span>
              </div>

              {isExpanded && (
                <div className="summary-panel-item-body">
                  <div className="summary-panel-summary-text">
                    {s.summary.split('\n').map((line, i) => (
                      <div key={i}>{line || ' '}</div>
                    ))}
                  </div>

                  {s.todos.length > 0 && (
                    <div className="summary-panel-todos">
                      <div className="summary-panel-todos-title">✅ 待办事项</div>
                      {s.todos.map((todo) => (
                        <div key={todo.id} className="summary-panel-todo-item">
                          {editingTodo?.id === todo.id ? (
                            <div className="summary-panel-todo-edit">
                              <Input
                                size="small"
                                value={editText}
                                onChange={(e) => setEditText(e.target.value)}
                                onPressEnter={() => handleEditTodo(todo)}
                                style={{ flex: 1 }}
                              />
                              <CheckOutlined onClick={() => handleEditTodo(todo)} style={{ cursor: 'pointer', color: '#52c41a' }} />
                              <CloseOutlined onClick={() => setEditingTodo(null)} style={{ cursor: 'pointer', color: '#999' }} />
                            </div>
                          ) : (
                            <>
                              <Checkbox
                                checked={todo.done}
                                onChange={() => toggleTodo(todo)}
                              />
                              <span style={{ flex: 1, textDecoration: todo.done ? 'line-through' : 'none', color: todo.done ? 'var(--aim-text-tertiary)' : 'var(--aim-text)', fontSize: 13 }}>
                                {todo.content}
                              </span>
                              <EditOutlined
                                onClick={() => { setEditingTodo({ id: todo.id, summaryId: s.summary_id, content: todo.content }); setEditText(todo.content); }}
                                style={{ cursor: 'pointer', color: '#999', fontSize: 12, marginRight: 4 }}
                              />
                              <Popconfirm title="删除待办？" onConfirm={() => handleDeleteTodo(todo.id)} okText="确认" cancelText="取消">
                                <DeleteOutlined style={{ cursor: 'pointer', color: '#999', fontSize: 12 }} />
                              </Popconfirm>
                            </>
                          )}
                        </div>
                      ))}
                    </div>
                  )}

                  {addingTodo[s.summary_id] ? (
                    <div className="summary-panel-todo-add">
                      <Input
                        size="small"
                        placeholder="输入待办内容"
                        value={newTodoText[s.summary_id] || ''}
                        onChange={(e) => setNewTodoText((prev) => ({ ...prev, [s.summary_id]: e.target.value }))}
                        onPressEnter={() => handleAddTodo(s.summary_id)}
                        style={{ flex: 1 }}
                      />
                      <Button size="small" type="primary" onClick={() => handleAddTodo(s.summary_id)}>添加</Button>
                      <Button size="small" onClick={() => { setAddingTodo((prev) => ({ ...prev, [s.summary_id]: false })); setNewTodoText((prev) => ({ ...prev, [s.summary_id]: '' })); }}>取消</Button>
                    </div>
                  ) : (
                    <Button
                      type="link"
                      size="small"
                      icon={<PlusOutlined />}
                      onClick={() => setAddingTodo((prev) => ({ ...prev, [s.summary_id]: true }))}
                      style={{ padding: 0, marginTop: 4 }}
                    >
                      添加待办
                    </Button>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
