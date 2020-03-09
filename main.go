package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/client"
)

// TODO parse durations in config
// TODO run multiple containers of same name
// TODO implement deploy policy
// TODO health check endpoint
// TODO prometheus exporter
// TODO http api
// TODO send log messages to journald

const errExitCode = 1

// Version of client. Set during build.
// "0.0.0" is the development version.
var Version = "0.0.0"

var (
	configPath   = flag.String("config", "/etc/container-manager.yaml", "config path")
	printVersion = flag.Bool("version", false, "print program version")
)

var (
	managers = make(map[string]*Manager)
	cli      *client.Client
	mu       sync.Mutex
)

func main() {
	flag.Parse()

	if *printVersion {
		fmt.Println(Version)
		return
	}

	var err error
	cli, err = client.NewEnvClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create docker client:", err.Error())
		os.Exit(errExitCode)
	}

	chanReload := make(chan os.Signal, 1)
	signal.Notify(chanReload, syscall.SIGHUP)

	reload() // for initial loading of config & starting of containers
	for {
		select {
		case <-chanReload:
			reload()
		case <-time.After(cfg.CheckInterval):
			reloadContainers()
		}
	}
}

func reload() {
	err := readConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read config:", err.Error())
		os.Exit(errExitCode)
	}
	reloadContainers()
}

func reloadContainers() {
	mu.Lock()
	defer mu.Unlock()

	for _, mgr := range managers {
		mgr.Reload()
	}

	for name, con := range cfg.Containers {
		if _, ok := managers[name]; !ok {
			managers[name] = Manage(name, con)
		}
	}
}
