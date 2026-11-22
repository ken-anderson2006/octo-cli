//go:build linux || darwin

package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
	"golang.org/x/sys/unix"
)

type strictAgentReadyRecord struct {
	ControlVersion         int    `json:"control_version"`
	CoordinatorIncarnation string `json:"coordinator_incarnation"`
}

// strictAgentPollReadiness replicates codex-channel-octo's exact privateFile(ready, 256) +
// decodeControl + object(['control_version', 'coordinator_incarnation']) validation from
// src/permissions/private-files.ts and src/permissions/coordinator-process.ts.
// Only ENOENT is treated as a retryable "not yet published" state; any 0-byte read,
// partial JSON, link count != 1, mode != 0600, or non-regular file is a fatal
// "Coordinator readiness invalid" failure.
func strictAgentPollReadiness(path string) (strictAgentReadyRecord, error) {
	var out strictAgentReadyRecord
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return out, unix.ENOENT
		}
		return out, fmt.Errorf("Coordinator readiness invalid: open failed: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer closePrivateFile(f)

	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return out, fmt.Errorf("Coordinator readiness invalid: fstat failed: %w", err)
	}
	uid := uint32(os.Geteuid())
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uid || st.Mode&0o777 != 0o600 || st.Size > 256 {
		return out, fmt.Errorf("Coordinator readiness invalid: bad stat mode=%#o nlink=%d uid=%d size=%d", st.Mode, st.Nlink, st.Uid, st.Size)
	}
	buf := make([]byte, 257)
	n, rerr := io.ReadFull(f, buf)
	if rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
		return out, fmt.Errorf("Coordinator readiness invalid: read failed: %w", rerr)
	}
	if int64(n) > 256 || int64(n) != st.Size {
		return out, fmt.Errorf("Coordinator readiness invalid: size mismatch read=%d stat=%d", n, st.Size)
	}
	raw := buf[:n]
	fields, err := strictObject(raw, "control_version", "coordinator_incarnation")
	if err != nil || len(fields) != 2 {
		return out, fmt.Errorf("Coordinator readiness invalid: strict JSON decode failed on %d bytes (%q): %v", n, string(raw), err)
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ControlVersion != 1 || !controlID(out.CoordinatorIncarnation) {
		return out, fmt.Errorf("Coordinator readiness invalid: unexpected record %+v (err=%v)", out, err)
	}
	return out, nil
}

func assertNoTempArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp.") {
			t.Fatalf("unexpected leftover temporary readiness artifact %q in %s", entry.Name(), dir)
		}
	}
}

