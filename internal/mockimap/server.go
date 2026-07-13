// Package mockimap provides an in-process IMAP server for use in tests and
// --browser --imap-mock mode. It wraps github.com/emersion/go-imap/v2/imapserver
// and imapmemserver so callers can start a server on a random localhost port,
// add users, and append messages without any external infrastructure.
package mockimap

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// Server is an in-process IMAP server.
type Server struct {
	mem      *imapmemserver.Server
	srv      *imapserver.Server
	listener net.Listener

	mu    sync.Mutex
	users map[string]*imapmemserver.User
}

// Start creates a user with the given email and password, binds a TCP listener
// on a random localhost port, and begins serving IMAP. The returned *Server is
// ready to accept connections.
func Start(_ context.Context, email, password string) (*Server, error) {
	mem := imapmemserver.New()

	s := &Server{
		mem:   mem,
		users: make(map[string]*imapmemserver.User),
	}

	if err := s.addUserLocked(email, password); err != nil {
		return nil, fmt.Errorf("mockimap: create initial user: %w", err)
	}

	opts := &imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			s.mu.Lock()
			active := s.mem
			s.mu.Unlock()
			return active.NewSession(), &imapserver.GreetingData{}, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
	}

	srv := imapserver.New(opts)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("mockimap: listen: %w", err)
	}

	s.srv = srv
	s.listener = ln

	go func() {
		_ = srv.Serve(ln)
	}()

	return s, nil
}

// Addr returns the host:port the server is listening on.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Close shuts down the server and releases all resources.
func (s *Server) Close() error {
	return s.srv.Close()
}

// Reset drops all in-memory users and mailboxes. New IMAP sessions created
// after Reset see the fresh state; existing sessions may still hold the old
// memserver until they reconnect. Playwright's per-test reset runs between
// tests with no long-lived IMAP clients, so this is sufficient.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem = imapmemserver.New()
	s.users = make(map[string]*imapmemserver.User)
}

// AddUser adds a new user with INBOX pre-created. If the user already exists
// no error is returned.
func (s *Server) AddUser(email, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[email]; ok {
		return nil
	}
	return s.addUserLocked(email, password)
}

// addUserLocked creates the user and INBOX. Must be called with s.mu held (or
// during single-threaded startup).
func (s *Server) addUserLocked(email, password string) error {
	u := imapmemserver.NewUser(email, password)
	if err := u.Create("INBOX", nil); err != nil {
		return fmt.Errorf("create INBOX for %s: %w", email, err)
	}
	s.mem.AddUser(u)
	s.users[email] = u
	return nil
}

// User returns the in-memory user object for the given email, or nil if the
// user does not exist. Callers can use the returned *imapmemserver.User to
// append messages directly via u.Append.
func (s *Server) User(email string) *imapmemserver.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[email]
}
