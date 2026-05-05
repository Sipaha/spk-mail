package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt" // for Sprintf in stubWithRawSetup
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRawFilename_FromSubject(t *testing.T) {
	s := "Hello world"
	mid := ""
	got := rawFilename(&s, &mid, 1)
	require.Equal(t, "Hello world.eml", got)
}

func TestRawFilename_SanitizesSubject(t *testing.T) {
	s := `foo/bar:baz?<x>"|*`
	mid := ""
	got := rawFilename(&s, &mid, 1)
	require.Equal(t, "foo_bar_baz__x____.eml", got)
}

func TestRawFilename_TrimsTrailingDotsAndSpaces(t *testing.T) {
	s := "foo bar.   "
	mid := ""
	got := rawFilename(&s, &mid, 1)
	require.Equal(t, "foo bar.eml", got)
}

func TestRawFilename_TruncatesLongSubjectByRunes(t *testing.T) {
	long := ""
	for i := 0; i < 120; i++ {
		long += "ы"
	}
	mid := ""
	got := rawFilename(&long, &mid, 1)
	want := ""
	for i := 0; i < 80; i++ {
		want += "ы"
	}
	want += ".eml"
	require.Equal(t, want, got)
}

func TestRawFilename_FallbackToMessageID(t *testing.T) {
	empty := ""
	mid := "<abc.def@example.com>"
	got := rawFilename(&empty, &mid, 1)
	require.Equal(t, "abc.def@example.com.eml", got)
}

func TestRawFilename_FallbackToMessageDBID(t *testing.T) {
	got := rawFilename(nil, nil, 42)
	require.Equal(t, "message-42.eml", got)
}

func TestRawFilename_EmptyStringsFallback(t *testing.T) {
	empty := ""
	got := rawFilename(&empty, &empty, 7)
	require.Equal(t, "message-7.eml", got)
}

const sampleRaw = "From: alice@example.com\r\n" +
	"To: bob@example.com\r\n" +
	"Subject: hello\r\n" +
	"Message-ID: <m1@example.com>\r\n" +
	"Date: Mon, 27 Apr 2026 10:30:00 +0000\r\n" +
	"\r\n" +
	"hi from raw"

// rawLiteral is a minimal imap.LiteralReader over a []byte for use in
// tests that call imapmemserver.User.Append directly.
type rawLiteral struct {
	data []byte
	pos  int
}

func (l *rawLiteral) Read(p []byte) (int, error) {
	if l.pos >= len(l.data) {
		return 0, io.EOF
	}
	n := copy(p, l.data[l.pos:])
	l.pos += n
	return n, nil
}

func (l *rawLiteral) Size() int64 { return int64(len(l.data)) }

// stubWithRawSetup wires a Stub against a tempdir + mockimap so the
// raw-fetch tests can drive both the cache-hit and lazy-fetch paths
// from one fixture builder. Returns the stub, accID, mID, mock user, and mock server.
func stubWithRawSetup(t *testing.T) (*Stub, int64, int64, *imapmemserver.User, *mockimap.Server) {
	t.Helper()
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	t.Cleanup(func() { _ = mock.Close() })

	dir := t.TempDir()
	st, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)

	host, port := splitHostPortAddrAPI(mock.Addr())
	ctx := context.Background()
	accID, err := st.InsertAccount(ctx, storage.AccountRow{
		Name: "X", Email: "alice@example.com",
		IMAPHost: host, IMAPPort: port,
		IMAPUsername: "alice@example.com", UseTLS: false,
		Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	require.NoError(t, sec.Set(fmt.Sprintf("account:%d", accID), []byte("secret")))

	role := "inbox"
	fID, err := st.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role,
		UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)

	subject := "hello"
	mid := "<m1@example.com>"
	mID, err := st.InsertMessage(ctx, storage.MessageRow{
		AccountID: accID, FolderID: fID, UID: 1, Date: time.Now().Unix(),
		Flags: "[]", Subject: &subject, MessageID: &mid,
	})
	require.NoError(t, err)

	stub := NewStub(st, sec, NewEmitter(), nil)
	stub.DataDir = dir

	u := mock.User("alice@example.com")
	require.NotNil(t, u)
	_, err = u.Append("INBOX", &rawLiteral{data: []byte(sampleRaw)}, &imap.AppendOptions{})
	require.NoError(t, err)

	return stub, accID, mID, u, mock
}

