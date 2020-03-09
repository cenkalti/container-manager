package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"gopkg.in/yaml.v2"
)

const defaultCheckInterval = 60 * time.Second

var (
	cfg         Config
	definitions map[string]*Container
)

type Config struct {
	Containers    map[string]*Container
	CheckInterval time.Duration
}

type Container struct {
	Version     string
	Count       uint
	Image       string
	WorkingDir  string
	Entrypoint  []string
	Cmd         []string
	StopSignal  string
	StopTimeout time.Duration
	NetworkMode string
	Env         map[string]string
	Binds       []string
}

func (c *Config) setDefaults() {
	if c.CheckInterval <= 0 {
		c.CheckInterval = defaultCheckInterval
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
		if con.Count > 1 { // nolint: gomnd
			for i := uint(0); i < con.Count; i++ {
				const containerIndexStart = 1
				definitions[name+"-"+strconv.FormatUint(uint64(i+containerIndexStart), 10)] = con
			}
		} else {
			definitions[name] = con
		}
	}
	return nil
}

func getContainerDefinion(name string) *Container {
	mu.Lock()
	defer mu.Unlock()
	return definitions[name]
}

func (c *Container) containerConfig(name string) *container.Config {
	env := make([]string, 0, len(c.Env))
	for k, v := range c.Env {
		env = append(env, k+"="+v)
	}
	var stopTimeout *int
	if sec := int(c.StopTimeout / time.Second); sec > 0 {
		stopTimeout = &sec
	}
	return &container.Config{
		Hostname:     name,
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
		RestartPolicy: container.RestartPolicy{Name: "always"},
	}
}
