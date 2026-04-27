//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-mail/internal/api"
)

// API wraps an api.API as a Wails service. The struct's package-qualified
// name is the prefix of the binding name on the JS side: methods are
// registered by Wails as
//
//	github.com/spk/spk-mail/internal/api/transport.API.<MethodName>
//
// (Wails uses reflect.Type.PkgPath() + "." + reflect.Type.Name() + "." +
// methodName as the FQN — see pkg/application/bindings.go in Wails v3.) The
// frontend mirrors that FQN in client.ts via Call.ByName.
type API struct {
	a  api.API
	em *api.Emitter
}

// NewAPI returns a Wails-bindable wrapper around the provided api.API.
func NewAPI(a api.API, em *api.Emitter) *API { return &API{a: a, em: em} }

func (w *API) ListAccounts() ([]api.AccountDTO, error) {
	return w.a.ListAccounts(context.Background())
}
func (w *API) AddAccount(req api.AddAccountRequest) (api.AccountDTO, error) {
	return w.a.AddAccount(context.Background(), req)
}
func (w *API) RemoveAccount(id int64) error { return w.a.RemoveAccount(context.Background(), id) }
func (w *API) ListThreads(f api.ThreadFilter) ([]api.ThreadDTO, error) {
	return w.a.ListThreads(context.Background(), f)
}
func (w *API) GetThread(id int64) ([]api.MessageDTO, error) {
	return w.a.GetThread(context.Background(), id)
}
func (w *API) MarkRead(ids []int64) error { return w.a.MarkRead(context.Background(), ids) }
func (w *API) AllowRemoteForMessage(id int64) (string, error) {
	return w.a.AllowRemoteForMessage(context.Background(), id)
}
func (w *API) Search(q string, limit, offset int) ([]api.SearchHitDTO, error) {
	return w.a.Search(context.Background(), q, limit, offset)
}
func (w *API) UnreadCounts() (api.UnreadCountsDTO, error) {
	return w.a.UnreadCounts(context.Background())
}
func (w *API) GetAttachmentLocalPath(id int64) (string, error) {
	return w.a.GetAttachmentLocalPath(context.Background(), id)
}
func (w *API) OpenAttachment(id int64) error {
	return w.a.OpenAttachment(context.Background(), id)
}

func (w *API) ListProfiles() ([]api.ProfileDTO, error) {
	return w.a.ListProfiles(context.Background())
}
func (w *API) AddProfile(req api.AddProfileRequest) (api.ProfileDTO, error) {
	return w.a.AddProfile(context.Background(), req)
}
func (w *API) UpdateProfile(req api.UpdateProfileRequest) (api.ProfileDTO, error) {
	return w.a.UpdateProfile(context.Background(), req)
}
func (w *API) DeleteProfile(id int64) error { return w.a.DeleteProfile(context.Background(), id) }

func (w *API) SetProfileMuted(id int64, muted bool) error {
	return w.a.SetProfileMuted(context.Background(), id, muted)
}

// Events returns the api.Emitter for the desktop runner to bridge to Wails event bus.
func (w *API) Events() *api.Emitter { return w.em }
