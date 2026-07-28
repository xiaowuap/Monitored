package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const credentialBundleVersion = 1

type EnrollmentOptions struct {
	ServerURL string
	AgentID   string
	Token     string
	Client    *http.Client
}

type credentialBundle struct {
	Version        int       `json:"version"`
	AgentID        string    `json:"agentId"`
	CertificatePEM string    `json:"certificatePem"`
	PrivateKeyPEM  string    `json:"privateKeyPem"`
	CAPEM          string    `json:"caPem"`
	Serial         string    `json:"serial"`
	NotAfter       time.Time `json:"notAfter"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type pendingCredential struct {
	requestID     string
	privateKeyPEM []byte
}

type CredentialStore struct {
	path       string
	mu         sync.Mutex
	bundle     *credentialBundle
	pending    *pendingCredential
	enrollment *EnrollmentOptions
}

func OpenCredentialStore(path string) (*CredentialStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("managed Agent credential file path is required")
	}
	store := &CredentialStore{path: path}
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read managed Agent credentials: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect managed Agent credentials: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("managed Agent credential file must not be accessible by group or other users")
	}
	var bundle credentialBundle
	if err := json.Unmarshal(payload, &bundle); err != nil {
		return nil, fmt.Errorf("decode managed Agent credentials: %w", err)
	}
	if bundle.Version != credentialBundleVersion {
		return nil, fmt.Errorf("unsupported managed Agent credential version %d", bundle.Version)
	}
	if _, err := validateStoredCredentialBundle(&bundle); err != nil {
		return nil, fmt.Errorf("validate managed Agent credentials: %w", err)
	}
	store.bundle = &bundle
	return store, nil
}

func (s *CredentialStore) ConfigureEnrollment(options EnrollmentOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := options
	s.enrollment = &copy
}

func (s *CredentialStore) HasCredentials() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bundle != nil
}

func (s *CredentialStore) AgentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bundle == nil {
		return ""
	}
	return s.bundle.AgentID
}

func (s *CredentialStore) Ensure(ctx context.Context) error {
	s.mu.Lock()
	if s.bundle != nil && s.bundle.NotAfter.After(time.Now()) {
		s.mu.Unlock()
		return nil
	}
	options := s.enrollment
	s.mu.Unlock()
	if options == nil {
		if s.HasCredentials() {
			return errors.New("managed Agent certificate has expired; create a new one-time enrollment token")
		}
		return errors.New("managed Agent credentials are missing and no enrollment token was configured")
	}
	return s.Enroll(ctx, *options)
}

func (s *CredentialStore) Enroll(ctx context.Context, options EnrollmentOptions) error {
	s.mu.Lock()
	if s.bundle != nil && s.bundle.NotAfter.After(time.Now()) {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if err := validateEnrollmentURL(options.ServerURL); err != nil {
		return err
	}
	if strings.TrimSpace(options.AgentID) == "" || strings.TrimSpace(options.Token) == "" {
		return errors.New("Agent ID and one-time enrollment token are required")
	}
	privateKeyPEM, csrPEM, err := newAgentKeyAndCSR(options.AgentID)
	if err != nil {
		return err
	}
	requestBody, err := json.Marshal(map[string]string{
		"agentId": options.AgentID,
		"token":   options.Token,
		"csrPem":  string(csrPEM),
	})
	if err != nil {
		return fmt.Errorf("encode Agent enrollment request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(options.ServerURL, "/")+"/api/v1/agents/enroll",
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return fmt.Errorf("create Agent enrollment request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("enroll Agent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("enroll Agent returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var result struct {
		AgentID        string    `json:"agentId"`
		CertificatePEM string    `json:"certificatePem"`
		CAPEM          string    `json:"caPem"`
		Serial         string    `json:"serial"`
		NotAfter       time.Time `json:"notAfter"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 256<<10))
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("decode Agent enrollment response: %w", err)
	}
	if result.AgentID != options.AgentID {
		return errors.New("Agent enrollment response identity does not match the request")
	}
	bundle := &credentialBundle{
		Version:        credentialBundleVersion,
		AgentID:        options.AgentID,
		CertificatePEM: result.CertificatePEM,
		PrivateKeyPEM:  string(privateKeyPEM),
		CAPEM:          result.CAPEM,
		Serial:         result.Serial,
		NotAfter:       result.NotAfter,
		UpdatedAt:      time.Now().UTC(),
	}
	if _, err := validateCredentialBundle(bundle, time.Now()); err != nil {
		return fmt.Errorf("validate enrolled Agent certificate: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bundle != nil && s.bundle.NotAfter.After(time.Now()) {
		return nil
	}
	if err := s.writeBundleLocked(bundle); err != nil {
		return err
	}
	s.bundle = bundle
	return nil
}

func (s *CredentialStore) TLSConfig(serverName string) (*tls.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bundle == nil {
		return nil, errors.New("managed Agent credentials are not enrolled")
	}
	certificate, roots, _, err := credentialTLSMaterial(s.bundle)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		RootCAs:      roots,
		ServerName:   serverName,
	}, nil
}

