package testapi

import (
	"encoding/json"
	"net/http"

	"github.com/spk/spk-mail/internal/storage"
)

func dbDumpHandler(s *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		out := map[string]any{}

		// Project AccountRow into a stable JSON shape with explicit lowercase
		// tags. Without this, the marshaller emits Go-default PascalCase
		// field names ("Email", "Name") and Playwright assertions like
		// `dump.accounts[].email` silently see undefined.
		type accountDump struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Email     string `json:"email"`
			Color     string `json:"color"`
			ProfileID *int64 `json:"profile_id,omitempty"`
		}
		accs, _ := s.ListAccounts(ctx)
		accountList := make([]accountDump, 0, len(accs))
		for _, a := range accs {
			accountList = append(accountList, accountDump{
				ID: a.ID, Name: a.Name, Email: a.Email, Color: a.Color, ProfileID: a.ProfileID,
			})
		}
		out["accounts"] = accountList

		type profileDump struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Color     string `json:"color"`
			SortOrder int    `json:"sort_order"`
			CreatedAt int64  `json:"created_at"`
			Muted     bool   `json:"muted"`
		}
		rawProfiles, _ := s.ListProfiles(ctx)
		profiles := make([]profileDump, 0, len(rawProfiles))
		for _, p := range rawProfiles {
			profiles = append(profiles, profileDump{ID: p.ID, Name: p.Name, Color: p.Color, SortOrder: p.SortOrder, CreatedAt: p.CreatedAt, Muted: p.Muted})
		}
		out["profiles"] = profiles

		threads, _ := s.ListThreadsRecent(ctx, 1000, 0)
		out["threads"] = threads

		var folders []storage.FolderRow
		for _, a := range accs {
			fs, _ := s.ListFolders(ctx, a.ID)
			folders = append(folders, fs...)
		}
		out["folders"] = folders

		type msgDump struct {
			ID             int64  `json:"id"`
			AccountID      int64  `json:"account_id"`
			FolderID       int64  `json:"folder_id"`
			UID            int64  `json:"uid"`
			Subject        string `json:"subject"`
			From           string `json:"from"`
			Flags          string `json:"flags"`
			Date           int64  `json:"date"`
			HasAttachments bool   `json:"has_attachments"`
		}
		var msgs []msgDump
		rows, err := s.DB().QueryContext(ctx, `SELECT id,account_id,folder_id,uid,COALESCE(subject,''),COALESCE(from_addr,''),flags,date,has_attachments FROM messages ORDER BY date DESC LIMIT 1000`)
		if err == nil && rows != nil {
			defer rows.Close()
			for rows.Next() {
				var m msgDump
				var hasAtt int
				_ = rows.Scan(&m.ID, &m.AccountID, &m.FolderID, &m.UID, &m.Subject, &m.From, &m.Flags, &m.Date, &hasAtt)
				m.HasAttachments = hasAtt != 0
				msgs = append(msgs, m)
			}
		}
		out["messages"] = msgs

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
