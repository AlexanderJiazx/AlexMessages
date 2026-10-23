package db

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// PruneUnusedUploads retains unsent drafts for 24 hours. Only regular files
// without persisted attachment references are removed. The write transaction
// serializes deletion against attachment insertion in all three processes.
func PruneUnusedUploads(now time.Time) error {
	return filepath.WalkDir(UploadRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() || now.Sub(info.ModTime()) < 24*time.Hour {
			return nil
		}
		rel, err := filepath.Rel(UploadRoot, path)
		if err != nil {
			return err
		}
		relPath := filepath.ToSlash(rel)
		// Most old files are sent attachments. A plain read skips them without
		// taking the write lock, which every process's sweep would otherwise
		// hold once per historical upload.
		referenced, err := uploadReferenced(pool, relPath)
		if err != nil || referenced {
			return err
		}
		// pool.Begin() is BEGIN IMMEDIATE (see the DSN in InitDB), so this holds
		// SQLite's cross-process write lock before the reference re-check: an
		// attachment inserted since the read above is seen, and none can be
		// inserted until the file is gone.
		tx, err := pool.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if referenced, err := uploadReferenced(tx, relPath); err != nil || referenced {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return tx.Commit()
	})
}

// uploadReferenced reports whether any attachment row points at relPath.
func uploadReferenced(q interface{ QueryRow(string, ...any) *sql.Row }, relPath string) (bool, error) {
	var referenced bool
	err := q.QueryRow("SELECT EXISTS(SELECT 1 FROM attachments WHERE rel_path = ?)", relPath).Scan(&referenced)
	return referenced, err
}
