package agentupdate

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const DefaultMaximumPackageBytes int64 = 256 << 20

var safeMetadata = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:/@-]{0,254}$`)
var sha256Digest = regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`)

type Package struct {
	ID           string    `json:"id"`
	Version      string    `json:"version"`
	Strategy     string    `json:"strategy"`
	OS           string    `json:"os"`
	Architecture string    `json:"architecture"`
	FileName     string    `json:"fileName,omitempty"`
	ContentType  string    `json:"contentType,omitempty"`
	SizeBytes    int64     `json:"sizeBytes,omitempty"`
	SHA256       string    `json:"sha256,omitempty"`
	Image        string    `json:"image,omitempty"`
	ImageDigest  string    `json:"imageDigest,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	CreatedBy    string    `json:"createdBy"`
}

type Cache struct {
	root       string
	signingKey []byte
	maxBytes   int64
}

func NewCache(root string, signingKey []byte, maximumBytes int64) (*Cache, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("Agent update cache directory is required")
	}
	if len(signingKey) < 16 {
		return nil, errors.New("Agent update download signing key must be at least 16 bytes")
	}
	if maximumBytes <= 0 {
		maximumBytes = DefaultMaximumPackageBytes
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create Agent update cache: %w", err)
	}
	return &Cache{root: root, signingKey: append([]byte(nil), signingKey...), maxBytes: maximumBytes}, nil
}

func (c *Cache) PutBinary(ctx context.Context, value Package, body io.Reader) (Package, error) {
	value.Strategy = "binary"
	if err := validatePackage(value); err != nil {
		return Package{}, err
	}
	id, err := randomID()
	if err != nil {
		return Package{}, err
	}
	value.ID = id
	value.CreatedAt = time.Now().UTC()
	value.FileName = filepath.Base(value.FileName)
	if value.FileName == "." || value.FileName == "" {
		value.FileName = "monitored-agent"
	}
	temporary, err := os.CreateTemp(c.root, ".upload-*")
	if err != nil {
		return Package{}, fmt.Errorf("create cached update package: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	_ = temporary.Chmod(0o600)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(body, c.maxBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return Package{}, fmt.Errorf("cache Agent update package: %w", copyErr)
	}
	if closeErr != nil {
		return Package{}, fmt.Errorf("close cached Agent update package: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return Package{}, err
	}
	if written == 0 {
		return Package{}, errors.New("Agent update package is empty")
	}
	if written > c.maxBytes {
		return Package{}, fmt.Errorf("Agent update package exceeds %d bytes", c.maxBytes)
	}
	value.SizeBytes = written
	value.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if err := os.Rename(temporaryName, c.payloadPath(value.ID)); err != nil {
		return Package{}, fmt.Errorf("publish cached Agent update package: %w", err)
	}
	if err := c.writeManifest(value); err != nil {
		_ = os.Remove(c.payloadPath(value.ID))
		return Package{}, err
	}
	return value, nil
}

func (c *Cache) PutDocker(value Package) (Package, error) {
	value.Strategy = "docker"
	if err := validatePackage(value); err != nil {
		return Package{}, err
	}
	id, err := randomID()
	if err != nil {
		return Package{}, err
	}
	value.ID = id
	value.CreatedAt = time.Now().UTC()
	if err := c.writeManifest(value); err != nil {
		return Package{}, err
	}
	return value, nil
}

func (c *Cache) List() ([]Package, error) {
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return nil, fmt.Errorf("list Agent update cache: %w", err)
	}
	result := make([]Package, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		value, readErr := c.readManifest(strings.TrimSuffix(entry.Name(), ".json"))
		if readErr != nil {
			continue
		}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (c *Cache) Get(id string) (Package, error) {
	if !validID(id) {
		return Package{}, os.ErrNotExist
	}
	return c.readManifest(id)
}

func (c *Cache) Open(id string) (Package, *os.File, error) {
	value, err := c.Get(id)
	if err != nil {
		return Package{}, nil, err
	}
	if value.Strategy != "binary" {
		return Package{}, nil, errors.New("Docker update manifests do not contain a downloadable package")
	}
	file, err := os.Open(c.payloadPath(id))
	if err != nil {
		return Package{}, nil, err
	}
	return value, file, nil
}

func (c *Cache) SignDownload(id string, expiresAt time.Time) string {
	message := id + "\n" + fmt.Sprintf("%d", expiresAt.Unix())
	mac := hmac.New(sha256.New, c.signingKey)
	_, _ = mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (c *Cache) VerifyDownload(id string, expiresAt time.Time, token string, now time.Time) bool {
	if !validID(id) || expiresAt.Before(now) || expiresAt.After(now.Add(25*time.Hour)) {
		return false
	}
	expected := c.SignDownload(id, expiresAt)
	return hmac.Equal([]byte(expected), []byte(token))
}

func (c *Cache) payloadPath(id string) string {
	return filepath.Join(c.root, id+".payload")
}

func (c *Cache) manifestPath(id string) string {
	return filepath.Join(c.root, id+".json")
}

func (c *Cache) readManifest(id string) (Package, error) {
	body, err := os.ReadFile(c.manifestPath(id))
	if err != nil {
		return Package{}, err
	}
	var value Package
	if err := json.Unmarshal(body, &value); err != nil {
		return Package{}, fmt.Errorf("decode Agent update manifest: %w", err)
	}
	if value.ID != id {
		return Package{}, errors.New("Agent update manifest identity mismatch")
	}
	return value, nil
}

func (c *Cache) writeManifest(value Package) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(c.root, ".manifest-*")
	if err != nil {
		return fmt.Errorf("create Agent update manifest: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	_ = temporary.Chmod(0o600)
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Agent update manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync Agent update manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Agent update manifest: %w", err)
	}
	if err := os.Rename(name, c.manifestPath(value.ID)); err != nil {
		return fmt.Errorf("publish Agent update manifest: %w", err)
	}
	return nil
}

func validatePackage(value Package) error {
	if !safeMetadata.MatchString(value.Version) {
		return errors.New("Agent update version contains unsupported characters")
	}
	if value.OS != "linux" && value.OS != "freebsd" {
		return errors.New("Agent update OS must be linux or freebsd")
	}
	if !oneOf(value.Architecture, "amd64", "arm64") {
		return errors.New("Agent update architecture must be amd64 or arm64")
	}
	switch value.Strategy {
	case "binary":
		return nil
	case "docker":
		if value.OS != "linux" {
			return errors.New("Docker Agent updates are only supported on linux")
		}
		if !safeMetadata.MatchString(value.Image) {
			return errors.New("Docker update image is required and contains unsupported characters")
		}
		separator := strings.LastIndex(value.Image, "@")
		embeddedDigest := ""
		if separator >= 0 {
			embeddedDigest = value.Image[separator+1:]
			if !sha256Digest.MatchString(embeddedDigest) {
				return errors.New("Docker update image contains an invalid digest")
			}
		}
		if value.ImageDigest != "" && !sha256Digest.MatchString(value.ImageDigest) {
			return errors.New("Docker image digest must use sha256")
		}
		if embeddedDigest != "" && value.ImageDigest != "" && !strings.EqualFold(embeddedDigest, value.ImageDigest) {
			return errors.New("Docker image and separate digest do not match")
		}
		if value.ImageDigest == "" {
			if embeddedDigest == "" {
				return errors.New("Docker update requires an immutable sha256 image digest")
			}
		}
		return nil
	default:
		return errors.New("Agent update strategy must be binary or docker")
	}
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create Agent update package identity: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func validID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
