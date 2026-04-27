package sync

import "sync"

// folderRegistry tracks per-account, per-folder UIDVALIDITY and last-seen UIDNEXT
// in memory. Authoritative source is the DB; this is a fast-path cache.
type folderRegistry struct {
	mu    sync.Mutex
	state map[FolderUID]folderInfo
}

type folderInfo struct {
	UIDValidity int64
	UIDNext     int64
}

func newFolderRegistry() *folderRegistry {
	return &folderRegistry{state: map[FolderUID]folderInfo{}}
}

func (r *folderRegistry) Set(fid int64, validity, next int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state[FolderUID{FolderID: fid}] = folderInfo{validity, next}
}

func (r *folderRegistry) Get(fid int64) (int64, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.state[FolderUID{FolderID: fid}]
	return v.UIDValidity, v.UIDNext
}
