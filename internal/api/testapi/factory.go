// Package testapi provides constructors for tests in other packages.
package testapi

import (
	"testing"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/events"
	"github.com/spk/spk-mail/internal/teststore"
)

func NewStub(t *testing.T) api.API {
	t.Helper()
	st, sec := teststore.Open(t)
	return api.NewStub(st, sec, events.NewEmitter(), nil)
}
