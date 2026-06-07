import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Upload, Form, Input, Select, DatePicker, message, Skeleton, Tooltip, Modal } from 'antd';
import {
  UploadOutlined,
  EditOutlined,
  LockOutlined,
  MailOutlined,
  PhoneOutlined,
  ManOutlined,
  WomanOutlined,
  GiftOutlined,
  WalletOutlined,
  CalendarOutlined,
  IdcardOutlined,
  RobotOutlined,
  UserOutlined,
  CheckOutlined,
  CloseOutlined,
} from '@ant-design/icons';
import { useAuthStore } from '@/stores/auth';
import { authApi } from '@/services/auth';
import { modelApi } from '@/services/model';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { modelOptionLabel } from '@/utils/provider';
import type { UserSettings } from '@/types/model';
import dayjs from 'dayjs';

import './ProfilePage.css';

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

function fmtDate(ts: string | number | undefined): string {
  if (!ts) return '-';
  const v = Number(ts);
  if (isNaN(v) || v <= 0) return '-';
  return new Date(v * 1000).toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  });
}

function fmtDateTime(ts: string | number | undefined): string {
  if (!ts) return '-';
  const v = Number(ts);
  if (isNaN(v) || v <= 0) return '-';
  return new Date(v * 1000).toLocaleString('zh-CN');
}

const GENDER_LABEL: Record<number, string> = { 0: '未设置', 1: '男', 2: '女' };

