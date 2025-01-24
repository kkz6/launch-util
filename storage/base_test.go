package storage

import (
	"testing"

	"github.com/gigcodes/launch-agent/config"
	"github.com/longbridge/assert"
)

func TestBase_newBase(t *testing.T) {
	model := config.ModelConfig{}
	archivePath := "/tmp/launch/test-storage/foo.zip"
	s, _ := newBase(model, archivePath, config.SubConfig{})

	assert.Equal(t, s.archivePath, archivePath)
	assert.Equal(t, s.model, model)
	assert.Equal(t, s.viper, model.Viper)
	assert.Equal(t, s.keep, 0)
}
