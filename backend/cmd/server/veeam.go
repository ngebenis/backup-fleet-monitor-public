package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Veeam Backup Enterprise Manager REST (XML). Read-only: only logon, GET, and logoff of our own session.
func veeamLogon(ctx context.Context, cl *pveClient, user, pass, ver string) (string, int, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", cl.base+"/api/sessionMngr/?v="+ver, nil)
	req.SetBasicAuth(user, pass)
	resp, err := cl.hc.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	hint := strings.Join(strings.Fields(string(body)), " ")
	if len(hint) > 300 {
		hint = hint[:300]
	}
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return "", resp.StatusCode, hint, nil
	}
	sid := resp.Header.Get("X-RestSvcSessionId")
	if sid == "" {
		return "", resp.StatusCode, "no X-RestSvcSessionId header", nil
	}
	return sid, resp.StatusCode, "", nil
}

// veeamSession logs on with the stored account. Exactly ONE attempt per poll:
// repeated bad passwords can lock out a domain account, so no automatic retries.
// Username-format probing only happens in explicit PROBE mode.
func veeamSession(ctx context.Context, cl *pveClient) (string, error) {
	sid, code, hint, err := veeamLogon(ctx, cl, cl.creds.Username, cl.creds.Password, "v1_5")
	if err != nil {
		return "", err
	}
	if code == 200 || code == 201 {
		return sid, nil
	}
	if code == 401 {
		pw := cl.creds.Password
		flags := []string{}
		if len(pw) != len(strings.TrimSpace(pw)) {
			flags = append(flags, "leading/trailing whitespace")
		}
		for _, r := range pw {
			if r < 32 || r > 126 {
				flags = append(flags, "non-ASCII or control char")
				break
			}
		}
		if len(flags) == 0 {
			flags = append(flags, "clean")
		}
		return "", fmt.Errorf("veeam logon failed: HTTP 401 for user %q. Password check: %s. EM reached the credential check and rejected it, so this is the password or an account lockout, not the username format.",
			cl.creds.Username, strings.Join(flags, ", "))
	}
	return "", fmt.Errorf("veeam logon failed: HTTP %d (%s)", code, hint)
}

// veeamAltUsers returns up to 3 alternate basic-auth username spellings for a domain account.
func veeamAltUsers(user string) []string {
	u := strings.TrimSpace(user)
	var domain, name string
	if i := strings.LastIndexAny(u, "\\/"); i >= 0 {
		domain, name = u[:i], u[i+1:]
	} else if i := strings.Index(u, "@"); i >= 0 {
		name, domain = u[:i], u[i+1:]
	} else {
		return []string{"Administrator@DAPROT06"}
	}
	up := strings.ToUpper(domain)
	return []string{
		up + "\\" + name,          // DOMAIN\user (uppercase realm)
		name + "@" + up,            // user@DOMAIN (down-level UPN)
		name,                       // bare account name
	}
}

func veeamGet(ctx context.Context, cl *pveClient, sid, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", cl.base+path, nil)
	req.Header.Set("X-RestSvcSessionId", sid)
	resp, err := cl.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return xml.Unmarshal(b, out)
}

func veeamLogoff(ctx context.Context, cl *pveClient, sid string) {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", cl.base+"/api/logonSessions/"+sid, nil)
	req.Header.Set("X-RestSvcSessionId", sid)
	if r, err := cl.hc.Do(req); err == nil {
		r.Body.Close()
	}
}

// veeamRaw performs an authenticated GET and returns the body (read-only probe).
func veeamRaw(ctx context.Context, cl *pveClient, sid, path string) ([]byte, int, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", cl.base+path, nil)
	req.Header.Set("X-RestSvcSessionId", sid)
	req.Header.Set("Accept", "application/xml")
	resp, err := cl.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return b, resp.StatusCode, nil
}

func xmlKeys(b []byte, limit int) []string {
	var seen []string
	type node struct{ depth int }
	d := xml.NewDecoder(bytes.NewReader(b))
	var stack []string
	for len(seen) < limit {
		t, err := d.Token()
		if err != nil {
			break
		}
		switch el := t.(type) {
		case xml.StartElement:
			path := el.Name.Local
			if len(stack) > 0 {
				path = stack[len(stack)-1] + "/" + el.Name.Local
			}
			stack = append(stack, path)
			seen = append(seen, path)
			for _, a := range el.Attr {
				seen = append(seen, path+"@"+a.Name.Local+"="+a.Value)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return seen
}

// probeVeeam: read-only shape discovery. Only when this source's last_error starts with "PROBE".
func probeVeeam(ctx context.Context, s srcRow) (map[string]any, []string, error) {
	cl, err := s.client()
	if err != nil {
		return nil, nil, err
	}
	sid, err := veeamSession(ctx, cl)
	if err != nil {
		return nil, nil, err
	}
	defer veeamLogoff(context.Background(), cl, sid)
	out := map[string]any{}
	for _, p := range []string{
		"/api/backupServers?format=Entity",
		"/api/jobs/states",
		"/api/backupSessions?format=Entity",
		"/api/backupSessions",
	} {
		b, code, err := veeamRaw(ctx, cl, sid, p)
		if err != nil {
			out[p] = "ERR " + err.Error()
			continue
		}
		if code != 200 {
			out[p] = fmt.Sprintf("HTTP %d", code)
			continue
		}
		k := xmlKeys(b, 60)
		if len(k) > 40 {
			k = k[:40]
		}
		out[p] = fmt.Sprintf("%d bytes | %s", len(b), strings.Join(k, ", "))
	}
	return out, []string{"veeam probe"}, nil
}

func testVeeam(ctx context.Context, s srcRow) (map[string]any, error) {
	cl, err := s.client()
	if err != nil {
		return nil, err
	}
	sid, err := veeamSession(ctx, cl)
	if err != nil {
		return nil, err
	}
	defer veeamLogoff(context.Background(), cl, sid)
	var bs struct {
		Refs struct {
			Items []struct {
				Name string `xml:"Name,attr"`
			} `xml:"Ref"`
		} `xml:"Refs"`
	}
	n := -1
	if err := veeamGet(ctx, cl, sid, "/api/backupServers", &bs); err == nil {
		n = len(bs.Refs.Items)
	} else if !strings.Contains(err.Error(), "HTTP") {
		return nil, err
	}
	return map[string]any{"logon": "ok", "apiVersion": "v1_5", "backupServers": n}, nil
}
