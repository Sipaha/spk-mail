// Package api defines the transport-agnostic surface that the React frontend
// uses. Two transports bind it: Wails service (production desktop) and HTTP
// (browser mode for development and UI automation).
package api

type AccountDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Color     string `json:"color"`
	Status    string `json:"status"` // ok|error|connecting (plan 2 fills it; plan 1 returns "ok")
	ProfileID *int64 `json:"profile_id,omitempty"`
}

type ProfileDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
	Muted     bool   `json:"muted"`
}

type AddProfileRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type UpdateProfileRequest struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type ThreadDTO struct {
	ID          int64  `json:"id"`
	Subject     string `json:"subject"`
	LastDate    int64  `json:"last_date"`
	MsgCount    int64  `json:"msg_count"`
	UnreadCount int64  `json:"unread_count"`
	HasFlagged  bool   `json:"has_flagged"`
	HasAttach   bool   `json:"has_attach"`
	// LastFrom is the raw "Name <addr>" of the most recent message in the
	// thread; the frontend parses out a display name. Empty if the thread
	// has no messages.
	LastFrom string `json:"last_from"`
	// Snippet is the first ~200 chars of the most recent message's
	// body_text, with whitespace runs collapsed. The frontend truncates
	// further to its visible width.
	Snippet string `json:"snippet"`
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

type FolderDTO struct {
	ID           int64  `json:"id"`
	AccountID    int64  `json:"account_id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	UnreadCount  int64  `json:"unread_count"`
	TotalCount   int64  `json:"total_count"`
	FlaggedCount int64  `json:"flagged_count"`
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
	ProfileID    *int64 `json:"profile_id,omitempty"`
}

// FlagToggleResult is the wire shape returned by ToggleThreadFlagged.
// Action mirrors storage.FlagToggleOutcome.Action ("added"/"removed"/"noop")
// so the frontend can decide whether to refresh optimistically vs no-op.
type FlagToggleResult struct {
	Action string `json:"action"`
	Count  int64  `json:"count"`
}

// RawMessageDTO carries the bytes + metadata returned by GetRawMessage.
// Bytes are base64-encoded so a single JSON path covers both Wails and
// HTTP transports without a separate streaming endpoint.
type RawMessageDTO struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	RawB64    string `json:"raw_b64"`
}
