package s3cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/longbridgeapp/assert"
)

func validCfg() Config {
	return Config{Bucket: "b", Region: "us-east-1", AccessKeyID: "ak", SecretAccessKey: "sk"}
}

func TestConfig_validate(t *testing.T) {
	assert.Nil(t, validCfg().validate())

	bad := map[string]Config{
		"no bucket": {Region: "r", AccessKeyID: "ak", SecretAccessKey: "sk"},
		"no akid":   {Bucket: "b", SecretAccessKey: "sk"},
		"no secret": {Bucket: "b", AccessKeyID: "ak"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) { assert.NotNil(t, c.validate()) })
	}
}

func TestUpload_RequiresFileAndKey(t *testing.T) {
	_, err := Upload(validCfg(), "", "key", "")
	assert.NotNil(t, err)
	_, err = Upload(validCfg(), "/tmp/x", "", "")
	assert.NotNil(t, err)
}

// Upload must refuse a zero-byte artifact (a truncated/empty dump should
// fail loudly, never silently overwrite a good backup with nothing).
func TestUpload_RejectsEmptyFile(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.gz")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Upload(validCfg(), empty, "key", "")
	assert.NotNil(t, err)
}

func TestUpload_MissingFile(t *testing.T) {
	_, err := Upload(validCfg(), "/no/such/file.gz", "key", "")
	assert.NotNil(t, err)
}

func TestDownload_RequiresKeyAndDest(t *testing.T) {
	assert.NotNil(t, Download(validCfg(), "", "/tmp/x"))
	assert.NotNil(t, Download(validCfg(), "key", ""))
}

func TestDelete_RequiresKey(t *testing.T) {
	assert.NotNil(t, Delete(validCfg(), ""))
}

func TestOps_RequireCredentials(t *testing.T) {
	noCreds := Config{Bucket: "b", Region: "r"}
	_, err := Upload(noCreds, "/tmp/x", "k", "")
	assert.NotNil(t, err)
	assert.NotNil(t, Download(noCreds, "k", "/tmp/x"))
	assert.NotNil(t, Delete(noCreds, "k"))
}
