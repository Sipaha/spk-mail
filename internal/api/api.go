package api

import "context"

// API is the surface exposed to the frontend. Both transports (Wails, HTTP)
// implement it identically — any new method must be added here so both
// transports get it.
type API interface {
	ListAccounts(ctx context.Context) ([]AccountDTO, error)
	AddAccount(ctx context.Context, req AddAccountRequest) (AccountDTO, error)
	RemoveAccount(ctx context.Context, id int64) error

	ListThreads(ctx context.Context, filter ThreadFilter) ([]ThreadDTO, error)
	GetThread(ctx context.Context, id int64) ([]MessageDTO, error)

	MarkRead(ctx context.Context, messageIDs []int64) error
	AllowRemoteForMessage(ctx context.Context, id int64) (string, error) // returns updated body_html
	Search(ctx context.Context, query string, limit, offset int) ([]SearchHitDTO, error)

	UnreadCounts(ctx context.Context) (UnreadCountsDTO, error)
}

type ThreadFilter struct {
	AccountID  *int64 `json:"account_id,omitempty"`
	FolderID   *int64 `json:"folder_id,omitempty"`
	UnreadOnly bool   `json:"unread_only,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}
