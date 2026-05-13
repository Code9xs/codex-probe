package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const logo = `
  ██████╗ ██████╗ ██████╗ ███████╗██╗  ██╗      ██████╗ ██████╗  ██████╗ ██████╗ ███████╗
 ██╔════╝██╔═══██╗██╔══██╗██╔════╝╚██╗██╔╝      ██╔══██╗██╔══██╗██╔═══██╗██╔══██╗██╔════╝
 ██║     ██║   ██║██║  ██║█████╗   ╚███╔╝ █████╗██████╔╝██████╔╝██║   ██║██████╔╝█████╗
 ██║     ██║   ██║██║  ██║██╔══╝   ██╔██╗ ╚════╝██╔═══╝ ██╔══██╗██║   ██║██╔══██╗██╔══╝
 ╚██████╗╚██████╔╝██████╔╝███████╗██╔╝ ██╗      ██║     ██║  ██║╚██████╔╝██████╔╝███████╗
  ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝╚═╝  ╚═╝      ╚═╝     ╚═╝  ╚═╝ ╚═════╝ ╚═════╝ ╚══════╝
`

const helpText = `Usage:
  codex-probe [options] <file-or-dir>

Last positional argument (required):
  <file>   A single credential JSON file
             format: {"access_token":"...","account_id":"..."}
  <dir>    Directory — processes all *.json credential files inside

Options:
  --login          OAuth PKCE login, listen on :1455, write credential JSON
  -o       <path>  Output file or directory for --login (required with --login)
  --config <path>  Config file path; default is config.json next to the executable
  --renew          Refresh credential(s) with refresh_token and write back JSON by policy
  -f               Force refresh when used with --renew
  --sync           Synchronize local token files with the configured remote store
  --status         Query remaining quota (5h window + weekly window)
  --apitest        Test availability of every model endpoint
  --convert        Convert credentials to sub2api or CPA format
  --format <fmt>   Output format for --convert: sub2api (default) or cpa
  --serve          Start web dashboard and API server
  --port   <num>   Port for --serve (default: 18152)
  --output <path>  Write --status / --apitest results to a CSV file (must end in .csv)
                   For --convert, specifies the output directory
  --proxy  <url>   Proxy URL (e.g. http://127.0.0.1:7890 or socks5://...)
                   Pass "" to force direct connection (skip auto-detection)
                   Omit flag to auto-detect system proxy (env / registry / scutil)
  --help           Show this help

Examples:
  codex-probe --login -o ./keys/my.json
  codex-probe --login -o ./keys/
  codex-probe --sync
  codex-probe --renew ./keys/my.json
  codex-probe --status ./keys/my.json
  codex-probe --apitest --output apitest.csv ./keys/
  codex-probe --convert --format sub2api ./keys/
  codex-probe --convert --format cpa ./keys/me.json
  codex-probe --convert ./cpa.txt
  codex-probe --serve --port 8080 ./keys/
  codex-probe --proxy http://127.0.0.1:7890 --status ./keys/my.json
  codex-probe --proxy "" --status ./keys/my.json
`

type config struct {
	doLogin      bool
	doRenew      bool
	doSync       bool
	doStatus     bool
	doAPITest    bool
	doConvert    bool
	doServe      bool
	forceRenew   bool
	loginOutPath string
	output       string
	configPath   string
	convertFmt   string
	servePort    int
	// proxySet=false           → auto-detect system proxy
	// proxySet=true, proxy=""  → force direct (no proxy)
	// proxySet=true, proxy!="" → use this URL
	proxy    string
	proxySet bool
	pathArg  string
}

