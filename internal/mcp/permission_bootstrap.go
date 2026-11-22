package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

// LoadPermissionHostConfig reads one private, immutable operator file. Its
// configured authority and release digest are not selectable from MCP traffic.
func LoadPermissionHostConfig(path string) (PermissionHostConfig, error) {
	var cfg PermissionHostConfig
	dir, err := secureDirectory(filepath.Dir(path), false)
	if err != nil {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	defer closePrivateFile(dir)
	f, err := openPrivatePermissionConfig(dir, filepath.Base(path))
	if err != nil {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	defer closePrivateFile(f)
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	fields, err := strictObjectWithOptional(b, []string{"version", "origin", "bot_uid", "recipient_id", "endpoint_incarnation", "peer_sha256", "control_address", "relay_address", "relay_url", "certificate", "private_key", "peer_ca", "release_path", "release_sha256", "authority_pins", "ready_path", "relay_executable"}, "relay_ca", "relay_ca_sha256", "relay_peer_sha256", "relay_cert_sha256", "relay_server_name", "response_budgets", "isolated_test_catalog", "catalog_mode")
	if err != nil {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if json.Unmarshal(b, &cfg) != nil || cfg.Version != 1 {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	rawIsolatedVal, hasIsolatedField := fields["isolated_test_catalog"]
	if hasIsolatedField {
		trimmed := string(bytes.TrimSpace(rawIsolatedVal))
		if trimmed != "true" && trimmed != "false" {
			return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
	}
	if _, hasModeField := fields["catalog_mode"]; hasModeField {
		if cfg.CatalogMode != IsolatedTestCatalogMode && cfg.CatalogMode != "production" {
			return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
		if hasIsolatedField && ((cfg.IsolatedTestCatalog && cfg.CatalogMode == "production") || (!cfg.IsolatedTestCatalog && cfg.CatalogMode == IsolatedTestCatalogMode)) {
			return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
	}
	envIsolated, envExplicit, err := parseIsolatedTestCatalogEnv(os.Getenv(EnvPermissionIsolatedTestCatalog))
	if err != nil {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if envExplicit {
		if envIsolated {
			if cfg.CatalogMode == "production" || (hasIsolatedField && !cfg.IsolatedTestCatalog) {
				return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
			}
			cfg.IsolatedTestCatalog = true
		} else if cfg.IsIsolatedTestCatalog() {
			return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
	}
	if cfg.IsIsolatedTestCatalog() {
		cfg.IsolatedTestCatalog = true
		if cfg.CatalogMode == "" {
			cfg.CatalogMode = IsolatedTestCatalogMode
		}
	}
	if rawBudgets, ok := fields["response_budgets"]; ok {
		if _, err := strictObject(rawBudgets, "max_acquisition_bytes", "max_page_items", "max_pages", "max_cumulative_bytes", "max_serialized_rpc_bytes"); err != nil || cfg.ResponseBudgets == nil || !cfg.ResponseBudgets.Valid() {
			return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
		if cfg.IsIsolatedTestCatalog() && !IsolatedTestResponseBudgets().Contains(*cfg.ResponseBudgets) {
			return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
	}
	u, err := url.Parse(cfg.RelayURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(cfg.ReadyPath) {
		return cfg, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	return cfg, nil
}

func permissionReadiness(incarnation string) ([]byte, error) {
	return permission.PrivateJSON(map[string]any{"control_version": 1, "coordinator_incarnation": incarnation})
}

// ReadSupportedRelease requires a complete immutable release and the exact
// operator allowlisted byte digest. Candidate/index/partial artifacts refuse.
func ReadSupportedRelease(path, expected string) (permission.Catalog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	catalog, _, err := readPermissionRelease(ctx, path, expected, supportedPermissionPackages)
	return catalog, err
}

func readPermissionRelease(ctx context.Context, path, expected string, packages map[string]permission.CatalogPin) (permission.Catalog, *permission.VerifiedCatalog, error) {
	if pin, ok := packages[expected]; ok {
		if pin.Digest != expected || !filepath.IsAbs(path) || filepath.Base(path) != "manifest.json" {
			return permission.Catalog{}, nil, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
		}
		baseline, err := PermissionCatalogBaseline()
		if err != nil {
			return permission.Catalog{}, nil, err
		}
		snapshot, err := permission.ReadCatalogPackageDirectory(ctx, filepath.Dir(path), pin, baseline, PermissionCatalogSlotValues)
		if err != nil {
			return permission.Catalog{}, nil, err
		}
		catalog := snapshot.Catalog()
		return catalog, snapshot, nil
	}
	catalog, err := readSupportedLegacyRelease(path, expected)
	return catalog, nil, err
}

func readSupportedLegacyRelease(path, expected string) (permission.Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return permission.Catalog{}, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
	}
	defer closePermissionIO(f)
	b, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return permission.Catalog{}, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != expected {
		return permission.Catalog{}, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
	}
	// Digest selection by an operator is necessary, not a proof of source
	// equivalence. Only a reviewed, compiled supported release can enable this
	// mode. There is currently no compatible finite release at the pinned source.
	supported, ok := supportedPermissionReleases[expected]
	if !ok || !bytes.Equal(b, supported.bytes) {
		return permission.Catalog{}, errors.New("PERMISSION_RELEASE_SOURCE_BOUNDS_UNPROVEN")
	}
	result := supported.catalog
	result.Capabilities = append([]string(nil), result.Capabilities...)
	return result, nil
}

func permissionReleasePageBounds(catalog permission.Catalog, snapshot *permission.VerifiedCatalog) (map[string]permission.PageBound, error) {
	if snapshot != nil {
		return PermissionCatalogPageBounds()
	}
	return supportedPermissionReleases[catalog.Digest].pageBounds, nil
}

// ServePermissionHost runs the private authenticated control and turn relay
// inside the existing MCP host process. No second unmanaged endpoint is opened.
func ServePermissionHost(ctx context.Context, cfg PermissionHostConfig, token string, build RootBuilder, admission *Admission, facade facadeMode, base cmdutil.GlobalOptions, uploadRoot string, downloads *SecretDownloads) error { //nolint:gocritic // immutable startup inputs stay together
	if cfg.IsIsolatedTestCatalog() {
		if cfg.CatalogMode != "" && cfg.CatalogMode != IsolatedTestCatalogMode {
			return errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
		if cfg.ReleaseSHA256 != IsolatedTestCatalogDigest || !filepath.IsAbs(cfg.ReleasePath) || filepath.Base(cfg.ReleasePath) != "manifest.json" {
			return errors.New("PERMISSION_RELEASE_UNAVAILABLE")
		}
		if cfg.ResponseBudgets == nil {
			b := IsolatedTestResponseBudgets()
			cfg.ResponseBudgets = &b
		} else if !IsolatedTestResponseBudgets().Contains(*cfg.ResponseBudgets) {
			return errors.New("PERMISSION_HOST_CONFIG_INVALID")
		}
		cfg.IsolatedTestCatalog = true
		cfg.CatalogMode = IsolatedTestCatalogMode
		return servePermissionHostWithPackages(ctx, cfg, token, build, admission, facade, base, uploadRoot, downloads, map[string]permission.CatalogPin{IsolatedTestCatalogDigest: IsolatedTestCatalogPin()})
	}
	if cfg.CatalogMode != "" && cfg.CatalogMode != "production" {
		return errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	return servePermissionHostWithPackages(ctx, cfg, token, build, admission, facade, base, uploadRoot, downloads, supportedPermissionPackages)
}

func servePermissionHostWithPackages(ctx context.Context, cfg PermissionHostConfig, token string, build RootBuilder, admission *Admission, facade facadeMode, base cmdutil.GlobalOptions, uploadRoot string, downloads *SecretDownloads, packages map[string]permission.CatalogPin) error { //nolint:gocritic,gocyclo // immutable startup inputs and complete verification/readiness ordering stay together
	startup, cancelStartup := context.WithTimeout(ctx, 120*time.Second)
	catalog, snapshot, err := readPermissionRelease(startup, cfg.ReleasePath, cfg.ReleaseSHA256, packages)
	cancelStartup()
	if err != nil {
		return err
	}
	if cfg.IsIsolatedTestCatalog() {
		if snapshot == nil || catalog.Version != IsolatedTestCatalogVersion || catalog.Digest != IsolatedTestCatalogDigest || snapshot.SemanticDigest() != IsolatedTestCatalogSemanticSHA256 {
			return errors.New("PERMISSION_RELEASE_UNAVAILABLE")
		}
	}
	cert, err := tls.LoadX509KeyPair(cfg.Certificate, cfg.PrivateKey)
	if err != nil || len(cert.Certificate) == 0 {
		return errors.New("PERMISSION_CONTROL_CERTIFICATE_UNAVAILABLE")
	}
	ca, err := os.ReadFile(cfg.PeerCA)
	if err != nil {
		return errors.New("PERMISSION_CONTROL_CERTIFICATE_UNAVAILABLE")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("PERMISSION_CONTROL_CERTIFICATE_UNAVAILABLE")
	}
	leafSum := sha256.Sum256(cert.Certificate[0])
	leafHex := hex.EncodeToString(leafSum[:])
	if (cfg.RelayCertSHA256 != "" && cfg.RelayCertSHA256 != leafHex) || (cfg.RelayPeerSHA256 != "" && cfg.RelayPeerSHA256 != leafHex) {
		return errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.RelayCA == "" {
		cfg.RelayCA = cfg.PeerCA
	}
	originRoots, err := x509.SystemCertPool()
	if err != nil || originRoots == nil {
		originRoots = x509.NewCertPool()
	}
	originRoots.AppendCertsFromPEM(ca)
	relayCABytes := ca
	if cfg.RelayCA != "" && cfg.RelayCA != cfg.PeerCA {
		var readErr error
		relayCABytes, readErr = os.ReadFile(cfg.RelayCA)
		if readErr != nil || !originRoots.AppendCertsFromPEM(relayCABytes) {
			return errors.New("PERMISSION_CONTROL_CERTIFICATE_UNAVAILABLE")
		}
	}
	caSum := sha256.Sum256(relayCABytes)
	caHex := hex.EncodeToString(caSum[:])
	if cfg.RelayCASHA256 != "" && cfg.RelayCASHA256 != caHex {
		return errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
	if cfg.RelayCertSHA256 == "" || cfg.RelayPeerSHA256 == "" || cfg.RelayCASHA256 == "" {
		var maxResponseBytes int64
		if cfg.ResponseBudgets != nil {
			maxResponseBytes = cfg.ResponseBudgets.MaxSerializedRPCBytes
		}
		if _, err := TurnRelayHandleWithTrust(cfg.RelayURL, strings.Repeat("a", 64), TurnRelayTrust{CAFile: cfg.RelayCA, CASHA256: caHex, PeerSHA256: leafHex, CertSHA256: leafHex, ServerName: cfg.RelayServerName, MaxResponseBytes: maxResponseBytes}); err == nil {
			cfg.RelayCASHA256 = caHex
			cfg.RelayPeerSHA256 = leafHex
			cfg.RelayCertSHA256 = leafHex
		}
	}
	policies, err := permission.NewSelfPolicyReader(cfg.Origin, cfg.BotUID, token, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: originRoots}, cfg.Origin)
	if err != nil {
		return err
	}
	native, err := permission.NewNativeReader(policies, cfg.AuthorityPins)
	if err != nil {
		return err
	}
	coordinator, err := permission.NewCoordinator(catalog, policies, native)
	if err != nil {
		return err
	}
	if snapshot != nil {
		if err := coordinator.ConfigureCatalogPackage(snapshot); err != nil {
			return err
		}
	}
	pageBounds, err := permissionReleasePageBounds(catalog, snapshot)
	if err != nil {
		return err
	}
	if err := coordinator.ConfigurePageBounds(pageBounds); err != nil {
		return err
	}
	if cfg.IsIsolatedTestCatalog() {
		if err := coordinator.ConfigureIsolatedTestFixture(IsolatedTestFixturePolicy()); err != nil {
			return err
		}
	}
	prototype, err := NewServer(build)
	if err != nil {
		return err
	}
	server := func(turn *ManagedTurn) *Server {
		s := &Server{reg: prototype.reg, mapping: prototype.mapping, build: prototype.build, protocolVersion: defaultProtocolVersion}
		s.WithManagedTurn(turn).WithAdmission(admission).WithFacade(facade).WithBaseGlobals(base).WithUploadPolicy(uploadRoot, false).WithSecretDownloads(downloads)
		s.trusted = TrustedContext{SpaceID: turn.source.SpaceID}
		s.credentialToken = token
		s.apiOrigin = cfg.Origin
		s.httpMode = true
		s.factoryFn = func(TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
			f, out, errout := s.makeFactory(s.trusted)
			f.ConfigFunc = func() (*config.Config, error) {
				return &config.Config{APIBaseURL: cfg.Origin, Token: token, SpaceID: turn.source.SpaceID}, nil
			}
			f.CredentialFunc = func() (*credential.Credential, error) {
				return &credential.Credential{Token: token, SpaceID: turn.source.SpaceID, BotKind: credential.TokenKind(token)}, nil
			}
			return f, out, errout
		}
		return s
	}
	host, err := NewPermissionHost(cfg, coordinator, policies, native, server)
	if err != nil {
		return err
	}
	control, err := tls.Listen("tcp", cfg.ControlAddress, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots})
	if err != nil {
		return err
	}
	defer closePermissionIO(control)
	relay, err := net.Listen("tcp", cfg.RelayAddress)
	if err != nil {
		return err
	}
	defer closePermissionIO(relay)
	cleanup, err := writePermissionReadiness(cfg.ReadyPath, host.Incarnation())
	if err != nil {
		return err
	}
	defer cleanup()
	secured := permission.PublicationTLSListener(relay, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})
	return host.serve(ctx, control, secured)
}

func (h *PermissionHost) serve(ctx context.Context, control, relay net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	result := make(chan error, 2)
	httpServer := &http.Server{Handler: h, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 120 * time.Second, IdleTimeout: 5 * time.Minute}
	wg.Add(2)
	go func() { defer wg.Done(); result <- httpServer.Serve(relay) }()
	go func() {
		defer wg.Done()
		for {
			conn, err := control.Accept()
			if err != nil {
				result <- err
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { mu.Lock(); delete(connections, conn); mu.Unlock(); closePermissionIO(conn) }()
				tlsConn, ok := conn.(*tls.Conn)
				if ok {
					if controlErr := h.ServeControl(ctx, tlsConn); controlErr != nil {
						return
					}
				}
			}()
		}
	}()
	var err error
	select {
	case <-ctx.Done():
	case err = <-result:
	}
	cancel()
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	closePermissionIO(control)
	closePermissionIO(relay)
	closePermissionIO(httpServer)
	mu.Lock()
	for conn := range connections {
		closePermissionIO(conn)
	}
	mu.Unlock()
	wg.Wait()
	h.handlers.Wait()
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func closePermissionIO(c io.Closer) { _ = c.Close() } //nolint:errcheck // cleanup or a disconnected protocol sink cannot be repaired by replay

type supportedPermissionRelease struct {
	bytes      []byte
	catalog    permission.Catalog
	pageBounds map[string]permission.PageBound
}

// No synthetic, partial, identity-only, or over-limit source artifact is a
// supported release. Add only complete source-compatible reviewed releases.
var supportedPermissionReleases = map[string]supportedPermissionRelease{}

// Versioned package pins are independent reviewed support inputs. Endpoint
// descriptor v2 binds release_path to this exact manifest.json and release_sha256
// to its raw canonical bytes. No format is guessed from a model or file prefix.
// Source-compatible docs/search/byte contracts and independent gates are still
// missing, so a structurally verified candidate is not added to this table.
var supportedPermissionPackages = map[string]permission.CatalogPin{}

const (
	// EnvPermissionIsolatedTestCatalog explicitly enables the bounded isolated-test
	// catalog path when starting the managed permission host. Production startup
	// leaves this unset so supportedPermissionPackages stays empty and fail-closed.
	EnvPermissionIsolatedTestCatalog = "OCTO_PERMISSION_ISOLATED_TEST_CATALOG"
	// IsolatedTestCatalogMode is the explicit PermissionHostConfig catalog_mode
	// discriminator for the bounded isolated-test catalog path.
	IsolatedTestCatalogMode = "isolated_test_v1"
	// IsolatedTestCatalogFormat is the v42 catalog package format discriminator.
	IsolatedTestCatalogFormat = permission.CatalogPackageFormat
	// IsolatedTestCatalogContractRevision is the v42 manifest contract_revision.
	IsolatedTestCatalogContractRevision = 3
	// IsolatedTestCatalogSemanticsVersion is the v42 manifest semantics_version.
	IsolatedTestCatalogSemanticsVersion = "1"
	// IsolatedTestCatalogVersion is the pinned isolated-test catalog_version.
	IsolatedTestCatalogVersion = "octo-capabilities-v1-xin2748-v4"
	// IsolatedTestCatalogDigest is the SHA-256 of docs/permission/catalog-package-v2/manifest.json (4,934 bytes).
	IsolatedTestCatalogDigest = "ecc56c2ab0a2c520ab7e5203d2abab06671daf90c28d69f88fe1de8d154109fc"
	// IsolatedTestCatalogIdentityIndexSHA256 is the identity_index_sha256 in manifest.json.
	IsolatedTestCatalogIdentityIndexSHA256 = "70cb8771d93bb38788418156029f59f35eb0031f1a8ee3b8d900eb18b673f798"
	// IsolatedTestCatalogSemanticSHA256 is the representation-neutral semantic digest
	// shared across octo-cli, octo-server, and codex-channel-octo.
	IsolatedTestCatalogSemanticSHA256 = "15c7ca2bf6df703979df529afbca6828f116b2463b1a818456edfad80bcad799"
)

// IsolatedTestCatalogPin returns the exact immutable v42 catalog package pin
// scoped exclusively to the isolated-test catalog fixture path.
func IsolatedTestCatalogPin() permission.CatalogPin {
	return permission.CatalogPin{
		Version: IsolatedTestCatalogVersion,
		Digest:  IsolatedTestCatalogDigest,
	}
}

// IsolatedTestResponseBudgets returns the bounded response budgets scoped
// exclusively to the isolated-test catalog fixture when response_budgets is
// omitted in PermissionHostConfig. Production defaults remain unselected.
func IsolatedTestResponseBudgets() permission.ResponseBudgets {
	return permission.ResponseBudgets{
		MaxAcquisitionBytes:   2097152,
		MaxPageItems:          100,
		MaxPages:              10,
		MaxCumulativeBytes:    4194304,
		MaxSerializedRPCBytes: 4194304,
	}
}

// IsolatedTestFixturePolicy returns the checked fixture policy that bounds
// runtime capability exposure in isolated-test mode to only the capabilities
// required for the four user journeys (including explicitly mapped denied
// writes/reads), while preserving the full 827-entry normative catalog artifact.
func IsolatedTestFixturePolicy() permission.IsolatedFixturePolicy {
	return permission.IsolatedFixturePolicy{
		CatalogVersion: IsolatedTestCatalogVersion,
		CatalogDigest:  IsolatedTestCatalogDigest,
		SemanticDigest: IsolatedTestCatalogSemanticSHA256,
		AllowedCapabilities: []string{
			"octo-cli/catalog/resource-list/skills",
			"octo-cli/discovery/module-map",
			"octo-cli/discovery/search-all",
			"octo-cli/docs/discovery/search",
			"octo-cli/docs/op/docs.content.get",
			"octo-cli/docs/op/docs.get",
			"octo-cli/docs/op/docs.scene.export",
			"octo-cli/docs/op/docs.search",
			"octo-cli/docs/schema/docs.content.get",
			"octo-cli/docs/schema/docs.get",
			"octo-cli/docs/schema/docs.scene.export",
			"octo-cli/docs/schema/docs.search",
			"octo-cli/message/discovery/search",
			"octo-cli/message/op/message.search",
			"octo-cli/message/op/message.send",
			"octo-cli/message/schema/message.search",
			"octo-cli/message/schema/message.send",
			"octo-cli/skills/resource/octo-docs/SKILL.md",
			"octo-cli/skills/resource/octo-messaging/SKILL.md",
		},
		DeniedCapabilities: []string{
			"octo-cli/docs/op/docs.create",
			"octo-cli/docs/op/docs.scene.edit",
			"octo-cli/docs/op/docs.scene.get",
		},
	}
}

// ReadIsolatedTestRelease loads and verifies the complete immutable v42 catalog
// package for the explicit isolated-test fixture path. It never trusts an
// arbitrary runtime digest or modifies the production support registries.
func ReadIsolatedTestRelease(path, expected string) (permission.Catalog, *permission.VerifiedCatalog, error) {
	if expected != IsolatedTestCatalogDigest || !filepath.IsAbs(path) || filepath.Base(path) != "manifest.json" {
		return permission.Catalog{}, nil, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	catalog, snapshot, err := readPermissionRelease(ctx, path, expected, map[string]permission.CatalogPin{
		IsolatedTestCatalogDigest: IsolatedTestCatalogPin(),
	})
	if err != nil {
		return permission.Catalog{}, nil, err
	}
	if snapshot == nil || catalog.Version != IsolatedTestCatalogVersion || catalog.Digest != IsolatedTestCatalogDigest || snapshot.SemanticDigest() != IsolatedTestCatalogSemanticSHA256 {
		return permission.Catalog{}, nil, errors.New("PERMISSION_RELEASE_UNAVAILABLE")
	}
	return catalog, snapshot, nil
}

func parseIsolatedTestCatalogEnv(raw string) (enabled, explicit bool, err error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, false, nil
	case "0", "false", "no", "off", "production":
		return false, true, nil
	case "1", "true", "yes", "on", IsolatedTestCatalogMode:
		return true, true, nil
	default:
		return false, true, errors.New("PERMISSION_HOST_CONFIG_INVALID")
	}
}
