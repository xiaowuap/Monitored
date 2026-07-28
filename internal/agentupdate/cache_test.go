package agentupdate

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCacheStoresAndSignsBinaryPackage(t *testing.T) {
	cache, err := NewCache(t.TempDir(), []byte("0123456789abcdef0123456789abcdef"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	value, err := cache.PutBinary(context.Background(), Package{
		Version: "1.2.3", OS: "linux", Architecture: "amd64",
		FileName: "monitored-agent", CreatedBy: "admin@example.com",
	}, strings.NewReader("agent-binary"))
	if err != nil {
		t.Fatal(err)
	}
	if value.SizeBytes != int64(len("agent-binary")) || value.SHA256 == "" {
		t.Fatalf("unexpected manifest: %+v", value)
	}
	loaded, file, err := cache.Open(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if loaded.ID != value.ID {
		t.Fatalf("loaded package ID = %q", loaded.ID)
	}
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	token := cache.SignDownload(value.ID, expires)
	if !cache.VerifyDownload(value.ID, expires, token, time.Now()) {
		t.Fatal("valid signed download was rejected")
	}
	if cache.VerifyDownload(value.ID, expires, token+"x", time.Now()) {
		t.Fatal("tampered signed download was accepted")
	}
}

func TestCacheRejectsOversizedAndMutableDockerPackages(t *testing.T) {
	cache, err := NewCache(t.TempDir(), []byte("0123456789abcdef0123456789abcdef"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.PutBinary(context.Background(), Package{
		Version: "1.0.0", OS: "linux", Architecture: "amd64",
	}, strings.NewReader("12345")); err == nil {
		t.Fatal("oversized package was accepted")
	}
	if _, err := cache.PutDocker(Package{
		Version: "1.0.0", OS: "linux", Architecture: "amd64", Image: "example/agent:latest",
	}); err == nil {
		t.Fatal("mutable Docker image was accepted")
	}
	if _, err := cache.PutDocker(Package{
		Version: "1.0.0", OS: "linux", Architecture: "amd64",
		Image:       "example/agent@sha256:" + strings.Repeat("a", 64),
		ImageDigest: "sha256:" + strings.Repeat("b", 64),
	}); err == nil {
		t.Fatal("conflicting Docker image digests were accepted")
	}
	entries, err := os.ReadDir(cache.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed upload left cache entries: %v", entries)
	}
}
