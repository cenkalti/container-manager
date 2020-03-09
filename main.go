package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

// TODO clean previous stale containers on startup
// TODO prometheus exporter
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

	reload() // for initial loading of config & starting of containers

	http.HandleFunc("/health", handleHealth)
	err = http.ListenAndServe(cfg.ListenAddr, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot serve http:", err.Error())
		os.Exit(errExitCode)
	}

	chanReload := make(chan os.Signal, 1)
	signal.Notify(chanReload, syscall.SIGHUP)
	for {
		select {
		case <-chanReload:
			reload()
		case <-time.After(cfg.CheckInterval):
			reloadContainers()
		}
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	errors := make([]string, 0)
	addError := func(s string) {
		errors = append(errors, s)
	}
	defer func() {
		w.Header().Set("content-type", "application/json")
		if len(errors) > 0 {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": errors})
	}()
	containers, err := cli.ContainerList(r.Context(), types.ContainerListOptions{})
	if err != nil {
		addError(err.Error())
		return
	}
	running := make(map[string]struct{}, len(containers))
	for _, con := range containers {
		for _, name := range con.Names {
			running[name] = struct{}{}
		}
	}
	mu.Lock()
	for name := range managers {
		if _, ok := running["/"+name]; !ok {
			addError("container not running: " + name)
		}
	}
	mu.Unlock()
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

	for name, con := range definitions {
		if _, ok := managers[name]; !ok {
			managers[name] = Manage(name, con)
		}
	}
}
