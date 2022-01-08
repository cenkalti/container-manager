package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Containers map[string]*Container

	// Containers will be checked periodically with this interval value to match the definitions in config file.
	// Config file does not get read on every check interval. It only gets read on startup and when SIGHUP is received.
	CheckInterval time.Duration

	// HTTP server runs on this address for /health endpoint.
	ListenAddr string
}

type Container struct {
	// Running container will get replaced only if version changes.
	// You should update this value for every deploy.
	// Usually you should set a value such as build number from your CI tool.
	Version string

	// Number of running copies of this container.
	// An index number will be appended to the container name after first container.
	// Example: ["foo", "foo.2", "foo.3", "foo.4"]
	Count uint

	// Command to run on each CheckInterval to determine if the container is healty.
	CheckCmd []string

	// Timeout for CheckCmd. Command must exit with 0 in CheckTimeout.
	CheckTimeout time.Duration

	// Following options are passed directly to the Docker Engine API when creating the container.
	Image       string
	WorkingDir  string
	Entrypoint  []string
	Cmd         []string
	StopSignal  string
	StopTimeout time.Duration
	NetworkMode string
	Hostname    string
	Env         map[string]string
	Binds       []string
	LogConfig   container.LogConfig
	Resources   container.Resources
}

func (c *Config) setDefaults() {
	if c.CheckInterval <= 0 {
		c.CheckInterval = defaultCheckInterval
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:26662"
	}
}

func readConfig() error {
	log.Println("loading config from:", *configPath)
	f, err := os.Open(*configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var c Config
	err = yaml.NewDecoder(f).Decode(&c)
	if err != nil {
		return err
	}
	c.setDefaults()

	mu.Lock()
	defer mu.Unlock()

	cfg = c
	definitions = make(map[string]*Container, len(cfg.Containers))
	for name, con := range cfg.Containers {
		con.setDefaults()
		const start = 1
		for i := uint(start); i < con.Count+start; i++ {
			if i == start {
				definitions[name] = con
			} else {
				definitions[name+"."+strconv.FormatUint(uint64(i), 10)] = con
			}
		}
	}
	return nil
}

func (con *Container) setDefaults() {
	if con.Count == 0 {
		con.Count = 1
	}
	if len(con.CheckCmd) == 0 {
		con.CheckCmd = []string{"ls", "/"}
	}
	if con.CheckTimeout == 0 {
		con.CheckTimeout = 10 * time.Second
	}
}

func getContainerDefinion(name string) *Container {
	mu.Lock()
	defer mu.Unlock()
	return definitions[name]
}

func getCheckInterval() time.Duration {
	mu.Lock()
	defer mu.Unlock()
	return cfg.CheckInterval
}

func (c *Container) dockerConfig() *container.Config {
	env := make([]string, 0, len(c.Env))
	for k, v := range c.Env {
		env = append(env, k+"="+v)
	}
	var stopTimeout *int
	if sec := int(c.StopTimeout / time.Second); sec > 0 {
		stopTimeout = &sec
	}
	return &container.Config{
		Hostname:     c.Hostname,
		AttachStdout: true,         // Attach the standard output
		AttachStderr: true,         // Attach the standard error
		Env:          env,          // List of environment variable to set in the container
		Cmd:          c.Cmd,        // Command to run when starting the container
		Image:        c.Image,      // Name of the image as it was passed by the operator (e.g. could be symbolic)
		WorkingDir:   c.WorkingDir, // Current directory (PWD) in the command will be launched
		Entrypoint:   c.Entrypoint, // Entrypoint to run when starting the container
		StopSignal:   c.StopSignal, // Signal to stop a container
		StopTimeout:  stopTimeout,  // Timeout (in seconds) to stop a container
		Labels: map[string]string{
			containerVersionKey: c.Version,
		}, // List of labels set to this container
	}
}

func (c *Container) hostConfig() *container.HostConfig {
	return &container.HostConfig{
		Binds:         c.Binds,                              // List of volume bindings for this container
		NetworkMode:   container.NetworkMode(c.NetworkMode), // Network mode to use for the container
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		LogConfig:     c.LogConfig,
		Resources:     c.Resources,
	}
}
