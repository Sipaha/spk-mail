package imap

import (
	"context"
	"testing"

	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/stretchr/testify/require"
)

func TestClient_LoginAndList(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()

	host, port := splitHostPort(mock.Addr())

	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret", UseTLS: false})
	require.NoError(t, err)
	defer c.Close()

	folders, err := c.ListFolders(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, folders)
	require.Contains(t, folderNames(folders), "INBOX")
}

func folderNames(fs []FolderInfo) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}
