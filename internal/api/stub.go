package api

import (
	"context"
	"errors"

	"github.com/spk/spk-mail/internal/flagop"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// ErrAttachmentNotReady is returned by GetAttachmentLocalPath when the
// attachment has not yet been downloaded (or its local file disappeared).
// Callers should trigger a re-download and retry.
var ErrAttachmentNotReady = errors.New("attachment not yet downloaded")

// ErrProfileInUse is the api-side sentinel returned by DeleteProfile when the
// underlying storage layer reports that accounts are still attached to the
// profile. Wraps storage.ErrProfileInUse so callers can errors.Is-detect either.
var ErrProfileInUse = errors.New("api: profile has attached accounts")

// FlagOpSubmitter is implemented by the per-account worker. The engine
// returns one for a given account ID. The Op type lives in
// internal/flagop so api and sync can share it without an import cycle
// (sync depends on api.Emitter).
type FlagOpSubmitter interface {
	SubmitFlagOp(op flagop.Op)
}

// Engine is the minimal surface the API stub needs from the sync engine.
// internal/sync.Engine satisfies this via a tiny adapter wired in main.go.
type Engine interface {
	StartAccount(ctx context.Context, id int64)
	StopAccount(id int64)
	WorkerFor(id int64) FlagOpSubmitter
}

// Stub is the API impl: talks directly to storage/secrets and dispatches to
// the sync engine when present (production wires one; unit tests pass nil).
//
// Methods are split across multiple files in this package by DTO boundary so
// each new feature lands in a focused file rather than this 400-line bag:
//
//	account_stub.go     ListAccounts / AddAccount / RemoveAccount /
//	                    AccountIsMuted / TotalUnreadExcludingMuted
//	thread_stub.go      ListFolders / ListThreads / GetThread / MarkRead /
//	                    AllowRemoteForMessage
//	attachment_stub.go  GetAttachmentLocalPath / OpenAttachment
//	search_stub.go      Search
//	profile_stub.go     ListProfiles / AddProfile / UpdateProfile /
//	                    DeleteProfile / SetProfileMuted
type Stub struct {
	Store   *storage.Store
	Secrets *secrets.Store
	Emitter *Emitter
	Engine  Engine
}

func NewStub(s *storage.Store, sec *secrets.Store, em *Emitter, eng Engine) *Stub {
	return &Stub{Store: s, Secrets: sec, Emitter: em, Engine: eng}
}
