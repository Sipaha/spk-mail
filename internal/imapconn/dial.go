package imapconn

import (
	"context"
	"fmt"

	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// AccountReader is the minimal Store surface DialAccount needs.
type AccountReader interface {
	GetAccount(ctx context.Context, id int64) (storage.AccountRow, error)
}

// DialAccount opens a fresh IMAP session for the given account.
// Caller is responsible for c.Close(). Used by the sync worker, attachment
// downloader, and on-demand raw fetcher — each wants a per-request connection
// that does not disturb an AccountWorker's IDLE session.
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
