package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type pveCreds struct {
	AuthType    string `json:"authType"`
	TokenID     string `json:"tokenId,omitempty"`
	TokenSecret string `json:"tokenSecret,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
}

type srcRow struct {
	ID, Name, Platform, BaseURL, CA string
	LastError                       string
	TLSVerify                       bool
	SecretEnc                       []byte
}

func (s srcRow) client() (*pveClient, error) {
	if len(s.SecretEnc) == 0 {
		return nil, errors.New("no credentials stored")
	}
	pt, err := decrypt(s.SecretEnc)
	if err != nil {
		return nil, errors.New("cannot decrypt credentials (SECRET_KEY changed?)")
	}
	var c pveCreds
	if err := json.Unmarshal(pt, &c); err != nil {
		return nil, errors.New("corrupt credentials")
	}
	return newPVE(s.BaseURL, c, s.TLSVerify, s.CA)
}

type pveClient struct {
	base   string
	hc     *http.Client
	creds  pveCreds
	ticket string
}

func newPVE(base string, c pveCreds, tlsVerify bool, caPEM string) (*pveClient, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if !tlsVerify {
		tc.InsecureSkipVerify = true
	} else if caPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, errors.New("invalid CA PEM")
		}
		tc.RootCAs = pool
	}
	tr := &http.Transport{TLSClientConfig: tc, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConns: 4}
	hc := &http.Client{Transport: tr, Timeout: 90 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &pveClient{base: strings.TrimRight(base, "/"), hc: hc, creds: c}, nil
}

func (c *pveClient) login(ctx context.Context) error {
	form := url.Values{"username": {c.creds.Username}, "password": {c.creds.Password}}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.base+"/api2/json/access/ticket", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("login failed: HTTP %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			Ticket string `json:"ticket"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Data.Ticket == "" {
		return errors.New("login failed: no ticket (two-factor enabled? use API token)")
	}
	c.ticket = env.Data.Ticket
	return nil
}

// get performs a read-only GET. No other HTTP method is ever issued against a source.
func (c *pveClient) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.base + "/api2/json" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	if c.creds.AuthType == "token" {
		req.Header.Set("Authorization", "PVEAPIToken="+c.creds.TokenID+"="+c.creds.TokenSecret)
	} else {
		if c.ticket == "" {
			if err := c.login(ctx); err != nil {
				return err
			}
		}
		req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: c.ticket})
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d on %s", resp.StatusCode, path)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("bad JSON on %s", path)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

