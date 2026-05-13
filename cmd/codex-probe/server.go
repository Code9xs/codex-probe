package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var webFS embed.FS

// ── in-memory credential store ──

type keyStore struct {
	mu   sync.RWMutex
	keys []*OAuthKey
}

func (s *keyStore) list() []*OAuthKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*OAuthKey, len(s.keys))
	copy(out, s.keys)
	return out
}

func (s *keyStore) get(idx int) (*OAuthKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if idx < 0 || idx >= len(s.keys) {
		return nil, false
	}
	return s.keys[idx], true
}

func (s *keyStore) add(keys ...*OAuthKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for _, k := range keys {
		if k == nil || strings.TrimSpace(k.AccessToken) == "" {
			continue
		}
		// dedup by email+account_id
		dup := false
		for _, existing := range s.keys {
			if existing.Email == k.Email && existing.AccountID == k.AccountID {
				dup = true
				break
			}
		}
		if !dup {
			s.keys = append(s.keys, k)
			added++
		}
	}
	return added
}

func (s *keyStore) remove(idx int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 || idx >= len(s.keys) {
		return false
	}
	s.keys = append(s.keys[:idx], s.keys[idx+1:]...)
	return true
}

func (s *keyStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = nil
}

func (s *keyStore) byIndices(indices []int) []*OAuthKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*OAuthKey
	for _, i := range indices {
		if i >= 0 && i < len(s.keys) {
			out = append(out, s.keys[i])
		}
	}
	return out
}

// ── server ──

type probeServer struct {
	store   *keyStore
	client  *http.Client
	cfg     ProbeConfig
	port    int
	saveDir string

	// pending OAuth login flow (at most one at a time)
	loginMu   sync.Mutex
	loginFlow *oauthFlow

	// credential status cache: key = "email|account_id", value = "ok" | "invalid" | "error"
	statusMu  sync.RWMutex
	statusMap map[string]string
}

func runServe(httpClient *http.Client, probeCfg ProbeConfig, port int, pathArg string) {
	store := &keyStore{}

	// determine save directory — either the user-provided path or ./tokens/
	saveDir := pathArg
	if saveDir == "" {
		saveDir = "./tokens"
	}
	fi, err := os.Stat(saveDir)
	if err == nil && !fi.IsDir() {
		// pathArg is a file, use its parent dir
		saveDir = filepath.Dir(saveDir)
	}
	_ = os.MkdirAll(saveDir, 0755)

	// pre-load credentials if path given
	if pathArg != "" {
		loaded, err := loadConvertInput(pathArg)
		if err != nil {
			warnf("pre-load: %v", err)
		} else {
			store.add(loaded...)
			infof("pre-loaded %d credential(s)", len(loaded))
		}
	}

	srv := &probeServer{store: store, client: httpClient, cfg: probeCfg, saveDir: saveDir, statusMap: make(map[string]string)}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/api/keys", srv.handleKeys)
	mux.HandleFunc("/api/keys/upload", srv.handleUpload)
	mux.HandleFunc("/api/keys/batch-check", srv.handleBatchCheck)
	mux.HandleFunc("/api/keys/", srv.handleKeyByIndex) // /api/keys/{idx}[/status|/renew]
	mux.HandleFunc("/api/convert", srv.handleConvert)
	mux.HandleFunc("/api/health", srv.handleHealth)
	mux.HandleFunc("/api/login", srv.handleLoginStart)

	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fatalf("failed to listen on %s: %v", addr, err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port
	srv.port = actualPort

	fmt.Println()
	fmt.Println(colorBold("═══════════════════ WEB SERVER ═══════════════════"))
	fmt.Printf("  %s server started\n", colorGreen("✓"))
	fmt.Printf("  address : %s\n", colorCyan(fmt.Sprintf("http://localhost:%d", actualPort)))
	fmt.Printf("  API     : %s\n", colorCyan(fmt.Sprintf("http://localhost:%d/api/", actualPort)))
	fmt.Printf("  login   : %s\n", colorCyan(fmt.Sprintf("http://localhost:%d/api/login", actualPort)))
	fmt.Printf("  save_dir: %s\n", colorCyan(saveDir))
	fmt.Printf("  loaded  : %s credential(s)\n", colorBold(fmt.Sprintf("%d", len(store.list()))))
	fmt.Println(colorBold("═══════════════════════════════════════════════════"))
	fmt.Println()
	infof("press Ctrl+C to stop")

	server := &http.Server{Handler: corsMiddleware(mux)}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fatalf("server error: %v", err)
	}
}

// ── middleware ──

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── handlers ──

func (s *probeServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *probeServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "keys": len(s.store.list())})
}

