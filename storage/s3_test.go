package storage

import (
	"testing"
	"time"

	"github.com/gigcodes/launch-util/config"
	"github.com/longbridgeapp/assert"
	"github.com/spf13/viper"
)

func Test_S3_open(t *testing.T) {
	v := viper.New()
	v.Set("bucket", "test-bucket")
	v.Set("region", "us-east-2")
	v.Set("storage_class", "STANDARD_IA")

	base, err := newBase(
		config.ModelConfig{
			DumpPath: "/data/backups",
		},
		"foo/bar",
		config.SubConfig{
			Type:  "s3",
			Name:  "s3-1",
			Viper: v,
		},
	)
	assert.NoError(t, err)

	storage := &S3{Base: base}

	err = storage.open()
	assert.NoError(t, err)

	// storage_class is read straight from config (no implicit default).
	assert.Equal(t, "STANDARD_IA", storage.storageClass)
	assert.Equal(t, "test-bucket", storage.bucket)
	assert.Equal(t, "", storage.path)

	// init() defaults: max_retries=3, timeout=300s.
	assert.Equal(t, 3, *storage.awsCfg.MaxRetries)
	assert.Equal(t, "us-east-2", *storage.awsCfg.Region)
	assert.Equal(t, 300*time.Second, storage.awsCfg.HTTPClient.Timeout)
}

// Test_S3_open_NoStorageClass confirms storage_class is left empty when
// the config doesn't set it (AWS then applies the bucket default).
func Test_S3_open_NoStorageClass(t *testing.T) {
	v := viper.New()
	v.Set("bucket", "test-bucket")
	v.Set("region", "us-east-1")

	base, err := newBase(config.ModelConfig{DumpPath: "/data/backups"}, "foo/bar",
		config.SubConfig{Type: "s3", Name: "s3-2", Viper: v})
	assert.NoError(t, err)

	storage := &S3{Base: base}
	assert.NoError(t, storage.open())
	assert.Equal(t, "", storage.storageClass)
}
