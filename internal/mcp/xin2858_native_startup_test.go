package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestXIN2858IsolatedTestCatalogContractAndManifestV7(t *testing.T) {
	if len(supportedPermissionReleases) != 0 || len(supportedPermissionPackages) != 0 {
		t.Fatalf("production support registries must remain empty: releases=%d packages=%d", len(supportedPermissionReleases), len(supportedPermissionPackages))
	}

	manifestRaw, err := os.ReadFile("../../docs/permission/contract-manifest-v7.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Format                     string `json:"format"`
		CatalogDigest              any    `json:"catalog_digest"`
		IsolatedTestCatalogMode    string `json:"isolated_test_catalog_mode"`
		IsolatedTestCatalogVersion string `json:"isolated_test_catalog_version"`
		IsolatedTestManifestSHA256 string `json:"isolated_test_manifest_sha256"`
		IsolatedTestSemanticSHA256 string `json:"isolated_test_semantic_sha256"`
		Files                      map[string]struct {
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != "permission-cli-contract-manifest-v7" ||
		manifest.CatalogDigest != nil ||
		manifest.IsolatedTestCatalogMode != IsolatedTestCatalogMode ||
		manifest.IsolatedTestCatalogVersion != IsolatedTestCatalogVersion ||
		manifest.IsolatedTestManifestSHA256 != IsolatedTestCatalogDigest ||
		manifest.IsolatedTestSemanticSHA256 != IsolatedTestCatalogSemanticSHA256 {
		t.Fatalf("unexpected contract-manifest-v7 header: %+v", manifest)
	}
	for relPath, entry := range manifest.Files {
		b, rerr := os.ReadFile(filepath.Join("../..", filepath.FromSlash(relPath)))
		if rerr != nil {
			t.Fatalf("missing contract-manifest-v7 file %s: %v", relPath, rerr)
		}
		if len(b) != entry.Bytes || sha256Sum(b) != entry.SHA256 {
			t.Fatalf("contract-manifest-v7 mismatch for %s: got bytes=%d sha=%s, want bytes=%d sha=%s", relPath, len(b), sha256Sum(b), entry.Bytes, entry.SHA256)
		}
	}

	fixtureRaw, err := os.ReadFile("../../docs/permission/isolated-test-catalog-fixture-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtureDoc struct {
		Format                     string                     `json:"format"`
		ScopedBudgets              permission.ResponseBudgets `json:"scoped_isolated_fixture_response_budgets"`
		ImmutablePackageCommitment struct {
			CatalogFormat       string `json:"catalog_format"`
			ContractRevision    int    `json:"contract_revision"`
			SemanticsVersion    string `json:"semantics_version"`
			CatalogVersion      string `json:"catalog_version"`
			ManifestSHA256      string `json:"manifest_sha256"`
			IdentityIndexSHA256 string `json:"identity_index_sha256"`
			SemanticSHA256      string `json:"semantic_sha256"`
		} `json:"immutable_package_commitment"`
		CheckedFixturePolicy struct {
			AllowedCount int `json:"allowed_capabilities_count"`
			DeniedCount  int `json:"denied_capabilities_count"`
			Allowed      []struct {
				ID      string          `json:"id"`
				Kind    string          `json:"kind"`
				Profile string          `json:"profile"`
				Target  json.RawMessage `json:"target"`
			} `json:"allowed_capabilities"`
			Denied []struct {
				ID      string          `json:"id"`
				Kind    string          `json:"kind"`
				Profile string          `json:"profile"`
				Target  json.RawMessage `json:"target"`
			} `json:"denied_capabilities"`
		} `json:"checked_fixture_policy"`
	}
	if err := json.Unmarshal(fixtureRaw, &fixtureDoc); err != nil {
		t.Fatal(err)
	}
	if fixtureDoc.Format != "permission-isolated-test-catalog-fixture-v1" ||
		fixtureDoc.ImmutablePackageCommitment.CatalogFormat != IsolatedTestCatalogFormat ||
		fixtureDoc.ImmutablePackageCommitment.ContractRevision != IsolatedTestCatalogContractRevision ||
		fixtureDoc.ImmutablePackageCommitment.SemanticsVersion != IsolatedTestCatalogSemanticsVersion ||
		fixtureDoc.ImmutablePackageCommitment.CatalogVersion != IsolatedTestCatalogVersion ||
		fixtureDoc.ImmutablePackageCommitment.ManifestSHA256 != IsolatedTestCatalogDigest ||
		fixtureDoc.ImmutablePackageCommitment.IdentityIndexSHA256 != IsolatedTestCatalogIdentityIndexSHA256 ||
		fixtureDoc.ImmutablePackageCommitment.SemanticSHA256 != IsolatedTestCatalogSemanticSHA256 {
		t.Fatalf("unexpected fixture package commitment: %+v", fixtureDoc.ImmutablePackageCommitment)
	}
	if fixtureDoc.ScopedBudgets != IsolatedTestResponseBudgets() {
		t.Fatalf("scoped budgets mismatch: doc=%+v code=%+v", fixtureDoc.ScopedBudgets, IsolatedTestResponseBudgets())
	}

	baseline, err := PermissionCatalogBaseline()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]permission.CatalogEntry{}
	for _, e := range baseline.Entries {
		byID[e.ID] = e
	}
	compiledPolicy := IsolatedTestFixturePolicy()
	if len(fixtureDoc.CheckedFixturePolicy.Allowed) != fixtureDoc.CheckedFixturePolicy.AllowedCount ||
		len(fixtureDoc.CheckedFixturePolicy.Denied) != fixtureDoc.CheckedFixturePolicy.DeniedCount {
		t.Fatalf("fixture capability count mismatch: %+v", fixtureDoc.CheckedFixturePolicy)
	}
	var docAllowed, docDenied []string
	for _, item := range fixtureDoc.CheckedFixturePolicy.Allowed {
		docAllowed = append(docAllowed, item.ID)
		baseEntry, ok := byID[item.ID]
		if !ok || baseEntry.Kind != item.Kind || baseEntry.Profile != item.Profile || string(catalogCanonicalJSON(baseEntry.Target)) != string(catalogCanonicalJSON(item.Target)) {
			t.Fatalf("allowed fixture entry mismatch for %s: doc=%+v base=%+v", item.ID, item, baseEntry)
		}
	}
	for _, item := range fixtureDoc.CheckedFixturePolicy.Denied {
		docDenied = append(docDenied, item.ID)
		baseEntry, ok := byID[item.ID]
		if !ok || baseEntry.Kind != item.Kind || baseEntry.Profile != item.Profile || string(catalogCanonicalJSON(baseEntry.Target)) != string(catalogCanonicalJSON(item.Target)) {
			t.Fatalf("denied fixture entry mismatch for %s: doc=%+v base=%+v", item.ID, item, baseEntry)
		}
	}
	if !reflect.DeepEqual(docAllowed, compiledPolicy.AllowedCapabilities) {
		t.Fatalf("allowed capability list mismatch:\ndoc=%v\ncode=%v", docAllowed, compiledPolicy.AllowedCapabilities)
	}
	if !reflect.DeepEqual(docDenied, compiledPolicy.DeniedCapabilities) {
		t.Fatalf("denied capability list mismatch:\ndoc=%v\ncode=%v", docDenied, compiledPolicy.DeniedCapabilities)
	}
}

func privateHostTestDir(t *testing.T) string {
	t.Helper()
	d := secureTestDir(t)
	if f, err := secureDirectory(d, true); err == nil {
		closePrivateFile(f)
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	alt, err := os.MkdirTemp(home, ".xin2858-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(alt, 0o700); err != nil {
		_ = os.RemoveAll(alt)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(alt) })
	return alt
}

func TestXIN2858NativeCLIStartupIsolatedTestCatalogAndFailClosedGates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	dir := privateHostTestDir(t)
	cliBin := filepath.Join(dir, "octo-cli-xin2858")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", cliBin, "./cmd/octo-cli")
	buildCmd.Dir = "../.."
	if out, berr := buildCmd.CombinedOutput(); berr != nil {
		t.Fatalf("failed to build octo-cli binary: %v\n%s", berr, string(out))
	}
	if err := os.Chmod(cliBin, 0o700); err != nil {
		t.Fatal(err)
	}

	files, pin, _ := retainedPackageFixture(t)
	if pin != IsolatedTestCatalogPin() {
		t.Fatalf("unexpected retained package pin: got=%+v want=%+v", pin, IsolatedTestCatalogPin())
	}
	writePackageDir := func(sub string, pkgFiles map[string][]byte) string {
		t.Helper()
		pkgDir := filepath.Join(dir, sub)
		for relPath, raw := range pkgFiles {
			target := filepath.Join(pkgDir, filepath.FromSlash(relPath))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, raw, 0o400); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(pkgDir, "manifest.json")
	}
	manifestPath := writePackageDir("pkg-v2", files)

	// Verify ReadSupportedRelease remains fail-closed on the candidate v2 package,
	// while ReadIsolatedTestRelease succeeds only on the exact pinned manifest.
	if _, err := ReadSupportedRelease(manifestPath, pin.Digest); err == nil {
		t.Fatal("ReadSupportedRelease must remain fail-closed when supportedPermissionPackages is empty")
	}
	if _, snap, err := ReadIsolatedTestRelease(manifestPath, pin.Digest); err != nil || snap == nil || snap.SemanticDigest() != IsolatedTestCatalogSemanticSHA256 {
		t.Fatalf("ReadIsolatedTestRelease failed on pinned v2 package: err=%v snap=%v", err, snap)
	}
	if _, _, err := ReadIsolatedTestRelease(manifestPath, strings.Repeat("0", 64)); err == nil {
		t.Fatal("ReadIsolatedTestRelease accepted wrong digest")
	}

	serverCert, clientCert, roots, peerHash := controlCertificates(t)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[1]})
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	certPEM := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[0]}),
		caPEM...,
	)
	certPath := filepath.Join(dir, "server-cert.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(serverCert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "server-key.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}), 0o600); err != nil {
		t.Fatal(err)
	}
	leafSHA := sha256Sum(serverCert.Certificate[0])

	f := newManagedFixture(t)
	f.backend.Close()
	f.backend = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.backend.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}}
	f.backend.StartTLS()
	defer f.backend.Close()

	f.bundle.Key.Origin = f.backend.URL
	f.bundle.CatalogVersion = pin.Version
	f.bundle.CatalogDigest = pin.Digest
	allowedFixtureCaps := IsolatedTestFixturePolicy().AllowedCapabilities
	f.bundle.Capability.Rules[0]["capabilities"] = allowedFixtureCaps
	f.save()

	var docsSearchCalls atomic.Int32
	var messageSearchCalls atomic.Int32
	var exportCalls atomic.Int32
	var sendCalls atomic.Int32
	var deniedWriteDispatches atomic.Int32
	baseServe := f.serve

	f.backend.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/permission-facts/v1":
			recorder := httptest.NewRecorder()
			f.mu.Lock()
			baseServe(recorder, r)
			f.mu.Unlock()
			var env map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &env); err != nil {
				t.Error(err)
				return
			}
			if results, ok := env["results"].([]any); ok {
				for _, item := range results {
					row := item.(map[string]any)
					ref, _ := row["reference"].(string)
					if ref == "delivery_denied" {
						if preds, ok := row["predicates"].(map[string]any); ok {
							for k := range preds {
								preds[k] = false
							}
						}
					}
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(recorder.Code)
			_ = json.NewEncoder(w).Encode(env)
		case "/v1/bot/docs/search":
			docsSearchCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total":      1,
				"items":      []map[string]any{{"docId": "doc_1", "title": "Isolated Test Doc"}},
				"nextCursor": "",
			})
		case "/v1/bot/messages/_search", "/v1/bot/messages/_search_global_messages":
			page := messageSearchCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if page%2 == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data":       []map[string]any{{"message_id": "m1"}},
					"pagination": map[string]any{"has_more": true, "next_cursor": "c2"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":       []map[string]any{{"message_id": "m2"}},
				"pagination": map[string]any{"has_more": false, "next_cursor": ""},
			})
		case "/v1/bot/docs/doc_ok/export":
			exportCalls.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 16))
		case "/v1/bot/sendMessage":
			sendCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message_id":"msg_reply_1"}`))
		case "/v1/bot/docs", "/v1/bot/docs/board_1/scene", "/v1/bot/docs/doc_1/scene":
			deniedWriteDispatches.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			baseServe(w, r)
		}
	})

	reserveAddr := func() string {
		l, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			t.Fatal(lerr)
		}
		addr := l.Addr().String()
		_ = l.Close()
		return addr
	}

	makeBaseHostConfig := func(readyPath, relPath, relSHA string) map[string]any {
		cAddr := reserveAddr()
		rAddr := reserveAddr()
		return map[string]any{
			"version":              1,
			"origin":               f.backend.URL,
			"bot_uid":              "bot",
			"recipient_id":         "endpoint-1",
			"endpoint_incarnation": strings.Repeat("5", 32),
			"peer_sha256":          peerHash,
			"control_address":      cAddr,
			"relay_address":        rAddr,
			"relay_url":            "https://" + rAddr,
			"certificate":          certPath,
			"private_key":          keyPath,
			"peer_ca":              caPath,
			"release_path":         relPath,
			"release_sha256":       relSHA,
			"ready_path":           readyPath,
			"relay_executable":     cliBin,
			"relay_ca":             caPath,
			"relay_cert_sha256":    leafSHA,
			"relay_server_name":    "localhost",
			"authority_pins": map[string]any{
				"im":  map[string]any{"kind": "im", "incarnation": strings.Repeat("3", 32)},
				"sql": map[string]any{"kind": "sql", "incarnation": strings.Repeat("4", 32)},
			},
		}
	}

	runNegativeStartup := func(name string, cfgMap map[string]any, extraArgs []string, extraEnv []string, wantSubstring string) {
		t.Helper()
		t.Run("negative_"+name, func(t *testing.T) {
			subDir := filepath.Join(dir, "neg_"+name)
			if err := os.MkdirAll(subDir, 0o700); err != nil {
				t.Fatal(err)
			}
			logDir := filepath.Join(subDir, "logs")
			if err := os.MkdirAll(logDir, 0o700); err != nil {
				t.Fatal(err)
			}
			homeDir := filepath.Join(subDir, "home")
			if err := os.MkdirAll(homeDir, 0o700); err != nil {
				t.Fatal(err)
			}
			readyPath := cfgMap["ready_path"].(string)
			cfgBytes, err := permission.PrivateJSON(cfgMap)
			if err != nil {
				t.Fatal(err)
			}
			cfgPath := filepath.Join(subDir, "host.json")
			if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"mcp", "serve", "--permission-config", cfgPath}, extraArgs...)
			nctx, ncancel := context.WithTimeout(ctx, 30*time.Second)
			defer ncancel()
			cmd := exec.CommandContext(nctx, cliBin, args...)
			cmd.Env = append([]string{
				"HOME=" + homeDir,
				"OCTO_TOKEN=bf_managed",
				"OCTO_API_BASE_URL=" + f.backend.URL,
				"OCTO_MCP_REQUEST_LOG_DIR=" + logDir,
				"SSL_CERT_FILE=" + caPath,
			}, extraEnv...)
			out, cerr := cmd.CombinedOutput()
			if cerr == nil {
				t.Fatalf("expected native octo-cli startup %s to fail closed, but it exited 0: %s", name, string(out))
			}
			if !strings.Contains(string(out), wantSubstring) {
				t.Fatalf("expected output to contain %q, got err=%v out=%s", wantSubstring, cerr, string(out))
			}
			if _, serr := os.Stat(readyPath); !os.IsNotExist(serr) {
				t.Fatalf("expected ready.json NOT to be created on failed startup %s", name)
			}
		})
	}

	// (a) Default/production startup without isolated_test_catalog opt-in fails closed even on the valid v2 package.
	runNegativeStartup(
		"production_default_no_opt_in",
		makeBaseHostConfig(filepath.Join(dir, "ready_prod.json"), manifestPath, pin.Digest),
		nil,
		nil,
		"PERMISSION_RELEASE_SOURCE_BOUNDS_UNPROVEN",
	)

	// (b) Conflicting isolated_test_catalog=false + catalog_mode="isolated_test_v1" fails closed.
	conflictCfg := makeBaseHostConfig(filepath.Join(dir, "ready_conflict.json"), manifestPath, pin.Digest)
	conflictCfg["isolated_test_catalog"] = false
	conflictCfg["catalog_mode"] = IsolatedTestCatalogMode
	runNegativeStartup("conflicting_catalog_mode", conflictCfg, nil, nil, "PERMISSION_HOST_CONFIG_INVALID")

	// (c) Over-ceiling response_budgets in isolated-test mode fails closed.
	overBudgetCfg := makeBaseHostConfig(filepath.Join(dir, "ready_overbudget.json"), manifestPath, pin.Digest)
	overBudgetCfg["isolated_test_catalog"] = true
	overBudgetCfg["response_budgets"] = map[string]any{
		"max_acquisition_bytes":    8388608,
		"max_page_items":           100,
		"max_pages":                10,
		"max_cumulative_bytes":     16777216,
		"max_serialized_rpc_bytes": 16777216,
	}
	runNegativeStartup("over_ceiling_isolated_budgets", overBudgetCfg, nil, nil, "PERMISSION_HOST_CONFIG_INVALID")

	// (d) Wrong release_sha256 pin in isolated-test mode fails closed.
	wrongPinCfg := makeBaseHostConfig(filepath.Join(dir, "ready_wrongpin.json"), manifestPath, strings.Repeat("a", 64))
	wrongPinCfg["isolated_test_catalog"] = true
	runNegativeStartup("wrong_release_sha256", wrongPinCfg, nil, nil, "PERMISSION_RELEASE_UNAVAILABLE")

	// (e) Tampered catalog file (1 byte flipped in blobs/0.bin) in isolated-test mode fails closed.
	tamperedFiles := clonePackageFiles(files)
	tamperedFiles["blobs/0.bin"][0] ^= 0xff
	tamperedManifestPath := writePackageDir("pkg-tampered", tamperedFiles)
	tamperedCfg := makeBaseHostConfig(filepath.Join(dir, "ready_tampered.json"), tamperedManifestPath, pin.Digest)
	tamperedCfg["isolated_test_catalog"] = true
	runNegativeStartup("tampered_catalog_blob", tamperedCfg, nil, nil, "catalog listed file")

	// (f) Recommitted mismatched catalog (mutated profile in index.json + recomputed manifest.json SHA-256) fails closed.
	recommittedFiles := clonePackageFiles(files)
	idxObj := packageTestObject(t, recommittedFiles["index.json"])
	for _, item := range idxObj["profiles"].([]any) {
		p := item.(map[string]any)
		if p["name"] == "leaf" {
			p["limits"].(map[string]any)["local_input_bytes"] = 16777215
		}
	}
	recommittedFiles["index.json"] = catalogCanonicalJSON(idxObj)
	recommittedPin := commitPackageFixture(t, recommittedFiles, "")
	recommittedManifestPath := writePackageDir("pkg-recommitted", recommittedFiles)
	recommittedCfg := makeBaseHostConfig(filepath.Join(dir, "ready_recommitted.json"), recommittedManifestPath, recommittedPin.Digest)
	recommittedCfg["isolated_test_catalog"] = true
	runNegativeStartup("arbitrary_recommitted_runtime_digest", recommittedCfg, nil, nil, "PERMISSION_RELEASE_UNAVAILABLE")

	// Now verify positive native CLI startup via `octo-cli mcp serve --permission-config <host.json>`
	// using the bounded isolated-test catalog path (without any injected test registry!).
	posDir := filepath.Join(dir, "pos_native")
	logDir := filepath.Join(posDir, "logs")
	homeDir := filepath.Join(posDir, "home")
	for _, d := range []string{posDir, logDir, homeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	readyPath := filepath.Join(posDir, "ready.json")
	posCfgMap := makeBaseHostConfig(readyPath, manifestPath, pin.Digest)
	posCfgMap["isolated_test_catalog"] = true
	posCfgMap["catalog_mode"] = IsolatedTestCatalogMode
	controlAddr := posCfgMap["control_address"].(string)
	relayURL := posCfgMap["relay_url"].(string)
	cfgBytes, err := permission.PrivateJSON(posCfgMap)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(posDir, "host.json")
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	hostCtx, stopHost := context.WithCancel(ctx)
	hostCmd := exec.CommandContext(hostCtx, cliBin, "mcp", "serve", "--permission-config", cfgPath)
	hostCmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + homeDir,
		"TMPDIR=" + posDir,
		"OCTO_TOKEN=bf_managed",
		"OCTO_API_BASE_URL=" + f.backend.URL,
		"OCTO_MCP_REQUEST_LOG_DIR=" + logDir,
		"SSL_CERT_FILE=" + caPath,
	}
	var hostStderr bytes.Buffer
	hostCmd.Stderr = &hostStderr
	if err := hostCmd.Start(); err != nil {
		t.Fatalf("failed to start native octo-cli permission host: %v", err)
	}
	hostDone := make(chan error, 1)
	go func() { hostDone <- hostCmd.Wait() }()
	defer func() {
		stopHost()
		select {
		case <-hostDone:
		case <-time.After(10 * time.Second):
			t.Error("native octo-cli permission host did not shut down")
		}
	}()

	var ready struct {
		ControlVersion         int    `json:"control_version"`
		CoordinatorIncarnation string `json:"coordinator_incarnation"`
	}
	for deadline := time.Now().Add(120 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		select {
		case herr := <-hostDone:
			t.Fatalf("native octo-cli exited before readiness: %v (stderr=%s)", herr, hostStderr.String())
		default:
		}
		if b, rerr := os.ReadFile(readyPath); rerr == nil && json.Unmarshal(b, &ready) == nil && ready.CoordinatorIncarnation != "" {
			break
		}
	}
	if ready.ControlVersion != 1 || len(ready.CoordinatorIncarnation) != 32 {
		t.Fatalf("native octo-cli did not publish valid ready.json: %+v (stderr=%s)", ready, hostStderr.String())
	}

	conn, err := tls.Dial("tcp", controlAddr, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   "localhost",
		Certificates: []tls.Certificate{clientCert},
	})
	if err != nil {
		t.Fatalf("control mTLS dial to native octo-cli failed: %v", err)
	}
	defer conn.Close()

	session := permission.NewID()
	sendControlFrame := func(method string, payload any) map[string]any {
		t.Helper()
		frame, ferr := permission.PrivateJSON(map[string]any{
			"control_version": 1,
			"request_id":      permission.NewID(),
			"session_id":      session,
			"method":          method,
			"payload":         payload,
		})
		if ferr != nil {
			t.Fatal(ferr)
		}
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(frame)))
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, werr := conn.Write(append(prefix[:], frame...)); werr != nil {
			t.Fatal(werr)
		}
		if _, rerr := io.ReadFull(conn, prefix[:]); rerr != nil {
			t.Fatal(rerr)
		}
		body := make([]byte, binary.BigEndian.Uint32(prefix[:]))
		if _, rerr := io.ReadFull(conn, body); rerr != nil {
			t.Fatal(rerr)
		}
		var resp map[string]any
		if json.Unmarshal(body, &resp) != nil {
			t.Fatalf("invalid control reply for %s: %s", method, string(body))
		}
		return resp
	}
	requestOK := func(method string, payload any) map[string]any {
		t.Helper()
		resp := sendControlFrame(method, payload)
		if resp["status"] != "ok" {
			t.Fatalf("control %s failed: %v", method, resp)
		}
		return resp["payload"].(map[string]any)
	}

	requestOK("hello", map[string]any{
		"versions":             []int{2},
		"metadata_versions":    []int{2},
		"identity_versions":    []int{2},
		"endpoint_incarnation": posCfgMap["endpoint_incarnation"],
		"configured_origin":    posCfgMap["origin"],
		"recipient_id":         posCfgMap["recipient_id"],
	})

	// Journey 2: Admission denied before model work.
	f.mu.Lock()
	f.header.MessageID = "900"
	f.header.Reference = "header_denied"
	f.header.Delivery.Sequence = "900"
	deniedStream := f.header.Delivery
	f.mu.Unlock()
	ingDenied := requestOK("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "delivery_denied",
		"stream":             deniedStream,
		"payload_sha256":     f.header.PayloadSHA256,
	})
	admitDeniedResp := sendControlFrame("admit", map[string]any{
		"ingress_id":          ingDenied["ingress_id"],
		"proposed_context_id": permission.NewID(),
	})
	if admitDeniedResp["status"] != "refused" || admitDeniedResp["payload"].(map[string]any)["category"] != "invocation_denied" {
		t.Fatalf("expected Journey 2 admit to refuse with invocation_denied, got %v", admitDeniedResp)
	}

	// Journey 1 & Journey 3 over HTTP MCP and child octo-cli --turn-relay stdio.
	f.mu.Lock()
	f.header.MessageID = "42"
	f.header.Reference = "header"
	f.header.Delivery.Sequence = "1"
	f.mu.Unlock()
	ing1 := requestOK("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "delivery_j1_http",
		"stream":             f.header.Delivery,
		"payload_sha256":     f.header.PayloadSHA256,
	})
	ctxID1 := permission.NewID()
	adm1 := requestOK("admit", map[string]any{
		"ingress_id":          ing1["ingress_id"],
		"proposed_context_id": ctxID1,
	})
	man1 := map[string]any{
		"context_id":         ctxID1,
		"context_generation": adm1["generation"],
		"originals":          []permission.NativeHeader{f.header},
	}
	m1Bytes, _ := permission.PrivateJSON(man1)
	man1["bytes"] = len(m1Bytes)
	man1["sha256"] = sha256Sum(m1Bytes)
	requestOK("bind_context", map[string]any{
		"capsule_id": adm1["capsule_id"],
		"manifest":   man1,
	})
	httpRelay := requestOK("open_relay", map[string]any{
		"capsule_id":        adm1["capsule_id"],
		"recipient_id":      "endpoint-1",
		"transport":         "http",
		"relay_nonce":       permission.NewID(),
		"include_tls_trust": true,
	})
	httpToken := httpRelay["handle"].(string)

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13,
				RootCAs:    roots,
				ServerName: "localhost",
			},
		},
	}
	postHTTP := func(req rpcRequest) (rpcResponse, http.Header, int) {
		t.Helper()
		raw, _ := json.Marshal(req)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+httpToken)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			t.Fatalf("HTTP relay request failed: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusAccepted && len(body) == 0 {
			return rpcResponse{}, resp.Header, resp.StatusCode
		}
		var out rpcResponse
		if json.Unmarshal(body, &out) != nil {
			t.Fatalf("invalid HTTP relay JSON response (status=%d): %s", resp.StatusCode, string(body))
		}
		return out, resp.Header, resp.StatusCode
	}

	if initResp, hdr, code := postHTTP(initializedRequest()); code != http.StatusOK || initResp.Error != nil || hdr.Get("X-Octo-Acquisition-Budget-Bytes") != "4194304" {
		t.Fatalf("HTTP initialize failed: code=%d err=%v budgetHdr=%q", code, initResp.Error, hdr.Get("X-Octo-Acquisition-Budget-Bytes"))
	}
	if _, _, code := postHTTP(rpcRequest{Jsonrpc: "2.0", Method: "notifications/initialized"}); code != http.StatusAccepted {
		t.Fatalf("expected HTTP 202 Accepted for notifications/initialized, got %d", code)
	}

	makeToolReqWithMsg := func(id int, msgID, toolName, opID string, args map[string]any) rpcRequest {
		t.Helper()
		params, _ := json.Marshal(map[string]any{
			"name": toolName,
			"arguments": map[string]any{
				"operation_id": opID,
				"arguments":    args,
			},
		})
		req := rpcRequest{
			Jsonrpc: "2.0",
			ID:      json.RawMessage(strconv.Itoa(id)),
			Method:  "tools/call",
			Params:  params,
		}
		if err := relayDeclaration(&req, permission.DeclarationV2{
			Sender:      "alice",
			Channel:     "group",
			ChannelType: 2,
			SpaceID:     "space",
			MessageID:   msgID,
		}); err != nil {
			t.Fatal(err)
		}
		return req
	}
	makeToolReq := func(id int, toolName, opID string, args map[string]any) rpcRequest {
		return makeToolReqWithMsg(id, "42", toolName, opID, args)
	}

	// Journey 1 positive HTTP MCP allowed reads (`docs.search`, `message.search` --page-all, `docs.scene.export`) and reply (`message.send`) across execute and call_op.
	for idx, toolName := range []string{"execute", "call_op"} {
		rDoc, hdr, _ := postHTTP(makeToolReq(10+idx, toolName, "docs.search", map[string]any{"q": "isolated"}))
		if rDoc.Error != nil || bytes.Contains(rDoc.Result, []byte(`"isError":true`)) || hdr.Get("X-Octo-Acquisition-Budget-Bytes") != "4194304" {
			t.Fatalf("%s docs.search failed: err=%v result=%s hdr=%v", toolName, rDoc.Error, string(rDoc.Result), hdr)
		}
		rMsg, _, _ := postHTTP(makeToolReq(20+idx, toolName, "message.search", map[string]any{"query": "hello", "page_all": true}))
		if rMsg.Error != nil || bytes.Contains(rMsg.Result, []byte(`"isError":true`)) {
			t.Fatalf("%s message.search failed: err=%v result=%s", toolName, rMsg.Error, string(rMsg.Result))
		}
		rExp, _, _ := postHTTP(makeToolReq(30+idx, toolName, "docs.scene.export", map[string]any{"docId": "doc_ok", "format": "png"}))
		if rExp.Error != nil || bytes.Contains(rExp.Result, []byte(`"isError":true`)) {
			t.Fatalf("%s docs.scene.export failed: err=%v result=%s", toolName, rExp.Error, string(rExp.Result))
		}
		rSend, _, _ := postHTTP(makeToolReq(40+idx, toolName, "message.send", map[string]any{"channel_id": "target", "channel_type": 2, "payload": map[string]any{"text": "allowed reply"}}))
		if rSend.Error != nil || bytes.Contains(rSend.Result, []byte(`"isError":true`)) {
			t.Fatalf("%s message.send failed: err=%v result=%s", toolName, rSend.Error, string(rSend.Result))
		}
	}
	if docsSearchCalls.Load() != 2 || messageSearchCalls.Load() != 4 || exportCalls.Load() != 2 || sendCalls.Load() != 2 {
		t.Fatalf("unexpected positive call counts: docs=%d msgSearch=%d export=%d send=%d", docsSearchCalls.Load(), messageSearchCalls.Load(), exportCalls.Load(), sendCalls.Load())
	}

	// Verify allowed catalog schema, discovery, and skill resource reads on HTTP MCP.
	for i, rawReq := range []struct {
		method string
		params map[string]any
	}{
		{"tools/call", map[string]any{"name": "describe_op", "arguments": map[string]any{"operation_id": "message.send"}}},
		{"tools/call", map[string]any{"name": "get_skill", "arguments": map[string]any{"intent": "describe", "operation_id": "docs.search", "depth": "summary"}}},
		{"tools/call", map[string]any{"name": "search_ops", "arguments": map[string]any{"query": " "}}},
		{"tools/call", map[string]any{"name": "get_skill", "arguments": map[string]any{"intent": "search", "domain": "message", "query": "send"}}},
		{"resources/list", map[string]any{}},
		{"resources/read", map[string]any{"uri": "octo://skills/octo-messaging/SKILL.md#source"}},
		{"resources/read", map[string]any{"uri": "octo://skills/octo-docs/SKILL.md"}},
	} {
		pBytes, _ := json.Marshal(rawReq.params)
		req := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(strconv.Itoa(45 + i)), Method: rawReq.method, Params: pBytes}
		if err := relayDeclaration(&req, permission.DeclarationV2{Sender: "alice", Channel: "group", ChannelType: 2, SpaceID: "space", MessageID: "42"}); err != nil {
			t.Fatal(err)
		}
		resp, _, _ := postHTTP(req)
		if resp.Error != nil || bytes.Contains(resp.Result, []byte(`"isError":true`)) {
			t.Fatalf("catalog method %s (%v) failed: err=%v result=%s", rawReq.method, rawReq.params, resp.Error, string(resp.Result))
		}
	}

	// Unmapped schema capability (`octo-cli/loop/schema/loop.task.create`) is refused by checked fixture policy with capability_denied.
	unmappedSchemaBytes, _ := json.Marshal(map[string]any{"name": "describe_op", "arguments": map[string]any{"operation_id": "loop.task.create"}})
	unmappedSchemaReq := rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`49`), Method: "tools/call", Params: unmappedSchemaBytes}
	if err := relayDeclaration(&unmappedSchemaReq, permission.DeclarationV2{Sender: "alice", Channel: "group", ChannelType: 2, SpaceID: "space", MessageID: "42"}); err != nil {
		t.Fatal(err)
	}
	if resp, _, _ := postHTTP(unmappedSchemaReq); resp.Error == nil || resp.Error.Code != -32007 || !strings.Contains(fmt.Sprint(resp.Error.Data), "capability_denied") {
		t.Fatalf("expected unmapped schema describe_op to fail with -32007 capability_denied, got %+v", resp)
	}

	// Journey 3: Denied writes (`docs.create`, `docs.scene.edit`) and denied read (`docs.scene.get`) on HTTP MCP -> -32007 capability_denied and zero dispatches.
	deniedWriteDispatches.Store(0)
	for idx, toolName := range []string{"execute", "call_op"} {
		for _, deniedOp := range []struct {
			op   string
			args map[string]any
		}{
			{"docs.create", map[string]any{"title": "forbidden"}},
			{"docs.scene.edit", map[string]any{"docId": "board_1", "base-version": "v1", "deletedElementIds": []string{"e1"}}},
			{"docs.scene.get", map[string]any{"docId": "board_1"}},
		} {
			resp, _, _ := postHTTP(makeToolReq(50+idx*10, toolName, deniedOp.op, deniedOp.args))
			if resp.Error == nil || resp.Error.Code != -32007 || !strings.Contains(fmt.Sprint(resp.Error.Data), "capability_denied") {
				t.Fatalf("expected -32007 capability_denied on %s %s, got %+v", toolName, deniedOp.op, resp)
			}
		}
	}
	if deniedWriteDispatches.Load() != 0 {
		t.Fatalf("expected 0 upstream dispatches on denied writes/reads, got %d", deniedWriteDispatches.Load())
	}

	// Verify stdio --turn-relay child process on the same native host (Journey 1 allowed + Journey 3 denied write zero dispatch).
	requestOK("close", map[string]any{"capsule_id": adm1["capsule_id"], "reason": "completed"})

	f.mu.Lock()
	f.header.MessageID = "43"
	f.header.Reference = "header_stdio"
	f.header.Delivery.Sequence = "2"
	f.mu.Unlock()
	ing2 := requestOK("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "delivery_j1_stdio",
		"stream":             f.header.Delivery,
		"payload_sha256":     f.header.PayloadSHA256,
	})
	ctxID2 := permission.NewID()
	adm2 := requestOK("admit", map[string]any{
		"ingress_id":          ing2["ingress_id"],
		"proposed_context_id": ctxID2,
	})
	man2 := map[string]any{
		"context_id":         ctxID2,
		"context_generation": adm2["generation"],
		"originals":          []permission.NativeHeader{f.header},
	}
	m2Bytes, _ := permission.PrivateJSON(man2)
	man2["bytes"] = len(m2Bytes)
	man2["sha256"] = sha256Sum(m2Bytes)
	requestOK("bind_context", map[string]any{
		"capsule_id": adm2["capsule_id"],
		"manifest":   man2,
	})
	stdioRelay := requestOK("open_relay", map[string]any{
		"capsule_id":        adm2["capsule_id"],
		"recipient_id":      "endpoint-1",
		"transport":         "stdio",
		"relay_nonce":       permission.NewID(),
		"include_tls_trust": true,
	})
	stdioHandle := stdioRelay["handle"].(string)
	rawTicket, err := base64.RawURLEncoding.Strict().DecodeString(stdioHandle)
	if err != nil {
		t.Fatal(err)
	}
	var ticketMap map[string]any
	if err := json.Unmarshal(rawTicket, &ticketMap); err != nil {
		t.Fatal(err)
	}
	if ticketMap["max_response_bytes"] != "4194304" {
		t.Fatalf("expected stdio ticket max_response_bytes=\"4194304\" from scoped IsolatedTestResponseBudgets, got %v", ticketMap)
	}

	makeStdioReqLine := func(id int, toolName, opID string, args map[string]any) []byte {
		req := makeToolReqWithMsg(id, "43", toolName, opID, args)
		b, _ := json.Marshal(req)
		return b
	}
	initBytes, _ := json.Marshal(initializedRequest())
	var stdioIn bytes.Buffer
	for _, line := range [][]byte{
		initBytes,
		makeStdioReqLine(101, "execute", "docs.search", map[string]any{"q": "stdio"}),
		makeStdioReqLine(102, "call_op", "message.send", map[string]any{"channel_id": "target", "channel_type": 2, "payload": map[string]any{"text": "stdio reply"}}),
		makeStdioReqLine(103, "execute", "docs.create", map[string]any{"title": "denied stdio"}),
		makeStdioReqLine(104, "call_op", "docs.scene.edit", map[string]any{"docId": "board_1", "base-version": "v1", "deletedElementIds": []string{"e1"}}),
	} {
		stdioIn.Write(line)
		stdioIn.WriteByte('\n')
	}
	relayCmd := exec.CommandContext(ctx, cliBin, "--turn-relay", stdioHandle)
	relayCmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + homeDir,
		"TMPDIR=" + posDir,
		"SSL_CERT_FILE=" + caPath,
	}
	relayCmd.Stdin = &stdioIn
	var stdioOut, stdioErr bytes.Buffer
	relayCmd.Stdout = &stdioOut
	relayCmd.Stderr = &stdioErr
	if err := relayCmd.Run(); err != nil {
		t.Fatalf("child octo-cli --turn-relay failed: err=%v stderr=%s stdout=%s", err, stdioErr.String(), stdioOut.String())
	}
	lines := strings.Split(strings.TrimSpace(stdioOut.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 stdio JSON-RPC responses, got %d:\n%s", len(lines), stdioOut.String())
	}
	if !strings.Contains(lines[1], "Isolated Test Doc") || !strings.Contains(lines[2], "msg_reply_1") {
		t.Fatalf("unexpected stdio positive responses:\n1=%s\n2=%s", lines[1], lines[2])
	}
	if !strings.Contains(lines[3], "-32007") || !strings.Contains(lines[3], "capability_denied") ||
		!strings.Contains(lines[4], "-32007") || !strings.Contains(lines[4], "capability_denied") {
		t.Fatalf("expected -32007 capability_denied on stdio denied writes:\n3=%s\n4=%s", lines[3], lines[4])
	}
	if deniedWriteDispatches.Load() != 0 {
		t.Fatalf("expected 0 upstream dispatches after stdio denied writes, got %d", deniedWriteDispatches.Load())
	}

	// Verify checked fixture policy rejects a policy bundle that tries to ALLOW a capability requiring static provenance (`octo-cli/docs/derived/portable-attachment-bytes`) with policy_unavailable,
	// while a synthetic `allow-write` policy bundle containing `message.send` + `docs.create` admits the turn and still enforces `capability_denied` (with 0 dispatches) on `docs.create`!
	f.mu.Lock()
	f.bundle.Revision = "1"
	f.bundle.Capability.Rules[0]["capabilities"] = append(append([]string{}, allowedFixtureCaps...), "octo-cli/docs/derived/portable-attachment-bytes")
	f.save()
	f.mu.Unlock()

	var badPolicyIn bytes.Buffer
	badPolicyIn.Write(makeStdioReqLine(105, "execute", "docs.search", map[string]any{"q": "attempted static provenance allow"}))
	badPolicyIn.WriteByte('\n')
	badPolicyCmd := exec.CommandContext(ctx, cliBin, "--turn-relay", stdioHandle)
	badPolicyCmd.Env = relayCmd.Env
	badPolicyCmd.Stdin = &badPolicyIn
	var badPolicyOut bytes.Buffer
	badPolicyCmd.Stdout = &badPolicyOut
	if err := badPolicyCmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(badPolicyOut.String(), "-32007") || !strings.Contains(badPolicyOut.String(), "policy_unavailable") || deniedWriteDispatches.Load() != 0 {
		t.Fatalf("expected checked fixture policy to reject allow-rule containing portable-attachment-bytes with policy_unavailable and 0 dispatches, got: %s", badPolicyOut.String())
	}

	// Also verify Capability.State = "U" is rejected by checked fixture policy.
	f.mu.Lock()
	f.bundle.Revision = "2"
	f.bundle.Capability.State = "U"
	f.bundle.Capability.Rules = []permission.Rule{}
	f.save()
	f.mu.Unlock()

	var stateUIn bytes.Buffer
	stateUIn.Write(makeStdioReqLine(106, "execute", "docs.search", map[string]any{"q": "state_u"}))
	stateUIn.WriteByte('\n')
	stateUCmd := exec.CommandContext(ctx, cliBin, "--turn-relay", stdioHandle)
	stateUCmd.Env = relayCmd.Env
	stateUCmd.Stdin = &stateUIn
	var stateUOut bytes.Buffer
	stateUCmd.Stdout = &stateUOut
	if err := stateUCmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stateUOut.String(), "-32007") || !strings.Contains(stateUOut.String(), "policy_unavailable") {
		t.Fatalf("expected checked fixture policy to reject Capability.State='U' with policy_unavailable, got: %s", stateUOut.String())
	}

	// Restore valid fixture policy with explicit DENY rule for docs.create and docs.scene.edit, then rotate context_epoch (Journey 4).
	f.mu.Lock()
	f.bundle.Revision = "3"
	f.bundle.ContextEpoch = "1"
	f.bundle.Capability.State = "E"
	f.bundle.Capability.Rules = []permission.Rule{
		{
			"rule_id":      "allow_fixture",
			"effect":       "allow",
			"caller":       map[string]any{"kind": "all_humans"},
			"source":       map[string]any{"kind": "all_in_space", "conversation_types": []any{"group"}},
			"capabilities": allowedFixtureCaps,
		},
		{
			"rule_id":      "deny_writes",
			"effect":       "deny",
			"caller":       map[string]any{"kind": "all_humans"},
			"source":       map[string]any{"kind": "all_in_space", "conversation_types": []any{"group"}},
			"capabilities": IsolatedTestFixturePolicy().DeniedCapabilities,
		},
	}
	f.save()
	f.mu.Unlock()

	var epochRotIn bytes.Buffer
	epochRotIn.Write(makeStdioReqLine(107, "execute", "docs.search", map[string]any{"q": "epoch_rotated"}))
	epochRotIn.WriteByte('\n')
	epochRotCmd := exec.CommandContext(ctx, cliBin, "--turn-relay", stdioHandle)
	epochRotCmd.Env = relayCmd.Env
	epochRotCmd.Stdin = &epochRotIn
	var epochRotOut bytes.Buffer
	epochRotCmd.Stdout = &epochRotOut
	if err := epochRotCmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(epochRotOut.String(), "-32007") || !strings.Contains(epochRotOut.String(), "context_untrusted") {
		t.Fatalf("expected rotated context_epoch to invalidate active turn with context_untrusted, got: %s", epochRotOut.String())
	}

	requestOK("close", map[string]any{"capsule_id": adm2["capsule_id"], "reason": "completed"})
}