func main() {
	printLogo()
	cfg := parseArgs(os.Args[1:])
	if err := validateCommandSelection(cfg); err != nil {
		fatalf("%v", err)
	}

	// -- proxy setup --
	proxyURL := resolveProxy(cfg.proxySet, cfg.proxy)
	client, err := buildHTTPClient(proxyURL)
	if err != nil {
		fatalf("failed to initialize HTTP client: %v", err)
	}

	if !hasCommand(cfg) {
		fmt.Print(helpText)
		os.Exit(0)
	}

	exePath, err := os.Executable()
	if err != nil {
		fatalf("failed to determine executable path: %v", err)
	}
	probeCfg, configPath, err := loadProbeConfig(cfg.configPath, exePath)
	if err != nil {
		fatalf("failed to load config: %v", err)
	}
	infof("config: %s", configPath)

	if cfg.output != "" && !strings.HasSuffix(strings.ToLower(cfg.output), ".csv") {
		fatalf("--output path must end with .csv, got: %s", cfg.output)
	}

	if cfg.doLogin && strings.TrimSpace(cfg.loginOutPath) == "" {
		fatalf("--login requires -o <path> to specify the output file or directory")
	}

	loginPathArg := cfg.pathArg
	if cfg.doLogin {
		loginPathArg = strings.TrimSpace(cfg.loginOutPath)
	}
	if requiresPathArg(cfg) && loginPathArg == "" {
		fatalf("missing file/directory argument — run with --help for usage")
	}

	// Region check only when a real command will run (not on bare help).
	checkNotChina(client)

	if cfg.doLogin {
		runLogin(client, cfg, loginPathArg, proxyURL)
		return
	}
	if cfg.doSync {
		fmt.Println()
		infof("sync remote: %s", probeCfg.SyncURL)
		infof("sync dir: %s", probeCfg.SyncDir)
		result, err := runSync(context.Background(), client, probeCfg)
		if err != nil {
			fatalf("sync failed: %v", err)
		}
		printSyncSummary(result)
		return
	}
	if cfg.doConvert {
		runConvert(cfg.pathArg, cfg.convertFmt, cfg.output)
		return
	}
	if cfg.doServe {
		port := cfg.servePort
		if port <= 0 {
			port = 18152
		}
		runServe(client, probeCfg, port, cfg.pathArg)
		return
	}

	entries, err := loadKeysFromPath(cfg.pathArg)
	if err != nil {
		fatalf("failed to load credentials: %v", err)
	}
	infof("loaded %d credential file(s)", len(entries))

	var usageRows []UsageResult
	var apitestSummaries []APITestTokenSummary
	var renewRows []RenewResult

	for _, entry := range entries {
		fmt.Println()
		infof("processing: %s  (account_id: %s)", entry.path, entry.key.AccountID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		if cfg.doRenew {
			fmt.Println(colorCyan("  [renew]"))
			needRenew, reason := shouldRenewKey(entry.key, probeCfg, cfg.forceRenew, time.Now())
			if !needRenew {
				infof("  skipped: policy not matched (use -f to force refresh)")
				renewRows = append(renewRows, RenewResult{
					File:      entry.path,
					AccountID: entry.key.AccountID,
					Skipped:   true,
					Reason:    "policy not matched (use -f to force refresh)",
				})
			} else if renewedKey, err := renewKeyEntryWithRetry(ctx, client, entry, codexOAuthTokenURL, defaultRenewRetryMax); err != nil {
				errorf("  renew failed: %v", err)
				renewRows = append(renewRows, RenewResult{
					File:      entry.path,
					AccountID: entry.key.AccountID,
					Reason:    reason,
					Err:       err,
				})
			} else {
				entry.key = renewedKey
				infof("  credential renewed; expires at %s", renewedKey.Expired)
				renewRows = append(renewRows, RenewResult{
					File:      entry.path,
					AccountID: entry.key.AccountID,
					Reason:    reason,
				})
			}
		}

		if cfg.doStatus {
			fmt.Println(colorCyan("  [quota]"))
			res := fetchUsage(ctx, client, entry, probeCfg)
			printUsageResult(res)
			usageRows = append(usageRows, res)
		}

		if cfg.doAPITest {
			fmt.Println(colorCyan("  [model test]"))
			results := testAllModels(ctx, client, entry)
			printModelTestResults(results)
			apitestSummaries = append(apitestSummaries, summarizeAPITestForCSV(results))
		}

		cancel()
	}

	// ---------- terminal summary ----------
	fmt.Println()
	fmt.Println(colorBold("══════════════════════ SUMMARY ══════════════════════"))
	if cfg.doRenew {
		writeRenewSummary(os.Stdout, renewRows)
	}
	if cfg.doRenew && shouldPromptForSyncAfterRenew(probeCfg, renewRows) {
		fmt.Println()
		if promptForSync(os.Stdin, os.Stdout) {
			syncResult, err := runSync(context.Background(), client, probeCfg)
			if err != nil {
				fatalf("sync failed: %v", err)
			}
			printSyncSummary(syncResult)
		}
	}
	if cfg.doStatus {
		fmt.Println(colorCyan("  [quota summary]"))
		fmt.Printf("  %-40s  %-8s  %s\n", "file", "HTTP", "status")
		fmt.Printf("  %s\n", strings.Repeat("-", 65))
		for _, r := range usageRows {
			label := r.File
			if r.AccountID != "" {
				label = r.AccountID
			}
			if r.Err != nil {
				fmt.Printf("  %-40s  %-8s  %s\n", label, "-", colorRed("ERROR: "+r.Err.Error()))
			} else if !r.Allowed || r.LimitReached {
				fmt.Printf("  %-40s  %-8d  %s\n", label, r.UpstreamStatus, colorRed("✗ limited"))
			} else {
				fmt.Printf("  %-40s  %-8d  %s\n", label, r.UpstreamStatus, colorGreen("✓ available"))
			}
		}
		ok, fail := 0, 0
		for _, r := range usageRows {
			if r.Err == nil && r.Allowed && !r.LimitReached {
				ok++
			} else {
				fail++
			}
		}
		fmt.Printf("  total: %d  ok: %s  fail: %s\n",
			len(usageRows), colorGreen(fmt.Sprintf("%d", ok)), colorRed(fmt.Sprintf("%d", fail)))
	}
	if cfg.doAPITest {
		fmt.Println(colorCyan("  [apitest summary]"))
		fmt.Printf("  %-40s  %s\n", "file", "available")
		fmt.Printf("  %s\n", strings.Repeat("-", 55))
		for _, s := range apitestSummaries {
			label := s.File
			if s.AccountID != "" {
				label = s.AccountID
			}
			if s.Available {
				fmt.Printf("  %-40s  %s\n", label, colorGreen("✓ yes"))
			} else {
				fmt.Printf("  %-40s  %s\n", label, colorRed("✗ no"))
			}
		}
		ok, fail := 0, 0
		for _, s := range apitestSummaries {
			if s.Available {
				ok++
			} else {
				fail++
			}
		}
		fmt.Printf("  total: %d  ok: %s  fail: %s\n",
			len(apitestSummaries), colorGreen(fmt.Sprintf("%d", ok)), colorRed(fmt.Sprintf("%d", fail)))
	}
	fmt.Println(colorBold("═════════════════════════════════════════════════════"))
	fmt.Println()

	if cfg.output != "" {
		if cfg.doStatus && len(usageRows) > 0 {
			outPath := cfg.output
			if cfg.doAPITest {
				outPath = insertSuffix(cfg.output, "_status")
			}
			if err := writeUsageCSV(outPath, usageRows); err != nil {
				errorf("failed to write usage CSV: %v", err)
			} else {
				infof("usage data written to: %s", outPath)
			}
		}
		if cfg.doAPITest && len(apitestSummaries) > 0 {
			outPath := cfg.output
			if cfg.doStatus {
				outPath = insertSuffix(cfg.output, "_apitest")
			}
			if err := writeAPITestCSV(outPath, apitestSummaries); err != nil {
				errorf("failed to write apitest CSV: %v", err)
			} else {
				infof("apitest data written to: %s", outPath)
			}
		}
	}
}

func parseArgs(args []string) config {
	cfg := config{}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--help", "-h":
			fmt.Print(helpText)
			os.Exit(0)
		case "--login":
			cfg.doLogin = true
		case "--renew":
			cfg.doRenew = true
		case "--sync":
			cfg.doSync = true
		case "-f":
			cfg.forceRenew = true
		case "--status":
			cfg.doStatus = true
		case "--apitest", "--test":
			cfg.doAPITest = true
		case "--convert":
			cfg.doConvert = true
		case "--format":
			i++
			if i >= len(args) {
				fatalf("--format requires an argument (sub2api or cpa)")
			}
			cfg.convertFmt = args[i]
		case "--serve":
			cfg.doServe = true
		case "--port":
			i++
			if i >= len(args) {
				fatalf("--port requires a number")
			}
			p, err := strconv.Atoi(args[i])
			if err != nil {
				fatalf("--port: invalid number %q", args[i])
			}
			cfg.servePort = p
		case "-o":
			i++
			if i >= len(args) {
				fatalf("-o requires a path argument")
			}
			cfg.loginOutPath = args[i]
		case "--output":
			i++
			if i >= len(args) {
				fatalf("--output requires a path argument")
			}
			cfg.output = args[i]
		case "--config":
			i++
			if i >= len(args) {
				fatalf("--config requires a path argument")
			}
			cfg.configPath = args[i]
		case "--proxy":
			i++
			if i >= len(args) {
				fatalf("--proxy requires a URL argument (or \"\" to force direct connection)")
			}
			cfg.proxy = args[i]
			cfg.proxySet = true
		default:
			cfg.pathArg = args[i]
		}
		i++
	}
	return cfg
}

