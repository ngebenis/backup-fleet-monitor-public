package main

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type pveStorageRes struct {
	Node    string `json:"node"`
	Storage string `json:"storage"`
	Plugin  string `json:"plugintype"`
	Content string `json:"content"`
	Shared  int    `json:"shared"`
	Status  string `json:"status"`
	Disk    int64  `json:"disk"`
	MaxDisk int64  `json:"maxdisk"`
}

// pollPVEStorage reads /cluster/resources?type=storage (read-only) and refreshes storage_pools.
func pollPVEStorage(ctx context.Context, db *pgxpool.Pool, cl *pveClient, sid string, warn func(string)) int {
	var res []pveStorageRes
	if err := cl.get(ctx, "/cluster/resources", url.Values{"type": {"storage"}}, &res); err != nil {
		warn("storage resources: " + err.Error())
		return 0
	}
	start := time.Now()
	n := 0
	for _, r := range res {
		if r.Storage == "" {
			continue
		}
		node := r.Node
		if r.Shared == 1 {
			node = ""
		}
		if _, err := db.Exec(ctx, `INSERT INTO storage_pools(source_id,node,name,kind,content,shared,total_bytes,used_bytes,online,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (source_id,node,name) DO UPDATE SET kind=excluded.kind,content=excluded.content,shared=excluded.shared,
			 total_bytes=excluded.total_bytes,used_bytes=excluded.used_bytes,online=excluded.online,updated_at=excluded.updated_at`,
			sid, node, r.Storage, r.Plugin, r.Content, r.Shared == 1, r.MaxDisk, r.Disk, r.Status == "available", start); err != nil {
			warn("storage db: " + err.Error())
			continue
		}
		n++
	}
	db.Exec(ctx, `DELETE FROM storage_pools WHERE source_id=$1 AND updated_at < $2`, sid, start)
	return n
}

func (a *App) storageList(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(r.Context(), `SELECT bs.name, p.node, p.name, p.kind, p.content, p.shared, p.total_bytes, p.used_bytes, p.online, p.updated_at
		FROM storage_pools p JOIN backup_sources bs ON bs.id=p.source_id AND bs.enabled WHERE p.total_bytes>0
		ORDER BY CASE WHEN p.total_bytes>0 THEN p.used_bytes::float8/p.total_bytes ELSE 0 END DESC, p.name`)
	if err != nil {
		apiErr(w, 500, "db", "query failed")
		return
	}
	defer rows.Close()
	type o struct {
		Source  string    `json:"source"`
		Node    string    `json:"node"`
		Name    string    `json:"name"`
		Kind    string    `json:"kind"`
		Content string    `json:"content"`
		Shared  bool      `json:"shared"`
		Total   int64     `json:"totalBytes"`
		Used    int64     `json:"usedBytes"`
		Online  bool      `json:"online"`
		Updated time.Time `json:"updatedAt"`
	}
	out := []o{}
	for rows.Next() {
		var x o
		if rows.Scan(&x.Source, &x.Node, &x.Name, &x.Kind, &x.Content, &x.Shared, &x.Total, &x.Used, &x.Online, &x.Updated) == nil {
			out = append(out, x)
		}
	}
	writeJSON(w, 200, out)
}
