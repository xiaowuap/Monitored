package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monitored-io/monitored/internal/security"
)

func TestCredentialStoreEnrollsAndRotatesKeys(t *testing.T) {
	issuer := testCredentialIssuer(t, 48*time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			AgentID string `json:"agentId"`
			Token   string `json:"token"`
			CSRPEM  string `json:"csrPem"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Token != "menr_test" {
			t.Fatalf("enrollment token = %q", request.Token)
		}
		issued, err := issuer.IssueAgentCertificate(request.AgentID, []byte(request.CSRPEM), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agentId":        request.AgentID,
			"certificatePem": string(issued.CertificatePEM),
			"caPem":          string(issued.CAPEM),
			"serial":         issued.Serial,
			"notAfter":       issued.NotAfter,
		})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "pki", "credentials.json")
	store, err := OpenCredentialStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.ConfigureEnrollment(EnrollmentOptions{
		ServerURL: server.URL,
		AgentID:   "edge-01",
		Token:     "menr_test",
	})
	if err := store.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	firstConfig, err := store.TLSConfig("master.example")
	if err != nil {
		t.Fatal(err)
	}
	firstLeaf := firstConfig.Certificates[0].Leaf
	if firstLeaf == nil || !certificateIdentityMatches(firstLeaf, "edge-01") {
		t.Fatal("enrolled certificate does not contain the Agent identity")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o, want 600", info.Mode().Perm())
	}

	if _, _, needed, err := store.RotationRequest("edge-01", 72*time.Hour, time.Now()); err != nil || needed {
		t.Fatalf("new certificate rotated immediately with an oversized renewal window: needed=%v err=%v", needed, err)
	}
	requestID, csrPEM, needed, err := store.RotationRequest("edge-01", 72*time.Hour, time.Now().Add(40*time.Hour))
	if err != nil || !needed {
		t.Fatalf("RotationRequest() needed=%v err=%v", needed, err)
	}
	rotated, err := issuer.IssueAgentCertificate("edge-01", csrPEM, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallRotation(requestID, rotated.CertificatePEM, rotated.CAPEM, time.Now()); err != nil {
		t.Fatalf("InstallRotation() error = %v", err)
	}
	secondConfig, err := store.TLSConfig("master.example")
	if err != nil {
		t.Fatal(err)
	}
	secondLeaf := secondConfig.Certificates[0].Leaf
	if string(firstLeaf.RawSubjectPublicKeyInfo) == string(secondLeaf.RawSubjectPublicKeyInfo) {
		t.Fatal("certificate rotation reused the previous Agent key")
	}

	reopened, err := OpenCredentialStore(path)
	if err != nil {
		t.Fatalf("OpenCredentialStore() after rotation error = %v", err)
	}
	if reopened.AgentID() != "edge-01" {
		t.Fatalf("reopened Agent ID = %q", reopened.AgentID())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCredentialStore(path); err == nil {
		t.Fatal("OpenCredentialStore() accepted credentials readable by other users")
	}
}

func TestCredentialStoreRejectsPlainHTTPEnrollmentOffLoopback(t *testing.T) {
	store, err := OpenCredentialStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = store.Enroll(context.Background(), EnrollmentOptions{
		ServerURL: "http://monitored.example",
		AgentID:   "edge-01",
		Token:     "menr_test",
	})
	if err == nil {
		t.Fatal("Enroll() accepted non-loopback HTTP")
	}
}

func TestCredentialStoreCanReopenExpiredBundleForReenrollment(t *testing.T) {
	issuer := testCredentialIssuer(t, 24*time.Hour)
	privateKeyPEM, csrPEM, err := newAgentKeyAndCSR("offline-edge")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuer.IssueAgentCertificate("offline-edge", csrPEM, time.Now().Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	bundle := credentialBundle{
		Version:        credentialBundleVersion,
		AgentID:        "offline-edge",
		CertificatePEM: string(issued.CertificatePEM),
		PrivateKeyPEM:  string(privateKeyPEM),
		CAPEM:          string(issued.CAPEM),
		Serial:         issued.Serial,
		NotAfter:       issued.NotAfter,
		UpdatedAt:      time.Now().Add(-48 * time.Hour),
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenCredentialStore(path)
	if err != nil {
		t.Fatalf("OpenCredentialStore() rejected recoverable expired credentials: %v", err)
	}
	if err := store.Ensure(context.Background()); err == nil {
		t.Fatal("Ensure() accepted expired credentials without a new enrollment token")
	}
}

func testCredentialIssuer(t *testing.T, ttl time.Duration) *security.AgentIssuer {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             now.Add(-365 * 24 * time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := security.NewAgentIssuer(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		ttl,
	)
	if err != nil {
		t.Fatal(err)
	}
	return issuer
}
