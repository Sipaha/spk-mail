package testapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/stretchr/testify/require"
)

const validFixtureYAML = `accounts:
  - name: Alice
    email: alice@example.com
    password: secret
`

// writeFixture writes name (a bare filename, no separators) into dir with
// valid fixture YAML content and returns its path.
func writeFixture(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(validFixtureYAML), 0o600))
	return path
}

func TestLoadFixtureFromRequest_QueryFixture(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "seed.yaml")

	var f mockimap.Fixture
	err := loadFixtureFromRequest(dir, "", "seed.yaml", nil, &f)
	require.NoError(t, err)
	require.Len(t, f.Accounts, 1)
	require.Equal(t, "alice@example.com", f.Accounts[0].Email)
}

func TestLoadFixtureFromRequest_DefaultFixture(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "default.yaml")

	var f mockimap.Fixture
	err := loadFixtureFromRequest(dir, "default.yaml", "", nil, &f)
	require.NoError(t, err)
	require.Len(t, f.Accounts, 1)
}

func TestLoadFixtureFromRequest_DecodeBodyTakesPriority(t *testing.T) {
	decode := func(target any) error {
		f := target.(*mockimap.Fixture)
		f.Accounts = []mockimap.FixtureAccount{{Name: "Bob", Email: "bob@example.com"}}
		return nil
	}
	var f mockimap.Fixture
	err := loadFixtureFromRequest("", "", "", decode, &f)
	require.NoError(t, err)
	require.Len(t, f.Accounts, 1)
	require.Equal(t, "bob@example.com", f.Accounts[0].Email)
}

func TestLoadFixtureFromRequest_NoBodyNoDefault(t *testing.T) {
	var f mockimap.Fixture
	err := loadFixtureFromRequest(t.TempDir(), "", "", nil, &f)
	require.Error(t, err)
}

// TestLoadFixtureFromRequest_TraversalRejected: a query fixture name that
// attempts to escape fixturesDir must not read a file outside it. Since
// filepath.Base(name) strips any directory components, the worst a crafted
// name can do is collapse to fixturesDir's own parent *directory*, which
// mockimap.LoadFixture then fails to read as a fixture file (os.ReadFile on
// a directory errors) - so the traversal never yields readable content
// outside fixturesDir.
func TestLoadFixtureFromRequest_TraversalRejected(t *testing.T) {
	dir := t.TempDir()
	// Plant a secret file next to (outside) the fixtures dir to prove it's
	// never reached.
	secret := filepath.Join(filepath.Dir(dir), "secret-outside.yaml")
	require.NoError(t, os.WriteFile(secret, []byte(validFixtureYAML), 0o600))
	defer func() { _ = os.Remove(secret) }()

	for _, name := range []string{"..", "../..", "../secret-outside.yaml", "a/../../secret-outside.yaml"} {
		var f mockimap.Fixture
		err := loadFixtureFromRequest(dir, "", name, nil, &f)
		require.Error(t, err, "name=%q must not resolve to a file outside fixturesDir", name)
	}
}

// TestLoadFixtureFromRequest_DotDotPrefixNameAccepted: a legitimate fixture
// filename that merely starts with two dots must still be loadable (this is
// the precise-containment-check regression the imprecise
// strings.HasPrefix(rel, "..") form used to break).
func TestLoadFixtureFromRequest_DotDotPrefixNameAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "..hidden.yaml")

	var f mockimap.Fixture
	err := loadFixtureFromRequest(dir, "", "..hidden.yaml", nil, &f)
	require.NoError(t, err)
	require.Len(t, f.Accounts, 1)
}
