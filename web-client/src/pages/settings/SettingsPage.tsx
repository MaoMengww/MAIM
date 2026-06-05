import { useState } from 'react';
import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { Button, Descriptions, Upload, Modal, Form, Input, Select, DatePicker, message } from 'antd';
import { UploadOutlined, EditOutlined } from '@ant-design/icons';
import { useAuthStore } from '@/stores/auth';
import { authApi } from '@/services/auth';
import { modelApi } from '@/services/model';
import { useQuery } from '@tanstack/react-query';
import { modelOptionLabel } from '@/utils/provider';
import type { UserSettings } from '@/types/model';
import dayjs from 'dayjs';

import './SettingsPage.css';

const { TextArea } = Input;

const GENDER_OPTIONS = [
  { value: 0, label: '未设置' },
  { value: 1, label: '男' },
  { value: 2, label: '女' },
];

const LANG_OPTIONS = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'en-US', label: 'English' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
];

function formatUnixTs(ts: string | number | undefined): string {
  if (!ts) return '-';
  const ms = Number(ts);
  if (isNaN(ms) || ms <= 0) return '-';
  return new Date(ms * 1000).toLocaleString('zh-CN');
}

function formatBirthday(ts: string | number | undefined): string {
  if (!ts) return '-';
  const ms = Number(ts);
  if (isNaN(ms) || ms <= 0) return '-';
  return new Date(ms * 1000).toLocaleDateString('zh-CN');
}

