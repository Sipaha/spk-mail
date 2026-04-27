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

		accs, _ := s.ListAccounts(ctx)
		out["accounts"] = accs

		type profileDump struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Color     string `json:"color"`
			SortOrder int    `json:"sort_order"`
			CreatedAt int64  `json:"created_at"`
		}
		rawProfiles, _ := s.ListProfiles(ctx)
		profiles := make([]profileDump, 0, len(rawProfiles))
		for _, p := range rawProfiles {
			profiles = append(profiles, profileDump{ID: p.ID, Name: p.Name, Color: p.Color, SortOrder: p.SortOrder, CreatedAt: p.CreatedAt})
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
			ID, AccountID, FolderID int64
			UID                     int64
			Subject, From, Flags    string
			Date                    int64
			HasAttachments          bool
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
