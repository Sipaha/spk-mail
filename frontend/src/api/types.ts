export interface AccountDTO { id: number; name: string; email: string; color: string; status: string; profile_id?: number }
export interface ThreadDTO {
  id: number; subject: string; last_date: number; msg_count: number; unread_count: number;
  has_flagged: boolean; has_attach: boolean;
  last_from: string;  // raw "Name <addr>" of the most recent message; UI parses display name
  snippet: string;    // ~200-char preview of the most recent message's body_text
}
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
  profile_id?: number;
}
export interface ThreadFilter {
  account_id?: number; folder_id?: number; unread_only?: boolean; has_flagged?: boolean; limit?: number; offset?: number;
  profile_id?: number;
}
export interface FolderDTO {
  id: number
  account_id: number
  name: string
  role: string  // 'inbox' | 'sent' | 'drafts' | 'archive' | 'spam' | 'trash' | ''
  unread_count: number
}
export type EventType = 'MessageInserted'|'MessageArrived'|'MessageUpdated'|'SyncProgress'|'AccountStatus'|'WriteError'|'AttachmentReady'
export interface ApiEvent { type: EventType; payload: Record<string, unknown> }
export interface SearchHitDTO {
  message_id: number; thread_id?: number;
  subject: string; from_addr: string; date: number;
  snippet: string; // contains \x01 BEGIN and \x02 END sentinels around matches
}
export interface ProfileDTO { id: number; name: string; color: string; sort_order: number; muted: boolean }
export interface AddProfileRequest { name: string; color: string }
export interface UpdateProfileRequest { id: number; name: string; color: string }
