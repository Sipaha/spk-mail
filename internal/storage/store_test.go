package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpen_CreatesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer s.Close()

	var v int
	err = s.DB().QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&v)
	require.NoError(t, err)
	require.Equal(t, 7, v)

	// Tables exist
	for _, tbl := range []string{"accounts", "folders", "messages", "threads", "attachments", "messages_fts"} {
		var name string
		err := s.DB().QueryRow("SELECT name FROM sqlite_master WHERE name = ?", tbl).Scan(&name)
		require.NoError(t, err, "table %q should exist", tbl)
	}
}

func TestOpen_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s1, err := Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	s2, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer s2.Close()
	var v int
	require.NoError(t, s2.DB().QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&v))
	require.Equal(t, 7, v)
}

func TestOpen_ReadDB_RejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer s.Close()

	_, err = s.readDB.ExecContext(context.Background(),
		`INSERT INTO accounts (name, email, imap_host, imap_port, imap_username, use_tls, color, created_at)
		 VALUES ('x','x','x',0,'x',0,'x',0)`)
	require.Error(t, err, "readDB must reject writes")
}

func TestReadsDoNotBlockOnLongWriteTx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(context.Background(), path)
	require.NoError(t, err)
	defer s.Close()

	ctx := context.Background()
	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)
	msgID, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1, Date: 0, Flags: "[]",
	})
	require.NoError(t, err)

	txStarted := make(chan struct{})
	txDone := make(chan struct{})
	go func() {
		defer close(txDone)
		_ = s.WithTx(ctx, func(tx *sql.Tx) error {
			close(txStarted)
			time.Sleep(500 * time.Millisecond)
			return nil
		})
	}()
	<-txStarted

	readDone := make(chan time.Duration, 1)
	go func() {
		t0 := time.Now()
		_, err := s.GetMessage(ctx, msgID)
		require.NoError(t, err)
		readDone <- time.Since(t0)
	}()

	select {
	case d := <-readDone:
		require.Less(t, d, 200*time.Millisecond,
			"read should not wait on the write tx; observed %v", d)
	case <-time.After(450 * time.Millisecond):
		t.Fatal("read blocked behind write tx (timed out)")
	}
	<-txDone
}
