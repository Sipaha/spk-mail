//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-mail/internal/api"
)

// Wails wraps an api.API as a Wails service. The service name in JS is "api".
type Wails struct {
	a  api.API
	em *api.Emitter
}

func NewWails(a api.API, em *api.Emitter) *Wails { return &Wails{a: a, em: em} }

func (w *Wails) ListAccounts() ([]api.AccountDTO, error) {
	return w.a.ListAccounts(context.Background())
}
func (w *Wails) AddAccount(req api.AddAccountRequest) (api.AccountDTO, error) {
	return w.a.AddAccount(context.Background(), req)
}
func (w *Wails) RemoveAccount(id int64) error { return w.a.RemoveAccount(context.Background(), id) }
func (w *Wails) ListThreads(f api.ThreadFilter) ([]api.ThreadDTO, error) {
	return w.a.ListThreads(context.Background(), f)
}
func (w *Wails) GetThread(id int64) ([]api.MessageDTO, error) {
	return w.a.GetThread(context.Background(), id)
}
func (w *Wails) MarkRead(ids []int64) error { return w.a.MarkRead(context.Background(), ids) }
func (w *Wails) AllowRemoteForMessage(id int64) (string, error) {
	return w.a.AllowRemoteForMessage(context.Background(), id)
}
func (w *Wails) Search(q string, limit, offset int) ([]api.SearchHitDTO, error) {
	return w.a.Search(context.Background(), q, limit, offset)
}
func (w *Wails) UnreadCounts() (api.UnreadCountsDTO, error) {
	return w.a.UnreadCounts(context.Background())
}
func (w *Wails) GetAttachmentLocalPath(id int64) (string, error) {
	return w.a.GetAttachmentLocalPath(context.Background(), id)
}
func (w *Wails) OpenAttachment(id int64) error {
	return w.a.OpenAttachment(context.Background(), id)
}

// Events returns the api.Emitter for the desktop runner to bridge to Wails event bus.
func (w *Wails) Events() *api.Emitter { return w.em }
