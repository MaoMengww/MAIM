import { useState, useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Select, Input, message, Spin, Empty, Checkbox, Popconfirm } from 'antd';
import { PlusOutlined, DeleteOutlined, EditOutlined, CheckOutlined, CloseOutlined } from '@ant-design/icons';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { convToolApi } from '@/services/conversation-tool';
import { wsOn } from '@/services/ws';
import type { TodoItem, SummariesResponse } from '@/services/conversation-tool';

interface SummaryPanelProps {
  convId: string;
  open: boolean;
  onClose: () => void;
}

export function SummaryPanel({ convId, open }: SummaryPanelProps) {
  const queryClient = useQueryClient();
  const [summarizing, setSummarizing] = useState(false);
  const [range, setRange] = useState<number>(100);
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set());
  const [addingTodo, setAddingTodo] = useState<Record<string, boolean>>({});
  const [newTodoText, setNewTodoText] = useState<Record<string, string>>({});
  const [editingTodo, setEditingTodo] = useState<TodoItem | null>(null);
  const [editText, setEditText] = useState('');
  const [standaloneTodoText, setStandaloneTodoText] = useState('');
  const [addingStandaloneTodo, setAddingStandaloneTodo] = useState(false);

  useEffect(() => {
    setExpandedIds(new Set());
    setAddingTodo({});
    setNewTodoText({});
    setEditingTodo(null);
    setEditText('');
    setStandaloneTodoText('');
  }, [convId]);

  const { data: summaries, isLoading } = useQuery<SummariesResponse>({
    queryKey: ['conv-summaries', convId],
    queryFn: () => convToolApi.getSummaries(convId, 20),
    enabled: open,
  });

  // Listen for async summarize results via WebSocket
  useEffect(() => {
    if (!open) return;

    const unsubDone = wsOn('conv.summarize.done', (payload: any) => {
      if (payload.conv_id == null || payload.conv_id !== convId) return;
      setSummarizing(false);
      message.success('总结完成');
      queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
    });

    const unsubFailed = wsOn('conv.summarize.failed', (payload: any) => {
      if (payload.conv_id == null || payload.conv_id !== convId) return;
      setSummarizing(false);
      message.error(payload.error || '总结失败');
    });

    return () => { unsubDone(); unsubFailed(); };
  }, [open, convId, queryClient]);

  const handleSummarize = async () => {
    setSummarizing(true);
    try {
      const payload = range === 0 ? { all: true } : { last_message_count: range };
      const resp: any = await convToolApi.summarize(convId, payload);
      if (resp?.status === 'processing') {
        message.info('总结任务已提交，正在处理中...');
        // Keep summarizing=true, wait for WS event conv.summarize.done
      } else {
        message.success('总结完成');
        queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
        setSummarizing(false);
      }
    } catch {
      message.error('总结请求失败');
      setSummarizing(false);
    }
  };

  const toggleExpand = (id: string) => {
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

  const handleDeleteTodo = async (id: string) => {
    try {
      await convToolApi.deleteTodo(convId, id);
      queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
    } catch {
      message.error('删除失败');
    }
  };

  const handleAddTodo = async (summaryId: string) => {
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

  const handleAddStandaloneTodo = async () => {
    const text = standaloneTodoText.trim();
    if (!text || addingStandaloneTodo) return;
    setAddingStandaloneTodo(true);
    try {
      await convToolApi.createTodo(convId, { content: text });
      setStandaloneTodoText('');
      queryClient.invalidateQueries({ queryKey: ['conv-summaries', convId] });
    } catch {
      message.error('添加失败');
    } finally {
      setAddingStandaloneTodo(false);
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

  const renderTodo = (todo: TodoItem) => (
    <div key={todo.id} className="summary-panel-todo-item">
      {editingTodo?.id === todo.id ? (
        <div className="summary-panel-todo-edit">
          <Input
            size="small"
            aria-label="待办内容"
            value={editText}
            onChange={(e) => setEditText(e.target.value)}
            onPressEnter={() => handleEditTodo(todo)}
            style={{ flex: 1 }}
          />
          <Button type="text" size="small" aria-label="保存待办" icon={<CheckOutlined />} onClick={() => handleEditTodo(todo)} />
          <Button type="text" size="small" aria-label="取消编辑" icon={<CloseOutlined />} onClick={() => setEditingTodo(null)} />
        </div>
      ) : (
        <>
          <Checkbox checked={todo.done} aria-label={`完成待办：${todo.content}`} onChange={() => toggleTodo(todo)} />
          <span style={{ flex: 1, textDecoration: todo.done ? 'line-through' : 'none', color: todo.done ? 'var(--aim-text-tertiary)' : 'var(--aim-text)', fontSize: 13 }}>
            {todo.content}
          </span>
          <Button type="text" size="small" aria-label="编辑待办" icon={<EditOutlined />} onClick={() => { setEditingTodo(todo); setEditText(todo.content); }} />
          <Popconfirm title="删除待办？" onConfirm={() => handleDeleteTodo(todo.id)} okText="确认" cancelText="取消">
            <Button type="text" size="small" aria-label="删除待办" icon={<DeleteOutlined />} />
          </Popconfirm>
        </>
      )}
    </div>
  );

  const items = summaries?.items || [];
  const standaloneTodos = summaries?.standalone_todos ?? [];

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

        <div className="summary-panel-item">
          <div className="summary-panel-item-body">
            <div className="summary-panel-todos-title">会话待办</div>
            {standaloneTodos.map(renderTodo)}
            <div className="summary-panel-todo-add">
              <Input
                size="small"
                aria-label="新会话待办"
                placeholder="输入待办内容"
                value={standaloneTodoText}
                onChange={(e) => setStandaloneTodoText(e.target.value)}
                onPressEnter={handleAddStandaloneTodo}
                disabled={addingStandaloneTodo}
                style={{ flex: 1 }}
              />
              <Button size="small" type="primary" loading={addingStandaloneTodo} disabled={!standaloneTodoText.trim()} onClick={handleAddStandaloneTodo}>添加</Button>
            </div>
          </div>
        </div>

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
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>
                      {s.summary}
                    </ReactMarkdown>
                  </div>

                  {s.todos?.length > 0 && (
                    <div className="summary-panel-todos">
                      <div className="summary-panel-todos-title">✅ 待办事项</div>
                      {s.todos.map(renderTodo)}
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
