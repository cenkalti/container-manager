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

// TODO set client timeout
// TODO cancel running operations on on exit with context
// TODO run multiple containers of same name
// TODO implement deploy policy
// TODO health check endpoint
// TODO prometheus exporter
// TODO http api

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
	chanTerm := make(chan os.Signal, 1)
	signal.Notify(chanReload, syscall.SIGHUP)
	signal.Notify(chanTerm, syscall.SIGTERM, syscall.SIGINT)

	reload() // for initial loading of config & starting of containers
	for {
		select {
		case <-chanReload:
			reload()
		case <-chanTerm:
			// Close container-manager gracefully, leave running containers as they are.
			closeManagers()
			return
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

func closeManagers() {
	mu.Lock()
	defer mu.Unlock()

	for _, mgr := range managers {
		mgr.Close()
	}
}