func hasCommand(cfg config) bool {
	return cfg.doLogin || cfg.doRenew || cfg.doSync || cfg.doStatus || cfg.doAPITest || cfg.doConvert || cfg.doServe
}

func validateCommandSelection(cfg config) error {
	if cfg.doSync && (cfg.doLogin || cfg.doRenew || cfg.doStatus || cfg.doAPITest || cfg.doConvert || cfg.doServe) {
		return fmt.Errorf("--sync cannot be combined with other commands; run it as a standalone sync command")
	}
	if cfg.doConvert && (cfg.doLogin || cfg.doRenew || cfg.doStatus || cfg.doAPITest || cfg.doSync || cfg.doServe) {
		return fmt.Errorf("--convert cannot be combined with other commands; run it as a standalone convert command")
	}
	if cfg.doServe && (cfg.doLogin || cfg.doRenew || cfg.doStatus || cfg.doAPITest || cfg.doSync || cfg.doConvert) {
		return fmt.Errorf("--serve cannot be combined with other commands; run it as a standalone serve command")
	}
	return nil
}

func requiresPathArg(cfg config) bool {
	return !cfg.doLogin && !cfg.doSync && !cfg.doServe
}


// resolveProxy determines the effective proxy URL.
func resolveProxy(proxySet bool, proxyURL string) string {
	if proxySet {
		if strings.TrimSpace(proxyURL) == "" {
			infof("proxy: direct connection (--proxy \"\" explicitly set)")
			return ""
		}
		infof("proxy: using %s", colorGreen(proxyURL))
		return strings.TrimSpace(proxyURL)
	}

	detected, err := detectSystemProxy()
	if err != nil {
		warnf("system proxy detection error: %v", err)
	}
	if detected != "" {
		infof("proxy: system proxy detected %s", colorGreen(detected))
		return detected
	}

	infof("proxy: none detected, connecting directly")
	return ""
}

