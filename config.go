package main

import (
	"log"
	"os"
	"time"

	"github.com/docker/docker/api/types/container"
	"gopkg.in/yaml.v2"
)

var cfg Config

type Config struct {
	Containers    map[string]Container
	CheckInterval time.Duration
	// ClientTimeout time.Duration
	// Namespace string
}

type Container struct {
	Image       string
	WorkingDir  string
	Entrypoint  []string
	Command     []string
	StopSignal  string
	StopTimeout int
	NetworkMode string
	Environment map[string]string
	Binds       []string
	DNS         []string
	Labels      map[string]string
}

func (c *Config) setDefaults() {
	if c.CheckInterval <= 0 {
		c.CheckInterval = 60 * time.Second
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
	cfg = c
	mu.Unlock()
	return nil
}

func getContainerDefinion(name string) *Container {
	mu.Lock()
	defer mu.Unlock()
	if c, ok := cfg.Containers[name]; ok {
		return &c
	} else {
		return nil
	}
}

func (c *Container) containerConfig(name string) *container.Config {
	env := make([]string, 0, len(c.Environment))
	for k, v := range c.Environment {
		env = append(env, k+"="+v)
	}
	return &container.Config{
		Hostname:     name,
		AttachStdout: true,           // Attach the standard output
		AttachStderr: true,           // Attach the standard error
		Env:          env,            // List of environment variable to set in the container
		Cmd:          c.Command,      // Command to run when starting the container
		Image:        c.Image,        // Name of the image as it was passed by the operator (e.g. could be symbolic)
		WorkingDir:   c.WorkingDir,   // Current directory (PWD) in the command will be launched
		Entrypoint:   c.Entrypoint,   // Entrypoint to run when starting the container
		StopSignal:   c.StopSignal,   // Signal to stop a container
		StopTimeout:  &c.StopTimeout, // Timeout (in seconds) to stop a container
		Labels:       c.Labels,       // List of labels set to this container
	}
}

func (c *Container) hostConfig() *container.HostConfig {
	return &container.HostConfig{
		Binds:         c.Binds,                              // List of volume bindings for this container
		NetworkMode:   container.NetworkMode(c.NetworkMode), // Network mode to use for the container
		DNS:           c.DNS,                                // List of DNS server to lookup
		RestartPolicy: container.RestartPolicy{Name: "always"},
	}
}
