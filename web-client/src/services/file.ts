import client, { unwrap } from './client';
import type { GetUploadURLReq } from '@/types/api';
import type { APIResponse, UploadURLData, FileInfo, FileDownloadData } from '@/types/model';
import { entityId, quantity } from '@/utils/json';

function normalizeFile(file: FileInfo, expectedId: string): FileInfo {
  if (!file || entityId(file.file_id, 'file.file_id') !== expectedId) throw new Error('文件身份不匹配');
  return {
    ...file,
    uploader_id: entityId(file.uploader_id, 'file.uploader_id'),
    size: quantity(file.size, 'file.size'),
    width: quantity(file.width, 'file.width'),
    height: quantity(file.height, 'file.height'),
    duration: quantity(file.duration, 'file.duration'),
    created_at: quantity(file.created_at, 'file.created_at'),
  };
}

export const fileApi = {
  getUploadUrl: async (data: GetUploadURLReq): Promise<UploadURLData> => {
    const result = await client.post<APIResponse<UploadURLData>>('/files/upload_url', data).then(unwrap);
    if (!result.upload_url) throw new Error('文件上传地址不可用');
    return { ...result, file_id: entityId(result.file_id, 'upload.file_id'), expires_at: quantity(result.expires_at, 'upload.expires_at') };
  },

  confirmUpload: async (fileId: string): Promise<FileInfo> => {
    entityId(fileId, 'file_id');
    const result = await client.post<APIResponse<{ file: FileInfo }>>('/files/confirm', { file_id: fileId }).then(unwrap);
    return normalizeFile(result.file, fileId);
  },

  getDownloadUrl: async (fileId: string): Promise<FileDownloadData> => {
    entityId(fileId, 'file_id');
    const result = await client.get<APIResponse<FileDownloadData>>(`/files/${fileId}/download`).then(unwrap);
    if (!result.download_url) throw new Error('文件下载地址不可用');
    return { ...result, expires_at: quantity(result.expires_at, 'download.expires_at'), file: normalizeFile(result.file, fileId) };
  },

  getFileInfo: async (fileId: string): Promise<FileInfo> => {
    entityId(fileId, 'file_id');
    const result = await client.get<APIResponse<{ file: FileInfo }>>(`/files/${fileId}/info`).then(unwrap);
    return normalizeFile(result.file, fileId);
  },

  deleteFile: (fileId: string) => {
    entityId(fileId, 'file_id');
    return client.delete<APIResponse<null>>(`/files/${fileId}`).then(unwrap);
  },
};
