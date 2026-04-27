// Package api defines the transport-agnostic surface that the React frontend
// uses. Two transports bind it: Wails service (production desktop) and HTTP
// (browser mode for development and UI automation).
package api

type AccountDTO struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Color  string `json:"color"`
	Status string `json:"status"` // ok|error|connecting (plan 2 fills it; plan 1 returns "ok")
}

type ThreadDTO struct {
	ID          int64  `json:"id"`
	Subject     string `json:"subject"`
	LastDate    int64  `json:"last_date"`
	MsgCount    int64  `json:"msg_count"`
	UnreadCount int64  `json:"unread_count"`
	HasFlagged  bool   `json:"has_flagged"`
	HasAttach   bool   `json:"has_attach"`
}

type MessageDTO struct {
	ID          int64           `json:"id"`
	AccountID   int64           `json:"account_id"`
	FolderID    int64           `json:"folder_id"`
	Subject     string          `json:"subject"`
	FromAddr    string          `json:"from_addr"`
	ToAddrs     []string        `json:"to_addrs"`
	Date        int64           `json:"date"`
	Flags       []string        `json:"flags"`
	BodyHTML    string          `json:"body_html"`
	BodyText    string          `json:"body_text"`
	Attachments []AttachmentDTO `json:"attachments"`
}

type SearchHitDTO struct {
	MessageID int64  `json:"message_id"`
	ThreadID  *int64 `json:"thread_id,omitempty"`
	Subject   string `json:"subject"`
	FromAddr  string `json:"from_addr"`
	Date      int64  `json:"date"`
	// Snippet contains \x01 BEGIN and \x02 END sentinels around matched terms.
	// Frontend splits on these to render highlights as <mark> React elements.
	Snippet string `json:"snippet"`
}

type AttachmentDTO struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	Downloaded  bool   `json:"downloaded"`
}

type UnreadCountsDTO struct {
	Total      int64           `json:"total"`
	PerAccount map[int64]int64 `json:"per_account"`
}

type AddAccountRequest struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	IMAPHost     string `json:"imap_host"`
	IMAPPort     int    `json:"imap_port"`
	IMAPUsername string `json:"imap_username"`
	IMAPPassword string `json:"imap_password"`
	UseTLS       bool   `json:"use_tls"`
	Color        string `json:"color"`
	UseMock      bool   `json:"use_mock,omitempty"`
}
