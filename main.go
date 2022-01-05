package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

const (
	errExitCode          = 1
	defaultCheckInterval = 60 * time.Second
	containerVersionKey  = "com.cenkalti.container-manager.container-version"
)

// Version of client. Set during build.
// "0.0.0" is the development version.
var Version = "v0.0.0"

// Command line flags
var (
	configPath   = flag.String("config", "/etc/container-manager.yaml", "config path")
	printVersion = flag.Bool("version", false, "print program version")
)

// Global state
var (
	// Config file unmarshaled from YAML
	cfg Config
	// Contains container defintions from config after count adding "-<count>" postfix
	definitions map[string]*Container
	// Containers currently managed by the app
	managers = make(map[string]*Manager)
	// Docker daemon client
	cli *client.Client
	// Protects config and other global state
	mu sync.Mutex
	// Error channel for passing HTTP server errors to main goroutine
	httpErrC = make(chan error, 1)
)

func main() {
	flag.Parse()

	if *printVersion {
		fmt.Println(Version)
		return
	}

	var err error
	cli, err = client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create docker client:", err.Error())
		os.Exit(errExitCode)
	}

	err = readConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read config:", err.Error())
		os.Exit(errExitCode)
	}

	err = removeStaleContainers()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot remove stale continers:", err.Error())
		os.Exit(errExitCode)
	}

	reloadContainers()

	http.HandleFunc("/health", handleHealth)
	go runHTTPServer()

	chanReload := make(chan os.Signal, 1)
	signal.Notify(chanReload, syscall.SIGHUP)
	for {
		select {
		case <-chanReload:
			err := readConfig()
			if err != nil {
				fmt.Fprintln(os.Stderr, "cannot read config:", err.Error())
				os.Exit(errExitCode)
			}
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
	for name, manager := range managers {
		if _, ok := running["/"+name]; !ok {
			addError("container is not running: " + name)
			return
		}

		if manager.IsStuck() {
			addError("container is stuck: " + name)
			return
		}

		_, err := exec.Command("docker", "exec", name, "echo").Output()
		if err != nil {
			addError("container is in an uknown state: " + name)
			return
		}
	}
	mu.Unlock()
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
		if len(con.Names) == 0 {
			// Container state is "removal in progress" and does not have any name.
			continue
		}
		if inDefinitions(con.Names) {
			// Container has a definition in config
			continue
		}
		name := strings.TrimPrefix(con.Names[0], "/")
		managers[name] = Manage(name, nil)
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
