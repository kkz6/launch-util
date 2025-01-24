package model

import (
	"fmt"
	"github.com/gigcodes/launch-agent/archive"
	"github.com/gigcodes/launch-agent/compressor"
	"github.com/gigcodes/launch-agent/config"
	"github.com/gigcodes/launch-agent/database"
	"github.com/gigcodes/launch-agent/logger"
	"github.com/gigcodes/launch-agent/notifier"
	"github.com/gigcodes/launch-agent/storage"
	"github.com/spf13/viper"
	"os"
)

type Model struct {
	Config config.ModelConfig
}

// Perform model
func (m Model) Perform() (err error) {
	tag := logger.Tag(fmt.Sprintf("Model: %s", m.Config.Name))

	defer func() {
		if err != nil {
			tag.Error(err)
			notifier.Failure(m.Config, err.Error())
		} else {
			notifier.Success(m.Config)
		}
	}()

	tag.Info("WorkDir:", m.Config.DumpPath)

	defer func() {
		if r := recover(); r != nil {
			m.after()
		}

		m.after()
	}()

	err = database.Run(m.Config)
	if err != nil {
		return
	}

	if m.Config.Archive != nil {
		err = archive.Run(m.Config)
		if err != nil {
			return
		}
	}

	// It always to use compressor, default use tar, even not enable compress.
	archivePath, err := compressor.Run(m.Config)
	if err != nil {
		return
	}

	err = storage.Run(m.Config, archivePath)
	if err != nil {
		return
	}

	return nil
}

// Cleanup model temp files
func (m Model) after() {
	tag := logger.Tag("Model")

	tempDir := m.Config.TempPath
	if viper.GetBool("useTempWorkDir") {
		tempDir = viper.GetString("workdir")
	}
	tag.Infof("Cleanup temp: %s/", tempDir)
	if err := os.RemoveAll(tempDir); err != nil {
		tag.Errorf("Cleanup temp dir %s error: %v", tempDir, err)
	}
}

// GetModels get models
func GetModels() (models []*Model) {
	for _, modelConfig := range config.Models {
		m := Model{
			Config: modelConfig,
		}
		models = append(models, &m)
	}
	return
}

// GetModelByName get model by name
func GetModelByName(name string) *Model {
	modelConfig := config.GetModelConfigByName(name)
	if modelConfig == nil {
		return nil
	}
	return &Model{
		Config: *modelConfig,
	}
}
