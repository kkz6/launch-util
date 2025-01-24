package database

import (
	"fmt"
	"testing"

	"github.com/gigcodes/launch-agent/config"
	"github.com/longbridge/assert"
)

func init() {
	if err := config.Init("../launch.example.yml"); err != nil {
		panic(err.Error())
	}
}

type Monkey struct {
	Base
}

func (db Monkey) perform() error {
	if db.model.Name != "TestMonkey" {
		return fmt.Errorf("error")
	}
	if db.dbConfig.Name != "mysql1" {
		return fmt.Errorf("error")
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
		DumpPath: "/tmp/launch/test",
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
	assert.Equal(t, base.dumpPath, "/tmp/launch/test/mysql/mysql-master")
}
