package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ── model mapping (consistent with sub2api) ──

var defaultModelMapping = map[string]string{
	"gpt-5.2":              "gpt-5.2",
	"gpt-5.2-mini":         "gpt-5.2-mini",
	"gpt-5.3-codex":        "gpt-5.3-codex",
	"gpt-5.4":              "gpt-5.4",
	"gpt-5.4-2026-03-05":   "gpt-5.4-2026-03-05",
	"gpt-5.4-mini":         "gpt-5.4-mini",
	"gpt-5.5":              "gpt-5.5",
	"gpt-image-1":          "gpt-image-1",
	"gpt-image-1.5":        "gpt-image-1.5",
	"gpt-image-2":          "gpt-image-2",
}

// ── sub2api output types ──

type sub2apiPayload struct {
	ExportedAt string              `json:"exported_at"`
	Proxies    []any               `json:"proxies"`
	Accounts   []sub2apiAccount    `json:"accounts"`
}

type sub2apiAccount struct {
	Name               string              `json:"name"`
	Platform           string              `json:"platform"`
	Type               string              `json:"type"`
	Credentials        sub2apiCredentials   `json:"credentials"`
	Extra              map[string]string    `json:"extra"`
	Concurrency        int                 `json:"concurrency"`
	Priority           int                 `json:"priority"`
	RateMultiplier     int                 `json:"rate_multiplier"`
	AutoPauseOnExpired bool                `json:"auto_pause_on_expired"`
}

type sub2apiCredentials struct {
	AccessToken      string            `json:"access_token"`
	ChatgptAccountID string            `json:"chatgpt_account_id,omitempty"`
	ChatgptUserID    string            `json:"chatgpt_user_id,omitempty"`
	ClientID         string            `json:"client_id"`
	Email            string            `json:"email,omitempty"`
	ExpiresAt        string            `json:"expires_at,omitempty"`
	IDToken          string            `json:"id_token,omitempty"`
	ModelMapping     map[string]string `json:"model_mapping"`
	OrganizationID   string            `json:"organization_id,omitempty"`
	PlanType         string            `json:"plan_type,omitempty"`
	RefreshToken     string            `json:"refresh_token,omitempty"`
}

// ── Shanghai timezone ──

var shanghaiLoc = time.FixedZone("CST", 8*3600)

// ── loadConvertInput reads credentials from file or directory ──

func loadConvertInput(pathArg string) ([]*OAuthKey, error) {
	info, err := os.Stat(pathArg)
	if err != nil {
		return nil, fmt.Errorf("path not found: %s", pathArg)
	}

	if info.IsDir() {
		return loadConvertFromDir(pathArg)
	}

	ext := strings.ToLower(filepath.Ext(pathArg))
	if ext == ".txt" || ext == "" {
		return loadConvertFromLines(pathArg)
	}
	// .json: could be single key or multi-line
	return loadConvertFromJSON(pathArg)
}

func loadConvertFromDir(dir string) ([]*OAuthKey, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// sort for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var keys []*OAuthKey
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		fp := filepath.Join(dir, e.Name())
		key, err := loadKeyFromFile(fp)
		if err != nil {
			warnf("skipping %s: %v", fp, err)
			continue
		}
		if strings.TrimSpace(key.AccessToken) == "" {
			warnf("skipping %s: missing access_token", fp)
			continue
		}
		email := key.Email
		if email == "" {
			email = e.Name()
		}
		infof("  [OK] %s (%s)", e.Name(), email)
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid credential files found in %s", dir)
	}
	return keys, nil
}

func loadConvertFromLines(path string) ([]*OAuthKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var keys []*OAuthKey
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // handle long lines
	idx := 0
	for scanner.Scan() {
		idx++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		key, err := parseOAuthKey(line)
		if err != nil {
			warnf("line %d: %v", idx, err)
			continue
		}
		if strings.TrimSpace(key.AccessToken) == "" {
			warnf("line %d: missing access_token", idx)
			continue
		}
		email := key.Email
		if email == "" {
			email = fmt.Sprintf("line_%d", idx)
		}
		infof("  [OK] line %d (%s)", idx, email)
		keys = append(keys, key)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid records found in %s", path)
	}
	return keys, nil
}

