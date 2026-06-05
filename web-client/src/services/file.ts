import client, { unwrap } from './client';
import type { GetUploadURLReq } from '@/types/api';
import type { APIResponse, UploadURLData, FileInfo } from '@/types/model';

export const fileApi = {
  getUploadUrl: (data: GetUploadURLReq) =>
    client.post<APIResponse<UploadURLData>>('/files/upload_url', data).then(unwrap),

  confirmUpload: (fileId: string) =>
    client.post<APIResponse<FileInfo>>('/files/confirm', { file_id: fileId }).then(unwrap),

  getDownloadUrl: (fileId: string) =>
    client.get<APIResponse<{ download_url: string; expires_at: number }>>(`/files/${fileId}/download`).then(unwrap),

  getFileInfo: (fileId: string) =>
    client.get<APIResponse<FileInfo>>(`/files/${fileId}/info`).then(unwrap),

  deleteFile: (fileId: string) =>
    client.delete<APIResponse<null>>(`/files/${fileId}`).then(unwrap),
};
