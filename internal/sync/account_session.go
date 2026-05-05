package sync

import (
	"context"
	"fmt"

	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// AccountReader is the minimal Store surface DialAccount needs.
// Defined here (not in storage) so per-call helpers can pass a focused
// interface without dragging in storage.Writer.
type AccountReader interface {
	GetAccount(ctx context.Context, id int64) (storage.AccountRow, error)
}

// DialAccount opens a fresh IMAP session for the given account.
// Caller is responsible for c.Close(). Used by the attachment
// downloader and the on-demand raw fetcher — both want a per-request
// connection that doesn't disturb the AccountWorker's IDLE session.
func DialAccount(ctx context.Context, store AccountReader, sec *secrets.Store, accountID int64) (*imap.Client, error) {
	acc, err := store.GetAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("dial account %d: %w", accountID, err)
	}
	pw, err := sec.Get(fmt.Sprintf("account:%d", accountID))
	if err != nil {
		return nil, fmt.Errorf("dial account %d: secrets: %w", accountID, err)
	}
	c, err := imap.Dial(ctx, imap.DialOpts{
		Host: acc.IMAPHost, Port: acc.IMAPPort,
		Username: acc.IMAPUsername, Password: string(pw),
		UseTLS: acc.UseTLS,
	})
	if err != nil {
		return nil, fmt.Errorf("dial account %d: %w", accountID, err)
	}
	return c, nil
}