func loadConvertFromJSON(path string) ([]*OAuthKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(data))

	// try single JSON object first
	if strings.HasPrefix(raw, "{") {
		key, err := parseOAuthKey(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		// check if it's already a sub2api payload
		var probe map[string]any
		_ = json.Unmarshal(data, &probe)
		if _, ok := probe["accounts"]; ok {
			return nil, fmt.Errorf("%s is already in sub2api format", path)
		}
		if strings.TrimSpace(key.AccessToken) == "" {
			return nil, fmt.Errorf("%s: missing access_token", path)
		}
		email := key.Email
		if email == "" {
			email = filepath.Base(path)
		}
		infof("  [OK] %s (%s)", filepath.Base(path), email)
		return []*OAuthKey{key}, nil
	}

	// might be multi-line JSONL
	return loadConvertFromLines(path)
}

// ── buildSub2apiAccount converts one OAuthKey → sub2apiAccount ──

func buildSub2apiAccount(key *OAuthKey) (sub2apiAccount, error) {
	accessClaims, ok := decodeJWTClaims(key.AccessToken)
	if !ok {
		return sub2apiAccount{}, fmt.Errorf("failed to decode access_token JWT")
	}

	authClaims := extractAuthClaims(accessClaims)

	var idAuthClaims map[string]any
	if key.IDToken != "" {
		if idClaims, ok := decodeJWTClaims(key.IDToken); ok {
			idAuthClaims = extractAuthClaims(idClaims)
		}
	}
	if idAuthClaims == nil {
		idAuthClaims = map[string]any{}
	}

	// organization_id
	orgID := ""
	if orgs, ok := idAuthClaims["organizations"].([]any); ok && len(orgs) > 0 {
		if firstOrg, ok := orgs[0].(map[string]any); ok {
			if id, ok := firstOrg["id"].(string); ok {
				orgID = id
			}
		}
	}

	// plan_type
	planType := stringFromMaps("chatgpt_plan_type", idAuthClaims, authClaims)
	if planType == "" {
		planType = "plus"
	}

	// expires_at
	expiresAt := ""
	if key.Expired != "" {
		t, err := parseFlexibleTime(key.Expired)
		if err == nil {
			expiresAt = t.In(shanghaiLoc).Truncate(time.Second).Format(time.RFC3339)
		}
	}
	if expiresAt == "" {
		// fallback: use JWT exp claim
		if exp, ok := accessClaims["exp"].(float64); ok && exp > 0 {
			expiresAt = time.Unix(int64(exp), 0).In(shanghaiLoc).Truncate(time.Second).Format(time.RFC3339)
		}
	}

	// email
	email := key.Email
	if email == "" {
		email, _ = authClaims["email"].(string)
	}

	// chatgpt_account_id
	accountID := key.AccountID
	if accountID == "" {
		accountID, _ = authClaims["chatgpt_account_id"].(string)
	}

	// chatgpt_user_id
	userID, _ := authClaims["chatgpt_user_id"].(string)
	if userID == "" {
		userID, _ = authClaims["user_id"].(string)
	}

	cred := sub2apiCredentials{
		AccessToken:      key.AccessToken,
		ChatgptAccountID: accountID,
		ChatgptUserID:    userID,
		ClientID:         codexOAuthClientID,
		Email:            email,
		ExpiresAt:        expiresAt,
		IDToken:          key.IDToken,
		ModelMapping:     defaultModelMapping,
		OrganizationID:   orgID,
		PlanType:         planType,
		RefreshToken:     key.RefreshToken,
	}

	return sub2apiAccount{
		Name:               email,
		Platform:           "openai",
		Type:               "oauth",
		Credentials:        cred,
		Extra:              map[string]string{"privacy_mode": "training_off"},
		Concurrency:        5,
		Priority:           1,
		RateMultiplier:     1,
		AutoPauseOnExpired: true,
	}, nil
}

// ── convertToSub2api converts a list of keys → sub2api payload ──

