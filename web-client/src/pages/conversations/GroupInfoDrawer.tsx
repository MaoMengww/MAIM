import { useState, useRef, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { message, Drawer, Button, Tag, Divider, Input, InputNumber, Switch, Popconfirm, Select, Radio, Modal as AntModal } from 'antd';
import { isAxiosError } from 'axios';
import {
  EditOutlined, UserAddOutlined, DeleteOutlined,
  CrownOutlined, CloseOutlined, CameraOutlined,
  RobotOutlined, AudioMutedOutlined, SoundOutlined,
} from '@ant-design/icons';
import { convApi } from '@/services/conversation';
import { friendApi } from '@/services/friend';
import { botApi, botParticipationEnabled, isBotBindingActive } from '@/services/bot';
import { Avatar } from '@/components/common/Avatar';
import type { ConvMember, BotInConv } from '@/types/model';
import { BindingRules } from './BindingRules';
import './ChatPage.css';


interface GroupInfoDrawerProps {
  conv: any;
  members: ConvMember[];
  isOwner: boolean;
  isAdmin: boolean;
  currentUserId: string;
  open: boolean;
  onClose: () => void;
  onMembersChange: () => void;
}

export function GroupInfoDrawer(props: GroupInfoDrawerProps) {
  const { conv, members, isOwner, isAdmin, currentUserId, open, onClose } = props;
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const convId = conv?.id;
  const isPrivate = conv?.type === 'private';

  const [editingName, setEditingName] = useState(false);
  const [editNameVal, setEditNameVal] = useState('');
  const [editingAnnouncement, setEditingAnnouncement] = useState(false);
  const [editAnnouncementVal, setEditAnnouncementVal] = useState('');
  const [addMemberOpen, setAddMemberOpen] = useState(false);
  const [selectedFriends, setSelectedFriends] = useState<string[]>([]);
  const [transferOpen, setTransferOpen] = useState(false);
  const [transferTarget, setTransferTarget] = useState<string | null>(null);
  const [addBotOpen, setAddBotOpen] = useState(false);
  const [selectedBotId, setSelectedBotId] = useState<string | null>(null);
  const [grantBot, setGrantBot] = useState<BotInConv | null>(null);
  const [startSeq, setStartSeq] = useState<number | null>(null);
  const [endSeq, setEndSeq] = useState<number | null>(null);
  const avatarRef = useRef<HTMLInputElement>(null);

  // ─── Queries ───

  const { data: friendsData } = useQuery({
    queryKey: ['friends'],
    queryFn: () => friendApi.list(),
    enabled: open,
  });
  const friends = friendsData?.list ?? [];

  const { data: convBots = [], isLoading: botsLoading, isError: botsError, refetch: refetchBots } = useQuery({
    queryKey: ['conv-bots', convId],
    queryFn: () => botApi.listConvBots(convId!),
    enabled: open && !!convId,
  });
  useEffect(() => {
    if (!open || !isAdmin || !grantBot || !convBots.some((bot) => bot.binding_id === grantBot.binding_id && isBotBindingActive(bot))) {
      setGrantBot(null); setStartSeq(null); setEndSeq(null);
    }
  }, [convId, convBots, grantBot, isAdmin, open]);

  const { data: userBotsData, isLoading: availableBotsLoading, isError: availableBotsError } = useQuery({
    queryKey: ['user-bots'],
    queryFn: () => botApi.list(),
    enabled: addBotOpen,
  });
  const userBots = userBotsData?.list ?? [];

  // ─── Mutations ───

  const updateAvatarMutation = useMutation({
    mutationFn: (file: File) => convApi.uploadAvatar(convId!, file),
    onSuccess: () => {
      message.success('群头像已更新');
      queryClient.invalidateQueries({ queryKey: ['conversation', convId] });
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const updateNameMutation = useMutation({
    mutationFn: (name: string) => convApi.update(convId!, { name }),
    onSuccess: () => {
      message.success('群名称已更新');
      setEditingName(false);
      queryClient.invalidateQueries({ queryKey: ['conversation', convId] });
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const announcementMutation = useMutation({
    mutationFn: (content: string) => convApi.setAnnouncement(convId!, content),
    onSuccess: () => {
      message.success('公告已更新');
      setEditingAnnouncement(false);
      queryClient.invalidateQueries({ queryKey: ['conversation', convId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const deleteAnnouncementMutation = useMutation({
    mutationFn: () => convApi.deleteAnnouncement(convId!),
    onSuccess: () => {
      message.success('公告已删除');
      queryClient.invalidateQueries({ queryKey: ['conversation', convId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const addMembersMutation = useMutation({
    mutationFn: (memberIds: string[]) => convApi.addMembers(convId!, memberIds),
    onSuccess: () => {
      message.success('已添加成员');
      setAddMemberOpen(false);
      setSelectedFriends([]);
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const removeMemberMutation = useMutation({
    mutationFn: (userId: string) => convApi.removeMembers(convId!, [userId]),
    onSuccess: () => {
      message.success('已移除成员');
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const muteMemberMutation = useMutation({
    mutationFn: ({ userId, duration }: { userId: string; duration?: number }) =>
      convApi.muteMember(convId!, userId, duration),
    onSuccess: () => {
      message.success('已禁言');
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const unmuteMemberMutation = useMutation({
    mutationFn: (userId: string) => convApi.unmuteMember(convId!, userId),
    onSuccess: () => {
      message.success('已取消禁言');
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const muteAllMutation = useMutation({
    mutationFn: () => convApi.muteAll(convId!),
    onSuccess: () => {
      message.success('已全员禁言');
      queryClient.invalidateQueries({ queryKey: ['conversation', convId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const unmuteAllMutation = useMutation({
    mutationFn: () => convApi.unmuteAll(convId!),
    onSuccess: () => {
      message.success('已取消全员禁言');
      queryClient.invalidateQueries({ queryKey: ['conversation', convId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const transferMutation = useMutation({
    mutationFn: (newOwnerId: string) => convApi.transferOwner(convId!, newOwnerId),
    onSuccess: () => {
      message.success('群主已转让');
      setTransferOpen(false);
      setTransferTarget(null);
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const leaveGroupMutation = useMutation({
    mutationFn: () => convApi.removeMembers(convId!, [currentUserId]),
    onSuccess: () => {
      message.success('已退出群聊');
      navigate('/conversations');
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const deleteConvMutation = useMutation({
    mutationFn: () => convApi.delete(convId!),
    onSuccess: () => {
      message.success('已删除会话');
      navigate('/conversations');
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '删除失败'),
  });

  const updateMemberRoleMutation = useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: number }) =>
      convApi.updateMember(convId!, userId, { role }),
    onSuccess: () => {
      message.success('成员角色已更新');
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const addBotMutation = useMutation({
    mutationFn: ({ botId }: { botId: string }) =>
      botApi.addToConv(convId!, botId),
    onSuccess: () => {
      message.success('已添加机器人');
      setAddBotOpen(false);
      setSelectedBotId(null);
      queryClient.invalidateQueries({ queryKey: ['conv-bots', convId] });
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const removeBotMutation = useMutation({
    mutationFn: (botId: string) => botApi.removeFromConv(convId!, botId),
    onSuccess: (_data, botId) => {
      message.success('已移除机器人');
      queryClient.setQueryData(['conv-bots', convId], (old: BotInConv[] | undefined) => {
        if (!old) return old;
        return old.filter((b) => b.bot_id !== botId);
      });
      queryClient.invalidateQueries({ queryKey: ['conv-bots', convId] });
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const participationMutation = useMutation({
    mutationFn: async ({ bot, enabled }: { bot: BotInConv; enabled: boolean }) => {
      if (!isAdmin) throw new Error('当前无权管理 Bot 绑定参与许可');
      const current = (await botApi.listConvBots(convId!)).find((candidate) => candidate.binding_id === bot.binding_id);
      if (!current || current.status !== 'active') throw new Error('此 Bot 已移出或停用，请刷新当前绑定');
      const settings: unknown = JSON.parse(current.bot_settings || '{}');
      if (!settings || typeof settings !== 'object' || Array.isArray(settings)) throw new Error('绑定设置无效，请刷新后重试');
      await botApi.setParticipation(convId!, bot.bot_id, JSON.stringify({ ...settings, enabled }));
    },
    onSuccess: async (_data, { enabled }) => {
      setGrantBot(null); setStartSeq(null); setEndSeq(null);
      await refetchBots();
      props.onMembersChange();
      message.success(enabled ? '已允许此绑定接收新工作，旧执行和交互不会恢复' : '已撤销此绑定参与许可，停止实际结果请在执行记录中查询');
    },
    onError: (error: unknown) => {
      if (isAxiosError(error) && typeof error.response?.data?.message === 'string') message.error(error.response.data.message);
      else if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') message.error(error.message);
      else message.error('参与许可更新失败，请刷新当前绑定后重试');
    },
  });

  const grantHistoryMutation = useMutation({
    mutationFn: async () => {
      if (!grantBot || startSeq === null || endSeq === null || !Number.isSafeInteger(startSeq)
        || !Number.isSafeInteger(endSeq) || startSeq < 1 || endSeq < startSeq || endSeq > conv.max_seq) throw new Error('请选择会话内有效的消息序号范围');
      if (!isAdmin || !convBots.some((bot) => bot.binding_id === grantBot.binding_id && isBotBindingActive(bot))) throw new Error('此 Bot 绑定已失效或当前无授权资格，请重新选择');
      return botApi.grantHistory(convId!, grantBot.bot_id, grantBot.binding_id, startSeq, endSeq);
    },
    onSuccess: ({ grant_id }) => {
      message.success(`历史授权已创建：${grant_id}`, 8);
      setGrantBot(null); setStartSeq(null); setEndSeq(null);
    },
    onError: (error: unknown) => message.error(error instanceof Error ? error.message : '历史授权失败'),
  });

  return (
    <>
      <Drawer
        title={isPrivate ? 'Bot 绑定信息' : '群聊信息'}
        placement="right"
        width="min(380px, 100vw)"
        open={open}
        onClose={() => { onClose(); setEditingName(false); setEditingAnnouncement(false); }}
        styles={{ body: { padding: '16px 20px' } }}
      >
        <div className="group-info-content">
          {/* Section 1: Avatar + Name */}
          <div className="group-info-avatar-section">
            <div className="group-info-avatar-wrapper">
              <Avatar name={isPrivate ? convBots[0]?.name || conv?.name || 'Bot' : conv?.name || '群聊'} src={isPrivate ? convBots[0]?.avatar || conv?.avatar : conv?.avatar} size={80} />
              {isAdmin && !isPrivate && (
                <>
                  <div className="group-info-avatar-overlay" onClick={() => avatarRef.current?.click()}>
                    <CameraOutlined />
                  </div>
                  <input
                    ref={avatarRef}
                    type="file"
                    accept="image/*"
                    style={{ display: 'none' }}
                    onChange={(e) => {
                      const file = e.target.files?.[0];
                      if (file) updateAvatarMutation.mutate(file);
                      e.target.value = '';
                    }}
                  />
                </>
              )}
            </div>
            <div className="group-info-name-section">
              {editingName ? (
                <div className="group-info-edit-row" style={{ justifyContent: 'center' }}>
                  <Input
                    value={editNameVal}
                    onChange={(e) => setEditNameVal(e.target.value)}
                    maxLength={64}
                    size="small"
                  />
                  <Button type="primary" size="small" onClick={() => updateNameMutation.mutate(editNameVal)} loading={updateNameMutation.isPending}>
                    保存
                  </Button>
                  <Button size="small" onClick={() => { setEditingName(false); setEditNameVal(conv?.name || ''); }}>
                    取消
                  </Button>
                </div>
              ) : (
                <div className="group-info-name-display" style={{ justifyContent: 'center', textAlign: 'center' }}>
                  <div>
                    <div className="group-info-name-text">{isPrivate ? convBots[0]?.name || conv?.name || 'Bot' : conv?.name || '群聊'}</div>
                    <div className="group-info-count">{conv?.member_count || 0} 人</div>
                  </div>
                  {isAdmin && !isPrivate && (
                    <Button type="text" icon={<EditOutlined />} size="small" onClick={() => { setEditingName(true); setEditNameVal(conv?.name || ''); }} />
                  )}
                </div>
              )}
            </div>
          </div>
          <Divider />

          {!isPrivate && <>
          {/* Section 2: Announcement */}
          <div className="group-info-section">
            <div className="group-info-section-title">群公告</div>
            {editingAnnouncement ? (
              <div className="group-info-edit-col">
                <Input.TextArea
                  value={editAnnouncementVal}
                  onChange={(e) => setEditAnnouncementVal(e.target.value)}
                  rows={3}
                  maxLength={500}
                />
                <div className="group-info-edit-actions">
                  <Button type="primary" size="small" onClick={() => announcementMutation.mutate(editAnnouncementVal)} loading={announcementMutation.isPending}>
                    保存
                  </Button>
                  <Button size="small" onClick={() => { setEditingAnnouncement(false); setEditAnnouncementVal(conv?.announcement || ''); }}>
                    取消
                  </Button>
                  {conv?.announcement && (
                    <Button size="small" danger onClick={() => deleteAnnouncementMutation.mutate()} loading={deleteAnnouncementMutation.isPending}>
                      删除
                    </Button>
                  )}
                </div>
              </div>
            ) : (
              <div className="group-info-announcement-display">
                <div className="group-info-announcement-text">
                  {conv?.announcement || <span className="group-info-no-data">暂无公告</span>}
                </div>
                {isAdmin && (
                  <Button type="text" icon={<EditOutlined />} size="small" onClick={() => { setEditingAnnouncement(true); setEditAnnouncementVal(conv?.announcement || ''); }} />
                )}
              </div>
            )}
          </div>
          <Divider />

          {/* Section 3: Members */}
          <div className="group-info-section">
            <div className="group-info-section-title">
              <span>成员 ({members.length})</span>
              {isAdmin && (
                <Button type="primary" size="small" icon={<UserAddOutlined />} onClick={() => setAddMemberOpen(true)}>
                  添加
                </Button>
              )}
            </div>
            <div className="group-info-members">
              {members.map((member) => {
                const roleLabels: Record<string, string> = { '1': '群主', MEMBER_ROLE_OWNER: '群主', '2': '管理员', MEMBER_ROLE_ADMIN: '管理员', '3': '成员', MEMBER_ROLE_MEMBER: '成员' };
                const roleColors: Record<string, string> = { '1': 'gold', MEMBER_ROLE_OWNER: 'gold', '2': 'blue', MEMBER_ROLE_ADMIN: 'blue', '3': 'default', MEMBER_ROLE_MEMBER: 'default' };
                const canRemove = isOwner && member.role !== 'MEMBER_ROLE_OWNER' && member.role !== 1;
                return (
                  <div key={member.user_id} className="group-info-member">
                    <Avatar name={member.username || `用户${member.user_id}`} src={member.avatar || ''} size={32} />
                    <div className="group-info-member-info">
                      <span className="group-info-member-name">{member.username || `用户${member.user_id}`}</span>
                      <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                        <Tag color={roleColors[member.role]} style={{ fontSize: 11, lineHeight: '18px', padding: '0 6px', margin: 0 }}>
                          {roleLabels[member.role]}
                        </Tag>
                      </div>
                    </div>
                    {(isAdmin || isOwner) && member.role !== 'MEMBER_ROLE_OWNER' && member.role !== 1 && (
                      member.is_muted ? (
                        <Button
                          type="text"
                          size="small"
                          icon={<AudioMutedOutlined />}
                          onClick={() => member.user_id && unmuteMemberMutation.mutate(member.user_id)}
                          title="取消禁言"
                          loading={unmuteMemberMutation.isPending}
                          style={{ color: '#ff4d4f' }}
                        />
                      ) : (
                        <Button
                          type="text"
                          size="small"
                          icon={<SoundOutlined />}
                          onClick={() => member.user_id && muteMemberMutation.mutate({ userId: member.user_id })}
                          title="禁言"
                        />
                      )
                    )}
                    {canRemove && (
                      <Popconfirm
                        title="移除成员"
                        description={`确定要移除 ${member.username || `用户${member.user_id}`} 吗？`}
                        onConfirm={() => member.user_id && removeMemberMutation.mutate(member.user_id)}
                        okText="移除"
                        cancelText="取消"
                      >
                        <Button type="text" size="small" danger icon={<CloseOutlined />} />
                      </Popconfirm>
                    )}
                    {isOwner && (member.role === 'MEMBER_ROLE_MEMBER' || member.role === 3) && (
                      <Button type="text" size="small" onClick={() => member.user_id && updateMemberRoleMutation.mutate({ userId: member.user_id, role: 2 })} title="设为管理员">
                        升管
                      </Button>
                    )}
                    {isOwner && (member.role === 'MEMBER_ROLE_ADMIN' || member.role === 2) && (
                      <Button type="text" size="small" onClick={() => member.user_id && updateMemberRoleMutation.mutate({ userId: member.user_id, role: 3 })} title="取消管理员">
                        降级
                      </Button>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
          <Divider />
          </>}

          {/* Section 4: Bots */}
          <div className="group-info-section">
            <div className="group-info-section-title">
              <span>Bot 绑定 ({convBots.length})</span>
              {isAdmin && (
                <Button type="primary" size="small" icon={<RobotOutlined />} onClick={() => setAddBotOpen(true)}>
                  添加
                </Button>
              )}
            </div>
            <div className="group-info-members">
              {botsLoading && <div role="status">加载 Bot 绑定中...</div>}
              {botsError && <div role="alert">Bot 绑定读取失败 <Button size="small" onClick={() => void refetchBots()}>重试</Button></div>}
              {!botsLoading && !botsError && convBots.length === 0 && <div className="group-info-no-data">暂无 Bot 绑定；邀请后建立新的参与生命周期</div>}
              {convBots.map((bot: BotInConv) => {
                return (
                  <div key={bot.binding_id} className="group-info-member group-info-bot">
                    <Avatar name={bot.name || `Bot#${bot.bot_id}`} src={bot.avatar || ''} size={32} />
                    <div className="group-info-member-info">
                      <span className="group-info-member-name">{bot.name || `Bot#${bot.bot_id}`}</span>
                      <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                        <Tag color="purple" style={{ fontSize: 11, lineHeight: '18px', padding: '0 6px', margin: 0 }}>
                          BOT
                        </Tag>
                      </div>
                      <div className="group-info-binding-details">
                        <div>Bot ID: {bot.bot_id}</div>
                        <div>绑定 ID: {bot.binding_id}</div>
                        <div>所有者: {bot.owner_type === 'platform' ? '平台' : members.find((member) => member.user_id === bot.owner_id)?.username || bot.owner_id}</div>
                        <div>邀请者: {members.find((member) => member.user_id === bot.added_by)?.username || bot.added_by}</div>
                        <div>加入时间: {new Date(bot.added_at * 1000).toLocaleString('zh-CN')}</div>
                        <div>Bot 状态: {bot.status === 'active' ? '启用' : '已停用'}</div>
                        <div>绑定参与许可: {botParticipationEnabled(bot) ? '允许' : '已撤销'}</div>
                      </div>
                      {isAdmin && <div className="group-info-binding-permission">
                        <label htmlFor={`binding-enabled-${bot.binding_id}`}>允许参与此会话</label>
                        <Switch id={`binding-enabled-${bot.binding_id}`} aria-label={`允许 ${bot.name || 'Bot'} 参与此会话`}
                          checked={botParticipationEnabled(bot)} disabled={bot.status !== 'active'} loading={participationMutation.isPending}
                          onChange={(enabled) => AntModal.confirm({
                            title: enabled ? '恢复此绑定接收新工作？' : '撤销 Bot 参与许可？',
                            content: enabled ? '只恢复新工作的参与许可，旧执行、历史授权和交互不会自动复活。'
                              : '停止后续会话访问和输出，并请求停止当前工作；实际停工仍须执行器确认，不能据此保证撤销已发生的行动。',
                            okText: enabled ? '允许新工作' : '撤销许可', cancelText: '取消', okButtonProps: { danger: !enabled },
                            onOk: () => participationMutation.mutateAsync({ bot, enabled }),
                          })} />
                      </div>}
                      {isAdmin && <Button size="small" disabled={!conv?.max_seq || !isBotBindingActive(bot)} onClick={() => { setGrantBot(bot); setStartSeq(null); setEndSeq(null); }}>授权历史范围</Button>}
                      <BindingRules key={`${convId}:${bot.binding_id}`} bot={bot} convId={convId!} isAdmin={isAdmin}
                        unavailable={!open || botsLoading || botsError || participationMutation.isPending}
                        onSaved={async () => { await refetchBots(); props.onMembersChange(); message.success('订阅与规则已保存，仅影响后续活动'); }} />
                    </div>
                    {isAdmin && (
                      <Popconfirm
                        title="移除机器人"
                        description={`确定要移出 ${bot.name || '此 Bot'} 吗？当前绑定及其历史授权将失效，重新邀请会创建新绑定。`}
                        onConfirm={() => removeBotMutation.mutate(bot.bot_id)}
                        okText="移除"
                        cancelText="取消"
                      >
                        <Button type="text" size="small" danger aria-label={`移出 ${bot.name || 'Bot'}`} icon={<CloseOutlined />} loading={removeBotMutation.isPending} />
                      </Popconfirm>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
          <Divider />

          {!isPrivate && <>
          {/* Section 5: Settings */}
          <div className="group-info-section">
            <div className="group-info-section-title">设置</div>
            {isAdmin && (
              <div className="group-info-setting-row">
                <span>全员禁言</span>
                <Switch
                  checked={conv?.is_muted_all}
                  onChange={(checked) => {
                    if (checked) muteAllMutation.mutate();
                    else unmuteAllMutation.mutate();
                  }}
                  loading={muteAllMutation.isPending || unmuteAllMutation.isPending}
                />
              </div>
            )}
          </div>
          <Divider />

          {/* Section 6: Danger Zone */}
          <div className="group-info-section">
            {isOwner && (
              <div className="group-info-actions" style={{ marginBottom: 8 }}>
                <Button block icon={<CrownOutlined />} onClick={() => setTransferOpen(true)}>
                  转让群聊
                </Button>
              </div>
            )}
            <div className="group-info-actions">
              <Popconfirm
                title="退出群聊"
                description="确定要退出此群聊吗？"
                onConfirm={() => leaveGroupMutation.mutate()}
                okText="退出"
                cancelText="取消"
              >
                <Button block danger icon={<DeleteOutlined />} loading={leaveGroupMutation.isPending}>
                  退出群聊
                </Button>
              </Popconfirm>
            </div>
            {!isOwner && (
              <div className="group-info-actions" style={{ marginTop: 8 }}>
                <Popconfirm
                  title="删除会话"
                  description="确定要删除此会话吗？"
                  onConfirm={() => deleteConvMutation.mutate()}
                  okText="删除"
                  cancelText="取消"
                >
                  <Button block danger icon={<DeleteOutlined />} loading={deleteConvMutation.isPending}>
                    删除会话
                  </Button>
                </Popconfirm>
              </div>
            )}
          </div>
          </>}
        </div>
      </Drawer>

      {/* Add Member Modal */}
      <AntModal
        title="添加成员"
        open={addMemberOpen}
        onOk={() => {
          if (selectedFriends.length > 0) {
            addMembersMutation.mutate(selectedFriends);
          }
        }}
        onCancel={() => { setAddMemberOpen(false); setSelectedFriends([]); }}
        confirmLoading={addMembersMutation.isPending}
        okText="添加"
        cancelText="取消"
      >
        <Select
          mode="multiple"
          placeholder="选择好友"
          style={{ width: '100%' }}
          value={selectedFriends}
          onChange={setSelectedFriends}
          options={friends
            .filter((f: any) => !members.some((m) => m.user_id === f.user_id))
            .map((f: any) => ({
              value: f.user_id,
              label: f.remark || f.username,
            }))
          }
        />
      </AntModal>

      {/* Transfer Owner Modal */}
      <AntModal
        title="转让群聊"
        open={transferOpen}
        onOk={() => {
          if (transferTarget) {
            transferMutation.mutate(transferTarget);
          }
        }}
        onCancel={() => { setTransferOpen(false); setTransferTarget(null); }}
        confirmLoading={transferMutation.isPending}
        okText="转让"
        cancelText="取消"
      >
        <Select
          placeholder="选择新群主"
          style={{ width: '100%' }}
          value={transferTarget}
          onChange={setTransferTarget}
          options={members
            .filter((m) => m.user_id !== currentUserId)
            .map((m) => ({
              value: m.user_id,
              label: m.username || `用户${m.user_id}`,
            }))
          }
        />
      </AntModal>

      {/* Add Bot Modal */}
      <AntModal
        title="添加机器人"
        open={addBotOpen}
        onOk={() => {
          if (selectedBotId) {
            addBotMutation.mutate({ botId: selectedBotId });
          }
        }}
        onCancel={() => { setAddBotOpen(false); setSelectedBotId(null); }}
        confirmLoading={addBotMutation.isPending}
        okText="添加"
        cancelText="取消"
        okButtonProps={{ disabled: !selectedBotId || !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(selectedBotId) }}
      >
        {availableBotsError && <p role="alert">可邀请的 Bot 读取失败，请关闭后重试。</p>}
        <Select
          placeholder="选择机器人"
          style={{ width: '100%' }}
          loading={availableBotsLoading}
          value={selectedBotId}
          onChange={setSelectedBotId}
          options={userBots
            .filter((bot) => bot.status === 'active' && !convBots.some((binding) => binding.bot_id === bot.id))
            .map((bot) => ({
              value: bot.id,
              label: bot.name,
            }))
          }
        />
        <label htmlFor="invite-bot-id">或输入他人分享的 Bot ID</label>
        <Input id="invite-bot-id" aria-label="Bot ID" placeholder="Bot UUID" value={selectedBotId ?? ''} onChange={(event) => setSelectedBotId(event.target.value.trim() || null)} />
        <p className="group-info-no-data">Bot 不占用人类成员名额，也不会自动获得群历史。移出后重新邀请会建立新绑定。</p>
      </AntModal>

      <AntModal title={`授权 ${grantBot?.name || 'Bot'} 读取历史`} open={!!grantBot}
        onCancel={() => { setGrantBot(null); setStartSeq(null); setEndSeq(null); }}
        onOk={() => grantHistoryMutation.mutate()} confirmLoading={grantHistoryMutation.isPending}
        okText="授权此范围" cancelText="取消"
        okButtonProps={{ disabled: startSeq === null || endSeq === null || startSeq < 1 || endSeq < startSeq || endSeq > (conv?.max_seq || 0) }}>
        <p>仅授权当前绑定读取所选范围，不授予完整历史；移出后授权失效，重入不恢复。</p>
        <p className="group-info-binding-details">绑定 ID: {grantBot?.binding_id}</p>
        <label htmlFor="bot-history-start">起始消息序号</label>
        <InputNumber id="bot-history-start" min={1} max={conv?.max_seq} precision={0} value={startSeq} onChange={setStartSeq} style={{ width: '100%', margin: '8px 0 16px' }} />
        <label htmlFor="bot-history-end">结束消息序号（包含）</label>
        <InputNumber id="bot-history-end" min={startSeq || 1} max={conv?.max_seq} precision={0} value={endSeq} onChange={setEndSeq} style={{ width: '100%', marginTop: 8 }} />
        <p className="group-info-no-data">当前会话最大消息序号: {conv?.max_seq || 0}</p>
      </AntModal>
    </>
  );
}