func (s *probeServer) statusKey(k *OAuthKey) string {
	return k.Email + "|" + k.AccountID
}

func (s *probeServer) getStatus(k *OAuthKey) string {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.statusMap[s.statusKey(k)]
}

func (s *probeServer) setStatus(k *OAuthKey, status string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.statusMap[s.statusKey(k)] = status
}

func (s *probeServer) handleKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		keys := s.store.list()
		// return simplified view with plan_type extracted from JWT + status
		type keyView struct {
			Email     string `json:"email"`
			AccountID string `json:"account_id"`
			PlanType  string `json:"plan_type"`
			Expired   string `json:"expired"`
			Type      string `json:"type"`
			Status    string `json:"status"` // "ok", "invalid", "error", "" (unknown)
		}
		views := make([]keyView, len(keys))
		for i, k := range keys {
			plan := ""
			if k.IDToken != "" {
				if claims, ok := decodeJWTClaims(k.IDToken); ok {
					ac := extractAuthClaims(claims)
					plan, _ = ac["chatgpt_plan_type"].(string)
				}
			}
			if plan == "" {
				if claims, ok := decodeJWTClaims(k.AccessToken); ok {
					ac := extractAuthClaims(claims)
					plan, _ = ac["chatgpt_plan_type"].(string)
				}
			}
			if plan == "" {
				plan = "plus"
			}
			views[i] = keyView{
				Email:     k.Email,
				AccountID: k.AccountID,
				PlanType:  plan,
				Expired:   k.Expired,
				Type:      k.Type,
				Status:    s.getStatus(k),
			}
		}
		writeJSON(w, 200, views)

	case http.MethodDelete:
		s.store.clear()
		s.statusMu.Lock()
		s.statusMap = make(map[string]string)
		s.statusMu.Unlock()
		writeJSON(w, 200, map[string]any{"status": "cleared"})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleBatchCheck checks the status of all credentials concurrently.
func (s *probeServer) handleBatchCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	keys := s.store.list()
	if len(keys) == 0 {
		writeJSON(w, 200, map[string]any{"checked": 0, "ok": 0, "invalid": 0})
		return
	}

	infof("[batch-check] checking %d credential(s)...", len(keys))

	type checkResult struct {
		idx    int
		key    *OAuthKey
		status string // "ok" | "invalid" | "error"
	}

	results := make([]checkResult, len(keys))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // limit concurrency to 5

	for i, k := range keys {
		wg.Add(1)
		go func(idx int, key *OAuthKey) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			entry := keyEntry{key: key, path: "memory"}
			result := fetchUsage(ctx, s.client, entry, s.cfg)

			status := "ok"
			if result.UpstreamStatus == 401 || result.UpstreamStatus == 403 {
				status = "invalid"
			} else if result.Err != nil || (!result.Allowed && result.UpstreamStatus != 200 && result.UpstreamStatus != 429) {
				if result.UpstreamStatus >= 400 {
					status = "invalid"
				} else if result.Err != nil {
					status = "error"
				}
			}

			results[idx] = checkResult{idx: idx, key: key, status: status}
		}(i, k)
	}
	wg.Wait()

	okCount, invalidCount, errCount := 0, 0, 0
	for _, r := range results {
		s.setStatus(r.key, r.status)
		switch r.status {
		case "ok":
			okCount++
		case "invalid":
			invalidCount++
		default:
			errCount++
		}
	}

	infof("[batch-check] done: ok=%d invalid=%d error=%d", okCount, invalidCount, errCount)
	writeJSON(w, 200, map[string]any{
		"checked": len(keys),
		"ok":      okCount,
		"invalid": invalidCount,
		"error":   errCount,
	})
}

func (s *probeServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "parse form: "+err.Error(), 400)
		return
	}

	var allKeys []*OAuthKey
	for _, fh := range r.MultipartForm.File["files"] {
		parsed, err := parseUploadedFile(fh)
		if err != nil {
			warnf("upload %s: %v", fh.Filename, err)
			continue
		}
		allKeys = append(allKeys, parsed...)
	}

	// save each key to disk as codex JSON
	for _, k := range allKeys {
		outPath := filepath.Join(s.saveDir, buildKeyFileName(k))
		if err := saveKeyToFile(outPath, k); err != nil {
			warnf("upload save to disk: %v", err)
		} else {
			infof("upload saved: %s", outPath)
		}
	}

	added := s.store.add(allKeys...)
	writeJSON(w, 200, map[string]any{"added": added, "total": len(s.store.list())})
}

