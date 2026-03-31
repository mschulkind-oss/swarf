package backends

type SyncResult struct {
	Success      bool
	Message      string
	FilesChanged int
}

type SyncBackend interface {
	Sync(storePath string) SyncResult
	HasChanges(storePath string) bool
}
