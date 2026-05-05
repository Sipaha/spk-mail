package api

import (
	"testing"

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
