package main

import (
	"context"
	"log"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

var ctx = context.TODO()

type Manager struct {
	name       string
	definition Container
	log        *log.Logger
	closeC     chan struct{}
	closedC    chan struct{}
	closeOnce  sync.Once
	reloadC    chan struct{}
}

func Manage(name string, c Container) *Manager {
	m := &Manager{
		name:       name,
		definition: c,
		log:        log.New(os.Stderr, "["+name+"] ", log.LstdFlags),
		closeC:     make(chan struct{}),
		closedC:    make(chan struct{}),
		reloadC:    make(chan struct{}, 1),
	}
	m.reloadC <- struct{}{}
	go m.run()
	return m
}

func (m *Manager) run() {
	defer close(m.closedC)
	for {
		select {
		case <-m.closeC:
			return
		case <-time.After(time.Minute):
			m.doReload()
		case <-m.reloadC:
			m.doReload()
		}
	}
}

func (m *Manager) doReload() {
	con, err := cli.ContainerInspect(ctx, m.name)
	if client.IsErrNotFound(err) {
		m.log.Println("container not found, creating container")
		conCfg := m.definition.containerConfig(m.name)
		hostCfg := m.definition.hostConfig()
		resp, err := cli.ContainerCreate(ctx, conCfg, hostCfg, nil, m.name)
		if err != nil {
			m.log.Println("cannot create container:", err.Error())
			return
		}
		err = cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
		if err != nil {
			m.log.Println("cannot start container:", err.Error())
			return
		}
		return
	}
	if err != nil {
		m.log.Println("cannot inspect container:", err.Error())
		return
	}
	newDef := getContainerDefinion(m.name)
	if newDef == nil {
		m.log.Println("removing deleted container")
		timeout := time.Duration(m.definition.StopTimeout) * time.Second
		err := cli.ContainerStop(ctx, m.name, &timeout)
		if err != nil {
			m.log.Println("cannot stop container:", err.Error())
			return
		}
		err = cli.ContainerRemove(ctx, m.name, types.ContainerRemoveOptions{Force: true})
		if err != nil {
			m.log.Println("cannot remove container:", err.Error())
			return
		}
		mu.Lock()
		delete(managers, m.name)
		mu.Unlock()
		m.doClose()
		return
	}
	if reflect.DeepEqual(m.definition, *newDef) {
		if !con.State.Running {
			m.log.Println("container not running, starting container")
			err = cli.ContainerStart(ctx, con.ID, types.ContainerStartOptions{})
			if err != nil {
				m.log.Println("cannot start container:", err.Error())
				return
			}
		}
		return
	}
	m.log.Println("container definition changed, reloading")
	if con.State.Running {
		timeout := time.Duration(m.definition.StopTimeout) * time.Second
		err := cli.ContainerStop(ctx, con.ID, &timeout)
		if err != nil {
			m.log.Println("cannot stop container:", err.Error())
			return
		}
	}
	err = cli.ContainerRemove(ctx, con.ID, types.ContainerRemoveOptions{Force: true})
	if err != nil {
		m.log.Println("cannot remove container:", err.Error())
		return
	}
	m.definition = *newDef
	conCfg := m.definition.containerConfig(m.name)
	hostCfg := m.definition.hostConfig()
	resp, err := cli.ContainerCreate(ctx, conCfg, hostCfg, nil, m.name)
	if err != nil {
		m.log.Println("cannot create container:", err.Error())
		return
	}
	err = cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		m.log.Println("cannot start container:", err.Error())
		return
	}
}

// Close the manager
func (m *Manager) Close() {
	m.doClose()
	<-m.closedC
}

func (m *Manager) doClose() {
	m.closeOnce.Do(func() { close(m.closeC) })
}

// Reload the definition from config and make necessary changes to container
func (m *Manager) Reload() {
	select {
	case m.reloadC <- struct{}{}:
	default:
	}
}