func (s *CredentialStore) RotationRequest(agentID string, renewBefore time.Duration, now time.Time) (string, []byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bundle == nil {
		return "", nil, false, errors.New("managed Agent credentials are not enrolled")
	}
	if s.bundle.AgentID != agentID {
		return "", nil, false, errors.New("managed Agent identity does not match configured Agent ID")
	}
	if now.IsZero() {
		now = time.Now()
	}
	_, _, leaf, err := credentialTLSMaterial(s.bundle)
	if err != nil {
		return "", nil, false, err
	}
	if maximumWindow := leaf.NotAfter.Sub(leaf.NotBefore) / 3; renewBefore > maximumWindow {
		renewBefore = maximumWindow
	}
	if s.pending != nil || s.bundle.NotAfter.After(now.Add(renewBefore)) {
		return "", nil, false, nil
	}
	privateKeyPEM, csrPEM, err := newAgentKeyAndCSR(agentID)
	if err != nil {
		return "", nil, false, err
	}
	requestBytes := make([]byte, 18)
	if _, err := rand.Read(requestBytes); err != nil {
		return "", nil, false, fmt.Errorf("generate certificate rotation request ID: %w", err)
	}
	requestID := base64.RawURLEncoding.EncodeToString(requestBytes)
	s.pending = &pendingCredential{requestID: requestID, privateKeyPEM: privateKeyPEM}
	return requestID, csrPEM, true, nil
}

func (s *CredentialStore) CancelRotation(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil && (requestID == "" || s.pending.requestID == requestID) {
		s.pending = nil
	}
}

func (s *CredentialStore) InstallRotation(requestID string, certificatePEM, caPEM []byte, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil || s.pending.requestID != requestID {
		return errors.New("certificate rotation response does not match an active request")
	}
	pending := s.pending
	s.pending = nil
	bundle := &credentialBundle{
		Version:        credentialBundleVersion,
		AgentID:        s.bundle.AgentID,
		CertificatePEM: string(certificatePEM),
		PrivateKeyPEM:  string(pending.privateKeyPEM),
		CAPEM:          string(caPEM),
		UpdatedAt:      now.UTC(),
	}
	leaf, err := validateCredentialBundle(bundle, now)
	if err != nil {
		return fmt.Errorf("validate rotated Agent certificate: %w", err)
	}
	bundle.Serial = leaf.SerialNumber.String()
	bundle.NotAfter = leaf.NotAfter.UTC()
	if err := s.writeBundleLocked(bundle); err != nil {
		return err
	}
	s.bundle = bundle
	return nil
}

func (s *CredentialStore) writeBundleLocked(bundle *credentialBundle) error {
	payload, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed Agent credentials: %w", err)
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create managed Agent credential directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create managed Agent credential file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect managed Agent credential file: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return fmt.Errorf("write managed Agent credentials: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync managed Agent credentials: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close managed Agent credentials: %w", err)
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return fmt.Errorf("replace managed Agent credentials: %w", err)
	}
	return nil
}

func newAgentKeyAndCSR(agentID string) ([]byte, []byte, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate Agent identity key: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Agent identity key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: agentID},
	}, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create Agent certificate request: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), nil
}

func validateCredentialBundle(bundle *credentialBundle, now time.Time) (*x509.Certificate, error) {
	_, _, leaf, err := credentialTLSMaterial(bundle)
	if err != nil {
		return nil, err
	}
	if !certificateIdentityMatches(leaf, bundle.AgentID) {
		return nil, errors.New("certificate identity does not match the Agent ID")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(bundle.CAPEM)) {
		return nil, errors.New("managed Agent CA contains no certificates")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return nil, fmt.Errorf("verify managed Agent certificate: %w", err)
	}
	bundle.NotAfter = leaf.NotAfter.UTC()
	bundle.Serial = leaf.SerialNumber.String()
	return leaf, nil
}

func validateStoredCredentialBundle(bundle *credentialBundle) (*x509.Certificate, error) {
	_, _, leaf, err := credentialTLSMaterial(bundle)
	if err != nil {
		return nil, err
	}
	verificationTime := time.Now()
	if verificationTime.Before(leaf.NotBefore) || verificationTime.After(leaf.NotAfter) {
		verificationTime = leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) / 2)
	}
	return validateCredentialBundle(bundle, verificationTime)
}

func credentialTLSMaterial(bundle *credentialBundle) (tls.Certificate, *x509.CertPool, *x509.Certificate, error) {
	certificate, err := tls.X509KeyPair([]byte(bundle.CertificatePEM), []byte(bundle.PrivateKeyPEM))
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("load managed Agent key pair: %w", err)
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, nil, nil, errors.New("managed Agent certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("parse managed Agent certificate: %w", err)
	}
	certificate.Leaf = leaf
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(bundle.CAPEM)) {
		return tls.Certificate{}, nil, nil, errors.New("managed Agent CA contains no certificates")
	}
	return certificate, roots, leaf, nil
}

func certificateIdentityMatches(certificate *x509.Certificate, agentID string) bool {
	if certificate.Subject.CommonName == agentID {
		return true
	}
	for _, name := range certificate.DNSNames {
		if name == agentID {
			return true
		}
	}
	for _, identity := range certificate.URIs {
		if identity.String() == agentID || strings.TrimSuffix(identity.Path, "/") == "/agent/"+agentID {
			return true
		}
	}
	return false
}

func validateEnrollmentURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("Agent enrollment server URL is invalid")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Scheme == "http" && (strings.EqualFold(parsed.Hostname(), "localhost") || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return errors.New("Agent enrollment requires HTTPS outside local development")
}