func convertToSub2api(keys []*OAuthKey) sub2apiPayload {
	var accounts []sub2apiAccount
	for _, key := range keys {
		acc, err := buildSub2apiAccount(key)
		if err != nil {
			email := key.Email
			if email == "" {
				email = key.AccountID
			}
			errorf("  convert failed (%s): %v", email, err)
			continue
		}
		accounts = append(accounts, acc)
	}
	return sub2apiPayload{
		ExportedAt: time.Now().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05.000Z"),
		Proxies:    []any{},
		Accounts:   accounts,
	}
}

// ── convertToCPA converts a list of keys → CPA line format ──

func convertToCPA(keys []*OAuthKey) string {
	var sb strings.Builder
	for _, key := range keys {
		data, err := json.Marshal(key)
		if err != nil {
			errorf("  marshal failed: %v", err)
			continue
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ── runConvert is the entry point for --convert ──

func runConvert(pathArg, format, outputDir string) {
	fmt.Println()
	infof("convert mode: loading credentials from %s", pathArg)

	keys, err := loadConvertInput(pathArg)
	if err != nil {
		fatalf("failed to load credentials: %v", err)
	}

	infof("loaded %d credential(s)", len(keys))

	if format == "" {
		format = promptFormat()
	}

	if outputDir == "" {
		// default: <executable-dir>/output
		exePath, _ := os.Executable()
		if exePath != "" {
			outputDir = filepath.Join(filepath.Dir(exePath), "output")
		} else {
			outputDir = "output"
		}
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fatalf("failed to create output directory: %v", err)
	}

	timestamp := time.Now().Format("20060102-150405")

	switch format {
	case "sub2api":
		payload := convertToSub2api(keys)
		if len(payload.Accounts) == 0 {
			fatalf("no records converted successfully")
		}
		outFile := filepath.Join(outputDir, fmt.Sprintf("sub2api-%d-%s.json", len(payload.Accounts), timestamp))
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fatalf("marshal error: %v", err)
		}
		if err := os.WriteFile(outFile, data, 0644); err != nil {
			fatalf("write error: %v", err)
		}
		printConvertSummary(format, len(payload.Accounts), outFile)

	case "cpa":
		content := convertToCPA(keys)
		outFile := filepath.Join(outputDir, fmt.Sprintf("cpa-%d-%s.txt", len(keys), timestamp))
		if err := os.WriteFile(outFile, []byte(content), 0644); err != nil {
			fatalf("write error: %v", err)
		}
		printConvertSummary(format, len(keys), outFile)

	default:
		fatalf("unknown format: %s (expected: sub2api or cpa)", format)
	}
}

func printConvertSummary(format string, count int, outFile string) {
	fmt.Println()
	fmt.Println(colorBold("═══════════════════════ CONVERT ═══════════════════════"))
	fmt.Printf("  %s convert completed!\n", colorGreen("✓"))
	fmt.Printf("  format  : %s\n", colorGreen(format))
	fmt.Printf("  count   : %s credential(s)\n", colorBold(fmt.Sprintf("%d", count)))
	fmt.Printf("  output  : %s\n", colorCyan(outFile))
	fmt.Println(colorBold("═══════════════════════════════════════════════════════"))
	fmt.Println()
}

func promptFormat() string {
	fmt.Println()
	fmt.Println(colorBold("Select output format:"))
	fmt.Printf("  %s sub2api  — sub2api import JSON (model_mapping, concurrency, etc.)\n", colorCyan("[1]"))
	fmt.Printf("  %s cpa      — JSONL archive format (one JSON per line)\n", colorCyan("[2]"))
	fmt.Println()
	for {
		fmt.Print("Choose (1/2): ")
		var input string
		fmt.Scanln(&input)
		input = strings.TrimSpace(input)
		switch input {
		case "1", "sub2api":
			return "sub2api"
		case "2", "cpa":
			return "cpa"
		default:
			fmt.Println(colorRed("  invalid choice, enter 1 or 2"))
		}
	}
}

// ── helpers ──

func extractAuthClaims(claims map[string]any) map[string]any {
	raw, ok := claims[codexJWTClaimPath]
	if !ok {
		return map[string]any{}
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return obj
}

func stringFromMaps(key string, maps ...map[string]any) string {
	for _, m := range maps {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func parseFlexibleTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try with Z → +00:00
	normalized := strings.Replace(s, "Z", "+00:00", 1)
	if t, err := time.Parse(time.RFC3339, normalized); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %q", s)
}