// splitHostPortAddrAPI mirrors the helper used by the sync tests. Local
// to keep internal/api decoupled from internal/sync's test files.
func splitHostPortAddrAPI(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		panic(err)
	}
	return host, port
}

// sha256Hex computes the lowercase hex sha256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestGetRawMessage_CacheHit: blob already linked + on-disk →
// returned without touching IMAP.
func TestGetRawMessage_CacheHit(t *testing.T) {
	stub, _, mID, _, mock := stubWithRawSetup(t)
	mock.Close() // any IMAP fetch attempt would now fail loudly

	ctx := context.Background()
	rawBytes := []byte(sampleRaw)
	sha := sha256Hex(rawBytes)
	blobID, _, err := stub.Store.InsertOrIncBlob(ctx, sha, int64(len(rawBytes)), 0)
	require.NoError(t, err)
	path := storage.BlobPath(stub.DataDir, sha)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, rawBytes, 0o600))
	_, _, err = stub.Store.SetMessageRawBlob(ctx, mID, blobID, time.Now().Unix())
	require.NoError(t, err)

	dto, err := stub.GetRawMessage(ctx, mID)
	require.NoError(t, err)
	got, err := base64.StdEncoding.DecodeString(dto.RawB64)
	require.NoError(t, err)
	require.Equal(t, rawBytes, got)
	require.Equal(t, "hello.eml", dto.Filename)
	require.EqualValues(t, len(rawBytes), dto.SizeBytes)
}

// TestGetRawMessage_LazyFetch: NULL slot triggers IMAP fetch, bytes
// land in the blob store, second call hits cache.
func TestGetRawMessage_LazyFetch(t *testing.T) {
	stub, _, mID, _, _ := stubWithRawSetup(t)
	ctx := context.Background()

	dto, err := stub.GetRawMessage(ctx, mID)
	require.NoError(t, err)
	got, err := base64.StdEncoding.DecodeString(dto.RawB64)
	require.NoError(t, err)
	require.Contains(t, string(got), "Subject: hello")

	_, _, found, err := stub.Store.GetMessageRawBlob(ctx, mID)
	require.NoError(t, err)
	require.True(t, found)
}

// TestGetRawMessage_MissingOnDisk: linked blob whose file was
// manually removed clears the link, decrements refcount, falls
// through to IMAP and returns the bytes.
func TestGetRawMessage_MissingOnDisk(t *testing.T) {
	stub, _, mID, _, _ := stubWithRawSetup(t)
	ctx := context.Background()

	const sha = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	blobID, _, err := stub.Store.InsertOrIncBlob(ctx, sha, 100, 0)
	require.NoError(t, err)
	_, _, err = stub.Store.SetMessageRawBlob(ctx, mID, blobID, time.Now().Unix())
	require.NoError(t, err)

	dto, err := stub.GetRawMessage(ctx, mID)
	require.NoError(t, err)
	require.NotEmpty(t, dto.RawB64, "lazy fallback must return bytes")

	gotBlob, _, found, _ := stub.Store.GetMessageRawBlob(ctx, mID)
	require.True(t, found)
	require.NotEqual(t, blobID, gotBlob)

	br, err := stub.Store.GetBlob(ctx, blobID)
	require.NoError(t, err)
	require.EqualValues(t, 0, br.Refcount)
}

// TestGetRawMessage_StaleUID: mockimap with no matching UID returns
// ErrRawUnavailable, not a generic transport error.
func TestGetRawMessage_StaleUID(t *testing.T) {
	stub, accID, _, _, _ := stubWithRawSetup(t)
	ctx := context.Background()

	folders, err := stub.Store.ListFolders(ctx, accID)
	require.NoError(t, err)
	require.NotEmpty(t, folders)

	subject := "ghost"
	ghostMID, err := stub.Store.InsertMessage(ctx, storage.MessageRow{
		AccountID: accID, FolderID: folders[0].ID, UID: 9999, Date: time.Now().Unix(),
		Flags: "[]", Subject: &subject,
	})
	require.NoError(t, err)

	_, err = stub.GetRawMessage(ctx, ghostMID)
	require.ErrorIs(t, err, ErrRawUnavailable)
}
