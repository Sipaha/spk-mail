package clock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNow_DefaultsToWallClock(t *testing.T) {
	c := New()
	got := c.Now()
	require.WithinDuration(t, time.Now(), got, time.Second)
}

func TestNow_FixedAfterSet(t *testing.T) {
	c := New()
	fixed := time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC)
	c.Set(fixed)
	require.True(t, c.Now().Equal(fixed))
	c.Reset()
	require.WithinDuration(t, time.Now(), c.Now(), time.Second)
}
