package testapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/spk/spk-mail/internal/mockimap"
)

type injectReq struct {
	Email    string `json:"email"`  // which account in the mock to inject for
	Folder   string `json:"folder"` // default INBOX
	From     string `json:"from"`
	Subject  string `json:"subject"`
	BodyText string `json:"body_text"`
}

type injectHandler struct{ mock *mockimap.Server }

func (h *injectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.mock == nil {
		http.Error(w, "mock IMAP not running", http.StatusBadRequest)
		return
	}
	var req injectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Folder == "" {
		req.Folder = "INBOX"
	}
	u := h.mock.User(req.Email)
	if u == nil {
		http.Error(w, "unknown email "+req.Email, http.StatusNotFound)
		return
	}

	now := time.Now().UTC()
	raw := []byte(fmt.Sprintf("From: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@spk-mail.test>\r\nContent-Type: text/plain\r\n\r\n%s",
		req.From, req.Subject, now.Format(time.RFC1123Z), now.UnixNano(), req.BodyText))

	if _, err := u.Append(req.Folder, bytes.NewReader(raw), &imap.AppendOptions{Time: now}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