type pveRes struct {
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Node     string `json:"node"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Template int    `json:"template"`
}
type pveNode struct {
	Node   string `json:"node"`
	Status string `json:"status"`
}
type pveTask struct {
	UPID   string `json:"upid"`
	Status string `json:"status"`
	Start  int64  `json:"starttime"`
	End    int64  `json:"endtime"`
}
type pveLog struct {
	T string `json:"t"`
}
type pveStore struct {
	Storage string `json:"storage"`
	Active  int    `json:"active"`
	Shared  int    `json:"shared"`
}
type pveContent struct {
	Volid string      `json:"volid"`
	VMID  json.Number `json:"vmid"`
	Ctime int64       `json:"ctime"`
	Size  int64       `json:"size"`
}

var nodeRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func testSource(ctx context.Context, s srcRow) (map[string]any, error) {
	switch s.Platform {
	case "veeam":
		return testVeeam(ctx, s)
	case "synology_abb":
		return testSynology(ctx, s)
	}
	cl, err := s.client()
	if err != nil {
		return nil, err
	}
	var v struct {
		Version string `json:"version"`
	}
	if err := cl.get(ctx, "/version", nil, &v); err != nil {
		return nil, err
	}
	var res []pveRes
	if err := cl.get(ctx, "/cluster/resources", url.Values{"type": {"vm"}}, &res); err != nil {
		return nil, err
	}
	var nodes []pveNode
	if err := cl.get(ctx, "/nodes", nil, &nodes); err != nil {
		return nil, err
	}
	n := 0
	for _, r := range res {
		if r.Template == 0 {
			n++
		}
	}
	return map[string]any{"version": v.Version, "nodes": len(nodes), "workloads": n}, nil
}

func pollProxmox(ctx context.Context, db *pgxpool.Pool, s srcRow) (map[string]any, []string, error) {
	cl, err := s.client()
	if err != nil {
		return nil, nil, err
	}
	start := time.Now()
	var res []pveRes
	if err := cl.get(ctx, "/cluster/resources", url.Values{"type": {"vm"}}, &res); err != nil {
		return nil, nil, fmt.Errorf("cluster/resources: %w", err)
	}
	var warns []string
	warn := func(m string) { warns = append(warns, m) }
	nWl := 0
	for _, r := range res {
		if r.VMID == 0 {
			continue
		}
		if _, err := db.Exec(ctx, `INSERT INTO backup_workloads(source_id,vmid,name,node,kind,status,template,last_seen)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (source_id,vmid) DO UPDATE SET name=$3,node=$4,kind=$5,status=$6,template=$7,last_seen=$8`,
			s.ID, r.VMID, r.Name, r.Node, r.Type, r.Status, r.Template == 1, start); err != nil {
			return nil, warns, fmt.Errorf("db workloads: %w", err)
		}
		nWl++
	}
	db.Exec(ctx, `UPDATE backup_workloads SET status='removed' WHERE source_id=$1 AND last_seen < $2 AND status<>'removed'`, s.ID, start)

	var nodes []pveNode
	if err := cl.get(ctx, "/nodes", nil, &nodes); err != nil {
		warn("nodes: " + err.Error())
	}
	nTasks, nSess, nPts := 0, 0, 0
	shared := map[string]bool{}
	for _, n := range nodes {
		if n.Status != "online" || !nodeRe.MatchString(n.Node) {
			continue
		}
		t, ss, err := pollNodeTasks(ctx, db, cl, s.ID, n.Node)
		nTasks += t
		nSess += ss
		if err != nil {
			warn(n.Node + " tasks: " + err.Error())
		}
		p := pollNodePoints(ctx, db, cl, s.ID, n.Node, shared, warn)
		nPts += p
	}
	nSt := pollPVEStorage(ctx, db, cl, s.ID, warn)
	return map[string]any{"storage": nSt, "workloads": nWl, "newTasks": nTasks, "newSessions": nSess, "points": nPts, "nodes": len(nodes)}, warns, nil
}

func pollNodeTasks(ctx context.Context, db *pgxpool.Pool, cl *pveClient, sid, node string) (int, int, error) {
	var tasks []pveTask
	if err := cl.get(ctx, "/nodes/"+node+"/tasks", url.Values{"typefilter": {"vzdump"}, "limit": {"300"}}, &tasks); err != nil {
		return 0, 0, err
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Start > tasks[j].Start })
	var upids []string
	for _, t := range tasks {
		if t.End > 0 && t.UPID != "" {
			upids = append(upids, t.UPID)
		}
	}
	known := map[string]bool{}
	if len(upids) > 0 {
		rows, err := db.Query(ctx, `SELECT upid FROM vzdump_tasks WHERE source_id=$1 AND upid = ANY($2)`, sid, upids)
		if err != nil {
			return 0, 0, err
		}
		for rows.Next() {
			var u string
			rows.Scan(&u)
			known[u] = true
		}
		rows.Close()
	}
	nt, ns := 0, 0
	for _, t := range tasks {
		if nt >= 100 {
			break
		}
		if t.End == 0 || t.UPID == "" || known[t.UPID] {
			continue
		}
		var lg []pveLog
		if err := cl.get(ctx, "/nodes/"+node+"/tasks/"+url.PathEscape(t.UPID)+"/log", url.Values{"limit": {"50000"}}, &lg); err != nil {
			return nt, ns, fmt.Errorf("task log: %w", err)
		}
		lines := make([]string, len(lg))
		for i, l := range lg {
			lines[i] = l.T
		}
		started := time.Unix(t.Start, 0)
		results := parseVzdumpLog(lines, started, t.Status)
		tx, err := db.Begin(ctx)
		if err != nil {
			return nt, ns, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO vzdump_tasks(source_id,upid,node,started_at,ended_at,status) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
			sid, t.UPID, node, started, time.Unix(t.End, 0), t.Status); err != nil {
			tx.Rollback(ctx)
			return nt, ns, err
		}
		for _, r := range results {
			if _, err := tx.Exec(ctx, `INSERT INTO backup_sessions(source_id,vmid,workload_id,upid,node,started_at,ended_at,status,message,size_bytes)
				VALUES($1,$2,(SELECT id FROM backup_workloads WHERE source_id=$1 AND vmid=$2),$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT (source_id,upid,vmid) DO NOTHING`,
				sid, r.VMID, t.UPID, node, r.Started, r.Ended, r.Status, r.Msg, r.Size); err != nil {
				tx.Rollback(ctx)
				return nt, ns, err
			}
			ns++
		}
		if err := tx.Commit(ctx); err != nil {
			return nt, ns, err
		}
		nt++
	}
	return nt, ns, nil
}

type vmResult struct {
	VMID           int
	Status, Msg    string
	Size           int64
	Dur            time.Duration
	Started, Ended time.Time
	warn           bool
	done           bool
}

var (
	reStart = regexp.MustCompile(`Starting Backup of VM (\d+) \((?:qemu|lxc)\)`)
	reFin   = regexp.MustCompile(`Finished Backup of VM (\d+) \((\d+):(\d+):(\d+)\)`)
	reFail  = regexp.MustCompile(`Backup of VM (\d+) failed - (.*)`)
	reSize  = regexp.MustCompile(`archive file size: ([0-9.]+)\s*([KMGT]?i?B)`)
)

func parseSize(num, unit string) int64 {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	mult := map[string]float64{"B": 1, "KB": 1 << 10, "KiB": 1 << 10, "MB": 1 << 20, "MiB": 1 << 20, "GB": 1 << 30, "GiB": 1 << 30, "TB": 1 << 40, "TiB": 1 << 40}
	return int64(f * mult[unit])
}

// parseVzdumpLog extracts per-VM results from a vzdump task log. Per-VM times are
// approximate: PVE logs only durations, so they are accumulated from the task start.
func parseVzdumpLog(lines []string, start time.Time, taskStatus string) []*vmResult {
	var out []*vmResult
	by := map[int]*vmResult{}
	var cur *vmResult
	get := func(id int) *vmResult {
		if r, ok := by[id]; ok {
			return r
		}
		r := &vmResult{VMID: id}
		by[id] = r
		out = append(out, r)
		return r
	}
	for _, l := range lines {
		if m := reStart.FindStringSubmatch(l); m != nil {
			id, _ := strconv.Atoi(m[1])
			cur = get(id)
			continue
		}
		if m := reFin.FindStringSubmatch(l); m != nil {
			id, _ := strconv.Atoi(m[1])
			r := get(id)
			h, _ := strconv.Atoi(m[2])
			mi, _ := strconv.Atoi(m[3])
			se, _ := strconv.Atoi(m[4])
			r.Dur = time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute + time.Duration(se)*time.Second
			r.done = true
			if r.warn {
				r.Status = "warning"
			} else {
				r.Status = "success"
			}
			cur = nil
			continue
		}
		if m := reFail.FindStringSubmatch(l); m != nil {
			id, _ := strconv.Atoi(m[1])
			r := get(id)
			r.Status, r.Msg, r.done = "failed", strings.TrimSpace(m[2]), true
			cur = nil
			continue
		}
		if cur != nil {
			if m := reSize.FindStringSubmatch(l); m != nil {
				cur.Size = parseSize(m[1], m[2])
			} else if strings.Contains(l, "WARNING:") {
				cur.warn = true
				if cur.Msg == "" {
					cur.Msg = strings.TrimSpace(l)
					if len(cur.Msg) > 500 {
						cur.Msg = cur.Msg[:500]
					}
				}
			}
		}
	}
	t := start
	for _, r := range out {
		r.Started = t
		if !r.done {
			r.Status = "failed"
			r.Msg = "no result in task log (task status: " + taskStatus + ")"
		}
		t = t.Add(r.Dur)
		r.Ended = t
		if len(r.Msg) > 1000 {
			r.Msg = r.Msg[:1000]
		}
	}
	return out
}

func pollNodePoints(ctx context.Context, db *pgxpool.Pool, cl *pveClient, sid, node string, shared map[string]bool, warn func(string)) int {
	var stores []pveStore
	if err := cl.get(ctx, "/nodes/"+node+"/storage", url.Values{"content": {"backup"}}, &stores); err != nil {
		warn(node + " storage: " + err.Error())
		return 0
	}
	total := 0
	if len(stores) == 0 {
		warn(node + ": no storage with content=backup visible to this credential")
	}
	for _, st := range stores {
		if st.Active == 0 || !nodeRe.MatchString(st.Storage) {
			warn(node + "/" + st.Storage + ": skipped (active=" + strconv.Itoa(st.Active) + ")")
			continue
		}
		scope := node
		if st.Shared == 1 {
			if shared[st.Storage] {
				continue
			}
			shared[st.Storage] = true
			scope = ""
		}
		var items []pveContent
		if err := cl.get(ctx, "/nodes/"+node+"/storage/"+st.Storage+"/content", url.Values{"content": {"backup"}}, &items); err != nil {
			warn(node + "/" + st.Storage + " content: " + err.Error())
			continue
		}
		skipVM, skipCt, skipVol := 0, 0, 0
		vols := make([]string, 0, len(items))
		for _, it := range items {
			vm, _ := it.VMID.Int64()
			if it.Volid == "" {
				skipVol++
				continue
			}
			if vm == 0 {
				skipVM++
				continue
			}
			if it.Ctime == 0 {
				skipCt++
				continue
			}
			if _, err := db.Exec(ctx, `INSERT INTO backup_points(source_id,storage,node,volid,vmid,ctime,size_bytes) VALUES($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (source_id,volid) DO UPDATE SET storage=$2,node=$3,vmid=$5,ctime=$6,size_bytes=$7`,
				sid, st.Storage, scope, it.Volid, int(vm), time.Unix(it.Ctime, 0), it.Size); err != nil {
				warn("db points: " + err.Error())
				break
			}
			vols = append(vols, it.Volid)
			total++
		}
		warn(node + "/" + st.Storage + ": raw=" + strconv.Itoa(len(items)) + " kept=" + strconv.Itoa(len(vols)) + " skip_novmid=" + strconv.Itoa(skipVM) + " skip_noctime=" + strconv.Itoa(skipCt) + " skip_novolid=" + strconv.Itoa(skipVol))
		db.Exec(ctx, `DELETE FROM backup_points WHERE source_id=$1 AND storage=$2 AND node=$3 AND NOT (volid = ANY($4))`, sid, st.Storage, scope, vols)
	}
	return total
}

func loadSource(ctx context.Context, db *pgxpool.Pool, id string) (srcRow, error) {
	var s srcRow
	err := db.QueryRow(ctx, `SELECT id::text,name,platform,base_url,ca_pem,tls_verify,secret_enc,last_error FROM backup_sources WHERE id=$1`, id).
		Scan(&s.ID, &s.Name, &s.Platform, &s.BaseURL, &s.CA, &s.TLSVerify, &s.SecretEnc, &s.LastError)
	return s, err
}
