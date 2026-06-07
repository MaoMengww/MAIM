import { useState } from 'react';
import { Select, DatePicker, Pagination } from 'antd';
import type { Dayjs } from 'dayjs';

const { RangePicker } = DatePicker;

export interface SenderOption {
  id: number;
  name: string;
  isBot: boolean;
}

export type SenderType = '' | 'user' | 'bot';

export interface SearchFilterBarProps {
  // Sender filter
  senderOptions: SenderOption[];
  senderId?: number;
  onSenderIdChange: (id: number | undefined) => void;
  // Sender type filter
  senderType: SenderType;
  onSenderTypeChange: (type: SenderType) => void;
  // Time filter
  startTime?: number;
  endTime?: number;
  onTimeRangeChange: (startTime: number | undefined, endTime: number | undefined) => void;
  // Message type filter
  messageTypes: number[];
  onMessageTypesChange: (types: number[]) => void;
  typeCounts: { msg_type: number; count: number }[];
  typeLabels: Record<number, string>;
  total: number;
  // Pagination
  page: number;
  pageSize: number;
  onPageChange: (page: number) => void;
  // In-conversation nav (optional)
  navIndex?: number;
  navTotal?: number;
  onNavPrev?: () => void;
  onNavNext?: () => void;
}

type TimePreset = 'day' | 'week' | 'month' | 'custom' | null;

// Compute unix timestamps for presets (seconds)
function presetToRange(preset: TimePreset): { start?: number; end?: number } {
  if (!preset || preset === 'custom') return {};
  const now = Math.floor(Date.now() / 1000);
  const durations: Record<string, number> = {
    day: 86400,
    week: 7 * 86400,
    month: 30 * 86400,
  };
  const dur = durations[preset];
  if (dur) return { start: now - dur, end: now };
  return {};
}

export function SearchFilterBar({
  senderOptions,
  senderId,
  onSenderIdChange,
  senderType,
  onSenderTypeChange,
  startTime,
  endTime,
  onTimeRangeChange,
  messageTypes,
  onMessageTypesChange,
  typeCounts,
  typeLabels,
  total,
  page,
  pageSize,
  onPageChange,
  navIndex,
  navTotal,
  onNavPrev,
  onNavNext,
}: SearchFilterBarProps) {
  const [activePreset, setActivePreset] = useState<TimePreset>(null);
  const [customOpen, setCustomOpen] = useState(false);
  const hasNav = navIndex !== undefined && navTotal !== undefined && navTotal > 0;

  const handlePresetClick = (preset: TimePreset) => {
    if (preset === 'custom') {
      setCustomOpen(!customOpen);
      setActivePreset(customOpen ? null : 'custom');
      if (customOpen) {
        // Closing custom, clear time
        onTimeRangeChange(undefined, undefined);
        setActivePreset(null);
      }
      return;
    }
    setActivePreset(preset);
    setCustomOpen(false);
    const range = presetToRange(preset);
    onTimeRangeChange(range.start, range.end);
  };

  const handleCustomRange = (dates: [Dayjs | null, Dayjs | null] | null) => {
    if (dates && dates[0] && dates[1]) {
      onTimeRangeChange(dates[0].unix(), dates[1].unix());
    } else {
      onTimeRangeChange(undefined, undefined);
      setActivePreset(null);
      setCustomOpen(false);
    }
  };

  const handleClearTime = () => {
    setActivePreset(null);
    setCustomOpen(false);
    onTimeRangeChange(undefined, undefined);
  };

  const typedTotal = typeCounts.reduce((sum, tc) => sum + tc.count, 0);

  return (
    <div className="search-filter-bar">
      {/* Message type chips */}
      {typeCounts.length > 0 && (
        <div className="conv-type-chips">
          <button
            className={`conv-type-chip ${messageTypes.length === 0 ? 'active' : ''}`}
            onClick={() => onMessageTypesChange([])}
          >全部({total})</button>
          {typeCounts.map((tc) => {
            const selected = messageTypes.includes(tc.msg_type);
            return (
              <button
                key={tc.msg_type}
                className={`conv-type-chip ${selected ? 'active' : ''}`}
                onClick={() => {
                  onMessageTypesChange(
                    selected
                      ? messageTypes.filter((t) => t !== tc.msg_type)
                      : [...messageTypes, tc.msg_type]
                  );
                }}
              >{typeLabels[tc.msg_type] || `类型${tc.msg_type}`}({tc.count})</button>
            );
          })}
        </div>
      )}

      {/* Filter conditions row */}
      <div className="search-filter-conditions">
        {/* Sender filter */}
        <div className="search-filter-item">
          <span className="search-filter-label">发送者</span>
          <Select
            size="small"
            showSearch
            allowClear
            placeholder="全部发送者"
            value={senderId}
            onChange={(val) => onSenderIdChange(val)}
            filterOption={(input, option) =>
              (option?.label as string)?.toLowerCase().includes(input.toLowerCase())
            }
            options={senderOptions.map((s) => ({
              value: s.id,
              label: s.name + (s.isBot ? ' [Bot]' : ''),
            }))}
            style={{ minWidth: 140 }}
            popupMatchSelectWidth={false}
          />
        </div>

        {/* Sender type */}
        <div className="search-filter-item">
          <span className="search-filter-label">类型</span>
          <div className="search-filter-type-group">
            {(['', 'user', 'bot'] as SenderType[]).map((t) => (
              <button
                key={t}
                className={`search-filter-type-btn ${senderType === t ? 'active' : ''}`}
                onClick={() => onSenderTypeChange(t)}
              >{t === '' ? '全部' : t === 'user' ? '用户' : 'Bot'}</button>
            ))}
          </div>
        </div>

        {/* Time range */}
        <div className="search-filter-item">
          <span className="search-filter-label">时间</span>
          <div className="search-filter-time-btns">
            {([
              { key: 'day' as TimePreset, label: '最近一天' },
              { key: 'week' as TimePreset, label: '最近一周' },
              { key: 'month' as TimePreset, label: '最近一月' },
              { key: 'custom' as TimePreset, label: '自定义' },
            ]).map(({ key, label }) => (
              <button
                key={key}
                className={`search-filter-time-btn ${activePreset === key && key !== 'custom' ? 'active' : ''} ${key === 'custom' && customOpen ? 'active' : ''}`}
                onClick={() => handlePresetClick(key)}
              >{label}</button>
            ))}
          </div>
          {customOpen && (
            <div className="search-filter-custom-time">
              <RangePicker
                size="small"
                showTime={{ format: 'HH:mm' }}
                format="YYYY-MM-DD HH:mm"
                placeholder={['开始时间', '结束时间']}
                onChange={handleCustomRange}
                style={{ marginRight: 6 }}
              />
              {(startTime || endTime) && (
                <button className="search-filter-clear-btn" onClick={handleClearTime}>
                  清除
                </button>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Bottom bar: nav + pagination */}
      <div className="search-filter-bottom">
        {hasNav && (
          <div className="search-filter-nav">
            <span className="search-filter-nav-count">{navIndex! + 1}/{navTotal}</span>
            <button className="search-filter-nav-btn" onClick={onNavPrev} title="上一个">↑</button>
            <button className="search-filter-nav-btn" onClick={onNavNext} title="下一个">↓</button>
          </div>
        )}
        <div style={{ flex: 1 }} />
        {total > pageSize && (
          <Pagination
            size="small"
            current={page}
            pageSize={pageSize}
            total={total}
            onChange={onPageChange}
            showSizeChanger={false}
          />
        )}
      </div>
    </div>
  );
}
