package scheduler

import (
	"github.com/gigcodes/launch-agent/psutil"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gigcodes/launch-agent/config"
	superlogger "github.com/gigcodes/launch-agent/logger"
	"github.com/go-co-op/gocron"
)

var (
	mycron *gocron.Scheduler
)

func init() {
	config.OnConfigChange(func(in fsnotify.Event) {
		Restart()
	})
}

// Start scheduler
func Start() error {
	logger := superlogger.Tag("Scheduler")

	mycron = gocron.NewScheduler(time.Local)

	mu := sync.Mutex{}

	if config.Pulse.Enabled {
		logger.Info("Launch pulse initiated")

		var scheduler *gocron.Scheduler

		scheduler = mycron.Every(5).Minute()

		if _, err := scheduler.Do(func() {
			defer mu.Unlock()

			mu.Lock()
			psutilData, err := psutil.Fetch()
			if err != nil {
				logger.Fatal("Error fetching system stats:", err)
			}

			psutil.Pulse(psutilData)
		}); err != nil {
			logger.Errorf("Failed to register job func: %s", err.Error())
		}
	}

	mycron.StartAsync()

	return nil
}

func Restart() error {
	logger := superlogger.Tag("Scheduler")
	logger.Info("Reloading...")
	Stop()
	return Start()
}

func Stop() {
	if mycron != nil {
		mycron.Stop()
	}
}
