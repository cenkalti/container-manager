package main

import (
	"context"
	"io"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/docker/cli/cli/command"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

type Manager struct {
	name       string
	definition *Container
	log        *log.Logger
	reloadC    chan struct{}
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
		err := m.doRemove(ctx)
		if err != nil {
			m.log.Println("cannot remove container:", err.Error())
			return
		}
		mu.Lock()
		delete(managers, m.name)
		mu.Unlock()
		return
	}
	con, err := cli.ContainerInspect(ctx, m.name)
	if client.IsErrNotFound(err) {
		m.log.Println("container not found")
		err = m.doCreate(ctx)
		if err != nil {
			m.log.Println("cannot create container:", err.Error())
			return
		}
		return
	}
	if err != nil {
		m.log.Println("cannot inspect container:", err.Error())
		return
	}
	if con.Config.Labels[containerVersionKey] == newDef.Version {
		// Nothing changed
		return
	}
	m.log.Println("container definition changed, reloading")
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
}

func (m *Manager) doRemove(ctx context.Context) error {
	m.log.Println("stopping container")
	err := cli.ContainerStop(ctx, m.name, nil)
	if err != nil {
		return err
	}
	m.log.Println("removing container")
	return cli.ContainerRemove(ctx, m.name, types.ContainerRemoveOptions{Force: true})
}

func (m *Manager) doCreate(ctx context.Context) error {
	dockerCli, err := command.NewDockerCli()
	if err != nil {
		return err
	}
	auth, err := command.RetrieveAuthTokenFromImage(ctx, dockerCli, m.definition.Image)
	if err != nil {
		return err
	}
	m.log.Println("pulling image:", m.definition.Image)
	body, err := cli.ImagePull(ctx, m.definition.Image, types.ImagePullOptions{
		RegistryAuth: auth,
	})
	if err != nil {
		return err
	}
	defer body.Close()
	_, _ = io.Copy(ioutil.Discard, body)
	m.log.Println("creating container")
	resp, err := cli.ContainerCreate(ctx, m.definition.containerConfig(m.name), m.definition.hostConfig(), nil, m.name)
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
