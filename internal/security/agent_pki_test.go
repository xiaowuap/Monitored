package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestAgentIssuerControlsIdentityAndLifetime(t *testing.T) {
	certificatePEM, keyPEM := testAgentCA(t)
	issuer, err := NewAgentIssuer(certificatePEM, keyPEM, 48*time.Hour)
	if err != nil {
		t.Fatalf("NewAgentIssuer() error = %v", err)
	}
	_, agentKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "untrusted-csr-name"},
		DNSNames: []string{"attacker.example"},
	}, agentKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	issued, err := issuer.IssueAgentCertificate(
		"edge-01",
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}),
		now,
	)
	if err != nil {
		t.Fatalf("IssueAgentCertificate() error = %v", err)
	}
	block, _ := pem.Decode(issued.CertificatePEM)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Subject.CommonName != "edge-01" || len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != "edge-01" {
		t.Fatalf("issued identity = CN %q, DNS %v", certificate.Subject.CommonName, certificate.DNSNames)
	}
	if certificate.NotAfter.After(now.Add(48*time.Hour + time.Second)) {
		t.Fatalf("certificate lifetime exceeded configured TTL: %s", certificate.NotAfter.Sub(now))
	}
	if len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("certificate EKU = %v", certificate.ExtKeyUsage)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certificatePEM)
	if _, err := certificate.Verify(x509.VerifyOptions{
		Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("verify issued certificate: %v", err)
	}
}

func TestAgentIssuerRejectsInvalidIdentityAndCSR(t *testing.T) {
	certificatePEM, keyPEM := testAgentCA(t)
	issuer, err := NewAgentIssuer(certificatePEM, keyPEM, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.IssueAgentCertificate("../edge", []byte("invalid"), time.Now()); err == nil {
		t.Fatal("IssueAgentCertificate() accepted an invalid Agent ID")
	}
	if _, err := issuer.IssueAgentCertificate("edge-01", []byte("invalid"), time.Now()); err == nil {
		t.Fatal("IssueAgentCertificate() accepted an invalid CSR")
	}
}

func testAgentCA(t *testing.T) ([]byte, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Monitored Test Agent CA"},
		NotBefore:             now.Add(-time.Hour),
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
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
