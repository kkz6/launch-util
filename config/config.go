package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"

	"github.com/gigcodes/launch-util/helper"
	"github.com/gigcodes/launch-util/logger"
)

var (
	// Exist Is config file exist
	Exist bool
	// Models configs
	Models []ModelConfig
	// LaunchAgentDir launchAgent base dir
	LaunchAgentDir string = getLaunchAgentDir()

	PidFilePath string = filepath.Join(LaunchAgentDir, "launch.pid")
	LogFilePath string = filepath.Join(LaunchAgentDir, "launch.log")

	wLock = sync.Mutex{}

	// UpdatedAt The config file loaded at
	UpdatedAt time.Time

	Pulse PulseConfig

	onConfigChanges = make([]func(fsnotify.Event), 0)
)

type PulseConfig struct {
	Enabled bool          `json:"enabled,omitempty"`
	Webhook WebhookConfig `json:"webhook,omitempty"`
}

type WebhookConfig struct {
	Url     string
	Method  string
	Headers map[string]string
}

type ScheduleConfig struct {
	Enabled bool `json:"enabled,omitempty"`
	// Cron expression
	Cron string `json:"cron,omitempty"`
}

func (sc ScheduleConfig) String() string {
	if sc.Enabled {
		if len(sc.Cron) > 0 {
			return fmt.Sprintf("cron %s", sc.Cron)
		}
	}

	return "disabled"
}

// ModelConfig for special case
type ModelConfig struct {
	Name           string
	WorkDir        string
	TempPath       string
	DumpPath       string
	Schedule       ScheduleConfig
	CompressWith   SubConfig
	Archive        *viper.Viper
	Databases      map[string]SubConfig
	Storages       map[string]SubConfig
	DefaultStorage string
	Webhook        WebhookConfig
	Viper          *viper.Viper
}

func getLaunchAgentDir() string {
	dir := os.Getenv("tag")
	if len(dir) == 0 {
		dir = filepath.Join(os.Getenv("HOME"), ".launcher")
	}
	return dir
}

// SubConfig sub config info
type SubConfig struct {
	Name  string
	Type  string
	Viper *viper.Viper
}

// Init
// loadConfig from:
// - ./launch.yml
// - ~/.launcher/launch.yml
// - /etc/launch-agent/launch.yml
func Init(configFile string) error {
	tag := logger.Tag("Config")

	viper.SetConfigType("yaml")

	// set config file directly
	if len(configFile) > 0 {
		configFile = helper.AbsolutePath(configFile)
		tag.Info("Load config:", configFile)

		viper.SetConfigFile(configFile)
	} else {
		tag.Info("Load config from default path.")
		viper.SetConfigName("launch")

		// ./launch.yml
		viper.AddConfigPath(".")
		// ~/.launcher/launch.yml
		viper.AddConfigPath("$HOME/.launch") // call multiple times to add many search paths
		// /etc/launch-agent/launch.yml
		viper.AddConfigPath("/etc/launch-agent/") // path to look for the config file in
	}

	viper.WatchConfig()
	viper.OnConfigChange(func(in fsnotify.Event) {
		tag.Info("Config file changed:", in.Name)
		defer onConfigChanged(in)
		if err := loadConfig(); err != nil {
			tag.Error(err.Error())
		}
	})

	return loadConfig()
}

// OnConfigChange add callback when config changed
func OnConfigChange(run func(in fsnotify.Event)) {
	onConfigChanges = append(onConfigChanges, run)
}

// Invoke callbacks when config changed
func onConfigChanged(in fsnotify.Event) {
	for _, fn := range onConfigChanges {
		fn(in)
	}
}

