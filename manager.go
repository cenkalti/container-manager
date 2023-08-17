package main

import (
	"context"
	"io"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/docker/cli/cli/command"
	cliflags "github.com/docker/cli/cli/flags"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

var networkEndpointErrorRegex = regexp.MustCompile(`^endpoint with name .* already exists in network (.*)$`)

type Manager struct {
	name             string
	definition       *Container
	log              *log.Logger
	reloadC          chan struct{}
	actionTime       time.Time
	lastDockerExecID string
	errHealthCheck   error
}

func (m *Manager) setActionTime() {
	mu.Lock()
	if m.actionTime.IsZero() {
		m.actionTime = time.Now()
	}
	mu.Unlock()
}

func (m *Manager) unsetActionTime() {
	mu.Lock()
	m.actionTime = time.Time{}
	mu.Unlock()
}

func Manage(name string, c *Container) *Manager {
	m := &Manager{
		name:       name,
		definition: c,
		log:        log.New(os.Stderr, "["+name+"] ", log.LstdFlags),
		reloadC:    make(chan struct{}, 1),
	}
	m.reloadC <- struct{}{}
	go m.run()
	return m
}

func (m *Manager) run() {
	ctx := context.Background()
	for {
		select {
		case <-time.After(getCheckInterval()):
			m.doReload(ctx)
		case <-m.reloadC:
			m.doReload(ctx)
		}
	}
}

func (m *Manager) doReload(ctx context.Context) {
	newDef := getContainerDefinion(m.name)
	if newDef == nil {
		m.log.Println("container definition not found")
		m.setActionTime()
		err := m.doRemove(ctx)
		if err != nil {
			m.log.Println("cannot remove container:", err.Error())
			return
		}
		m.unsetActionTime()
		mu.Lock()
		delete(managers, m.name)
		mu.Unlock()
		return
	}
	con, err := cli.ContainerInspect(ctx, m.name)
	if client.IsErrNotFound(err) {
		m.log.Println("container not found")
		m.setActionTime()
		err = m.pullImage(ctx, newDef.Image)
		if err != nil {
			m.log.Println("cannot pull image:", err.Error())
			return
		}
		m.definition = newDef
		err = m.doCreate(ctx)
		if err != nil {
			m.log.Println("cannot create container:", err.Error())
			return
		}
		m.unsetActionTime()
		return
	}
	if err != nil {
		m.log.Println("cannot inspect container:", err.Error())
		return
	}
	if con.Config.Labels[containerVersionKey] == newDef.Version {
		// Try to recover from existing network endpoint error
		match := networkEndpointErrorRegex.FindStringSubmatch(con.State.Error)
		if len(match) > 1 { // nolint: gomnd
			networkName := match[1]
			m.log.Println("detected error:", con.State.Error)
			m.setActionTime()
			err = cli.NetworkDisconnect(ctx, networkName, con.ID, true)
			if err != nil {
				m.log.Println("cannot disconnect container from network:", err.Error())
				return
			}
			m.unsetActionTime()
		}
		// Start container in "created" status.
		// This can happen if container-manager creates a container but cannot start it due to an error.
		if con.State.Status == "created" {
			m.log.Println("container not running, starting container")
			m.setActionTime()
			err = cli.ContainerStart(ctx, con.ID, types.ContainerStartOptions{})
			if err != nil {
				m.log.Println("cannot start container:", err.Error())
				return
			}
			m.unsetActionTime()
		}
		// Definition did not get change. Do nothing.
		m.unsetActionTime()
		m.CheckHealth()
		return
	}
	m.log.Println("container definition changed, reloading")
	m.setActionTime()
	err = m.pullImage(ctx, newDef.Image)
	if err != nil {
		m.log.Println("cannot pull image:", err.Error())
		return
	}
	err = m.doRemove(ctx)
	if err != nil {
		m.log.Println("cannot remove container:", err.Error())
		return
	}
	m.definition = newDef
	err = m.doCreate(ctx)
	if err != nil {
		m.log.Println("cannot create container:", err.Error())
		return
	}
	m.unsetActionTime()
}

func (m *Manager) doRemove(ctx context.Context) error {
	m.log.Println("stopping container")
	err := cli.ContainerStop(ctx, m.name, container.StopOptions{})
	if err != nil {
		return err
	}
	m.log.Println("removing container")
	return cli.ContainerRemove(ctx, m.name, types.ContainerRemoveOptions{Force: true})
}

func (m *Manager) pullImage(ctx context.Context, image string) error {
	dockerCli, err := command.NewDockerCli()
	if err != nil {
		return err
	}
	err = dockerCli.Initialize(cliflags.NewClientOptions())
	if err != nil {
		return err
	}
	auth, err := command.RetrieveAuthTokenFromImage(ctx, dockerCli, image)
	if err != nil {
		return err
	}
	m.log.Println("pulling image:", image)
	body, err := cli.ImagePull(ctx, image, types.ImagePullOptions{
		RegistryAuth: auth,
	})
	if err != nil {
		return err
	}
	defer body.Close()
	_, _ = io.Copy(io.Discard, body)
	return nil
}

func (m *Manager) doCreate(ctx context.Context) error {
	m.log.Println("creating container")
	resp, err := cli.ContainerCreate(ctx, m.definition.dockerConfig(), m.definition.hostConfig(), nil, nil, m.name)
	if err != nil {
		return err
	}
	m.log.Println("starting container")
	return cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
}

// Reload the definition from config and make necessary changes to container
func (m *Manager) Reload() {
	select {
	case m.reloadC <- struct{}{}:
	default:
	}
}

func (m *Manager) IsStuck() bool {
	stopTimeout := m.definition.StopTimeout
	if stopTimeout == 0 {
		stopTimeout = 10 * time.Second // Docker default
	}
	if m.actionTime.IsZero() {
		return false
	}
	return time.Since(m.actionTime) > stopTimeout+cfg.CheckInterval
}

func (m *Manager) CheckHealth() {
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), m.definition.CheckTimeout)
	defer cancel()
	m.lastDockerExecID, err = dockerExec(ctx, m.name, m.definition.CheckCmd, m.definition.CheckTimeout, m.lastDockerExecID)
	if err != nil {
		m.log.Println("health check failed: " + err.Error())
	}
	mu.Lock()
	m.errHealthCheck = err
	mu.Unlock()
}
