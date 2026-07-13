package sync

import (
	"context"

	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/imapconn"
	"github.com/spk/spk-mail/internal/secrets"
)

// AccountReader is the minimal Store surface DialAccount needs.
type AccountReader = imapconn.AccountReader

// DialAccount opens a fresh IMAP session for the given account.
func DialAccount(ctx context.Context, store AccountReader, sec *secrets.Store, accountID int64) (*imap.Client, error) {
	return imapconn.DialAccount(ctx, store, sec, accountID)
}