func parseUploadedFile(fh *multipart.FileHeader) ([]*OAuthKey, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(string(data))

	ext := strings.ToLower(filepath.Ext(fh.Filename))

	// JSON object — could be single codex key or sub2api payload
	if ext == ".json" && strings.HasPrefix(content, "{") {
		// probe for sub2api format (has "accounts" array)
		var probe map[string]json.RawMessage
		if json.Unmarshal(data, &probe) == nil {
			if _, ok := probe["accounts"]; ok {
				return parseSub2apiUpload(data)
			}
		}
		// single codex key
		key, err := parseOAuthKey(content)
		if err != nil {
			return nil, err
		}
		enrichOAuthKey(key)
		return []*OAuthKey{key}, nil
	}

	// line-delimited (txt or multi-line json) — CPA format
	var keys []*OAuthKey
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		key, err := parseOAuthKey(line)
		if err != nil {
			continue
		}
		enrichOAuthKey(key)
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid credentials found in %s", fh.Filename)
	}
	return keys, nil
}

// parseSub2apiUpload parses a sub2api JSON payload and extracts OAuthKey from each account.
func parseSub2apiUpload(data []byte) ([]*OAuthKey, error) {
	var payload sub2apiPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse sub2api: %w", err)
	}
	if len(payload.Accounts) == 0 {
		return nil, fmt.Errorf("sub2api payload has no accounts")
	}
	var keys []*OAuthKey
	for _, acc := range payload.Accounts {
		cred := acc.Credentials
		if strings.TrimSpace(cred.AccessToken) == "" {
			continue
		}
		key := &OAuthKey{
			AccessToken:  cred.AccessToken,
			RefreshToken: cred.RefreshToken,
			IDToken:      cred.IDToken,
			AccountID:    cred.ChatgptAccountID,
			Email:        cred.Email,
			Expired:      cred.ExpiresAt,
			Type:         "codex",
		}
		enrichOAuthKey(key)
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid credentials in sub2api accounts")
	}
	return keys, nil
}

func (s *probeServer) handleKeyByIndex(w http.ResponseWriter, r *http.Request) {
	// parse /api/keys/{idx}[/action]
	path := strings.TrimPrefix(r.URL.Path, "/api/keys/")
	parts := strings.SplitN(path, "/", 2)
	idx, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "invalid index", 400)
		return
	}

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		// get detail
		key, ok := s.store.get(idx)
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		writeJSON(w, 200, key)

	case action == "" && r.Method == http.MethodDelete:
		if !s.store.remove(idx) {
			http.Error(w, "not found", 404)
			return
		}
		writeJSON(w, 200, map[string]any{"status": "deleted"})

	case action == "status":
		key, ok := s.store.get(idx)
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		entry := keyEntry{key: key, path: "memory"}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		result := fetchUsage(ctx, s.client, entry, s.cfg)
		writeJSON(w, 200, map[string]any{
			"email":           result.Email,
			"account_id":      result.AccountID,
			"plan_type":       result.PlanType,
			"allowed":         result.Allowed,
			"limit_reached":   result.LimitReached,
			"upstream_status": result.UpstreamStatus,
			"five_hour":       result.FiveHour,
			"weekly":          result.Weekly,
			"error":           errStr(result.Err),
		})

	case action == "renew" && r.Method == http.MethodPost:
		key, ok := s.store.get(idx)
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		if strings.TrimSpace(key.RefreshToken) == "" {
			http.Error(w, "no refresh_token", 400)
			return
		}
		entry := keyEntry{key: key, path: "memory"}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		_, renewErr := renewKeyEntryWithRetry(ctx, s.client, entry, codexOAuthTokenURL, 3)
		if renewErr != nil {
			http.Error(w, "renew failed: "+renewErr.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]any{"status": "renewed", "email": key.Email, "expired": key.Expired})

	default:
		http.Error(w, "not found", 404)
	}
}