func runLogin(client *http.Client, cfg config, loginPathArg string, proxyURL string) {
	_ = proxyURL

	outDir, outFileHint, isDirMode, err := prepareLoginOutputPath(loginPathArg)
	if err != nil {
		fatalf("invalid output path: %v", err)
	}
	if isDirMode {
		infof("output directory: %s (filename will be auto-generated after login)", outDir)
	} else {
		infof("output file: %s", outFileHint)
	}

	flow, err := createOAuthFlow()
	if err != nil {
		fatalf("failed to create OAuth flow: %v", err)
	}

	fmt.Println()
	infof("authorization URL ready, opening browser...")
	fmt.Printf("\n  %s\n\n", colorCyan(flow.AuthURL))

	openBrowser(flow.AuthURL)

	infof("waiting for browser callback on localhost:1455 (timeout: 30 min)...")
	infof("after login the browser redirects to localhost:1455 — the page can be closed once it says success.")

	ctx, cancel := context.WithTimeout(context.Background(), oauthCallbackTimeout)
	defer cancel()

	code, state, err := waitForCallback(ctx, flow.State)
	if err != nil {
		fatalf("callback error: %v", err)
	}

	if state != flow.State {
		fatalf("state mismatch — possible CSRF attack (expected=%s got=%s)", flow.State, state)
	}

	infof("authorization code received, exchanging for tokens...")
	exchangeCtx, exchangeCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer exchangeCancel()

	tr, err := exchangeAuthCode(exchangeCtx, client, code, flow.Verifier)
	if err != nil {
		fatalf("token exchange failed: %v", err)
	}

	accountID, ok := tokenAccountID(tr.IDToken, tr.AccessToken)
	if !ok {
		fatalf("could not extract account_id from id_token or access_token")
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

	var outPath string
	if isDirMode {
		outPath = filepath.Join(outDir, buildKeyFileName(key))
	} else {
		outPath = outFileHint
	}

	if err := saveKeyToFile(outPath, key); err != nil {
		fatalf("failed to write credential file: %v", err)
	}

	fmt.Println()
	infof(colorGreen("✓ Login successful! Credential saved to: %s"), outPath)
	infof("  account_id : %s", accountID)
	if tr.Email != "" {
		infof("  email      : %s", tr.Email)
	}
	infof("  expires_at : %s", tr.ExpiresAt.Format(time.RFC3339))
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		warnf("could not open browser automatically — please copy the URL above manually")
	}
}

