package library

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"time"
)

// Optional system sqlite3 is used strictly read-only. The application does not
// modify the official database and does not require Python or a SQL driver.
func sharedLibraryPaths() []string {
	binary, e := exec.LookPath("sqlite3")
	if e != nil {
		return nil
	}
	for _, db := range []string{"/usr/local/apps/@appdata/trim.music/db/music.db", "/var/apps/trim.music/var/db/music.db"} {
		if info, e := os.Stat(db); e != nil || !info.Mode().IsRegular() {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		output, e := exec.CommandContext(ctx, binary, "-readonly", "-json", db, "SELECT path FROM shared_library ORDER BY id LIMIT 128").Output()
		cancel()
		if e != nil || len(output) > 1<<20 {
			continue
		}
		var rows []struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(output, &rows) != nil {
			continue
		}
		paths := []string{}
		for _, row := range rows {
			if p, e := directory(row.Path, false); e == nil {
				paths = append(paths, p)
			}
		}
		if len(paths) > 0 {
			return paths
		}
	}
	return nil
}