func loadConfig() error {
	wLock.Lock()
	defer wLock.Unlock()

	tag := logger.Tag("Config")

	err := viper.ReadInConfig()
	if err != nil {
		tag.Error("Load launch agent config failed: ", err)
		return err
	}

	viperConfigFile := viper.ConfigFileUsed()
	if info, err := os.Stat(viperConfigFile); err == nil {
		// max permission: 0770
		if info.Mode()&(1<<2) != 0 {
			tag.Warnf("Other users are able to access %s with mode %v", viperConfigFile, info.Mode())
		}
	}

	tag.Info("Config file:", viperConfigFile)

	// load .env if exists in the same directory of used config file and expand variables in the config
	dotEnv := filepath.Join(filepath.Dir(viperConfigFile), ".env")
	if _, err := os.Stat(dotEnv); err == nil {
		if err := godotenv.Load(dotEnv); err != nil {
			tag.Errorf("Load %s failed: %v", dotEnv, err)
			return err
		}
	}

	cfg, _ := os.ReadFile(viperConfigFile)
	if err := viper.ReadConfig(strings.NewReader(os.ExpandEnv(string(cfg)))); err != nil {
		tag.Errorf("Load expanded config failed: %v", err)
		return err
	}

	viper.Set("useTempWorkDir", false)
	if workdir := viper.GetString("workdir"); len(workdir) == 0 {
		// use temp dir as workdir
		dir, err := os.MkdirTemp("", "launch")
		if err != nil {
			return err
		}

		viper.Set("workdir", dir)
		viper.Set("useTempWorkDir", true)
	}

	Exist = true
	Models = []ModelConfig{}
	for key := range viper.GetStringMap("models") {
		model, err := loadModel(key)
		if err != nil {
			return fmt.Errorf("load model %s: %v", key, err)
		}

		Models = append(Models, model)
	}

	Pulse = PulseConfig{
		Enabled: viper.GetBool("pulse.enabled"),
		Webhook: WebhookConfig{
			Url:     viper.GetString("pulse.webhook.url"),
			Method:  viper.GetString("pulse.webhook.method"),
			Headers: viper.GetStringMapString("pulse.webhook.headers"),
		},
	}

	UpdatedAt = time.Now()
	tag.Infof("Config loaded, found %d models.", len(Models))

	return nil
}

func loadModel(key string) (ModelConfig, error) {
	var model ModelConfig
	model.Name = key

	workdir, _ := os.Getwd()

	model.WorkDir = workdir
	model.TempPath = filepath.Join(viper.GetString("workdir"), fmt.Sprintf("%d", time.Now().UnixNano()))
	model.DumpPath = filepath.Join(model.TempPath, key)
	model.Viper = viper.Sub("models." + key)
	model.Schedule = ScheduleConfig{Enabled: false}

	model.CompressWith = SubConfig{
		Type:  model.Viper.GetString("compress_with.type"),
		Viper: model.Viper.Sub("compress_with"),
	}

	model.Archive = model.Viper.Sub("archive")

	model.Webhook = WebhookConfig{
		Url:     model.Viper.GetString("webhook.url"),
		Method:  model.Viper.GetString("webhook.method"),
		Headers: model.Viper.GetStringMapString("webhook.headers"),
	}

	loadScheduleConfig(&model)
	loadDatabasesConfig(&model)
	loadStoragesConfig(&model)

	if len(model.Storages) == 0 {
		return ModelConfig{}, fmt.Errorf("no storage found in model %s", model.Name)
	}

	return model, nil
}

func loadScheduleConfig(model *ModelConfig) {
	subViper := model.Viper.Sub("schedule")
	model.Schedule = ScheduleConfig{Enabled: false}
	if subViper == nil {
		return
	}

	model.Schedule = ScheduleConfig{
		Enabled: true,
		Cron:    subViper.GetString("cron"),
	}
}

func loadDatabasesConfig(model *ModelConfig) {
	subViper := model.Viper.Sub("databases")
	model.Databases = map[string]SubConfig{}
	for key := range model.Viper.GetStringMap("databases") {
		dbViper := subViper.Sub(key)
		model.Databases[key] = SubConfig{
			Name:  key,
			Type:  dbViper.GetString("type"),
			Viper: dbViper,
		}
	}
}

func loadStoragesConfig(model *ModelConfig) {
	storageConfigs := map[string]SubConfig{}

	model.DefaultStorage = model.Viper.GetString("default_storage")

	subViper := model.Viper.Sub("storages")
	for key := range model.Viper.GetStringMap("storages") {
		storageViper := subViper.Sub(key)
		storageConfigs[key] = SubConfig{
			Name:  key,
			Type:  storageViper.GetString("type"),
			Viper: storageViper,
		}

		// Set default storage
		if len(model.DefaultStorage) == 0 {
			model.DefaultStorage = key
		}
	}
	model.Storages = storageConfigs

}

// GetModelConfigByName get model config by name
func GetModelConfigByName(name string) (model *ModelConfig) {
	for _, m := range Models {
		if m.Name == name {
			model = &m
			return
		}
	}
	return
}

// GetDatabaseByName get database config by name
func (model *ModelConfig) GetDatabaseByName(name string) (subConfig *SubConfig) {
	for _, m := range model.Databases {
		if m.Name == name {
			subConfig = &m
			return
		}
	}
	return
}

func (model *ModelConfig) GetDatabaseNames() []string {
	var databaseNames []string
	for name := range model.Databases {
		databaseNames = append(databaseNames, name)
	}
	return databaseNames
}
