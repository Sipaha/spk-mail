package mockimap

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"
)

func TestServer_StartsAndAcceptsLogin(t *testing.T) {
	srv, err := Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", srv.Addr(), 2*time.Second)
	require.NoError(t, err)

	c := imapclient.New(conn, nil)
	require.NoError(t, c.Login("alice@example.com", "secret").Wait())
	require.NoError(t, c.Logout().Wait())
}
