package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestXIN2794AsyncIngressContractAndManifestV8(t *testing.T) {
	if len(supportedPermissionReleases) != 0 || len(supportedPermissionPackages) != 0 {
		t.Fatalf("production support registries must remain empty: releases=%d packages=%d", len(supportedPermissionReleases), len(supportedPermissionPackages))
	}

	manifestRaw, err := os.ReadFile("../../docs/permission/contract-manifest-v8.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Format                     string `json:"format"`
		ContractVersion            string `json:"contract_version"`
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
	if manifest.Format != "permission-cli-contract-manifest-v8" ||
		manifest.ContractVersion != permission.LiveIngressContractVersion ||
		manifest.CatalogDigest != nil ||
		manifest.IsolatedTestCatalogMode != IsolatedTestCatalogMode ||
		manifest.IsolatedTestCatalogVersion != IsolatedTestCatalogVersion ||
		manifest.IsolatedTestManifestSHA256 != IsolatedTestCatalogDigest ||
		manifest.IsolatedTestSemanticSHA256 != IsolatedTestCatalogSemanticSHA256 {
		t.Fatalf("unexpected contract-manifest-v8 header: %+v", manifest)
	}
	for relPath, entry := range manifest.Files {
		b, rerr := os.ReadFile(filepath.Join("../..", filepath.FromSlash(relPath)))
		if rerr != nil {
			t.Fatalf("missing contract-manifest-v8 file %s: %v", relPath, rerr)
		}
		if len(b) != entry.Bytes || sha256Sum(b) != entry.SHA256 {
			t.Fatalf("contract-manifest-v8 mismatch for %s: got bytes=%d sha=%s, want bytes=%d sha=%s", relPath, len(b), sha256Sum(b), entry.Bytes, entry.SHA256)
		}
	}

	manifestV9Raw, err := os.ReadFile("../../docs/permission/contract-manifest-v9.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifestV9 struct {
		Format                     string `json:"format"`
		ContractVersion            string `json:"contract_version"`
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
	if err := json.Unmarshal(manifestV9Raw, &manifestV9); err != nil {
		t.Fatal(err)
	}
	if manifestV9.Format != "permission-cli-contract-manifest-v9" ||
		manifestV9.ContractVersion != permission.LiveIngressContractVersion ||
		manifestV9.CatalogDigest != nil ||
		manifestV9.IsolatedTestCatalogMode != IsolatedTestCatalogMode ||
		manifestV9.IsolatedTestCatalogVersion != IsolatedTestCatalogVersion ||
		manifestV9.IsolatedTestManifestSHA256 != IsolatedTestCatalogDigest ||
		manifestV9.IsolatedTestSemanticSHA256 != IsolatedTestCatalogSemanticSHA256 {
		t.Fatalf("unexpected contract-manifest-v9 header: %+v", manifestV9)
	}
	for relPath, entry := range manifestV9.Files {
		b, rerr := os.ReadFile(filepath.Join("../..", filepath.FromSlash(relPath)))
		if rerr != nil {
			t.Fatalf("missing contract-manifest-v9 file %s: %v", relPath, rerr)
		}
		if len(b) != entry.Bytes || sha256Sum(b) != entry.SHA256 {
			t.Fatalf("contract-manifest-v9 mismatch for %s: got bytes=%d sha=%s, want bytes=%d sha=%s", relPath, len(b), sha256Sum(b), entry.Bytes, entry.SHA256)
		}
	}
}

func TestXIN2794AsyncIngressSeparationLiveTurnAndNegativeMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	dir := privateHostTestDir(t)
	cliBin := filepath.Join(dir, "octo-cli-xin2794-v8")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", cliBin, "./cmd/octo-cli")
	buildCmd.Dir = "../.."
	if out, berr := buildCmd.CombinedOutput(); berr != nil {
		t.Fatalf("failed to build octo-cli binary: %v\n%s", berr, string(out))
	}
	if err := os.Chmod(cliBin, 0o700); err != nil {
		t.Fatal(err)
	}

	files, pin, _ := retainedPackageFixture(t)
	pkgDir := filepath.Join(dir, "pkg-v2")
	for relPath, raw := range files {
		target := filepath.Join(pkgDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0o400); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := filepath.Join(pkgDir, "manifest.json")

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
	f.bundle.Invocation.Rules = []permission.Rule{
		{
			"rule_id": "allow_human_all_convs",
			"effect":  "allow",
			"caller":  map[string]any{"kind": "all_humans"},
			"source":  map[string]any{"kind": "all_in_space", "conversation_types": []any{"dm", "group", "thread"}},
		},
	}
	f.bundle.Capability.Rules = []permission.Rule{
		{
			"rule_id":      "allow_fixture_all_convs",
			"effect":       "allow",
			"caller":       map[string]any{"kind": "all_humans"},
			"source":       map[string]any{"kind": "all_in_space", "conversation_types": []any{"dm", "group", "thread"}},
			"capabilities": allowedFixtureCaps,
		},
		{
			"rule_id":      "deny_writes_all_convs",
			"effect":       "deny",
			"caller":       map[string]any{"kind": "all_humans"},
			"source":       map[string]any{"kind": "all_in_space", "conversation_types": []any{"dm", "group", "thread"}},
			"capabilities": IsolatedTestFixturePolicy().DeniedCapabilities,
		},
	}
	f.save()

	// Simulated server state demonstrating true asynchronous ingress separation:
	// - persistedRows tracks which message rows exist in MySQL (`message` table).
	//   For live WS turns, the trigger message row is NEVER inserted into persistedRows!
	// - If any request queries `s1-...` for an unpersisted row, or queries `purpose: "contributor"`
	//   for an unpersisted row, the server returns `status: "unavailable", reason: "mirror_lag"`.
	type storedRow struct {
		channelType   int
		channelID     string
		parentGroup   string
		messageID     string
		messageSeq    string
		fromUID       string
		payloadSHA256 string
		revoked       bool
		eligible      bool
	}
	var stateMu sync.Mutex
	persistedRows := map[string]storedRow{
		"s1-stored-hist-10": {
			channelType:   2,
			channelID:     "group",
			messageID:     "10",
			messageSeq:    "10",
			fromUID:       "alice",
			payloadSHA256: strings.Repeat("b", 64),
			eligible:      true,
		},
	}
	liveMemberEligible := map[string]bool{
		"alice": true,
		"bob":   true,
	}
	tamperNextLiveHeader := false

	var triggerRowSQLReads atomic.Int32
	var liveContributorPurposeCalls atomic.Int32
	var bindLiveIngressCalls atomic.Int32
	var evaluateCurrentAccessCalls atomic.Int32
	var liveAdmitResolveCalls atomic.Int32
	var liveReplyResolveCalls atomic.Int32
	var storedContributorResolveCalls atomic.Int32
	var docsSearchCalls atomic.Int32
	var sendCalls atomic.Int32
	var deniedWriteDispatches atomic.Int32

	var nonceSeq atomic.Uint64
	nextHex32 := func() string {
		n := nonceSeq.Add(1)
		return fmt.Sprintf("%032x", n)
	}
	makeAuthorities := func() ([]map[string]any, []string) {
		n1 := nextHex32()
		n2 := nextHex32()
		now := time.Now().UTC().Format(time.RFC3339Nano)
		return []map[string]any{
			{
				"authority_id": "im",
				"kind":         "im",
				"incarnation":  strings.Repeat("3", 32),
				"position":     "1",
				"read_nonce":   n1,
				"consistency":  "ack_before_read_start",
				"started_at":   now,
				"completed_at": now,
			},
			{
				"authority_id": "sql",
				"kind":         "sql",
				"incarnation":  strings.Repeat("4", 32),
				"position":     "1",
				"read_nonce":   n2,
				"consistency":  "ack_before_read_start",
				"started_at":   now,
				"completed_at": now,
			},
		}, []string{n1, n2}
	}

	sharedRaw, err := os.ReadFile("../permission/contracts/shared-contract-v6.json")
	if err != nil {
		t.Fatal(err)
	}
	var sharedContract struct {
		Profiles map[string][]string `json:"predicate_profiles"`
	}
	if err := json.Unmarshal(sharedRaw, &sharedContract); err != nil {
		t.Fatal(err)
	}
	predicatesForProfile := func(profile string, eligible bool) map[string]bool {
		out := map[string]bool{}
		for _, atom := range sharedContract.Profiles[profile] {
			out[atom] = eligible
		}
		return out
	}

	baseServe := f.serve
	f.backend.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/permission-facts/v1/bind-delivery":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			op, _ := req["operation"].(string)
			if op != "bind_live_ingress" {
				// Legacy synchronous bind_delivery requires the trigger row in MySQL!
				triggerRowSQLReads.Add(1)
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "unavailable", "reason": "mirror_lag"})
				return
			}
			bindLiveIngressCalls.Add(1)
			src := req
			if li, ok := req["live_ingress"].(map[string]any); ok && len(li) > 0 {
				src = li
			}
			ctFloat, _ := src["channel_type"].(float64)
			ct := int(ctFloat)
			chID, _ := src["channel_id"].(string)
			msgID, _ := src["message_id"].(string)
			msgSeq, _ := src["message_seq"].(string)
			fromUID, _ := src["from_uid"].(string)
			obsSHA, _ := src["observed_payload_sha256"].(string)

			canonicalCh := chID
			sourceKind := "group"
			var parentGroup any
			switch ct {
			case 1:
				sourceKind = "dm"
				pair, perr := permission.CanonicalDMPairID("bot", fromUID)
				if perr != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				canonicalCh = pair
			case 2:
				sourceKind = "group"
			case 5:
				sourceKind = "thread"
				parentGroup = "group"
			}
			l1Ref, err := permission.EncodeLiveIngressReference("l1", ct, canonicalCh, msgID, msgSeq, fromUID, obsSHA)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			l2Ref, err := permission.EncodeLiveIngressReference("l2", ct, canonicalCh, msgID, msgSeq, fromUID, obsSHA)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			stateMu.Lock()
			eligible := liveMemberEligible[fromUID]
			stateMu.Unlock()
			profile := sourceKind + "_admit_v1"
			auths, nonces := makeAuthorities()
			now := time.Now().UTC().Format(time.RFC3339Nano)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema_version":  1,
				"profile_version": 1,
				"key":             f.bundle.Key,
				"request_id":      req["request_id"],
				"read_id":         nextHex32(),
				"observed_at":     now,
				"authorities":     auths,
				"binding": map[string]any{
					"status":                     "bound",
					"current":                    true,
					"provenance":                 permission.LiveChannelACLProvenance,
					"trigger_row_persisted":      false,
					"content_attested_by_server": false,
					"delivery_reference":         l1Ref,
					"header_reference":           l2Ref,
					"native_message_version":     permission.LiveUnpersistedMessageVersion,
					"observed_payload_sha256":    obsSHA,
					"header": map[string]any{
						"header_version":         1,
						"header_reference":       l2Ref,
						"message_id":             msgID,
						"source":                 map[string]any{"kind": sourceKind, "id": canonicalCh},
						"parent_group":           parentGroup,
						"space_id":               "space",
						"author_uid":             fromUID,
						"author_kind":            "human",
						"recipient_bot_uid":      "bot",
						"native_message_version": permission.LiveUnpersistedMessageVersion,
						"payload_sha256":         obsSHA,
						"delivery": map[string]any{
							"stream_id":          "im_" + strconv.Itoa(ct) + "_" + canonicalCh,
							"stream_incarnation": strings.Repeat("3", 32),
							"sequence":           msgSeq,
						},
					},
					"proven_predicates":   predicatesForProfile(profile, eligible),
					"unknown_predicates":  []string{},
					"resolvable_purposes": []string{"admit", "reply"},
					"authority_refs":      nonces,
				},
			})
		case "/v1/bot/permission-facts/v1":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			op, _ := req["operation"].(string)
			purpose, _ := req["purpose"].(string)
			auths, nonces := makeAuthorities()
			now := time.Now().UTC().Format(time.RFC3339Nano)

			if op == "evaluate_current_access" {
				evaluateCurrentAccessCalls.Add(1)
				ca := req
				if nested, ok := req["current_access"].(map[string]any); ok && len(nested) > 0 {
					ca = nested
				}
				ctFloat, _ := ca["channel_type"].(float64)
				ct := int(ctFloat)
				chID, _ := ca["channel_id"].(string)
				callerUID, _ := ca["caller_uid"].(string)
				if callerUID == "" {
					callerUID, _ = ca["from_uid"].(string)
				}
				canonicalCh := chID
				sourceKind := "group"
				var parentGroup any
				switch ct {
				case 1:
					sourceKind = "dm"
					pair, perr := permission.CanonicalDMPairID("bot", callerUID)
					if perr != nil {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					canonicalCh = pair
				case 2:
					sourceKind = "group"
				case 5:
					sourceKind = "thread"
					parentGroup = "group"
				}
				stateMu.Lock()
				eligible := liveMemberEligible[callerUID]
				stateMu.Unlock()
				profile := sourceKind + "_" + purpose + "_v1"
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"schema_version":  1,
					"profile_version": 1,
					"key":             f.bundle.Key,
					"request_id":      req["request_id"],
					"read_id":         nextHex32(),
					"observed_at":     now,
					"authorities":     auths,
					"access": map[string]any{
						"status":                     "evaluated",
						"eligible":                   eligible,
						"profile":                    profile,
						"provenance":                 permission.LiveChannelACLProvenance,
						"trigger_row_persisted":      false,
						"content_attested_by_server": false,
						"native_message_version":     permission.LiveUnpersistedMessageVersion,
						"source":                     map[string]any{"kind": sourceKind, "id": canonicalCh},
						"parent_group":               parentGroup,
						"caller_uid":                 callerUID,
						"author_kind":                "human",
						"authority_refs":             nonces,
						"predicates":                 predicatesForProfile(profile, eligible),
					},
				})
				return
			}

			refsRaw, _ := req["references"].([]any)
			results := make([]map[string]any, 0, len(refsRaw))
			for _, item := range refsRaw {
				rm, _ := item.(map[string]any)
				ref, _ := rm["delivery_reference"].(string)
				if strings.HasPrefix(ref, permission.LiveIngressDeliveryPrefix) {
					if purpose == "contributor" {
						// Live unpersisted l1- tokens cannot be resolved as historical stored contributors!
						liveContributorPurposeCalls.Add(1)
						results = append(results, map[string]any{
							"reference": ref,
							"status":    "unavailable",
							"reason":    "mirror_lag",
						})
						continue
					}
					if purpose == "admit" {
						liveAdmitResolveCalls.Add(1)
					} else if purpose == "reply" {
						liveReplyResolveCalls.Add(1)
					}
					parsed, perr := permission.ParseLiveIngressReference("l1", ref)
					if perr != nil {
						results = append(results, map[string]any{
							"reference": ref,
							"status":    "invalid",
						})
						continue
					}
					stateMu.Lock()
					eligible := liveMemberEligible[parsed.FromUID]
					// Check if async persistence later wrote a conflicting/revoked row for this message_id.
					if row, hasPersisted := persistedRows["msg:"+parsed.MessageID]; hasPersisted {
						if row.revoked || row.payloadSHA256 != parsed.PayloadSHA256 || row.fromUID != parsed.FromUID {
							eligible = false
						}
					}
					tamper := tamperNextLiveHeader
					stateMu.Unlock()

					l2Ref := permission.LiveIngressHeaderPrefix + strings.TrimPrefix(ref, permission.LiveIngressDeliveryPrefix)
					var parentGroup any
					if parsed.SourceKind == "thread" {
						parentGroup = "group"
					}
					msgVersion := permission.LiveUnpersistedMessageVersion
					if tamper {
						msgVersion = "x0_tampered"
					}
					profile := parsed.SourceKind + "_" + purpose + "_v1"
					results = append(results, map[string]any{
						"reference":                  ref,
						"status":                     "resolved",
						"provenance":                 permission.LiveChannelACLProvenance,
						"trigger_row_persisted":      false,
						"content_attested_by_server": false,
						"header": map[string]any{
							"header_version":         1,
							"header_reference":       l2Ref,
							"message_id":             parsed.MessageID,
							"source":                 map[string]any{"kind": parsed.SourceKind, "id": parsed.CanonicalChannelID},
							"parent_group":           parentGroup,
							"space_id":               "space",
							"author_uid":             parsed.FromUID,
							"author_kind":            "human",
							"recipient_bot_uid":      "bot",
							"native_message_version": msgVersion,
							"payload_sha256":         parsed.PayloadSHA256,
							"delivery": map[string]any{
								"stream_id":          "im_" + strconv.Itoa(parsed.ChannelType) + "_" + parsed.CanonicalChannelID,
								"stream_incarnation": strings.Repeat("3", 32),
								"sequence":           parsed.MessageSeq,
							},
						},
						"profile":        profile,
						"authority_refs": nonces,
						"predicates":     predicatesForProfile(profile, eligible),
					})
					continue
				}

				// Stored reference (s1-...): requires the row in persistedRows!
				if purpose == "contributor" {
					storedContributorResolveCalls.Add(1)
				} else {
					triggerRowSQLReads.Add(1)
				}
				stateMu.Lock()
				row, ok := persistedRows[ref]
				stateMu.Unlock()
				if !ok {
					results = append(results, map[string]any{
						"reference": ref,
						"status":    "unavailable",
						"reason":    "mirror_lag",
					})
					continue
				}
				sourceKind := "group"
				var parentGroup any
				if row.channelType == 1 {
					sourceKind = "dm"
				} else if row.channelType == 5 {
					sourceKind = "thread"
					parentGroup = row.parentGroup
				}
				profile := sourceKind + "_" + purpose + "_v1"
				hRef := "h1-" + strings.TrimPrefix(ref, "s1-")
				results = append(results, map[string]any{
					"reference": ref,
					"status":    "resolved",
					"header": map[string]any{
						"header_version":         1,
						"header_reference":       hRef,
						"message_id":             row.messageID,
						"source":                 map[string]any{"kind": sourceKind, "id": row.channelID},
						"parent_group":           parentGroup,
						"space_id":               "space",
						"author_uid":             row.fromUID,
						"author_kind":            "human",
						"recipient_bot_uid":      "bot",
						"native_message_version": "x0_0",
						"payload_sha256":         row.payloadSHA256,
						"delivery": map[string]any{
							"stream_id":          "im_" + strconv.Itoa(row.channelType) + "_" + row.channelID,
							"stream_incarnation": strings.Repeat("3", 32),
							"sequence":           row.messageSeq,
						},
					},
					"profile":        profile,
					"authority_refs": nonces,
					"predicates":     predicatesForProfile(profile, row.eligible && !row.revoked),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema_version":  1,
				"profile_version": 1,
				"key":             f.bundle.Key,
				"request_id":      req["request_id"],
				"read_id":         nextHex32(),
				"observed_at":     now,
				"authorities":     auths,
				"results":         results,
			})
		case "/v1/bot/docs/search":
			docsSearchCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total":      1,
				"items":      []map[string]any{{"docId": "doc_async_1", "title": "Async Ingress Separated Doc"}},
				"nextCursor": "",
			})
		case "/v1/bot/sendMessage":
			sendCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message_id":"msg_reply_async_1"}`))
		case "/v1/bot/docs", "/v1/bot/docs/board_1/scene", "/v1/bot/docs/doc_1/scene":
			deniedWriteDispatches.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			baseServe(w, r)
		}
	})

	// 1. Direct NativeReader unit verification of Wire Contract A (BindLiveIngress) and Wire Contract C (EvaluateCurrentAccess).
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}
	selfReader, err := permission.NewSelfPolicyReader(f.backend.URL, "bot", "bf_managed", tlsCfg, f.backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	nativeReader, err := permission.NewNativeReader(selfReader, map[string]permission.AuthorityPin{
		"im":  {Kind: "im", Incarnation: strings.Repeat("3", 32)},
		"sql": {Kind: "sql", Incarnation: strings.Repeat("4", 32)},
	})
	if err != nil {
		t.Fatal(err)
	}
	dmPayloadSHA := strings.Repeat("a", 64)
	dmBinding, err := nativeReader.BindLiveIngress(ctx, f.bundle.Key, permission.LiveIngressRequest{
		ChannelType:           1,
		ChannelID:             "alice", // bare peer UID on live WS frame -> canonicalized to alice@bot
		MessageID:             "7001",
		MessageSeq:            "101",
		FromUID:               "alice",
		ObservedPayloadSHA256: dmPayloadSHA,
	})
	if err != nil {
		t.Fatalf("BindLiveIngress failed on unpersisted live DM: %v", err)
	}
	expectedDMPair, err := permission.CanonicalDMPairID("bot", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if dmBinding.TriggerRowPersisted || dmBinding.ContentAttestedByServer || dmBinding.Header.Source.Kind != "dm" || dmBinding.Header.Source.ID != expectedDMPair || dmBinding.NativeMessageVersion != permission.LiveUnpersistedMessageVersion {
		t.Fatalf("unexpected DM LiveIngressBinding: %+v", dmBinding)
	}
	currentFact, err := nativeReader.EvaluateCurrentAccess(ctx, f.bundle.Key, permission.CurrentAccessRequest{
		Purpose:               "admit",
		ChannelType:           1,
		ChannelID:             "alice",
		CallerUID:             "alice",
		MessageID:             "7001",
		MessageSeq:            "101",
		ObservedPayloadSHA256: dmPayloadSHA,
	})
	if err != nil || !currentFact.Eligible || currentFact.Subject.Source != expectedDMPair {
		t.Fatalf("EvaluateCurrentAccess failed on unpersisted live DM: err=%v fact=%+v", err, currentFact)
	}
	// Also verify NativeReader.Resolve with purpose="reply" on the l1- live reference.
	replyObs, err := nativeReader.Resolve(ctx, f.bundle.Key, "reply", []string{dmBinding.DeliveryReference})
	if err != nil || len(replyObs) != 1 || !replyObs[0].IsLiveIngress() || replyObs[0].Header().Source.ID != expectedDMPair {
		t.Fatalf("Resolve(purpose=reply) failed on unpersisted live DM l1- reference: err=%v obs=%+v", err, replyObs)
	}
	if bindLiveIngressCalls.Load() != 1 || evaluateCurrentAccessCalls.Load() != 1 || liveReplyResolveCalls.Load() != 1 || triggerRowSQLReads.Load() != 0 {
		t.Fatalf("unexpected direct wire call counts: bind=%d eval=%d replyResolve=%d triggerSQL=%d", bindLiveIngressCalls.Load(), evaluateCurrentAccessCalls.Load(), liveReplyResolveCalls.Load(), triggerRowSQLReads.Load())
	}

	// 2. Start native octo-cli permission host subprocess and verify full end-to-end turn flows.
	reserveAddr := func() string {
		l, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			t.Fatal(lerr)
		}
		addr := l.Addr().String()
		_ = l.Close()
		return addr
	}
	posDir := filepath.Join(dir, "host_async")
	logDir := filepath.Join(posDir, "logs")
	homeDir := filepath.Join(posDir, "home")
	for _, d := range []string{posDir, logDir, homeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	readyPath := filepath.Join(posDir, "ready.json")
	cAddr := reserveAddr()
	rAddr := reserveAddr()
	relayURL := "https://" + rAddr
	hostCfg := map[string]any{
		"version":               1,
		"origin":                f.backend.URL,
		"bot_uid":               "bot",
		"recipient_id":          "endpoint-1",
		"endpoint_incarnation":  strings.Repeat("5", 32),
		"peer_sha256":           peerHash,
		"control_address":       cAddr,
		"relay_address":         rAddr,
		"relay_url":             relayURL,
		"certificate":           certPath,
		"private_key":           keyPath,
		"peer_ca":               caPath,
		"release_path":          manifestPath,
		"release_sha256":        pin.Digest,
		"ready_path":            readyPath,
		"relay_executable":      cliBin,
		"relay_ca":              caPath,
		"relay_cert_sha256":     leafSHA,
		"relay_server_name":     "localhost",
		"isolated_test_catalog": true,
		"catalog_mode":          IsolatedTestCatalogMode,
		"authority_pins": map[string]any{
			"im":  map[string]any{"kind": "im", "incarnation": strings.Repeat("3", 32)},
			"sql": map[string]any{"kind": "sql", "incarnation": strings.Repeat("4", 32)},
		},
	}
	cfgBytes, err := permission.PrivateJSON(hostCfg)
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
		t.Fatalf("native octo-cli did not publish valid ready.json: %+v", ready)
	}

	conn, err := tls.Dial("tcp", cAddr, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   "localhost",
		Certificates: []tls.Certificate{clientCert},
	})
	if err != nil {
		t.Fatalf("control mTLS dial failed: %v", err)
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
		"endpoint_incarnation": hostCfg["endpoint_incarnation"],
		"configured_origin":    hostCfg["origin"],
		"recipient_id":         hostCfg["recipient_id"],
	})

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS13,
				RootCAs:    roots,
				ServerName: "localhost",
			},
		},
	}
	postHTTP := func(token string, req rpcRequest) (rpcResponse, int) {
		t.Helper()
		raw, _ := json.Marshal(req)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			t.Fatalf("HTTP relay request failed: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusAccepted && len(body) == 0 {
			return rpcResponse{}, resp.StatusCode
		}
		var out rpcResponse
		if json.Unmarshal(body, &out) != nil {
			t.Fatalf("invalid HTTP relay JSON response (status=%d): %s", resp.StatusCode, string(body))
		}
		return out, resp.StatusCode
	}
	makeToolReq := func(id int, decl permission.DeclarationV2, toolName, opID string, args map[string]any) rpcRequest {
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
		if err := relayDeclaration(&req, decl); err != nil {
			t.Fatal(err)
		}
		return req
	}

	// =========================================================================
	// Scenario 1: Live DM turn where the trigger message row is NEVER inserted
	// into MySQL (delayed/omitted indefinitely).
	// Covers:
	// - register_ingress with live_ingress (auto-invoking BindLiveIngress) AND
	//   with pre-bound l1- delivery_reference
	// - admit -> bind_context (with l2- NativeHeader in manifest.originals) ->
	//   open_relay (HTTP) -> allowed tool call (docs.search) + ordinary reply (message.send)
	// - Zero trigger-row SQL reads and zero purpose="contributor" calls for the live trigger!
	// =========================================================================
	dmStream := map[string]any{
		"stream_id":          "im_1_" + expectedDMPair,
		"stream_incarnation": strings.Repeat("3", 32),
		"sequence":           "101",
	}
	ingDM := requestOK("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": dmBinding.DeliveryReference,
		"stream":             dmStream,
		"payload_sha256":     dmPayloadSHA,
		"header":             dmBinding.Header,
		"provenance":         permission.LiveChannelACLProvenance,
	})
	// Verify register_ingress response has exact key ["ingress_id"] for codex-channel-octo compatibility.
	if len(ingDM) != 1 || ingDM["ingress_id"] == "" {
		t.Fatalf("register_ingress must return exact keys [ingress_id], got %v", ingDM)
	}

	// Duplicate/redelivery check: re-registering the same live delivery (either via l1- or via s1- with same delivery+message_id+payload_sha256) is rejected!
	dupLiveResp := sendControlFrame("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": dmBinding.DeliveryReference,
		"stream":             dmStream,
		"payload_sha256":     dmPayloadSHA,
	})
	if dupLiveResp["status"] != "unavailable" || dupLiveResp["payload"].(map[string]any)["reason"] != "authority_conflict" {
		t.Fatalf("expected duplicate live register_ingress to fail with authority_conflict, got %v", dupLiveResp)
	}
	// Also test redelivery after async persistence (s1-... reference for the same message_id=7001, sequence=101, payload_sha256, with native_message_version="x0_0"):
	// Because register_ingress fences both versioned and version-independent message identities, the s1- redelivery is also rejected with authority_conflict!
	stateMu.Lock()
	persistedRows["s1-persisted-dm-7001"] = storedRow{
		channelType:   1,
		channelID:     expectedDMPair,
		messageID:     "7001",
		messageSeq:    "101",
		fromUID:       "alice",
		payloadSHA256: dmPayloadSHA,
		eligible:      true,
	}
	stateMu.Unlock()
	dupPersistedResp := sendControlFrame("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": "s1-persisted-dm-7001",
		"stream":             dmStream,
		"payload_sha256":     dmPayloadSHA,
	})
	if dupPersistedResp["status"] != "unavailable" || dupPersistedResp["payload"].(map[string]any)["reason"] != "authority_conflict" {
		t.Fatalf("expected post-persistence s1- redelivery of live message 7001 to fail with authority_conflict, got %v", dupPersistedResp)
	}
	// Reset triggerRowSQLReads after the deliberate s1- redelivery negative check above.
	triggerRowSQLReads.Store(0)

	ctxDM := permission.NewID()
	admDM := requestOK("admit", map[string]any{
		"ingress_id":          ingDM["ingress_id"],
		"proposed_context_id": ctxDM,
	})
	manDM := map[string]any{
		"context_id":         ctxDM,
		"context_generation": admDM["generation"],
		"originals":          []permission.NativeHeader{dmBinding.Header},
	}
	mDMBytes, _ := permission.PrivateJSON(manDM)
	manDM["bytes"] = len(mDMBytes)
	manDM["sha256"] = sha256Sum(mDMBytes)
	requestOK("bind_context", map[string]any{
		"capsule_id": admDM["capsule_id"],
		"manifest":   manDM,
	})
	httpRelayDM := requestOK("open_relay", map[string]any{
		"capsule_id":        admDM["capsule_id"],
		"recipient_id":      "endpoint-1",
		"transport":         "http",
		"relay_nonce":       permission.NewID(),
		"include_tls_trust": true,
	})
	dmToken := httpRelayDM["handle"].(string)

	if initResp, code := postHTTP(dmToken, initializedRequest()); code != http.StatusOK || initResp.Error != nil {
		t.Fatalf("DM HTTP initialize failed: code=%d err=%v", code, initResp.Error)
	}
	dmDecl := permission.DeclarationV2{
		Sender:      "alice",
		Channel:     expectedDMPair,
		ChannelType: 1,
		SpaceID:     "space",
		MessageID:   "7001",
	}
	rDocDM, _ := postHTTP(dmToken, makeToolReq(1, dmDecl, "execute", "docs.search", map[string]any{"q": "live dm"}))
	if rDocDM.Error != nil || !bytes.Contains(rDocDM.Result, []byte("Async Ingress Separated Doc")) {
		t.Fatalf("live DM docs.search failed: err=%v result=%s", rDocDM.Error, string(rDocDM.Result))
	}
	// Ordinary reply (message.send) on the unpersisted live DM turn:
	// ManagedTurn.Reply resolves g.original.reference ("l1-...") with purpose="reply"
	// and sends to the canonical DM pair without any MySQL trigger-row lookup!
	rSendDM, _ := postHTTP(dmToken, makeToolReq(2, dmDecl, "call_op", "message.send", map[string]any{
		"channel_id":   "SpoofedTargetIgnoredByReplyRebuild",
		"channel_type": 1,
		"payload":      map[string]any{"text": "live dm reply without trigger row"},
	}))
	if rSendDM.Error != nil || !bytes.Contains(rSendDM.Result, []byte("msg_reply_async_1")) {
		t.Fatalf("live DM ordinary reply message.send failed: err=%v result=%s", rSendDM.Error, string(rSendDM.Result))
	}
	if triggerRowSQLReads.Load() != 0 || liveContributorPurposeCalls.Load() != 0 || liveReplyResolveCalls.Load() != 1 {
		t.Fatalf("live DM turn must have 0 trigger-row SQL reads and 0 live contributor-purpose calls: triggerSQL=%d liveContrib=%d liveReply=%d",
			triggerRowSQLReads.Load(), liveContributorPurposeCalls.Load(), liveReplyResolveCalls.Load())
	}

	// =========================================================================
	// Scenario 2: Forged or model-supplied context / wrong binding / l2- passed
	// as delivery_reference / unpersisted l1- passed as historical contributor
	// -> all rejected fail-closed!
	// =========================================================================
	// 2a. Forged _meta.octo declaration on HTTP relay (wrong sender or wrong message_id) -> -32007 context_untrusted.
	forgedDecl := dmDecl
	forgedDecl.Sender = "mallory"
	forgedResp, _ := postHTTP(dmToken, makeToolReq(3, forgedDecl, "execute", "docs.search", map[string]any{"q": "forged"}))
	if forgedResp.Error == nil || forgedResp.Error.Code != -32007 || !strings.Contains(fmt.Sprint(forgedResp.Error.Data), "context_untrusted") {
		t.Fatalf("expected forged _meta.octo sender to fail with -32007 context_untrusted, got %+v", forgedResp)
	}
	// 2b. Passing l2- header reference as delivery_reference in register_ingress -> refused.
	l2AsDeliveryResp := sendControlFrame("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": dmBinding.HeaderReference,
		"stream":             dmStream,
		"payload_sha256":     dmPayloadSHA,
	})
	if l2AsDeliveryResp["status"] == "ok" {
		t.Fatalf("expected l2- header reference to be rejected as delivery_reference, got %v", l2AsDeliveryResp)
	}
	// 2c. Passing mismatched live_ingress alongside delivery_reference in register_ingress -> authority_conflict.
	mismatchLiveResp := sendControlFrame("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": dmBinding.DeliveryReference,
		"live_ingress": map[string]any{
			"channel_type":            1,
			"channel_id":              "bob",
			"message_id":              "7001",
			"message_seq":             "101",
			"from_uid":                "bob",
			"observed_payload_sha256": dmPayloadSHA,
		},
		"stream":         dmStream,
		"payload_sha256": dmPayloadSHA,
	})
	if mismatchLiveResp["status"] != "unavailable" || mismatchLiveResp["payload"].(map[string]any)["reason"] != "authority_conflict" {
		t.Fatalf("expected mismatched live_ingress to fail with authority_conflict, got %v", mismatchLiveResp)
	}
	// 2d. Passing an l1- or l2- live token to resolve_contributors -> predicate_unsupported.
	liveContribResp := sendControlFrame("resolve_contributors", map[string]any{
		"capsule_id":          admDM["capsule_id"],
		"delivery_references": []string{dmBinding.DeliveryReference},
	})
	if liveContribResp["status"] != "unavailable" || liveContribResp["payload"].(map[string]any)["reason"] != "predicate_unsupported" {
		t.Fatalf("expected resolve_contributors to reject l1- live reference with predicate_unsupported, got %v", liveContribResp)
	}

	requestOK("close", map[string]any{"capsule_id": admDM["capsule_id"], "reason": "completed"})

	// =========================================================================
	// Scenario 3: Live Group turn (channel_type=2) with a valid stored historical
	// contributor (s1-stored-hist-10) over child `octo-cli --turn-relay` stdio,
	// followed by mid-turn member revocation and async persisted-row conflict.
	// =========================================================================
	grpPayloadSHA := strings.Repeat("c", 64)
	grpStream := map[string]any{
		"stream_id":          "im_2_group",
		"stream_incarnation": strings.Repeat("3", 32),
		"sequence":           "202",
	}
	// Register live group ingress using `live_ingress` object (host calls BindLiveIngress + Resolve).
	ingGrp := requestOK("register_ingress", map[string]any{
		"key": f.bundle.Key,
		"live_ingress": map[string]any{
			"channel_type":            2,
			"channel_id":              "group",
			"message_id":              "8002",
			"message_seq":             "202",
			"from_uid":                "alice",
			"observed_payload_sha256": grpPayloadSHA,
		},
		"stream":         grpStream,
		"payload_sha256": grpPayloadSHA,
	})
	ctxGrp := permission.NewID()
	admGrp := requestOK("admit", map[string]any{
		"ingress_id":          ingGrp["ingress_id"],
		"proposed_context_id": ctxGrp,
	})

	// Resolve stored historical contributor s1-stored-hist-10 via resolve_contributors.
	// Also verify missing stored historical contributor (s1-missing-hist-99) fails closed with mirror_lag!
	missingContribResp := sendControlFrame("resolve_contributors", map[string]any{
		"capsule_id":          admGrp["capsule_id"],
		"delivery_references": []string{"s1-missing-hist-99"},
	})
	if missingContribResp["status"] == "ok" {
		t.Fatalf("expected missing stored historical contributor to fail closed, got %v", missingContribResp)
	}
	resolvedContribs := requestOK("resolve_contributors", map[string]any{
		"capsule_id":          admGrp["capsule_id"],
		"delivery_references": []string{"s1-stored-hist-10"},
	})
	contribHeadersRaw := resolvedContribs["headers"].([]any)
	if len(contribHeadersRaw) != 1 {
		t.Fatalf("expected 1 resolved stored contributor header, got %d", len(contribHeadersRaw))
	}

	grpL2Ref, err := permission.EncodeLiveIngressReference("l2", 2, "group", "8002", "202", "alice", grpPayloadSHA)
	if err != nil {
		t.Fatal(err)
	}
	grpLiveHeader := map[string]any{
		"header_version":         1,
		"header_reference":       grpL2Ref,
		"message_id":             "8002",
		"source":                 map[string]any{"kind": "group", "id": "group"},
		"parent_group":           nil,
		"space_id":               "space",
		"author_uid":             "alice",
		"author_kind":            "human",
		"recipient_bot_uid":      "bot",
		"native_message_version": permission.LiveUnpersistedMessageVersion,
		"payload_sha256":         grpPayloadSHA,
		"delivery":               grpStream,
	}
	manGrp := map[string]any{
		"context_id":         ctxGrp,
		"context_generation": admGrp["generation"],
		"originals":          []any{grpLiveHeader, contribHeadersRaw[0]},
	}
	mGrpBytes, _ := permission.PrivateJSON(manGrp)
	manGrp["bytes"] = len(mGrpBytes)
	manGrp["sha256"] = sha256Sum(mGrpBytes)
	requestOK("bind_context", map[string]any{
		"capsule_id": admGrp["capsule_id"],
		"manifest":   manGrp,
	})

	stdioRelayGrp := requestOK("open_relay", map[string]any{
		"capsule_id":        admGrp["capsule_id"],
		"recipient_id":      "endpoint-1",
		"transport":         "stdio",
		"relay_nonce":       permission.NewID(),
		"include_tls_trust": true,
	})
	stdioHandle := stdioRelayGrp["handle"].(string)

	grpDecl := permission.DeclarationV2{
		Sender:      "alice",
		Channel:     "group",
		ChannelType: 2,
		SpaceID:     "space",
		MessageID:   "8002",
	}
	runStdioLines := func(reqs ...rpcRequest) []string {
		t.Helper()
		var in bytes.Buffer
		for _, r := range reqs {
			b, _ := json.Marshal(r)
			in.Write(b)
			in.WriteByte('\n')
		}
		cmd := exec.CommandContext(ctx, cliBin, "--turn-relay", stdioHandle)
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + homeDir,
			"TMPDIR=" + posDir,
			"SSL_CERT_FILE=" + caPath,
		}
		cmd.Stdin = &in
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		if rerr := cmd.Run(); rerr != nil {
			t.Fatalf("stdio --turn-relay failed: err=%v stderr=%s stdout=%s", rerr, errBuf.String(), out.String())
		}
		return strings.Split(strings.TrimSpace(out.String()), "\n")
	}

	stdioLines := runStdioLines(
		initializedRequest(),
		makeToolReq(10, grpDecl, "execute", "docs.search", map[string]any{"q": "group live stdio"}),
		makeToolReq(11, grpDecl, "call_op", "message.send", map[string]any{"channel_id": "group", "channel_type": 2, "payload": map[string]any{"text": "group live reply"}}),
		makeToolReq(12, grpDecl, "execute", "docs.create", map[string]any{"title": "denied write"}),
	)
	if len(stdioLines) != 4 {
		t.Fatalf("expected 4 stdio responses, got %d: %v", len(stdioLines), stdioLines)
	}
	if !strings.Contains(stdioLines[1], "Async Ingress Separated Doc") || !strings.Contains(stdioLines[2], "msg_reply_async_1") {
		t.Fatalf("unexpected stdio live group positive responses:\n1=%s\n2=%s", stdioLines[1], stdioLines[2])
	}
	if !strings.Contains(stdioLines[3], "-32007") || !strings.Contains(stdioLines[3], "capability_denied") || deniedWriteDispatches.Load() != 0 {
		t.Fatalf("expected stdio denied write to fail with -32007 capability_denied and 0 dispatches: %s", stdioLines[3])
	}
	if triggerRowSQLReads.Load() != 0 || liveContributorPurposeCalls.Load() != 0 || storedContributorResolveCalls.Load() < 2 {
		t.Fatalf("expected 0 trigger-row SQL reads, 0 live contributor calls, and >=2 stored contributor resolves, got triggerSQL=%d liveContrib=%d storedContrib=%d",
			triggerRowSQLReads.Load(), liveContributorPurposeCalls.Load(), storedContributorResolveCalls.Load())
	}

	// =========================================================================
	// Scenario 4: Revocation / async conflict between admission and tool call
	// or ordinary reply.
	// - 4a: Temporary header immutability violation (server changes native_message_version
	//   on l2- header mid-turn) -> fails closed with policy_unavailable (does not retire generation).
	// - 4b: Stage 2 capability revocation between admission and ordinary reply (`message.send`)
	//   -> `message.send` fails closed with -32007 capability_denied and 0 upstream dispatches.
	// - 4c: Async mirror persists a revoked/digest-mismatched row for message 8002
	//   -> next tool call fails closed with -32007 invocation_denied and permanently
	//   retires the turn generation so subsequent calls fail with -32007 context_untrusted!
	// =========================================================================
	beforeDocs := docsSearchCalls.Load()
	beforeSend := sendCalls.Load()

	// 4a: Simulate server header immutability violation (server changes native_message_version on l2- header mid-turn) -> fails closed with policy_unavailable!
	stateMu.Lock()
	tamperNextLiveHeader = true
	stateMu.Unlock()
	tamperLines := runStdioLines(
		makeToolReq(20, grpDecl, "execute", "docs.search", map[string]any{"q": "after header tamper"}),
	)
	if !strings.Contains(tamperLines[0], "-32007") || !strings.Contains(tamperLines[0], "policy_unavailable") || docsSearchCalls.Load() != beforeDocs {
		t.Fatalf("expected mid-turn header immutability violation on l2- to fail closed with policy_unavailable and 0 dispatches, got: %s", tamperLines[0])
	}
	stateMu.Lock()
	tamperNextLiveHeader = false
	stateMu.Unlock()

	// 4b: Policy revocation of message.send between admission and ordinary reply -> -32007 capability_denied (Stage 2 refusal does not retire generation).
	f.mu.Lock()
	f.bundle.Revision = "1"
	f.bundle.Capability.Rules = []permission.Rule{
		{
			"rule_id":      "allow_fixture_all_convs",
			"effect":       "allow",
			"caller":       map[string]any{"kind": "all_humans"},
			"source":       map[string]any{"kind": "all_in_space", "conversation_types": []any{"dm", "group", "thread"}},
			"capabilities": allowedFixtureCaps,
		},
		{
			"rule_id":      "deny_writes_and_send",
			"effect":       "deny",
			"caller":       map[string]any{"kind": "all_humans"},
			"source":       map[string]any{"kind": "all_in_space", "conversation_types": []any{"dm", "group", "thread"}},
			"capabilities": append(append([]string{}, IsolatedTestFixturePolicy().DeniedCapabilities...), "octo-cli/message/op/message.send"),
		},
	}
	f.save()
	f.mu.Unlock()

	revokedReplyLines := runStdioLines(
		makeToolReq(21, grpDecl, "call_op", "message.send", map[string]any{"channel_id": "group", "channel_type": 2, "payload": map[string]any{"text": "revoked reply"}}),
	)
	if !strings.Contains(revokedReplyLines[0], "-32007") || !strings.Contains(revokedReplyLines[0], "capability_denied") || sendCalls.Load() != beforeSend {
		t.Fatalf("expected mid-turn policy revocation of message.send to fail with -32007 capability_denied and 0 dispatches, got: %s", revokedReplyLines[0])
	}

	// 4c: Async mirror persists a revoked row for live trigger message 8002 -> next action fails with invocation_denied and permanently retires the generation (subsequent call gets context_untrusted)!
	stateMu.Lock()
	persistedRows["msg:8002"] = storedRow{
		channelType:   2,
		channelID:     "group",
		messageID:     "8002",
		messageSeq:    "202",
		fromUID:       "alice",
		payloadSHA256: grpPayloadSHA,
		revoked:       true,
		eligible:      false,
	}
	stateMu.Unlock()

	revokedLines := runStdioLines(
		makeToolReq(22, grpDecl, "execute", "docs.search", map[string]any{"q": "after async revoke"}),
		makeToolReq(23, grpDecl, "call_op", "message.send", map[string]any{"channel_id": "group", "channel_type": 2, "payload": map[string]any{"text": "retired generation"}}),
	)
	if !strings.Contains(revokedLines[0], "-32007") || !strings.Contains(revokedLines[0], "invocation_denied") ||
		!strings.Contains(revokedLines[1], "-32007") || !strings.Contains(revokedLines[1], "context_untrusted") {
		t.Fatalf("expected async-revoked trigger row to deny first call with invocation_denied and retire generation (second call context_untrusted):\n0=%s\n1=%s", revokedLines[0], revokedLines[1])
	}
	if docsSearchCalls.Load() != beforeDocs || sendCalls.Load() != beforeSend {
		t.Fatalf("expected 0 upstream dispatches when live trigger row is revoked mid-turn")
	}

	requestOK("close", map[string]any{"capsule_id": admGrp["capsule_id"], "reason": "completed"})

	// =========================================================================
	// Scenario 4d: Stored historical contributor revocation mid-turn on a fresh
	// live turn -> Stage 1 contributor check fails with -32007 context_untrusted!
	// =========================================================================
	grp2PayloadSHA := strings.Repeat("e", 64)
	grp2Stream := map[string]any{
		"stream_id":          "im_2_group",
		"stream_incarnation": strings.Repeat("3", 32),
		"sequence":           "203",
	}
	ingGrp2 := requestOK("register_ingress", map[string]any{
		"key": f.bundle.Key,
		"live_ingress": map[string]any{
			"channel_type":            2,
			"channel_id":              "group",
			"message_id":              "8003",
			"message_seq":             "203",
			"from_uid":                "alice",
			"observed_payload_sha256": grp2PayloadSHA,
		},
		"stream":         grp2Stream,
		"payload_sha256": grp2PayloadSHA,
	})
	ctxGrp2 := permission.NewID()
	admGrp2 := requestOK("admit", map[string]any{
		"ingress_id":          ingGrp2["ingress_id"],
		"proposed_context_id": ctxGrp2,
	})
	grp2L2Ref, err := permission.EncodeLiveIngressReference("l2", 2, "group", "8003", "203", "alice", grp2PayloadSHA)
	if err != nil {
		t.Fatal(err)
	}
	grp2LiveHeader := map[string]any{
		"header_version":         1,
		"header_reference":       grp2L2Ref,
		"message_id":             "8003",
		"source":                 map[string]any{"kind": "group", "id": "group"},
		"parent_group":           nil,
		"space_id":               "space",
		"author_uid":             "alice",
		"author_kind":            "human",
		"recipient_bot_uid":      "bot",
		"native_message_version": permission.LiveUnpersistedMessageVersion,
		"payload_sha256":         grp2PayloadSHA,
		"delivery":               grp2Stream,
	}
	manGrp2 := map[string]any{
		"context_id":         ctxGrp2,
		"context_generation": admGrp2["generation"],
		"originals":          []any{grp2LiveHeader, contribHeadersRaw[0]},
	}
	mGrp2Bytes, _ := permission.PrivateJSON(manGrp2)
	manGrp2["bytes"] = len(mGrp2Bytes)
	manGrp2["sha256"] = sha256Sum(mGrp2Bytes)
	requestOK("bind_context", map[string]any{
		"capsule_id": admGrp2["capsule_id"],
		"manifest":   manGrp2,
	})
	httpRelayGrp2 := requestOK("open_relay", map[string]any{
		"capsule_id":   admGrp2["capsule_id"],
		"recipient_id": "endpoint-1",
		"transport":    "http",
		"relay_nonce":  permission.NewID(),
	})
	grp2Token := httpRelayGrp2["handle"].(string)
	if initResp, code := postHTTP(grp2Token, initializedRequest()); code != http.StatusOK || initResp.Error != nil {
		t.Fatalf("Grp2 HTTP initialize failed: code=%d err=%v", code, initResp.Error)
	}
	// Now revoke historical contributor s1-stored-hist-10 before tool execution.
	stateMu.Lock()
	histRow := persistedRows["s1-stored-hist-10"]
	histRow.revoked = true
	persistedRows["s1-stored-hist-10"] = histRow
	stateMu.Unlock()
	grp2Decl := permission.DeclarationV2{
		Sender:      "alice",
		Channel:     "group",
		ChannelType: 2,
		SpaceID:     "space",
		MessageID:   "8003",
	}
	histRevokedResp, _ := postHTTP(grp2Token, makeToolReq(24, grp2Decl, "execute", "docs.search", map[string]any{"q": "after hist contributor revoke"}))
	if histRevokedResp.Error == nil || histRevokedResp.Error.Code != -32007 || !strings.Contains(fmt.Sprint(histRevokedResp.Error.Data), "context_untrusted") || docsSearchCalls.Load() != beforeDocs {
		t.Fatalf("expected revoked historical contributor to deny turn with -32007 context_untrusted and 0 dispatches, got %+v", histRevokedResp)
	}
	requestOK("close", map[string]any{"capsule_id": admGrp2["capsule_id"], "reason": "completed"})

	// =========================================================================
	// Scenario 5: Policy/member revocation BEFORE admission on a live turn
	// -> admit refuses with invocation_denied before any capsule/relay is opened.
	// =========================================================================
	stateMu.Lock()
	liveMemberEligible["bob"] = false
	stateMu.Unlock()
	bobPayloadSHA := strings.Repeat("d", 64)
	ingBob := requestOK("register_ingress", map[string]any{
		"key": f.bundle.Key,
		"live_ingress": map[string]any{
			"channel_type":            5,
			"channel_id":              "thread_1",
			"message_id":              "9003",
			"message_seq":             "303",
			"from_uid":                "bob",
			"observed_payload_sha256": bobPayloadSHA,
		},
		"stream": map[string]any{
			"stream_id":          "im_5_thread_1",
			"stream_incarnation": strings.Repeat("3", 32),
			"sequence":           "303",
		},
		"payload_sha256": bobPayloadSHA,
	})
	admitBobResp := sendControlFrame("admit", map[string]any{
		"ingress_id":          ingBob["ingress_id"],
		"proposed_context_id": permission.NewID(),
	})
	if admitBobResp["status"] != "refused" || admitBobResp["payload"].(map[string]any)["category"] != "invocation_denied" {
		t.Fatalf("expected non-member live thread ingress to be refused at admit with invocation_denied, got %v", admitBobResp)
	}

	// =========================================================================
	// Scenario 6: Exact XIN-2858 revision 9 (`fixture.py allow-write` / `permitted writes_allowed`)
	// regression test for requests d7c39fab1af626ba0b2172069c609f58 (live DM) and
	// c040f3ec27472c729af593e03d4d6883 (live Group), plus revision 10 (`fixture.py deny`).
	// - Under revision 9 (`allow-write`), `capability.rules` includes write capabilities
	//   (`octo-cli/docs/op/docs.create`, `octo-cli/docs/op/docs.scene.edit`) in an `allow` rule.
	// - Stage 1 `admit` MUST succeed (no longer refusing with `policy_unavailable`!),
	//   allowed read (`docs.search`) and ordinary reply (`message.send`) succeed with
	//   zero trigger-row SQL reads, while Stage 2 isolated-test fixture containment
	//   still refuses `docs.create` / `docs.scene.edit` with `-32007 capability_denied`
	//   and 0 upstream dispatches.
	// - Under revision 10 (`fixture.py deny`), Stage 1 `admit` refuses with `invocation_denied`
	//   (not `policy_unavailable`).
	// =========================================================================
	f.mu.Lock()
	f.bundle.Revision = "9"
	f.bundle.Invocation.State = "E"
	f.bundle.Invocation.Rules = []permission.Rule{
		{
			"rule_id": "allow_human_all_convs",
			"effect":  "allow",
			"caller":  map[string]any{"kind": "all_humans"},
			"source":  map[string]any{"kind": "all_in_space", "conversation_types": []any{"dm", "group", "thread"}},
		},
	}
	f.bundle.Capability.State = "E"
	allowWriteCaps := append([]string{}, allowedFixtureCaps...)
	allowWriteCaps = append(allowWriteCaps,
		"octo-cli/docs/op/docs.create",
		"octo-cli/docs/op/docs.scene.edit",
		"octo-cli/docs/op/docs.scene.get",
		"octo-cli/loop/op/task.create",
	)
	f.bundle.Capability.Rules = []permission.Rule{
		{
			"rule_id":      "allow_write_synthetic_rev9",
			"effect":       "allow",
			"caller":       map[string]any{"kind": "all_humans"},
			"source":       map[string]any{"kind": "all_in_space", "conversation_types": []any{"dm", "group", "thread"}},
			"capabilities": allowWriteCaps,
		},
	}
	f.save()
	f.mu.Unlock()

	rev9DMPayloadSHA := "e2073b955a30164e97c311ca1ea9777b0ff4e023ed39a8e1961f439028c16d64"
	rev9DMBinding, err := nativeReader.BindLiveIngress(ctx, f.bundle.Key, permission.LiveIngressRequest{
		ChannelType:           1,
		ChannelID:             "alice",
		MessageID:             "2123350342597775360",
		MessageSeq:            "13",
		FromUID:               "alice",
		ObservedPayloadSHA256: rev9DMPayloadSHA,
	})
	if err != nil {
		t.Fatalf("BindLiveIngress failed on revision 9 live DM: %v", err)
	}
	ingRev9DM := requestOK("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": rev9DMBinding.DeliveryReference,
		"stream":             rev9DMBinding.Header.Delivery,
		"payload_sha256":     rev9DMPayloadSHA,
		"header":             rev9DMBinding.Header,
		"provenance":         permission.LiveChannelACLProvenance,
	})
	ctxRev9DM := permission.NewID()
	admRev9DM := requestOK("admit", map[string]any{
		"ingress_id":          ingRev9DM["ingress_id"],
		"proposed_context_id": ctxRev9DM,
	})
	manRev9DM := map[string]any{
		"context_id":         ctxRev9DM,
		"context_generation": admRev9DM["generation"],
		"originals":          []permission.NativeHeader{rev9DMBinding.Header},
	}
	mRev9DMBytes, _ := permission.PrivateJSON(manRev9DM)
	manRev9DM["bytes"] = len(mRev9DMBytes)
	manRev9DM["sha256"] = sha256Sum(mRev9DMBytes)
	requestOK("bind_context", map[string]any{
		"capsule_id": admRev9DM["capsule_id"],
		"manifest":   manRev9DM,
	})
	relayRev9DM := requestOK("open_relay", map[string]any{
		"capsule_id":        admRev9DM["capsule_id"],
		"recipient_id":      "endpoint-1",
		"transport":         "http",
		"relay_nonce":       permission.NewID(),
		"include_tls_trust": true,
	})
	rev9DMToken := relayRev9DM["handle"].(string)
	if initResp, code := postHTTP(rev9DMToken, initializedRequest()); code != http.StatusOK || initResp.Error != nil {
		t.Fatalf("Revision 9 DM HTTP initialize failed: code=%d err=%v", code, initResp.Error)
	}
	rev9DMDecl := permission.DeclarationV2{
		Sender:      "alice",
		Channel:     expectedDMPair,
		ChannelType: 1,
		SpaceID:     "space",
		MessageID:   "2123350342597775360",
	}
	rSearchRev9, _ := postHTTP(rev9DMToken, makeToolReq(30, rev9DMDecl, "execute", "docs.search", map[string]any{"q": "rev9 allow-write"}))
	if rSearchRev9.Error != nil || !bytes.Contains(rSearchRev9.Result, []byte("Async Ingress Separated Doc")) {
		t.Fatalf("revision 9 live DM docs.search failed: err=%v result=%s", rSearchRev9.Error, string(rSearchRev9.Result))
	}
	rCreateRev9, _ := postHTTP(rev9DMToken, makeToolReq(31, rev9DMDecl, "execute", "docs.create", map[string]any{"title": "denied even under allow-write"}))
	if rCreateRev9.Error == nil || rCreateRev9.Error.Code != -32007 || !strings.Contains(fmt.Sprint(rCreateRev9.Error.Data), "capability_denied") || deniedWriteDispatches.Load() != 0 {
		t.Fatalf("expected revision 9 allow-write docs.create to be refused by fixture capabilityAllowed with -32007 capability_denied and 0 dispatches, got %+v", rCreateRev9)
	}
	rSendRev9, _ := postHTTP(rev9DMToken, makeToolReq(32, rev9DMDecl, "call_op", "message.send", map[string]any{
		"channel_id":   expectedDMPair,
		"channel_type": 1,
		"payload":      map[string]any{"text": "rev9 dm reply"},
	}))
	if rSendRev9.Error != nil || !bytes.Contains(rSendRev9.Result, []byte("msg_reply_async_1")) {
		t.Fatalf("revision 9 live DM message.send failed: err=%v result=%s", rSendRev9.Error, string(rSendRev9.Result))
	}
	requestOK("close", map[string]any{"capsule_id": admRev9DM["capsule_id"], "reason": "completed"})

	// Also verify revision 9 live Group admission (c040f3ec27472c729af593e03d4d6883 equivalent).
	rev9GrpPayloadSHA := "a10c9c6b42094ba88f64da027e29a8c3eb61c57189b5086bfac5d882e46f7664"
	rev9GrpBinding, err := nativeReader.BindLiveIngress(ctx, f.bundle.Key, permission.LiveIngressRequest{
		ChannelType:           2,
		ChannelID:             "group",
		MessageID:             "2123350342912348160",
		MessageSeq:            "13",
		FromUID:               "alice",
		ObservedPayloadSHA256: rev9GrpPayloadSHA,
	})
	if err != nil {
		t.Fatalf("BindLiveIngress failed on revision 9 live Group: %v", err)
	}
	ingRev9Grp := requestOK("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": rev9GrpBinding.DeliveryReference,
		"stream":             rev9GrpBinding.Header.Delivery,
		"payload_sha256":     rev9GrpPayloadSHA,
		"header":             rev9GrpBinding.Header,
		"provenance":         permission.LiveChannelACLProvenance,
	})
	admRev9Grp := requestOK("admit", map[string]any{
		"ingress_id":          ingRev9Grp["ingress_id"],
		"proposed_context_id": permission.NewID(),
	})
	requestOK("close", map[string]any{"capsule_id": admRev9Grp["capsule_id"], "reason": "completed"})

	// Now verify revision 10 (`fixture.py deny` / `denied writes_denied`) refuses at Stage 1 admit with `invocation_denied` (not `policy_unavailable`).
	f.mu.Lock()
	f.bundle.Revision = "10"
	f.bundle.Invocation.State = "E"
	f.bundle.Invocation.Rules = []permission.Rule{
		{
			"rule_id": "deny_human_all_convs",
			"effect":  "deny",
			"caller":  map[string]any{"kind": "all_humans"},
			"source":  map[string]any{"kind": "all_in_space", "conversation_types": []any{"dm", "group", "thread"}},
		},
	}
	f.save()
	f.mu.Unlock()

	rev10DMPayloadSHA := strings.Repeat("9", 64)
	rev10DMBinding, err := nativeReader.BindLiveIngress(ctx, f.bundle.Key, permission.LiveIngressRequest{
		ChannelType:           1,
		ChannelID:             "alice",
		MessageID:             "2123350342999999999",
		MessageSeq:            "14",
		FromUID:               "alice",
		ObservedPayloadSHA256: rev10DMPayloadSHA,
	})
	if err != nil {
		t.Fatalf("BindLiveIngress failed on revision 10 live DM: %v", err)
	}
	ingRev10DM := requestOK("register_ingress", map[string]any{
		"key":                f.bundle.Key,
		"delivery_reference": rev10DMBinding.DeliveryReference,
		"stream":             rev10DMBinding.Header.Delivery,
		"payload_sha256":     rev10DMPayloadSHA,
		"header":             rev10DMBinding.Header,
		"provenance":         permission.LiveChannelACLProvenance,
	})
	admitRev10Resp := sendControlFrame("admit", map[string]any{
		"ingress_id":          ingRev10DM["ingress_id"],
		"proposed_context_id": permission.NewID(),
	})
	if admitRev10Resp["status"] != "refused" || admitRev10Resp["payload"].(map[string]any)["category"] != "invocation_denied" {
		t.Fatalf("expected revision 10 deny policy to refuse admit with invocation_denied, got %v", admitRev10Resp)
	}
	if triggerRowSQLReads.Load() != 0 {
		t.Fatalf("expected 0 trigger-row SQL reads across all live scenarios, got %d", triggerRowSQLReads.Load())
	}
}
