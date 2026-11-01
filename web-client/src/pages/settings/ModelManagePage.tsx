import { useState } from 'react';
import { Alert, Tabs } from 'antd';
import { ConnectionsPanel } from './model-management/ConnectionsPanel';
import { ModelsPanel } from './model-management/ModelsPanel';
import { QuotaPoolsPanel } from './model-management/QuotaPoolsPanel';
import { useModelManagementResources } from './model-management/shared';
import './ModelManagePage.css';

export function ModelManagePage() {
  const [activeTab, setActiveTab] = useState('models');
  const resources = useModelManagementResources();
  return <div className="model-management-page">
    <h2 className="model-management-title">模型管理</h2>
    <p className="model-management-intro">先配置接入连接，再选择可调用模型。供应配额池集中管理真实供应额度，0 表示不限。</p>
    {!resources.currentUserId ? <Alert type="warning" showIcon message="请先登录后管理自己的模型、连接与配额池。" /> :
      <Tabs key={resources.currentUserId} activeKey={activeTab} onChange={setActiveTab} items={[
        { key: 'models', label: '模型', children: <ModelsPanel resources={resources} onManageConnections={() => setActiveTab('connections')} /> },
        { key: 'connections', label: '接入连接', children: <ConnectionsPanel resources={resources} onManagePools={() => setActiveTab('pools')} /> },
        { key: 'pools', label: '供应配额池', children: <QuotaPoolsPanel resources={resources} /> },
      ]} />}
  </div>;
}
