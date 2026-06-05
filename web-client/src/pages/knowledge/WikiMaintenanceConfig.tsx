import { useState, useEffect, useMemo } from 'react';
import { Switch, Select, Segmented, Space, Tag } from 'antd';
import type { SegmentedValue } from 'antd/es/segmented';

type Frequency = 'daily' | 'weekly' | 'hourly';

const WEEKDAY_LABELS = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];
const WEEKDAY_VALUES = [1, 2, 3, 4, 5, 6, 7];

const HOURS = Array.from({ length: 24 }, (_, i) => ({ value: i, label: `${i.toString().padStart(2, '0')}` }));
const MINUTES = Array.from({ length: 60 }, (_, i) => ({ value: i, label: `${i.toString().padStart(2, '0')}` }));
const INTERVALS = [1, 2, 3, 4, 6, 8, 12, 24].map(v => ({ value: v, label: `每 ${v} 小时` }));

export interface WikiMaintenanceValue {
  maintenance_enabled: boolean;
  maintenance_cron: string;
}

interface Props {
  value?: WikiMaintenanceValue;
  onChange?: (value: WikiMaintenanceValue) => void;
}

export function WikiMaintenanceConfig({ value, onChange }: Props) {
  const [enabled, setEnabled] = useState(value?.maintenance_enabled ?? false);
  const [freq, setFreq] = useState<Frequency>('daily');
  const [hour, setHour] = useState(3);
  const [minute, setMinute] = useState(0);
  const [weekday, setWeekday] = useState<number[]>([1]);
  const [intervalHours, setIntervalHours] = useState(6);

  // Restore from initial value
  useEffect(() => {
    if (value?.maintenance_cron) {
      const cron = value.maintenance_cron;
      const parts = cron.split(' ');
      if (cron.startsWith('0 */')) {
        setFreq('hourly');
        const h = parseInt(cron.match(/\*\/(\d+)/)?.[1] || '6');
        setIntervalHours(h);
      } else if (parts.length === 5 && parts[2] === '*' && parts[3] === '*') {
        setFreq('daily');
        setMinute(parseInt(parts[0]) || 0);
        setHour(parseInt(parts[1]) || 3);
      } else if (parts.length === 5 && parts[2] === '*' && parts[3] === '*') {
        setFreq('weekly');
        setMinute(parseInt(parts[0]) || 0);
        setHour(parseInt(parts[1]) || 9);
        setWeekday(parts[4].split(',').map(Number).filter(Boolean));
      }
    }
  }, []);

  const cronExpr = useMemo(() => {
    if (!enabled) return '';
    switch (freq) {
      case 'daily':
        return `${minute} ${hour} * * *`;
      case 'weekly':
        return `${minute} ${hour} * * ${weekday.join(',')}`;
      case 'hourly':
        return `0 */${intervalHours} * * *`;
    }
  }, [enabled, freq, hour, minute, weekday, intervalHours]);

  useEffect(() => {
    onChange?.({ maintenance_enabled: enabled, maintenance_cron: cronExpr });
  }, [cronExpr, enabled]);

  const freqOptions = [
    { value: 'daily' as const, label: '每天' },
    { value: 'weekly' as const, label: '每周' },
    { value: 'hourly' as const, label: '每 N 小时' },
  ];

  const handleFreqChange = (val: SegmentedValue) => {
    setFreq(val as Frequency);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <Space>
        <Switch checked={enabled} onChange={setEnabled} />
        <span style={{ fontSize: 13, color: 'var(--aim-text)' }}>启用自动维护</span>
      </Space>

      {enabled && (
        <div style={{ marginLeft: 28, display: 'flex', flexDirection: 'column', gap: 10 }}>
          <Segmented options={freqOptions} value={freq} onChange={handleFreqChange} size="small" />

          {freq === 'daily' && (
            <Space>
              <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)' }}>每天</span>
              <Select size="small" value={hour} onChange={setHour} options={HOURS} style={{ width: 72 }} />
              <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)' }}>:</span>
              <Select size="small" value={minute} onChange={setMinute} options={MINUTES} style={{ width: 72 }} />
            </Space>
          )}

          {freq === 'weekly' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                {WEEKDAY_VALUES.map(v => (
                  <Tag
                    key={v}
                    color={weekday.includes(v) ? 'blue' : 'default'}
                    style={{ cursor: 'pointer', userSelect: 'none', margin: 0 }}
                    onClick={() => {
                      setWeekday(prev =>
                        prev.includes(v) ? prev.filter(x => x !== v) : [...prev, v]
                      );
                    }}
                  >
                    {WEEKDAY_LABELS[v - 1]}
                  </Tag>
                ))}
              </div>
              <Space>
                <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)' }}>时间</span>
                <Select size="small" value={hour} onChange={setHour} options={HOURS} style={{ width: 72 }} />
                <span style={{ fontSize: 13, color: 'var(--aim-text-secondary)' }}>:</span>
                <Select size="small" value={minute} onChange={setMinute} options={MINUTES} style={{ width: 72 }} />
              </Space>
            </div>
          )}

          {freq === 'hourly' && (
            <Select
              size="small"
              value={intervalHours}
              onChange={setIntervalHours}
              options={INTERVALS}
              style={{ width: 160 }}
            />
          )}

          <div style={{ fontSize: 12, color: 'var(--aim-text-tertiary)' }}>
            {cronMap(cronExpr)}
          </div>
        </div>
      )}
    </div>
  );
}

function cronMap(expr: string): string {
  if (!expr) return '';
  const parts = expr.split(' ');
  if (parts.length !== 5) return expr;
  const [, hour, , , weekday] = parts;
  if (parts[2] === '*' && parts[3] === '*' && parts[4] === '*') {
    return `每天 ${hour.padStart(2, '0')}:${parts[0].padStart(2, '0')} 执行`;
  }
  if (parts[2] === '*' && parts[3] === '*') {
    const days = weekday.split(',').map(Number).map(d => WEEKDAY_LABELS[d - 1] || '?').join('、');
    return `每周 ${days} ${hour.padStart(2, '0')}:${parts[0].padStart(2, '0')} 执行`;
  }
  if (expr.startsWith('0 */')) {
    return `每 ${parts[1].replace('*/', '')} 小时执行一次`;
  }
  return expr;
}