export function ProfilePage() {
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);
  const queryClient = useQueryClient();

  // ─── settings query ───
  const { data: settings, isLoading: settingsLoading } = useQuery<UserSettings>({
    queryKey: ['userSettings'],
    queryFn: () => authApi.getSettings() as Promise<UserSettings>,
  });

  // ─── models ───
  const { data: modelsData } = useQuery({
    queryKey: ['models', 'chat'],
    queryFn: () => modelApi.list('chat'),
  });
  const models = modelsData?.list || [];

  const selectedModel = models.find((m) => m.id === settings?.ai_model_id);

  // ─── edit state ───
  const [editing, setEditing] = useState(false);
  const [editLoading, setEditLoading] = useState(false);
  const [form] = Form.useForm();

  // ─── password modal ───
  const [pwdOpen, setPwdOpen] = useState(false);
  const [pwdLoading, setPwdLoading] = useState(false);
  const [pwdForm] = Form.useForm();

  // ─── bind modals ───
  const [phoneOpen, setPhoneOpen] = useState(false);
  const [phoneLoading, setPhoneLoading] = useState(false);
  const [phoneForm] = Form.useForm();
  const [emailOpen, setEmailOpen] = useState(false);
  const [emailLoading, setEmailLoading] = useState(false);
  const [emailForm] = Form.useForm();

  // ─── avatar upload loading ───
  const [avatarLoading, setAvatarLoading] = useState(false);

  // ─── enter edit mode ───
  const startEdit = useCallback(() => {
    form.setFieldsValue({
      bio: user?.bio || '',
      gender: user?.gender ?? 0,
      birthday: user?.birthday ? dayjs.unix(Number(user.birthday)) : undefined,
      language: settings?.language || 'zh-CN',
      ai_model_id: settings?.ai_model_id || undefined,
    });
    setEditing(true);
  }, [user, settings, form]);

  // ─── cancel edit ───
  const cancelEdit = useCallback(() => {
    setEditing(false);
  }, []);

  // ─── submit edit ───
  const handleEditSubmit = async () => {
    try {
      const values = await form.validateFields();
      setEditLoading(true);

      // Update profile
      const profilePayload: Record<string, unknown> = {};
      if (values.bio !== undefined) profilePayload.bio = values.bio;
      if (values.gender !== undefined) profilePayload.gender = values.gender;
      if (values.birthday) {
        profilePayload.birthday = Math.floor(values.birthday.valueOf() / 1000);
      }
      if (Object.keys(profilePayload).length > 0) {
        const updated = await authApi.updateProfile(profilePayload);
        setUser({ ...user!, ...updated });
      }

      // Update settings
      const settingsPayload: Record<string, unknown> = {};
      if (values.language) settingsPayload.language = values.language;
      if (values.ai_model_id) {
        const selected = models.find((m) => m.id === values.ai_model_id);
        settingsPayload.ai_model_id = values.ai_model_id;
        settingsPayload.ai_model_name = selected?.model_name || settings?.ai_model_name || '';
      }
      if (Object.keys(settingsPayload).length > 0) {
        await authApi.updateSettings(settingsPayload);
        queryClient.invalidateQueries({ queryKey: ['userSettings'] });
      }

      message.success('保存成功');
      setEditing(false);
    } catch (err: any) {
      if (err?.errorFields) return;
      message.error(err?.response?.data?.message || '保存失败');
    } finally {
      setEditLoading(false);
    }
  };

  // ─── password submit ───
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

  // ─── bind phone ───
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

  // ─── bind email ───
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

  // ─── gender icon ───
  const genderIcon = user?.gender === 1 ? (
    <ManOutlined style={{ color: 'var(--aim-sky)' }} />
  ) : user?.gender === 2 ? (
    <WomanOutlined style={{ color: 'var(--aim-primary)' }} />
  ) : null;

  return (
    <div className="profile-page">
      {/* ═══ Hero Section ═══ */}
      <div className="profile-hero">
        <div className="profile-hero-bg" />
        <div className="profile-hero-content">
          {/* Avatar */}
          <div className="profile-avatar-wrap">
            <div className="profile-avatar-ring">
              {user?.avatar ? (
                <img
                  key={user.avatar}
                  src={user.avatar}
                  alt="avatar"
                  className="profile-avatar-img"
                />
              ) : (
                <div className="profile-avatar-placeholder">
                  {user?.username?.[0]?.toUpperCase() || '?'}
                </div>
              )}
            </div>
            <Upload
              showUploadList={false}
              customRequest={({ file }) => {
                setAvatarLoading(true);
                authApi
                  .uploadAvatar(file as File)
                  .then((data: any) => {
                    const url = data?.url || data?.avatar || '';
                    if (url && user) {
                      setUser({ ...user, avatar: url });
                    }
                    message.success('头像更新成功');
                  })
                  .catch(() => message.error('上传失败'))
                  .finally(() => setAvatarLoading(false));
              }}
            >
              <Tooltip title="更换头像">
                <button className="profile-avatar-upload-btn" disabled={avatarLoading}>
                  <UploadOutlined />
                </button>
              </Tooltip>
            </Upload>
          </div>

          {/* Name & Bio */}
          <div className="profile-hero-info">
            <div className="profile-hero-name-row">
              <h1 className="profile-hero-name">{user?.username || '未命名'}</h1>
              <button className="profile-edit-btn" onClick={startEdit}>
                <EditOutlined /> 编辑资料
              </button>
            </div>
            <p className="profile-hero-bio">{user?.bio || '这个人很懒，什么都没写...'}</p>
            <div className="profile-hero-meta">
              <span className="profile-meta-tag">
                <IdcardOutlined />
                MID: {user?.id ?? '-'}
              </span>
              <span className="profile-meta-tag">
                <CalendarOutlined />
                加入于 {fmtDate(user?.created_at)}
              </span>
            </div>
          </div>

          {/* Balance Card */}
          <div className="profile-balance-card">
            <div className="profile-balance-icon">
              <WalletOutlined />
            </div>
            <div className="profile-balance-info">
              <span className="profile-balance-label">账户余额</span>
              <span className="profile-balance-value">
                ¥{Number(user?.balance ?? 0).toFixed(2)}
              </span>
            </div>
          </div>
        </div>
      </div>

      {/* ═══ Content Grid ═══ */}
      <div className="profile-grid">
        {/* Left Column: Personal Info */}
        <div className="profile-grid-left">
          {/* Edit Panel */}
          {editing && (
            <div className="profile-card profile-edit-card">
              <div className="profile-card-header">
                <h3 className="profile-card-title">
                  <EditOutlined /> 编辑资料
                </h3>
                <div className="profile-card-actions">
                  <Button size="small" onClick={cancelEdit} icon={<CloseOutlined />}>
                    取消
                  </Button>
                  <Button
                    size="small"
                    type="primary"
                    onClick={handleEditSubmit}
                    loading={editLoading}
                    icon={<CheckOutlined />}
                  >
                    保存
                  </Button>
                </div>
              </div>
              <Form form={form} layout="vertical" className="profile-edit-form">
                <Form.Item label="个人简介" name="bio">
                  <TextArea rows={3} placeholder="介绍一下自己..." maxLength={200} showCount />
                </Form.Item>
                <div className="profile-edit-row">
                  <Form.Item label="性别" name="gender" className="profile-edit-half">
                    <Select options={GENDER_OPTIONS} />
                  </Form.Item>
                  <Form.Item label="生日" name="birthday" className="profile-edit-half">
                    <DatePicker style={{ width: '100%' }} placeholder="选择生日" />
                  </Form.Item>
                </div>
                <div className="profile-edit-row">
                  <Form.Item label="语言" name="language" className="profile-edit-half">
                    <Select options={LANG_OPTIONS} />
                  </Form.Item>
                  <Form.Item label="AI 助手模型" name="ai_model_id" className="profile-edit-half">
                    <Select
                      placeholder="选择模型"
                      options={models.map((m) => ({
                        value: m.id,
                        label: modelOptionLabel(m),
                      }))}
                      notFoundContent="暂无可用模型"
                    />
                  </Form.Item>
                </div>
              </Form>
            </div>
          )}

          {/* Personal Info Card */}
          <div className="profile-card">
            <div className="profile-card-header">
              <h3 className="profile-card-title">
                <UserOutlined /> 个人信息
              </h3>
            </div>
            <div className="profile-info-list">
              <div className="profile-info-item">
                <span className="profile-info-label">用户名</span>
                <span className="profile-info-value">{user?.username || '-'}</span>
              </div>
              <div className="profile-info-item">
                <span className="profile-info-label">
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                    性别 {genderIcon}
                  </span>
                </span>
                <span className="profile-info-value">
                  {GENDER_LABEL[user?.gender ?? 0]}
                </span>
              </div>
              <div className="profile-info-item">
                <span className="profile-info-label">
                  <GiftOutlined /> 生日
                </span>
                <span className="profile-info-value">{fmtDate(user?.birthday)}</span>
              </div>
              <div className="profile-info-item">
                <span className="profile-info-label">
                  <PhoneOutlined /> 手机
                </span>
                <span className="profile-info-value">
                  {user?.phone || (
                    <Button type="link" size="small" onClick={() => setPhoneOpen(true)}>
                      绑定手机
                    </Button>
                  )}
                </span>
              </div>
              <div className="profile-info-item">
                <span className="profile-info-label">
                  <MailOutlined /> 邮箱
                </span>
                <span className="profile-info-value">
                  {user?.email || (
                    <Button type="link" size="small" onClick={() => setEmailOpen(true)}>
                      绑定邮箱
                    </Button>
                  )}
                </span>
              </div>
            </div>
          </div>

          {/* Security Card */}
          <div className="profile-card">
            <div className="profile-card-header">
              <h3 className="profile-card-title">
                <LockOutlined /> 安全
              </h3>
            </div>
            <div className="profile-security-content">
              <div className="profile-info-item">
                <span className="profile-info-label">密码</span>
                <span className="profile-info-value">••••••••</span>
              </div>
              <Button onClick={() => setPwdOpen(true)} size="small">
                修改密码
              </Button>
            </div>
          </div>
        </div>

        {/* Right Column: Settings & Stats */}
        <div className="profile-grid-right">
          {/* AI Model Card */}
          <div className="profile-card profile-model-card">
            <div className="profile-card-header">
              <h3 className="profile-card-title">
                <RobotOutlined /> AI 助手
              </h3>
              <Button size="small" onClick={startEdit}>
                <EditOutlined />
              </Button>
            </div>
            {settingsLoading ? (
              <Skeleton active paragraph={{ rows: 2 }} />
            ) : (
              <div className="profile-model-display">
                {selectedModel ? (
                  <>
                    <div className="profile-model-name">
                      {selectedModel.model_name}
                      {Number(selectedModel.owner_id) === 0 && (
                        <span className="profile-model-badge">官方</span>
                      )}
                    </div>
                    <div className="profile-model-provider">
                      {selectedModel.provider}
                    </div>
                  </>
                ) : (
                  <div className="profile-model-empty">
                    <RobotOutlined style={{ fontSize: 28, color: 'var(--aim-text-tertiary)' }} />
                    <span>未配置 AI 模型</span>
                    <Button type="primary" size="small" onClick={startEdit}>
                      立即配置
                    </Button>
                  </div>
                )}
                <div className="profile-model-meta">
                  <div className="profile-model-meta-item">
                    <span className="profile-meta-dot" />
                    语言: {settings?.language || 'zh-CN'}
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* Stats Card */}
          <div className="profile-card profile-stats-card">
            <div className="profile-card-header">
              <h3 className="profile-card-title">账号信息</h3>
            </div>
            <div className="profile-stats-grid">
              <div className="profile-stat-item">
                <span className="profile-stat-value">{fmtDate(user?.created_at)}</span>
                <span className="profile-stat-label">注册时间</span>
              </div>
              <div className="profile-stat-item">
                <span className="profile-stat-value">{fmtDateTime(user?.updated_at)}</span>
                <span className="profile-stat-label">最后更新</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* ═══ Modals ═══ */}

      {/* Change Password */}
      <Modal
        title="修改密码"
        open={pwdOpen}
        onOk={handlePwdSubmit}
        onCancel={() => {
          setPwdOpen(false);
          pwdForm.resetFields();
        }}
        confirmLoading={pwdLoading}
        destroyOnHidden
      >
        <Form form={pwdForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item
            label="当前密码"
            name="old_password"
            rules={[{ required: true, message: '请输入当前密码' }]}
          >
            <Input.Password placeholder="请输入当前密码" />
          </Form.Item>
          <Form.Item
            label="新密码"
            name="new_password"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 6, message: '密码至少6位' },
            ]}
          >
            <Input.Password placeholder="请输入新密码" />
          </Form.Item>
          <Form.Item
            label="确认新密码"
            name="confirm_password"
            rules={[
              { required: true, message: '请确认新密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('new_password') === value)
                    return Promise.resolve();
                  return Promise.reject(new Error('两次输入的密码不一致'));
                },
              }),
            ]}
          >
            <Input.Password placeholder="请再次输入新密码" />
          </Form.Item>
        </Form>
      </Modal>

      {/* Bind Phone */}
      <Modal
        title="绑定手机"
        open={phoneOpen}
        onOk={handleBindPhone}
        onCancel={() => {
          setPhoneOpen(false);
          phoneForm.resetFields();
        }}
        confirmLoading={phoneLoading}
        destroyOnHidden
      >
        <Form form={phoneForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item
            label="手机号"
            name="phone"
            rules={[
              { required: true, message: '请输入手机号' },
              { pattern: /^1\d{10}$/, message: '手机号格式不正确' },
            ]}
          >
            <Input placeholder="请输入手机号" />
          </Form.Item>
        </Form>
      </Modal>

      {/* Bind Email */}
      <Modal
        title="绑定邮箱"
        open={emailOpen}
        onOk={handleBindEmail}
        onCancel={() => {
          setEmailOpen(false);
          emailForm.resetFields();
        }}
        confirmLoading={emailLoading}
        destroyOnHidden
      >
        <Form form={emailForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item
            label="邮箱"
            name="email"
            rules={[
              { required: true, message: '请输入邮箱' },
              { type: 'email', message: '邮箱格式不正确' },
            ]}
          >
            <Input placeholder="请输入邮箱" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
