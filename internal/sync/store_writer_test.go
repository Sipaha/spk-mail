package sync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/storage"
)

func TestStoreWriter_InsertsAndCreatesThread(t *testing.T) {
	st, _ := storage.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	defer st.Close()
	accID, _ := st.InsertAccount(context.Background(), storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	role := "inbox"
	fID, _ := st.UpsertFolder(context.Background(), storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	em := api.NewEmitter()
	w := NewStoreWriter(st, em)
	go w.Run(context.Background())

	raw := strings.Join([]string{
		"From: B <b@x>", "Subject: Hello", "Date: Mon, 27 Apr 2026 10:30:00 +0000",
		"Message-ID: <one@x>", "Content-Type: text/plain", "", "hi",
	}, "\r\n")
	w.Submit(IncomingMessage{AccountID: accID, FolderID: fID, FolderRole: "inbox", UID: 1, Flags: []string{}, InternalAt: time.Now(), Raw: []byte(raw)})

	// Wait briefly for processing
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		threads, _ := st.ListThreadsRecent(context.Background(), 10, 0)
		if len(threads) == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no thread created within timeout")
}
