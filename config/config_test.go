package config

import (
	"os"
	"testing"
	"time"

	"github.com/longbridgeapp/assert"
)

var (
	testConfigFile = "../launch_test.yml"
)

func init() {
	os.Setenv("S3_ACCESS_KEY_ID", "xxxxxxxxxxxxxxxxxxxx")
	os.Setenv("S3_SECRET_ACCESS_KEY", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	if err := Init(testConfigFile); err != nil {
		panic(err.Error())
	}
}

func TestModelsLength(t *testing.T) {
	assert.Equal(t, Exist, true)
	assert.Equal(t, len(Models), 2)
}

func TestModel(t *testing.T) {
	model := GetModelConfigByName("app_full")

	assert.Equal(t, model.Name, "app_full")

	// compress_with
	assert.Equal(t, model.CompressWith.Type, "tgz")
	assert.NotNil(t, model.CompressWith.Viper)

	// storages — s3 + local only (the slimmed backend set)
	assert.Equal(t, model.DefaultStorage, "s3")
	assert.Equal(t, model.Storages["local"].Type, "local")
	assert.Equal(t, model.Storages["local"].Viper.GetString("path"), "/var/backups/launch")
	assert.Equal(t, model.Storages["s3"].Type, "s3")
	assert.Equal(t, model.Storages["s3"].Viper.GetString("bucket"), "my-launch-backups")
	assert.Equal(t, model.Storages["s3"].Viper.GetString("region"), "us-east-1")

	// databases — postgresql / mysql / redis
	assert.Len(t, model.Databases, 3)

	db := model.GetDatabaseByName("app_postgres")
	assert.Equal(t, db.Name, "app_postgres")
	assert.Equal(t, db.Type, "postgresql")
	assert.Equal(t, db.Viper.GetString("host"), "127.0.0.1")
	assert.Equal(t, db.Viper.GetString("port"), "5432")
	assert.Equal(t, db.Viper.GetString("database"), "app_production")
	assert.Equal(t, db.Viper.GetString("username"), "postgres")
	assert.Equal(t, db.Viper.GetString("password"), "secret-pg")

	db = model.GetDatabaseByName("app_mysql")
	assert.Equal(t, db.Name, "app_mysql")
	assert.Equal(t, db.Type, "mysql")
	assert.Equal(t, db.Viper.GetString("port"), "3306")
	assert.Equal(t, db.Viper.GetString("username"), "root")

	db = model.GetDatabaseByName("app_redis")
	assert.Equal(t, db.Name, "app_redis")
	assert.Equal(t, db.Type, "redis")
	assert.Equal(t, db.Viper.GetString("mode"), "sync")
	assert.Equal(t, db.Viper.GetString("rdb_path"), "/var/lib/redis/dump.rdb")
	assert.Equal(t, db.Viper.GetBool("invoke_save"), true)
	assert.Equal(t, db.Viper.GetString("password"), "secret-redis")

	// archive
	includes := model.Archive.GetStringSlice("includes")
	assert.Len(t, includes, 2)
	assert.Contains(t, includes, "/etc/nginx/nginx.conf")
	assert.Contains(t, includes, "/home/app/.env")

	excludes := model.Archive.GetStringSlice("excludes")
	assert.Len(t, excludes, 1)
	assert.Contains(t, excludes, "/home/app/.ssh/known_hosts")

	// schedule (cron-only — the `every`/`at` forms were removed)
	schedule := model.Schedule
	assert.Equal(t, true, schedule.Enabled)
	assert.Equal(t, "0 2 * * *", schedule.Cron)
}

// TestMinimalModel covers a model with no schedule (schedule disabled)
// and only a local storage.
func TestMinimalModel(t *testing.T) {
	model := GetModelConfigByName("minimal")
	assert.Equal(t, model.Name, "minimal")
	assert.Equal(t, model.DefaultStorage, "local")
	assert.Equal(t, model.Storages["local"].Type, "local")
	assert.Equal(t, false, model.Schedule.Enabled)
}

// Test_ScheduleConfig_String pins the current String() contract: cron
// when enabled+cron is set, otherwise "disabled". (The legacy every/at
// schedule forms were removed from this fork.)
func Test_ScheduleConfig_String(t *testing.T) {
	// Enabled but no cron → still "disabled" (nothing to schedule).
	assert.Equal(t, ScheduleConfig{Enabled: true}.String(), "disabled")

	// Enabled + cron → "cron <expr>".
	assert.Equal(t, ScheduleConfig{Enabled: true, Cron: "5 4 * * sun"}.String(), "cron 5 4 * * sun")

	// Disabled → "disabled".
	assert.Equal(t, ScheduleConfig{Enabled: false}.String(), "disabled")
}

// TestExpandEnv verifies $VAR references in the config are expanded from
// the environment at load time.
func TestExpandEnv(t *testing.T) {
	model := GetModelConfigByName("app_full")
	assert.Equal(t, model.Storages["s3"].Type, "s3")
	assert.Equal(t, model.Storages["s3"].Viper.GetString("access_key_id"), "xxxxxxxxxxxxxxxxxxxx")
	assert.Equal(t, model.Storages["s3"].Viper.GetString("secret_access_key"), "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
}

func TestInitWithNotExistsConfigFile(t *testing.T) {
	err := Init("config/path/not-exist.yml")
	assert.NotNil(t, err)
}

func TestWatchConfigToReload(t *testing.T) {
	err := Init(testConfigFile)
	assert.Nil(t, err)

	lastUpdatedAt := UpdatedAt.UnixNano()
	time.Sleep(1 * time.Millisecond)

	// Touch testConfigFile to trigger a file-change event.
	err = updateFile(testConfigFile)
	assert.Nil(t, err)

	// Wait for reload.
	time.Sleep(10 * time.Millisecond)

	assert.NotEqual(t, lastUpdatedAt, UpdatedAt.UnixNano())
}

func updateFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