func insertSuffix(path, suffix string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return base + suffix + ext
}

func printSyncSummary(result SyncResult) {
	fmt.Println()
	fmt.Println(colorBold("══════════════════════ SYNC SUMMARY ══════════════════════"))
	fmt.Printf("  restored: %d  uploaded: %d  skipped: %d  failed: %d\n",
		result.RestoredCount, result.UploadedCount, result.SkippedCount, result.FailedCount)
	for _, err := range result.Errors {
		errorf("sync item failed: %v", err)
	}
	fmt.Println(colorBold("══════════════════════════════════════════════════════════"))
}

// ---------- print helpers ----------

func printLogo() {
	if supportsColor() {
		fmt.Print(colorCyan(logo))
	} else {
		fmt.Print(logo)
	}
	fmt.Println(colorBold("        codex-probe — Codex Credential & Diagnostics Tool"))
	fmt.Println()
}

func infof(format string, a ...any) {
	fmt.Printf(colorGreen("[INFO]")+" "+format+"\n", a...)
}

func warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, colorYellow("[WARN]")+" "+format+"\n", a...)
}

func errorf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, colorRed("[ERROR]")+" "+format+"\n", a...)
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, colorRed("[FATAL]")+" "+format+"\n", a...)
	os.Exit(1)
}

// ---------- ANSI color helpers ----------

func supportsColor() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func ansi(code string, s string) string {
	if !supportsColor() {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func colorGreen(s string) string  { return ansi("32", s) }
func colorRed(s string) string    { return ansi("31", s) }
func colorYellow(s string) string { return ansi("33", s) }
func colorBlue(s string) string   { return ansi("34", s) }
func colorCyan(s string) string   { return ansi("36", s) }
func colorBold(s string) string   { return ansi("1", s) }
