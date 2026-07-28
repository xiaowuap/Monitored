package security

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	defaultAgentCertificateTTL = 30 * 24 * time.Hour
	maxAgentCertificateTTL     = 90 * 24 * time.Hour
)

var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)

type AgentCertificate struct {
	CertificatePEM []byte
	CAPEM          []byte
	Serial         string
	NotAfter       time.Time
}

type AgentCertificateIssuer interface {
	IssueAgentCertificate(agentID string, csrPEM []byte, now time.Time) (AgentCertificate, error)
}

type AgentIssuer struct {
	certificate *x509.Certificate
	signer      crypto.Signer
	caPEM       []byte
	ttl         time.Duration
}

func LoadAgentIssuer(certificateFile, keyFile string, ttl time.Duration) (*AgentIssuer, error) {
	if strings.TrimSpace(certificateFile) == "" || strings.TrimSpace(keyFile) == "" {
		return nil, errors.New("agent CA certificate and key files are required")
	}
	certificatePEM, err := os.ReadFile(certificateFile)
	if err != nil {
		return nil, fmt.Errorf("read agent CA certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read agent CA key: %w", err)
	}
	return NewAgentIssuer(certificatePEM, keyPEM, ttl)
}

func NewAgentIssuer(certificatePEM, keyPEM []byte, ttl time.Duration) (*AgentIssuer, error) {
	certificateBlock, _ := pem.Decode(certificatePEM)
	if certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" {
		return nil, errors.New("agent CA file contains no certificate")
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse agent CA certificate: %w", err)
	}
	if !certificate.IsCA || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("agent CA certificate is not authorized to sign certificates")
	}
	signer, err := parseSigner(keyPEM)
	if err != nil {
		return nil, err
	}
	if !publicKeysEqual(certificate.PublicKey, signer.Public()) {
		return nil, errors.New("agent CA certificate does not match the configured private key")
	}
	if ttl <= 0 {
		ttl = defaultAgentCertificateTTL
	}
	if ttl > maxAgentCertificateTTL {
		return nil, fmt.Errorf("agent certificate lifetime exceeds %s", maxAgentCertificateTTL)
	}
	return &AgentIssuer{
		certificate: certificate,
		signer:      signer,
		caPEM:       append([]byte(nil), certificatePEM...),
		ttl:         ttl,
	}, nil
}

func (i *AgentIssuer) IssueAgentCertificate(agentID string, csrPEM []byte, now time.Time) (AgentCertificate, error) {
	if !ValidAgentID(agentID) {
		return AgentCertificate{}, errors.New("agent ID must be 1-128 letters, digits, dots, underscores, or hyphens")
	}
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return AgentCertificate{}, errors.New("CSR must contain exactly one PEM certificate request")
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return AgentCertificate{}, fmt.Errorf("parse agent CSR: %w", err)
	}
	if err := request.CheckSignature(); err != nil {
		return AgentCertificate{}, fmt.Errorf("verify agent CSR signature: %w", err)
	}
	if err := supportedAgentPublicKey(request.PublicKey); err != nil {
		return AgentCertificate{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(i.certificate.NotBefore) {
		return AgentCertificate{}, errors.New("agent CA is not valid yet")
	}
	notBefore := now.UTC().Add(-5 * time.Minute)
	notAfter := now.UTC().Add(i.ttl)
	if caLimit := i.certificate.NotAfter.Add(-time.Minute); notAfter.After(caLimit) {
		notAfter = caLimit
	}
	if !notAfter.After(now.Add(time.Hour)) {
		return AgentCertificate{}, errors.New("agent CA expires too soon to issue a client certificate")
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return AgentCertificate{}, fmt.Errorf("generate agent certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial = big.NewInt(1)
	}
	identity, _ := url.Parse("spiffe://monitored.local/agent/" + url.PathEscape(agentID))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: agentID, Organization: []string{"Monitored Agents"}},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{identity},
	}
	if validDNSIdentity(agentID) {
		template.DNSNames = []string{agentID}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, i.certificate, request.PublicKey, i.signer)
	if err != nil {
		return AgentCertificate{}, fmt.Errorf("sign agent certificate: %w", err)
	}
	return AgentCertificate{
		CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		CAPEM:          append([]byte(nil), i.caPEM...),
		Serial:         serial.String(),
		NotAfter:       notAfter,
	}, nil
}

func ValidAgentID(value string) bool {
	return len(value) <= 128 && agentIDPattern.MatchString(value)
}

func parseSigner(keyPEM []byte) (crypto.Signer, error) {
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("agent CA key must contain exactly one PEM private key")
	}
	var parsed any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		parsed, err = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported agent CA key type %q", block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("parse agent CA key: %w", err)
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, errors.New("agent CA key is not a signing key")
	}
	return signer, nil
}

func publicKeysEqual(left, right any) bool {
	leftDER, leftErr := x509.MarshalPKIXPublicKey(left)
	rightDER, rightErr := x509.MarshalPKIXPublicKey(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftDER, rightDER)
}

func supportedAgentPublicKey(value any) error {
	switch key := value.(type) {
	case ed25519.PublicKey:
		return nil
	case *ecdsa.PublicKey:
		if key.Curve.Params().BitSize < 256 {
			return errors.New("agent ECDSA key must use P-256 or stronger")
		}
		return nil
	case *rsa.PublicKey:
		if key.N.BitLen() < 2048 {
			return errors.New("agent RSA key must be at least 2048 bits")
		}
		return nil
	default:
		return fmt.Errorf("unsupported agent public key type %T", value)
	}
}

func validDNSIdentity(value string) bool {
	if len(value) > 253 || strings.Contains(value, "_") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}
