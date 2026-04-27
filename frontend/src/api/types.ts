export interface AccountDTO { id: number; name: string; email: string; color: string; status: string }
export interface ThreadDTO { id: number; subject: string; last_date: number; msg_count: number; unread_count: number; has_flagged: boolean; has_attach: boolean }
export interface AttachmentDTO { id: number; filename: string; content_type: string; size_bytes: number; downloaded: boolean }
export interface MessageDTO {
  id: number; account_id: number; folder_id: number;
  subject: string; from_addr: string; to_addrs: string[];
  date: number; flags: string[]; body_html: string; body_text: string;
  attachments: AttachmentDTO[];
}
export interface AddAccountRequest {
  name: string; email: string; imap_host: string; imap_port: number;
  imap_username: string; imap_password: string; use_tls: boolean; color: string;
}
export interface ThreadFilter {
  account_id?: number; folder_id?: number; unread_only?: boolean; limit?: number; offset?: number;
}
export type EventType = 'MessageInserted'|'MessageArrived'|'MessageUpdated'|'SyncProgress'|'AccountStatus'|'WriteError'
export interface ApiEvent { type: EventType; payload: Record<string, unknown> }
