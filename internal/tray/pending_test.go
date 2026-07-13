package tray

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// count reports the number of tracked notifications. Test-only — the
// controller never inspects the map's size, so this accessor lives here
// rather than on the production type.
func (p *pendingActions) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.by)
}

func TestPendingActions_PutTake(t *testing.T) {
	p := newPendingActions(8)
	p.Put(1, ActionContext{AccountID: 10, ThreadID: 100, MessageID: 1000})
	p.Put(2, ActionContext{AccountID: 20, ThreadID: 200, MessageID: 2000})

	got, ok := p.Take(1)
	require.True(t, ok)
	require.Equal(t, ActionContext{AccountID: 10, ThreadID: 100, MessageID: 1000}, got)

	// Second Take is empty — entries are consume-once.
	_, ok = p.Take(1)
	require.False(t, ok)

	require.Equal(t, 1, p.count())
}

func TestPendingActions_OverflowEvictsOldest(t *testing.T) {
	p := newPendingActions(3)
	p.Put(1, ActionContext{ThreadID: 1})
	p.Put(2, ActionContext{ThreadID: 2})
	p.Put(3, ActionContext{ThreadID: 3})
	p.Put(4, ActionContext{ThreadID: 4}) // evicts id=1

	_, ok := p.Take(1)
	require.False(t, ok, "oldest entry should have been evicted")

	got, ok := p.Take(4)
	require.True(t, ok)
	require.EqualValues(t, 4, got.ThreadID)

	// Cap survives churn: insert another batch and verify size stays bounded.
	for i := uint32(10); i < 30; i++ {
		p.Put(i, ActionContext{ThreadID: int64(i)})
	}
	require.LessOrEqual(t, p.count(), 3)
}

func TestPendingActions_PutSameIDReplaces(t *testing.T) {
	p := newPendingActions(4)
	p.Put(7, ActionContext{ThreadID: 1})
	p.Put(7, ActionContext{ThreadID: 99}) // replace, not insert

	require.Equal(t, 1, p.count())
	got, ok := p.Take(7)
	require.True(t, ok)
	require.EqualValues(t, 99, got.ThreadID)
}

func TestPendingActions_Delete(t *testing.T) {
	p := newPendingActions(4)
	p.Put(1, ActionContext{ThreadID: 1})
	p.Put(2, ActionContext{ThreadID: 2})

	p.Delete(1)
	require.Equal(t, 1, p.count())
	_, ok := p.Take(1)
	require.False(t, ok)

	// Delete of unknown id is a no-op, not a panic.
	p.Delete(999)
	require.Equal(t, 1, p.count())
}
