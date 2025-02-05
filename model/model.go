package model

import (
	"fmt"
	"os"

	"github.com/gigcodes/launch-util/archive"
	"github.com/gigcodes/launch-util/compressor"
	"github.com/gigcodes/launch-util/config"
	"github.com/gigcodes/launch-util/database"
	"github.com/gigcodes/launch-util/logger"
	"github.com/gigcodes/launch-util/notifier"
	"github.com/gigcodes/launch-util/storage"
	"github.com/spf13/viper"
)

type Model struct {
	Config config.ModelConfig
}

// Perform model
func (m Model) Perform() (err error) {
	tag := logger.Tag(fmt.Sprintf("Model: %s", m.Config.Name))

	webhook := notifier.NewWebhook(m.Config.Webhook)

	var fileSize int64

	defer func() {
		if err != nil {
			tag.Error(err)
			payload := map[string]interface{}{
				"error":  err.Error(),
				"model":  m.Config.Name,
				"status": "failed",
			}

			fmt.Println(payload)

			err := webhook.Notify(payload)
			if err != nil {
				fmt.Println("Error sending notification:", err)
			}
		} else {
			payload := map[string]interface{}{
				"error":  nil,
				"status": "finished",
				"model":  m.Config.Name,
				"size":   fileSize,
			}

			fmt.Println(payload)
			err := webhook.Notify(payload)
			if err != nil {
				fmt.Println("Error sending notification:", err)
			}
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
	fileInfo, err := os.Stat(archivePath)
	if err != nil {
		tag.Errorf("Error fetching file info: %v", err)
	} else {
		fileSize = fileInfo.Size()
	}
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