export function SettingsProfile() {
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);

  const [editOpen, setEditOpen] = useState(false);
  const [editLoading, setEditLoading] = useState(false);
  const [editForm] = Form.useForm();

  const [pwdOpen, setPwdOpen] = useState(false);
  const [pwdLoading, setPwdLoading] = useState(false);
  const [pwdForm] = Form.useForm();

  const [phoneOpen, setPhoneOpen] = useState(false);
  const [phoneLoading, setPhoneLoading] = useState(false);
  const [phoneForm] = Form.useForm();

  const [emailOpen, setEmailOpen] = useState(false);
  const [emailLoading, setEmailLoading] = useState(false);
  const [emailForm] = Form.useForm();

  const { data: modelsData, isLoading: modelsLoading } = useQuery({
    queryKey: ['models'],
    queryFn: () => modelApi.list(),
  });
  const models = modelsData?.list || [];

  const handleEditSubmit = async () => {
    try {
      const values = await editForm.validateFields();
      setEditLoading(true);
      const payload: Record<string, unknown> = {};
      if (values.bio !== undefined) payload.bio = values.bio;
      if (values.gender !== undefined) payload.gender = values.gender;
      if (values.birthday) {
        payload.birthday = Math.floor(values.birthday.valueOf() / 1000);
      }
      const updated = await authApi.updateProfile(payload);
      setUser({ ...user!, ...updated });

      const settingsPayload: Record<string, unknown> = {};
      if (values.language) settingsPayload.language = values.language;
      if (values.ai_model_id) {
        const selected = models?.find(m => m.id === values.ai_model_id);
        settingsPayload.ai_model_id = values.ai_model_id;
        settingsPayload.ai_model_name = selected?.model_name || '';
      }
      if (Object.keys(settingsPayload).length > 0) {
        await authApi.updateSettings(settingsPayload);
      }

      message.success('保存成功');
      setEditOpen(false);
    } catch (err: any) {
      if (err?.errorFields) return;
      message.error(err?.response?.data?.message || '保存失败');
    } finally {
      setEditLoading(false);
    }
  };

  const handleOpenEdit = () => {
    const s = (user?.settings || {}) as unknown as UserSettings;
    editForm.setFieldsValue({
      bio: user?.bio || '',
      gender: user?.gender ?? 0,
      birthday: user?.birthday ? dayjs.unix(Number(user.birthday)) : undefined,
      language: s.language || 'zh-CN',
      ai_model_id: s.ai_model_id || undefined,
    });
    setEditOpen(true);
  };

  const handlePwdSubmit = async () => {
    try {
      const values = await pwdForm.validateFields();
      setPwdLoading(true);
      await authApi.updatePassword(values.old_password, values.new_password);
      message.success('密码修改成功');
      setPwdOpen(false);
      pwdForm.resetFields();
    } catch (err: any) {
      if (err?.errorFields) return;
      message.error(err?.response?.data?.message || '修改失败');
    } finally {
      setPwdLoading(false);
    }
  };

  const handleBindPhone = async () => {
    try {
      const values = await phoneForm.validateFields();
      setPhoneLoading(true);
      await authApi.bindPhone(values.phone);
      setUser({ ...user!, phone: values.phone });
      message.success('手机绑定成功');
      setPhoneOpen(false);
      phoneForm.resetFields();
    } catch (err: any) {
      if (err?.errorFields) return;
      message.error(err?.response?.data?.message || '绑定失败');
    } finally {
      setPhoneLoading(false);
    }
  };

  const handleBindEmail = async () => {
    try {
      const values = await emailForm.validateFields();
      setEmailLoading(true);
      await authApi.bindEmail(values.email);
      setUser({ ...user!, email: values.email });
      message.success('邮箱绑定成功');
      setEmailOpen(false);
      emailForm.resetFields();
    } catch (err: any) {
      if (err?.errorFields) return;
      message.error(err?.response?.data?.message || '绑定失败');
    } finally {
      setEmailLoading(false);
    }
  };

  return (
    <>
      <div className="settings-content-header">
        <h2 className="settings-content-title">个人资料</h2>
        <div className="settings-content-actions">
          <Button icon={<EditOutlined />} onClick={handleOpenEdit}>编辑资料</Button>
        </div>
      </div>

      <div className="settings-avatar-section">
        <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
          {user?.avatar ? (
            <img
              key={user.avatar}
              src={user.avatar}
              alt="avatar"
              style={{ width: 64, height: 64, borderRadius: '50%', objectFit: 'cover' }}
            />
          ) : (
            <div style={{ width: 64, height: 64, borderRadius: '50%', background: 'linear-gradient(135deg, #FF7D4A, #FF5E62)', display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#fff', fontSize: 24, fontWeight: 600 }}>
              {user?.username?.[0]?.toUpperCase() || '?'}
            </div>
          )}
          <Upload
            showUploadList={false}
            customRequest={({ file }) => {
              authApi.uploadAvatar(file as File).then((data: any) => {
                const url = data?.url || data?.avatar || '';
                if (url && user) {
                  setUser({ ...user, avatar: url });
                }
                message.success('头像更新成功');
              }).catch(() => message.error('上传失败'));
            }}
          >
            <Button icon={<UploadOutlined />}>更换头像</Button>
          </Upload>
        </div>
      </div>

      <Descriptions
        column={1}
        bordered
        size="small"
        styles={{
          label: { color: 'var(--aim-text-secondary)', background: 'var(--aim-surface)', width: 120 },
          content: { color: 'var(--aim-text)' },
        }}
      >
        <Descriptions.Item label="MID">
          <span style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', fontFamily: 'monospace' }}>
            {user?.id ?? '-'}
          </span>
        </Descriptions.Item>
        <Descriptions.Item label="用户名">{user?.username || '-'}</Descriptions.Item>
        <Descriptions.Item label="手机">
          <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {user?.phone || '-'}
            {!user?.phone && (
              <Button type="link" size="small" onClick={() => setPhoneOpen(true)}>绑定</Button>
            )}
          </span>
        </Descriptions.Item>
        <Descriptions.Item label="邮箱">
          <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {user?.email || '-'}
            {!user?.email && (
              <Button type="link" size="small" onClick={() => setEmailOpen(true)}>绑定</Button>
            )}
          </span>
        </Descriptions.Item>
        <Descriptions.Item label="个人简介">{user?.bio || '-'}</Descriptions.Item>
        <Descriptions.Item label="性别">
          {user?.gender === 1 ? '男' : user?.gender === 2 ? '女' : user?.gender === 0 ? '未设置' : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="生日">
          {user?.birthday ? formatBirthday(user.birthday) : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="账户余额">
          <span style={{ color: '#52c41a', fontWeight: 600 }}>
            ¥{Number(user?.balance ?? 0).toFixed(2)}
          </span>
        </Descriptions.Item>
        <Descriptions.Item label="注册时间">
          {formatUnixTs(user?.created_at)}
        </Descriptions.Item>
        <Descriptions.Item label="更新时间">
          {formatUnixTs(user?.updated_at)}
        </Descriptions.Item>
      </Descriptions>

      <div className="settings-password-section">
        <Button onClick={() => setPwdOpen(true)}>修改密码</Button>
      </div>

      <Modal
        title="编辑个人资料"
        open={editOpen}
        onOk={handleEditSubmit}
        onCancel={() => setEditOpen(false)}
        confirmLoading={editLoading}
        destroyOnHidden
      >
        <Form form={editForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item label="个人简介" name="bio">
            <TextArea rows={3} placeholder="介绍一下自己" maxLength={200} showCount />
          </Form.Item>
          <Form.Item label="性别" name="gender">
            <Select options={GENDER_OPTIONS} />
          </Form.Item>
          <Form.Item label="生日" name="birthday">
            <DatePicker style={{ width: '100%' }} placeholder="选择生日" />
          </Form.Item>
          <Form.Item label="语言" name="language">
            <Select options={LANG_OPTIONS} />
          </Form.Item>
          <Form.Item label="AI 助手模型" name="ai_model_id">
            <Select
              placeholder="选择模型"
              options={models?.map(m => ({ value: m.id, label: modelOptionLabel(m) })) || []}
              loading={modelsLoading}
              notFoundContent="暂无可用模型"
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="修改密码"
        open={pwdOpen}
        onOk={handlePwdSubmit}
        onCancel={() => { setPwdOpen(false); pwdForm.resetFields(); }}
        confirmLoading={pwdLoading}
        destroyOnHidden
      >
        <Form form={pwdForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item label="当前密码" name="old_password" rules={[{ required: true, message: '请输入当前密码' }]}>
            <Input.Password placeholder="请输入当前密码" />
          </Form.Item>
          <Form.Item label="新密码" name="new_password" rules={[
            { required: true, message: '请输入新密码' },
            { min: 6, message: '密码至少6位' },
          ]}>
            <Input.Password placeholder="请输入新密码" />
          </Form.Item>
          <Form.Item label="确认新密码" name="confirm_password" rules={[
            { required: true, message: '请确认新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('new_password') === value) return Promise.resolve();
                return Promise.reject(new Error('两次输入的密码不一致'));
              },
            }),
          ]}>
            <Input.Password placeholder="请再次输入新密码" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="绑定手机"
        open={phoneOpen}
        onOk={handleBindPhone}
        onCancel={() => { setPhoneOpen(false); phoneForm.resetFields(); }}
        confirmLoading={phoneLoading}
        destroyOnHidden
      >
        <Form form={phoneForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item label="手机号" name="phone" rules={[
            { required: true, message: '请输入手机号' },
            { pattern: /^1\d{10}$/, message: '手机号格式不正确' },
          ]}>
            <Input placeholder="请输入手机号" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="绑定邮箱"
        open={emailOpen}
        onOk={handleBindEmail}
        onCancel={() => { setEmailOpen(false); emailForm.resetFields(); }}
        confirmLoading={emailLoading}
        destroyOnHidden
      >
        <Form form={emailForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item label="邮箱" name="email" rules={[
            { required: true, message: '请输入邮箱' },
            { type: 'email', message: '邮箱格式不正确' },
          ]}>
            <Input placeholder="请输入邮箱" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}

export function SettingsPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const currentSection = location.pathname === '/settings' ? 'settings' : (location.pathname.split('/').pop() || 'settings');

  return (
    <div className="settings-page">
      <div className="settings-sidebar">
        <div className="settings-menu">
          <h3>设置</h3>
          <button
            className={`settings-menu-item ${currentSection === 'settings' ? 'active' : ''}`}
            onClick={() => navigate('/settings')}
          >
            个人资料
          </button>
          <button
            className={`settings-menu-item ${currentSection === 'models' ? 'active' : ''}`}
            onClick={() => navigate('/settings/models')}
          >
            模型管理
          </button>
          <button
            className={`settings-menu-item ${currentSection === 'mcp' ? 'active' : ''}`}
            onClick={() => navigate('/settings/mcp')}
          >
            MCP 服务器
          </button>
          <button
            className={`settings-menu-item ${currentSection === 'billing' ? 'active' : ''}`}
            onClick={() => navigate('/settings/billing')}
          >
            账单管理
          </button>
        </div>
      </div>
      <div className="settings-content">
        <Outlet />
      </div>
    </div>
  );
}
