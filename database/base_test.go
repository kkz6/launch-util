package database

import (
	"fmt"
	"testing"

	"github.com/gigcodes/launch-util/config"
	"github.com/longbridgeapp/assert"
)

// These tests build ModelConfig / SubConfig inline, so there's no config
// fixture to load here (the old init() loaded `../gobackup_test.yml`,
// which this fork never shipped — that's why the package paniced on
// `go test`).

type Monkey struct {
	Base
}

func (db Monkey) perform() error {
	if db.model.Name != "TestMonkey" {
		return fmt.Errorf("Error")
	}
	if db.dbConfig.Name != "mysql1" {
		return fmt.Errorf("Error")
	}
	return nil
}

func TestBaseInterface(t *testing.T) {
	base := Base{
		model: config.ModelConfig{
			Name: "TestMonkey",
		},
		dbConfig: config.SubConfig{
			Name: "mysql1",
		},
	}
	db := Monkey{Base: base}
	err := db.perform()
	assert.Nil(t, err)
}

func TestBase_newBase(t *testing.T) {
	model := config.ModelConfig{
		DumpPath: "/tmp/gobackup/test",
	}
	dbConfig := config.SubConfig{
		Type: "mysql",
		Name: "mysql-master",
	}
	base := newBase(model, dbConfig)

	assert.Equal(t, base.model, model)
	assert.Equal(t, base.dbConfig, dbConfig)
	assert.Equal(t, base.viper, dbConfig.Viper)
	assert.Equal(t, base.name, "mysql-master")
	assert.Equal(t, base.dumpPath, "/tmp/gobackup/test/mysql/mysql-master")
}
