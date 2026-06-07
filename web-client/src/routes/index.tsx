import { Navigate, type RouteObject } from 'react-router-dom';
import { AuthLayout } from '@/layouts/AuthLayout';
import { MainLayout } from '@/layouts/MainLayout';
import { LoginPage } from '@/pages/auth/LoginPage';
import { RegisterPage } from '@/pages/auth/RegisterPage';
import { ConvListPage } from '@/pages/conversations/ConvListPage';
import { ChatPage } from '@/pages/conversations/ChatPage';
import { ContactListPage } from '@/pages/contacts/ContactListPage';
import { ContactDetailPage } from '@/pages/contacts/ContactDetailPage';
import { BotListPage } from '@/pages/bots/BotListPage';
import { BotDetailPage } from '@/pages/bots/BotDetailPage';
import { KBListPage } from '@/pages/knowledge/KBListPage';
import { KBDetailPage } from '@/pages/knowledge/KBDetailPage';
import { DocDetailPage } from '@/pages/knowledge/DocDetailPage';
import { WikiPageView } from '@/pages/knowledge/WikiPageView';
import { WikiGraphPage } from '@/pages/knowledge/WikiGraphPage';
import { WikiDocDetailPage } from '@/pages/knowledge/WikiDocDetailPage';
import { SettingsPage, ProfilePage } from '@/pages/settings/SettingsPage';
import { ModelManagePage } from '@/pages/settings/ModelManagePage';
import { McpServersPage } from '@/pages/settings/McpServersPage';
import { BillingPage } from '@/pages/settings/BillingPage';

export const routes: RouteObject[] = [
  {
    element: <AuthLayout />,
    children: [
      { path: '/login', element: <LoginPage /> },
      { path: '/register', element: <RegisterPage /> },
    ],
  },
  {
    element: <MainLayout />,
    children: [
      { index: true, element: <Navigate to="/conversations" replace /> },
      {
        path: '/conversations',
        element: <ConvListPage />,
        children: [
          { path: ':id', element: <ChatPage /> },
        ],
      },
      {
        path: '/contacts',
        element: <ContactListPage />,
        children: [
          { path: ':id', element: <ContactDetailPage /> },
        ],
      },
      { path: '/bots', element: <BotListPage /> },
      { path: '/bots/:id', element: <BotDetailPage /> },
      { path: '/knowledge', element: <KBListPage /> },
      { path: '/knowledge/:id', element: <KBDetailPage /> },
      { path: '/knowledge/:kbId/documents/:docId', element: <DocDetailPage /> },
      { path: '/knowledge/:kbId/wiki/documents/:docId', element: <WikiDocDetailPage /> },
      { path: '/knowledge/:kbId/wiki/graph', element: <WikiGraphPage /> },
      { path: '/knowledge/:kbId/wiki/*', element: <WikiPageView /> },
      { path: '/settings', element: <SettingsPage />, children: [
        { index: true, element: <ProfilePage /> },
        { path: 'models', element: <ModelManagePage /> },
        { path: 'mcp', element: <McpServersPage /> },
        { path: 'billing', element: <BillingPage /> },
      ] },
    ],
  },
];