func (s *probeServer) handleConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		Format  string `json:"format"`
		Indices []int  `json:"indices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", 400)
		return
	}

	if req.Format == "" {
		req.Format = "sub2api"
	}

	selected := s.store.byIndices(req.Indices)
	if len(selected) == 0 {
		http.Error(w, "no credentials selected", 400)
		return
	}

	switch req.Format {
	case "sub2api":
		payload := convertToSub2api(selected)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=sub2api-%d.json", len(payload.Accounts)))
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(payload)

	case "cpa":
		content := convertToCPA(selected)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=cpa-%d.txt", len(selected)))
		w.Write([]byte(content))

	default:
		http.Error(w, "unknown format: "+req.Format, 400)
	}
}

// ── utils ──

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// enrichOAuthKey fills in email/account_id/expired from JWT if missing.
func enrichOAuthKey(key *OAuthKey) {
	if key.Email == "" {
		key.Email = tokenEmail(key.IDToken, key.AccessToken)
	}
	if key.AccountID == "" {
		if id, ok := tokenAccountID(key.IDToken, key.AccessToken); ok {
			key.AccountID = id
		}
	}
	if key.Expired == "" {
		if claims, ok := decodeJWTClaims(key.AccessToken); ok {
			if exp, ok := claims["exp"].(float64); ok && exp > 0 {
				key.Expired = time.Unix(int64(exp), 0).Format(time.RFC3339)
			}
		}
	}
}

// ── OAuth login via web ──

func (s *probeServer) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	// use the standard registered redirect_uri (port 1455)
	flow, err := createOAuthFlow()
	if err != nil {
		http.Error(w, "failed to create OAuth flow: "+err.Error(), 500)
		return
	}

	s.loginMu.Lock()
	s.loginFlow = flow
	s.loginMu.Unlock()

	infof("[web-login] OAuth flow started, starting callback listener on :1455 ...")

	// start a temporary listener on :1455 for the OAuth callback
	go s.startCallbackListener()

	// redirect browser to OpenAI authorization page (uses redirect_uri=localhost:1455)
	http.Redirect(w, r, flow.AuthURL, http.StatusTemporaryRedirect)
}

// startCallbackListener starts a temporary HTTP server on :1455 to receive the OAuth
// callback from OpenAI, then redirects the browser back to the web dashboard.
func (s *probeServer) startCallbackListener() {
	mux := http.NewServeMux()
	srv := &http.Server{Addr: ":1455", Handler: mux}

	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			// shut down after handling callback
			go func() {
				time.Sleep(500 * time.Millisecond)
				shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutCtx)
			}()
		}()

		dashboardBase := fmt.Sprintf("http://localhost:%d", s.port)

		q := r.URL.Query()
		code := strings.TrimSpace(q.Get("code"))
		state := strings.TrimSpace(q.Get("state"))

		if code == "" {
			errMsg := q.Get("error_description")
			if errMsg == "" {
				errMsg = q.Get("error")
			}
			if errMsg == "" {
				errMsg = "missing code in callback"
			}
			infof("[web-login] callback error: %s", errMsg)
			http.Redirect(w, r, dashboardBase+"/?login=error&msg="+url.QueryEscape(errMsg), http.StatusTemporaryRedirect)
			return
		}

		s.loginMu.Lock()
		flow := s.loginFlow
		s.loginFlow = nil
		s.loginMu.Unlock()

		if flow == nil {
			http.Redirect(w, r, dashboardBase+"/?login=error&msg="+url.QueryEscape("no pending login flow"), http.StatusTemporaryRedirect)
			return
		}
		if state != flow.State {
			http.Redirect(w, r, dashboardBase+"/?login=error&msg="+url.QueryEscape("state mismatch"), http.StatusTemporaryRedirect)
			return
		}

		infof("[web-login] authorization code received, exchanging for tokens...")

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// exchange using the registered redirect_uri (port 1455)
		tr, err := exchangeAuthCode(ctx, s.client, code, flow.Verifier)
		if err != nil {
			infof("[web-login] token exchange failed: %v", err)
			http.Redirect(w, r, dashboardBase+"/?login=error&msg="+url.QueryEscape("token exchange: "+err.Error()), http.StatusTemporaryRedirect)
			return
		}

		accountID, ok := tokenAccountID(tr.IDToken, tr.AccessToken)
		if !ok {
			http.Redirect(w, r, dashboardBase+"/?login=error&msg="+url.QueryEscape("could not extract account_id"), http.StatusTemporaryRedirect)
			return
		}

		key := &OAuthKey{
			IDToken:      tr.IDToken,
			AccessToken:  tr.AccessToken,
			RefreshToken: tr.RefreshToken,
			AccountID:    accountID,
			Email:        tr.Email,
			LastRefresh:  time.Now().Format(time.RFC3339),
			Expired:      tr.ExpiresAt.Format(time.RFC3339),
			Type:         "codex",
		}

		// save to disk
		outPath := filepath.Join(s.saveDir, buildKeyFileName(key))
		if err := saveKeyToFile(outPath, key); err != nil {
			warnf("[web-login] save to disk failed: %v", err)
		} else {
			infof("[web-login] credential saved: %s", outPath)
		}

		// add to in-memory store
		s.store.add(key)

		infof("[web-login] ✓ login successful: %s (%s)", tr.Email, accountID)

		// redirect browser back to dashboard with success flag
		http.Redirect(w, r, dashboardBase+"/?login=success&email="+url.QueryEscape(tr.Email), http.StatusTemporaryRedirect)
	})

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		warnf("[web-login] callback listener failed: %v (is port 1455 already in use?)", err)
	}
}