func TestXIN2794Source92ReadinessPublicationDeterministicInterleaving(t *testing.T) {
	dir := privateHostTestDir(t)
	readyPath := filepath.Join(dir, "ready.json")
	incarnation := permission.NewID()

	// 1. Deterministically confirm the historical root cause: if ready.json is created
	// at its final path before the complete JSON payload is written (0-byte or partial write),
	// the strict Agent poller observes a non-ENOENT file and fails with "Coordinator readiness invalid".
	legacyFile, err := os.OpenFile(readyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, pollErr := strictAgentPollReadiness(readyPath); pollErr == nil || errors.Is(pollErr, unix.ENOENT) || !strings.Contains(pollErr.Error(), "Coordinator readiness invalid") {
		_ = legacyFile.Close()
		_ = os.Remove(readyPath)
		t.Fatalf("expected 0-byte in-place ready.json to trigger Coordinator readiness invalid, got %v", pollErr)
	}
	if _, werr := legacyFile.Write([]byte(`{"control_version":1`)); werr != nil {
		_ = legacyFile.Close()
		_ = os.Remove(readyPath)
		t.Fatal(werr)
	}
	if _, pollErr := strictAgentPollReadiness(readyPath); pollErr == nil || errors.Is(pollErr, unix.ENOENT) || !strings.Contains(pollErr.Error(), "Coordinator readiness invalid") {
		_ = legacyFile.Close()
		_ = os.Remove(readyPath)
		t.Fatalf("expected partial in-place ready.json to trigger Coordinator readiness invalid, got %v", pollErr)
	}
	_ = legacyFile.Close()
	if err := os.Remove(readyPath); err != nil {
		t.Fatal(err)
	}

	// 2. Deterministically interleave strictAgentPollReadiness across every stage of
	// atomic readiness publication: afterTempCreate (0 bytes staged), afterPartialWrite
	// (half payload staged), and beforeRename (full synced payload staged).
	var stagesObserved []string
	obs := &readinessPublishObserver{
		afterTempCreate: func(d *os.File, tmpName, finalName string) error {
			stagesObserved = append(stagesObserved, "afterTempCreate")
			if _, perr := strictAgentPollReadiness(readyPath); !errors.Is(perr, unix.ENOENT) {
				return fmt.Errorf("expected ENOENT at afterTempCreate, got %v", perr)
			}
			var st unix.Stat_t
			if err := unix.Fstatat(int(d.Fd()), tmpName, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0o777 != 0o600 || st.Nlink != 1 || st.Size != 0 {
				return fmt.Errorf("unexpected temp stat at afterTempCreate: %+v", st)
			}
			return nil
		},
		afterPartialWrite: func(d *os.File, tmpName, finalName string) error {
			stagesObserved = append(stagesObserved, "afterPartialWrite")
			if _, perr := strictAgentPollReadiness(readyPath); !errors.Is(perr, unix.ENOENT) {
				return fmt.Errorf("expected ENOENT at afterPartialWrite, got %v", perr)
			}
			var st unix.Stat_t
			if err := unix.Fstatat(int(d.Fd()), tmpName, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if st.Size <= 0 || st.Size >= 82 {
				return fmt.Errorf("expected partial temp file size in (0, 82), got %d", st.Size)
			}
			return nil
		},
		beforeRename: func(d *os.File, tmpName, finalName string) error {
			stagesObserved = append(stagesObserved, "beforeRename")
			if _, perr := strictAgentPollReadiness(readyPath); !errors.Is(perr, unix.ENOENT) {
				return fmt.Errorf("expected ENOENT at beforeRename, got %v", perr)
			}
			var st unix.Stat_t
			if err := unix.Fstatat(int(d.Fd()), tmpName, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if st.Size != 82 || st.Mode&0o777 != 0o600 || st.Nlink != 1 {
				return fmt.Errorf("unexpected synced temp stat at beforeRename: size=%d mode=%#o nlink=%d", st.Size, st.Mode, st.Nlink)
			}
			return nil
		},
	}

	cleanup, err := writePermissionReadinessWithObserver(readyPath, incarnation, obs)
	if err != nil {
		t.Fatalf("writePermissionReadinessWithObserver failed: %v", err)
	}
	defer cleanup()

	if strings.Join(stagesObserved, ",") != "afterTempCreate,afterPartialWrite,beforeRename" {
		t.Fatalf("unexpected interleaving stage sequence: %v", stagesObserved)
	}
	assertNoTempArtifacts(t, dir)

	rec, err := strictAgentPollReadiness(readyPath)
	if err != nil {
		t.Fatalf("strictAgentPollReadiness failed after atomic publication: %v", err)
	}
	if rec.ControlVersion != 1 || rec.CoordinatorIncarnation != incarnation {
		t.Fatalf("unexpected published readiness record: %+v", rec)
	}

	cleanup()
	if _, perr := strictAgentPollReadiness(readyPath); !errors.Is(perr, unix.ENOENT) {
		t.Fatalf("expected ENOENT after cleanup, got %v", perr)
	}
	assertNoTempArtifacts(t, dir)
}

func TestXIN2794Source92ReadinessNoClobberSymlinkAndFailureCleanup(t *testing.T) {
	dir := privateHostTestDir(t)
	readyPath := filepath.Join(dir, "ready.json")

	// 1. Existing regular file at ready.json must never be overwritten.
	sentinel := []byte(`{"sentinel":"must_not_clobber"}`)
	if err := os.WriteFile(readyPath, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writePermissionReadiness(readyPath, permission.NewID()); err == nil {
		t.Fatal("expected writePermissionReadiness to refuse existing regular file target")
	}
	gotSentinel, err := os.ReadFile(readyPath)
	if err != nil || !bytes.Equal(gotSentinel, sentinel) {
		t.Fatalf("existing regular file was modified: err=%v content=%q", err, string(gotSentinel))
	}
	assertNoTempArtifacts(t, dir)
	if err := os.Remove(readyPath); err != nil {
		t.Fatal(err)
	}

	// 2. Existing symlink (pointing to a live file) must never be followed or clobbered.
	targetPath := filepath.Join(dir, "symlink-target.json")
	if err := os.WriteFile(targetPath, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, readyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := writePermissionReadiness(readyPath, permission.NewID()); err == nil {
		t.Fatal("expected writePermissionReadiness to refuse existing symlink target")
	}
	if st, lerr := os.Lstat(readyPath); lerr != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at ready.json to remain intact, got lstat=%+v err=%v", st, lerr)
	}
	gotTarget, err := os.ReadFile(targetPath)
	if err != nil || !bytes.Equal(gotTarget, sentinel) {
		t.Fatalf("symlink target was modified: err=%v content=%q", err, string(gotTarget))
	}
	assertNoTempArtifacts(t, dir)
	_ = os.Remove(readyPath)
	_ = os.Remove(targetPath)

	// 3. Existing dangling symlink at ready.json must also be refused without creating the target.
	nonexistentTarget := filepath.Join(dir, "nonexistent-target.json")
	if err := os.Symlink(nonexistentTarget, readyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := writePermissionReadiness(readyPath, permission.NewID()); err == nil {
		t.Fatal("expected writePermissionReadiness to refuse dangling symlink target")
	}
	if _, statErr := os.Lstat(nonexistentTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dangling symlink target should not have been created, got err=%v", statErr)
	}
	assertNoTempArtifacts(t, dir)
	_ = os.Remove(readyPath)

	// 4. Deterministic TOCTOU collision: another creator places a file or symlink at ready.json
	// after the initial fstatat precheck and after the temp file is synced, right before renameExclusiveAt.
	// RENAME_EXCL (Darwin) / RENAME_NOREPLACE (Linux) must refuse without clobbering and clean up the temp file.
	toctouObs := &readinessPublishObserver{
		beforeRename: func(_ *os.File, _, _ string) error {
			return os.WriteFile(readyPath, sentinel, 0o600)
		},
	}
	if _, err := writePermissionReadinessWithObserver(readyPath, permission.NewID(), toctouObs); err == nil {
		t.Fatal("expected kernel exclusive rename to refuse TOCTOU file creation at ready.json")
	}
	gotTOCTOU, err := os.ReadFile(readyPath)
	if err != nil || !bytes.Equal(gotTOCTOU, sentinel) {
		t.Fatalf("TOCTOU regular file at ready.json was clobbered: err=%v content=%q", err, string(gotTOCTOU))
	}
	assertNoTempArtifacts(t, dir)
	_ = os.Remove(readyPath)

	toctouSymlinkObs := &readinessPublishObserver{
		beforeRename: func(_ *os.File, _, _ string) error {
			return os.Symlink(nonexistentTarget, readyPath)
		},
	}
	if _, err := writePermissionReadinessWithObserver(readyPath, permission.NewID(), toctouSymlinkObs); err == nil {
		t.Fatal("expected kernel exclusive rename to refuse TOCTOU symlink creation at ready.json")
	}
	if st, lerr := os.Lstat(readyPath); lerr != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("TOCTOU symlink at ready.json was clobbered: st=%+v err=%v", st, lerr)
	}
	assertNoTempArtifacts(t, dir)
	_ = os.Remove(readyPath)

	// 5. Failure at each staging hook cleans up the temporary file and leaves no ready.json.
	for _, stage := range []string{"afterTempCreate", "afterPartialWrite", "beforeRename"} {
		failObs := &readinessPublishObserver{}
		switch stage {
		case "afterTempCreate":
			failObs.afterTempCreate = func(*os.File, string, string) error { return errors.New("injected failure") }
		case "afterPartialWrite":
			failObs.afterPartialWrite = func(*os.File, string, string) error { return errors.New("injected failure") }
		case "beforeRename":
			failObs.beforeRename = func(*os.File, string, string) error { return errors.New("injected failure") }
		}
		if _, err := writePermissionReadinessWithObserver(readyPath, permission.NewID(), failObs); err == nil {
			t.Fatalf("expected failure at stage %s", stage)
		}
		if _, statErr := os.Lstat(readyPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unexpected ready.json after failure at stage %s: %v", stage, statErr)
		}
		assertNoTempArtifacts(t, dir)
	}

	// 6. Invalid incarnation, relative path, or unsafe group-writable parent directory must fail closed.
	if _, err := writePermissionReadiness(readyPath, "not-a-valid-32-hex-incarnation"); err == nil {
		t.Fatal("expected invalid incarnation to be rejected")
	}
	if _, err := writePermissionReadiness("relative/ready.json", permission.NewID()); err == nil {
		t.Fatal("expected relative path to be rejected")
	}
	unsafeSubdir := filepath.Join(dir, "unsafe-group-writable")
	if err := os.Mkdir(unsafeSubdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeSubdir, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := writePermissionReadiness(filepath.Join(unsafeSubdir, "ready.json"), permission.NewID()); err == nil {
		t.Fatal("expected group-writable parent directory to be rejected")
	}

	// 7. Cleanup idempotency and inode pinning: calling first boot's cleanup() after
	// a second boot has published a new ready.json must NOT unlink the second boot's file.
	id1 := permission.NewID()
	cleanup1, err := writePermissionReadiness(readyPath, id1)
	if err != nil {
		t.Fatal(err)
	}
	cleanup1()
	id2 := permission.NewID()
	cleanup2, err := writePermissionReadiness(readyPath, id2)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	// Invoking stale cleanup1 again must not delete id2's ready.json.
	cleanup1()
	rec2, err := strictAgentPollReadiness(readyPath)
	if err != nil || rec2.CoordinatorIncarnation != id2 {
		t.Fatalf("stale cleanup clobbered subsequent boot readiness record: err=%v rec=%+v", err, rec2)
	}
}

func TestXIN2794Source92ReadinessConcurrentPollingNoPartialRead(t *testing.T) {
	dir := privateHostTestDir(t)

	var invalidErrors atomic.Int32
	var validReads atomic.Int32
	var firstErrMu sync.Mutex
	var firstErr error
	recordErr := func(err error) {
		invalidErrors.Add(1)
		firstErrMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		firstErrMu.Unlock()
	}

	for cycle := 0; cycle < 40; cycle++ {
		readyPath := filepath.Join(dir, fmt.Sprintf("ready-%02d.json", cycle))
		id := permission.NewID()

		var readerWG sync.WaitGroup
		for i := 0; i < 4; i++ {
			readerWG.Add(1)
			go func() {
				defer readerWG.Done()
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
					rec, err := strictAgentPollReadiness(readyPath)
					if err != nil {
						if errors.Is(err, unix.ENOENT) {
							continue
						}
						recordErr(err)
						return
					}
					if rec.ControlVersion != 1 || rec.CoordinatorIncarnation != id {
						recordErr(fmt.Errorf("unexpected valid read record: %+v (want id=%s)", rec, id))
						return
					}
					validReads.Add(1)
					return
				}
				recordErr(fmt.Errorf("cycle %d reader timed out waiting for %s", cycle, readyPath))
			}()
		}

		cleanup, err := writePermissionReadiness(readyPath, id)
		if err != nil {
			readerWG.Wait()
			t.Fatalf("cycle %d writePermissionReadiness failed: %v", cycle, err)
		}
		readerWG.Wait()
		cleanup()
		if _, perr := strictAgentPollReadiness(readyPath); !errors.Is(perr, unix.ENOENT) {
			t.Fatalf("cycle %d expected ENOENT after cleanup, got %v", cycle, perr)
		}
	}

	if invalidErrors.Load() != 0 {
		t.Fatalf("concurrent strict Agent pollers observed %d invalid/partial readiness states; first error: %v", invalidErrors.Load(), firstErr)
	}
	if validReads.Load() != 40*4 {
		t.Fatalf("expected %d valid concurrent readiness reads, got %d", 40*4, validReads.Load())
	}
	assertNoTempArtifacts(t, dir)
}

func TestXIN2794Source92ReadinessActualCoordinatorProcessStrictAgentPollerCompatibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dir := privateHostTestDir(t)
	cliBin := filepath.Join(dir, "octo-cli-source92")
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

	reserveAddr := func() string {
		l, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			t.Fatal(lerr)
		}
		addr := l.Addr().String()
		_ = l.Close()
		return addr
	}

	posDir := filepath.Join(dir, "coordinator-boot")
	logDir := filepath.Join(posDir, "requests")
	homeDir := filepath.Join(posDir, "home")
	for _, d := range []string{posDir, logDir, homeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	readyPath := filepath.Join(posDir, "ready.json")
	cAddr := reserveAddr()
	rAddr := reserveAddr()
	endpointIncarnation := permission.NewID()
	hostCfg := map[string]any{
		"version":               1,
		"origin":                f.backend.URL,
		"bot_uid":               "bot",
		"recipient_id":          "endpoint-1",
		"endpoint_incarnation":  endpointIncarnation,
		"peer_sha256":           peerHash,
		"control_address":       cAddr,
		"relay_address":         rAddr,
		"relay_url":             "https://" + rAddr,
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
		t.Fatalf("failed to start octo-cli coordinator subprocess: %v", err)
	}
	hostDone := make(chan error, 1)
	go func() { hostDone <- hostCmd.Wait() }()
	defer func() {
		stopHost()
		select {
		case <-hostDone:
		case <-time.After(10 * time.Second):
			t.Error("octo-cli coordinator subprocess did not exit")
		}
	}()

	// Poll ready.json aggressively every 500 microseconds using the strict Agent validation
	// rule (any non-ENOENT error immediately fails with "Coordinator readiness invalid").
	var readyRec strictAgentReadyRecord
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		select {
		case herr := <-hostDone:
			t.Fatalf("octo-cli exited before readiness: %v (stderr=%s)", herr, hostStderr.String())
		default:
		}
		rec, perr := strictAgentPollReadiness(readyPath)
		if perr == nil {
			readyRec = rec
			break
		}
		if !errors.Is(perr, unix.ENOENT) {
			t.Fatalf("strict Agent readiness poll failed during live coordinator startup: %v", perr)
		}
		time.Sleep(500 * time.Microsecond)
	}
	if readyRec.ControlVersion != 1 || !controlID(readyRec.CoordinatorIncarnation) {
		t.Fatalf("did not receive valid readiness record: %+v", readyRec)
	}
	assertNoTempArtifacts(t, posDir)

	// Immediately connect over mTLS TLS 1.3 and execute control hello verifying coordinator_incarnation.
	conn, err := tls.Dial("tcp", cAddr, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   "localhost",
		Certificates: []tls.Certificate{clientCert},
	})
	if err != nil {
		t.Fatalf("control mTLS dial failed immediately after readiness publication: %v", err)
	}
	defer conn.Close()

	helloFrame, err := permission.PrivateJSON(map[string]any{
		"control_version": 1,
		"request_id":      permission.NewID(),
		"session_id":      permission.NewID(),
		"method":          "hello",
		"payload": map[string]any{
			"versions":             []int{2},
			"metadata_versions":    []int{2},
			"identity_versions":    []int{2},
			"endpoint_incarnation": endpointIncarnation,
			"configured_origin":    f.backend.URL,
			"recipient_id":         "endpoint-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(helloFrame)))
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(append(prefix[:], helloFrame...)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		t.Fatal(err)
	}
	replyBytes := make([]byte, binary.BigEndian.Uint32(prefix[:]))
	if _, err := io.ReadFull(conn, replyBytes); err != nil {
		t.Fatal(err)
	}
	var helloResp map[string]any
	if err := json.Unmarshal(replyBytes, &helloResp); err != nil || helloResp["status"] != "ok" {
		t.Fatalf("unexpected hello response: %s (err=%v)", string(replyBytes), err)
	}
	helloPayload, _ := helloResp["payload"].(map[string]any)
	if helloPayload["coordinator_incarnation"] != readyRec.CoordinatorIncarnation {
		t.Fatalf("hello coordinator_incarnation %v != ready.json %s", helloPayload["coordinator_incarnation"], readyRec.CoordinatorIncarnation)
	}
}
