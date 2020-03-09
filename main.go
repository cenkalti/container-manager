package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

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
	httpErrC = make(chan error, 1)
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

	err = removeStaleContainers()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot remove stale continers:", err.Error())
		os.Exit(errExitCode)
	}

	http.HandleFunc("/health", handleHealth)
	go runHTTPServer()

	chanReload := make(chan os.Signal, 1)
	signal.Notify(chanReload, syscall.SIGHUP)
	for {
		select {
		case <-chanReload:
			reload()
		case <-time.After(cfg.CheckInterval):
			reloadContainers()
		case err = <-httpErrC:
			fmt.Fprintln(os.Stderr, "cannot serve http:", err.Error())
			os.Exit(errExitCode)
		}
	}
}

func runHTTPServer() {
	if err := http.ListenAndServe(cfg.ListenAddr, nil); err != nil {
		httpErrC <- err
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
			managers[name] = Manage(name, con, false)
		}
	}
}

// Some containers left from previous runs may still be running.
// If config is changed while container-manager is not running and some container definitions are removed from config, they remain in running state.
// This function find those containers and remove them.
func removeStaleContainers() error {
	containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{All: true})
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	for _, con := range containers {
		if _, ok := con.Labels[containerVersionKey]; !ok {
			// Container didn't get started by container-manager
			continue
		}
		if inDefinitions(con.Names) {
			// Container has a definition in config
			continue
		}
		name := strings.TrimPrefix(con.Names[0], "/")
		managers[name] = Manage(name, nil, true)
	}
	return nil
}

func inDefinitions(names []string) bool {
	for _, name := range names {
		if _, ok := definitions[strings.TrimPrefix(name, "/")]; ok {
			return true
		}
	}
	return false
}
