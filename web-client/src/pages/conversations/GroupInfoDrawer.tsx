import { useState, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { message, Drawer, Button, Tag, Divider, Input, Switch, Popconfirm, Select, Radio, Modal as AntModal } from 'antd';
import {
  EditOutlined, UserAddOutlined, DeleteOutlined,
  CrownOutlined, CloseOutlined, CameraOutlined,
  RobotOutlined,
} from '@ant-design/icons';
import { convApi } from '@/services/conversation';
import { fileApi } from '@/services/file';
import { friendApi } from '@/services/friend';
import { botApi } from '@/services/bot';
import { Avatar } from '@/components/common/Avatar';
import type { ConvMember, BotInConv } from '@/types/model';
import './ChatPage.css';

/** Protobuf enum serializes member_type as number (1=user, 2=bot), but TS type says string */
function isBotMember(m: ConvMember): boolean {
  return (m as any).member_type === 2 || m.member_type === 'bot';
}

interface GroupInfoDrawerProps {
  conv: any;
  members: ConvMember[];
  isOwner: boolean;
  isAdmin: boolean;
  currentUserId: string;
  numericUserId: number;
  open: boolean;
  onClose: () => void;
  onMembersChange: () => void;
}

export function GroupInfoDrawer(props: GroupInfoDrawerProps) {
  const { conv, members, isOwner, isAdmin, currentUserId, numericUserId, open, onClose } = props;
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const convId = conv?.id;

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
  const [triggerMode, setTriggerMode] = useState<string>('always');
  const [keywordValue, setKeywordValue] = useState('');
  const avatarRef = useRef<HTMLInputElement>(null);

  // ─── Queries ───

  const { data: friendsData } = useQuery({
    queryKey: ['friends'],
    queryFn: () => friendApi.list(),
    enabled: open,
  });
  const friends = friendsData?.list ?? [];

  const { data: convBots = [] } = useQuery({
    queryKey: ['conv-bots', convId],
    queryFn: () => botApi.listConvBots(convId!),
    enabled: open && !!convId,
  });

  const { data: userBotsData } = useQuery({
    queryKey: ['user-bots'],
    queryFn: () => botApi.list(),
    enabled: addBotOpen,
  });
  const userBots = userBotsData?.list ?? [];

  // ─── Mutations ───

  const updateAvatarMutation = useMutation({
    mutationFn: async (file: File) => {
      const uploadData = await fileApi.getUploadUrl({
        name: file.name,
        mime_type: file.type || 'application/octet-stream',
        size: file.size,
      });
      await fetch(uploadData.upload_url, {
        method: 'PUT',
        body: file,
        headers: { 'Content-Type': file.type || 'application/octet-stream' },
      });
      const fileInfo = await fileApi.confirmUpload(uploadData.file_id);
      const downloadData = await fileApi.getDownloadUrl(uploadData.file_id);
      await convApi.update(convId!, { avatar: downloadData.download_url });
    },
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
    mutationFn: (memberIds: string[]) => convApi.addMembers(convId!, memberIds as any),
    onSuccess: () => {
      message.success('已添加成员');
      setAddMemberOpen(false);
      setSelectedFriends([]);
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const removeMemberMutation = useMutation({
    mutationFn: (userId: string) => convApi.removeMembers(convId!, [userId] as any),
    onSuccess: () => {
      message.success('已移除成员');
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
    mutationFn: (newOwnerId: string) => convApi.transferOwner(convId!, newOwnerId as any),
    onSuccess: () => {
      message.success('群主已转让');
      setTransferOpen(false);
      setTransferTarget(null);
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const leaveGroupMutation = useMutation({
    mutationFn: () => convApi.removeMembers(convId!, [numericUserId]),
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
    mutationFn: ({ userId, role }: { userId: number; role: string }) =>
      convApi.updateMember(convId!, userId, { role }),
    onSuccess: () => {
      message.success('成员角色已更新');
      props.onMembersChange();
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const addBotMutation = useMutation({
    mutationFn: ({ botId, triggers }: { botId: string; triggers: string[] }) =>
      botApi.addToConv(convId!, botId, triggers),
    onSuccess: () => {
      message.success('已添加机器人');
      setAddBotOpen(false);
      setSelectedBotId(null);
      setTriggerMode('always');
      setKeywordValue('');
      queryClient.invalidateQueries({ queryKey: ['conv-bots', convId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const removeBotMutation = useMutation({
    mutationFn: (botId: string) => botApi.removeFromConv(convId!, botId),
    onSuccess: (_data, botId) => {
      message.success('已移除机器人');
      queryClient.setQueryData(['conv-bots', convId], (old: any[]) => {
        if (!old) return old;
        return old.filter((b: any) => String(b.id) !== botId && String(b.bot_id) !== botId);
      });
      queryClient.invalidateQueries({ queryKey: ['conv-bots', convId] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  return (
    <>
      <Drawer
        title="群聊信息"
        placement="right"
        width={380}
        open={open}
        onClose={() => { onClose(); setEditingName(false); setEditingAnnouncement(false); }}
        styles={{ body: { padding: '16px 20px' } }}
      >
        <div className="group-info-content">
          {/* Section 1: Avatar + Name */}
          <div className="group-info-avatar-section">
            <div className="group-info-avatar-wrapper">
              <Avatar name={conv?.name || '群聊'} src={conv?.avatar} size={80} />
              {isAdmin && (
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
                    <div className="group-info-name-text">{conv?.name || '群聊'}</div>
                    <div className="group-info-count">{conv?.member_count || 0} 人</div>
                  </div>
                  {isAdmin && (
                    <Button type="text" icon={<EditOutlined />} size="small" onClick={() => { setEditingName(true); setEditNameVal(conv?.name || ''); }} />
                  )}
                </div>
              )}
            </div>
          </div>
          <Divider />

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
                const roleLabels: Record<string, string> = { owner: '群主', admin: '管理员', member: '成员' };
                const roleColors: Record<string, string> = { owner: 'gold', admin: 'blue', member: 'default' };
                const canRemove = isOwner && member.role !== 'owner';
                const isBot = isBotMember(member);
                return (
                  <div key={member.user_id} className="group-info-member">
                    <Avatar name={member.username || `用户${member.user_id}`} src={member.avatar || ''} size={32} />
                    <div className="group-info-member-info">
                      <span className="group-info-member-name">{member.username || `用户${member.user_id}`}</span>
                      <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                        <Tag color={roleColors[member.role]} style={{ fontSize: 11, lineHeight: '18px', padding: '0 6px', margin: 0 }}>
                          {roleLabels[member.role]}
                        </Tag>
                        {isBot && (
                          <Tag color="purple" style={{ fontSize: 11, lineHeight: '18px', padding: '0 6px', margin: 0 }}>
                            机器人
                          </Tag>
                        )}
                      </div>
                    </div>
                    {!isBot && canRemove && (
                      <Popconfirm
                        title="移除成员"
                        description={`确定要移除 ${member.username || `用户${member.user_id}`} 吗？`}
                        onConfirm={() => removeMemberMutation.mutate(String(member.user_id))}
                        okText="移除"
                        cancelText="取消"
                      >
                        <Button type="text" size="small" danger icon={<CloseOutlined />} />
                      </Popconfirm>
                    )}
                    {!isBot && isOwner && member.role === 'member' && (
                      <Button type="text" size="small" onClick={() => updateMemberRoleMutation.mutate({ userId: member.user_id, role: 'admin' })} title="设为管理员">
                        升管
                      </Button>
                    )}
                    {!isBot && isOwner && member.role === 'admin' && (
                      <Button type="text" size="small" onClick={() => updateMemberRoleMutation.mutate({ userId: member.user_id, role: 'member' })} title="取消管理员">
                        降级
                      </Button>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
          <Divider />

          {/* Section 4: Bots */}
          <div className="group-info-section">
            <div className="group-info-section-title">
              <span>群机器人 ({convBots.length})</span>
              {isAdmin && (
                <Button type="primary" size="small" icon={<RobotOutlined />} onClick={() => setAddBotOpen(true)}>
                  添加
                </Button>
              )}
            </div>
            <div className="group-info-members">
              {convBots.length === 0 && <div className="group-info-no-data">暂无机器人</div>}
              {convBots.map((bot: BotInConv) => {
                const roleLabels: Record<string, string> = { owner: '群主', admin: '管理员', member: '成员' };
                const roleColors: Record<string, string> = { owner: 'gold', admin: 'blue', member: 'default' };
                const botMember = members.find((m) => String(m.bot_id) === bot.bot_id);
                const botRole = botMember?.role || 'member';
                return (
                  <div key={bot.bot_id} className="group-info-member">
                    <Avatar name={bot.name || `Bot#${bot.bot_id}`} src={bot.avatar || ''} size={32} />
                    <div className="group-info-member-info">
                      <span className="group-info-member-name">{bot.name || `Bot#${bot.bot_id}`}</span>
                      <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                        <Tag color={roleColors[botRole]} style={{ fontSize: 11, lineHeight: '18px', padding: '0 6px', margin: 0 }}>
                          {roleLabels[botRole]}
                        </Tag>
                        <Tag color="purple" style={{ fontSize: 11, lineHeight: '18px', padding: '0 6px', margin: 0 }}>
                          机器人
                        </Tag>
                      </div>
                    </div>
                    {isAdmin && (
                      <Popconfirm
                        title="移除机器人"
                        description={`确定要移除 ${bot.name || '此机器人'} 吗？`}
                        onConfirm={() => removeBotMutation.mutate(bot.bot_id)}
                        okText="移除"
                        cancelText="取消"
                      >
                        <Button type="text" size="small" danger icon={<CloseOutlined />} loading={removeBotMutation.isPending} />
                      </Popconfirm>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
          <Divider />

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
            .filter((f: any) => !members.some((m) => String(m.user_id) === String(f.user_id)))
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
            .filter((m) => String(m.user_id) !== currentUserId && !isBotMember(m))
            .map((m) => ({
              value: String(m.user_id),
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
            const triggers: string[] = triggerMode === 'keyword'
              ? [`keyword:${keywordValue}`]
              : [triggerMode];
            addBotMutation.mutate({ botId: selectedBotId, triggers });
          }
        }}
        onCancel={() => { setAddBotOpen(false); setSelectedBotId(null); setTriggerMode('always'); setKeywordValue(''); }}
        confirmLoading={addBotMutation.isPending}
        okText="添加"
        cancelText="取消"
      >
        <Select
          placeholder="选择机器人"
          style={{ width: '100%' }}
          value={selectedBotId}
          onChange={setSelectedBotId}
          options={userBots
            .filter((bot: any) => !convBots.some((b: BotInConv) => String(b.bot_id) === bot.id))
            .map((bot: any) => ({
              value: bot.id,
              label: bot.name,
            }))
          }
        />
        <Divider />
        <div style={{ marginBottom: 8, color: '#666' }}>响应方式</div>
        <Radio.Group value={triggerMode} onChange={e => setTriggerMode(e.target.value)}>
          <Radio value="always">总是回复</Radio>
          <Radio value="mention">@时回复</Radio>
          <Radio value="keyword">关键词</Radio>
        </Radio.Group>
        {triggerMode === 'keyword' && (
          <Input
            placeholder="输入关键词"
            value={keywordValue}
            onChange={e => setKeywordValue(e.target.value)}
            style={{ width: '100%', marginTop: 8 }}
          />
        )}
      </AntModal>
    </>
  );
}
