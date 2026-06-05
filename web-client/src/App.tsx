import { useRoutes } from 'react-router-dom';
import { ConfigProvider, App as AntApp } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { antdTheme } from '@/theme/antd';
import { routes } from '@/routes';
import { ConnectionStatus } from '@/components/common/ConnectionStatus';
import '@/styles/theme.css';

function AppInner() {
  const element = useRoutes(routes);
  return <>{element}</>;
}

export function App() {
  return (
    <ConfigProvider theme={antdTheme} locale={zhCN}>
      <AntApp>
        <ConnectionStatus />
        <AppInner />
      </AntApp>
    </ConfigProvider>
  );
}
